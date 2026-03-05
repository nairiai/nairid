package handlers

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gammazero/workerpool"

	"nairid/core/log"
	"nairid/models"
)

const (
	// seenMessageTTL is how long we remember message IDs for deduplication
	seenMessageTTL = 5 * time.Minute
	// cleanupInterval is how often we run cleanup of old seen messages
	cleanupInterval = 5 * time.Minute
)

// JobDispatcher routes messages to per-job channels to ensure sequential processing
// for the same job while allowing different jobs to process in parallel.
type JobDispatcher struct {
	activeJobs   map[string]chan models.BaseMessage
	seenMessages map[string]time.Time // ProcessedMessageID → first seen time
	lastCleanup  time.Time
	mutex        sync.Mutex
	handler      *MessageHandler
	workerPool   *workerpool.WorkerPool
	appState     *models.AppState
}

// NewJobDispatcher creates a new JobDispatcher instance
func NewJobDispatcher(
	handler *MessageHandler,
	workerPool *workerpool.WorkerPool,
	appState *models.AppState,
) *JobDispatcher {
	return &JobDispatcher{
		activeJobs:   make(map[string]chan models.BaseMessage),
		seenMessages: make(map[string]time.Time),
		lastCleanup:  time.Now(),
		handler:      handler,
		workerPool:   workerPool,
		appState:     appState,
	}
}

// Dispatch routes a message to the appropriate per-job channel.
// If no channel exists for the job, it creates one and starts a worker goroutine.
func (d *JobDispatcher) Dispatch(msg models.BaseMessage) {
	// Deduplicate messages by ProcessedMessageID
	processedMsgID := d.extractProcessedMessageID(msg)
	if processedMsgID != "" {
		d.mutex.Lock()
		d.maybeCleanupSeenMessages()
		if _, seen := d.seenMessages[processedMsgID]; seen {
			d.mutex.Unlock()
			log.Info("🔁 Duplicate message %s, skipping", processedMsgID)
			return
		}
		d.seenMessages[processedMsgID] = time.Now()
		d.mutex.Unlock()
	}

	jobID := d.extractJobID(msg)
	if jobID == "" {
		// No job ID - process directly via worker pool (e.g., CheckIdleJobs)
		d.workerPool.Submit(func() {
			d.handler.HandleMessage(msg)
		})
		return
	}

	// Check/create channel under lock, but don't call Submit while holding lock
	// (Submit can block if worker pool is full, causing mutex contention)
	d.mutex.Lock()
	ch, exists := d.activeJobs[jobID]
	if !exists {
		ch = make(chan models.BaseMessage, 100)
		d.activeJobs[jobID] = ch
		log.Info("🔀 Created per-job channel for job %s", jobID)
	}
	d.mutex.Unlock()

	// Submit worker outside of lock to avoid blocking other dispatchers
	if !exists {
		d.workerPool.Submit(func() {
			d.processJobMessages(jobID, ch)
		})
	}

	// Send message to the job's channel (non-blocking since buffer is 100)
	select {
	case ch <- msg:
		log.Info("📥 Queued message to job %s channel", jobID)
	default:
		log.Error("❌ Job %s channel is full, dropping message", jobID)
	}
}

// processJobMessages processes messages sequentially for a specific job
func (d *JobDispatcher) processJobMessages(jobID string, ch chan models.BaseMessage) {
	log.Info("🔄 Started message processor for job %s", jobID)

	// Ensure channel is cleaned up when we exit
	defer d.cleanup(jobID)

	for msg := range ch {
		log.Info("🔧 Processing message for job %s", jobID)
		d.handler.HandleMessage(msg)

		// Check if job was removed from AppState
		jobData, exists := d.appState.GetJobData(jobID)
		if !exists {
			log.Info("✅ Job %s removed, exiting processor", jobID)
			return
		}

		// If job is completed or failed AND no more messages buffered, exit
		// This ensures we process all queued messages before exiting
		if (jobData.Status == models.JobStatusCompleted || jobData.Status == models.JobStatusFailed) && len(ch) == 0 {
			log.Info("✅ Job %s %s and channel empty, exiting processor", jobID, jobData.Status)
			return
		}
	}

	log.Info("📤 Message processor for job %s exited (channel closed)", jobID)
}

// cleanup removes a job's channel from the activeJobs map
func (d *JobDispatcher) cleanup(jobID string) {
	d.mutex.Lock()
	ch, exists := d.activeJobs[jobID]
	if exists {
		// Remove from map first to prevent new messages being sent
		delete(d.activeJobs, jobID)
	}
	d.mutex.Unlock()

	if exists {
		// Close channel outside of mutex to avoid holding it while draining
		close(ch)
		log.Info("🧹 Cleaned up channel for job %s", jobID)
	}
}

// EvictJob forcefully removes a job from the dispatcher, closing its channel
// and causing the processor goroutine to exit. This should be called when a
// job encounters an unrecoverable error (e.g., API error) to immediately free
// up the worker slot instead of waiting for the next message.
func (d *JobDispatcher) EvictJob(jobID string) {
	log.Info("🚫 Evicting job %s from dispatcher", jobID)
	d.cleanup(jobID)
}

// extractJobID extracts the job ID from a message based on its type
func (d *JobDispatcher) extractJobID(msg models.BaseMessage) string {
	switch msg.Type {
	case models.MessageTypeStartConversation:
		var payload models.StartConversationPayload
		if err := d.unmarshalPayload(msg.Payload, &payload); err != nil {
			log.Error("❌ Failed to unmarshal StartConversation payload: %v", err)
			return ""
		}
		return payload.JobID

	case models.MessageTypeUserMessage:
		var payload models.UserMessagePayload
		if err := d.unmarshalPayload(msg.Payload, &payload); err != nil {
			log.Error("❌ Failed to unmarshal UserMessage payload: %v", err)
			return ""
		}
		return payload.JobID

	default:
		// Other message types (CheckIdleJobs, RefreshToken) don't have job IDs
		return ""
	}
}

// unmarshalPayload unmarshals a message payload into the target struct
func (d *JobDispatcher) unmarshalPayload(payload any, target any) error {
	if payload == nil {
		return nil
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return json.Unmarshal(payloadBytes, target)
}

// extractProcessedMessageID extracts the ProcessedMessageID from a message for deduplication
func (d *JobDispatcher) extractProcessedMessageID(msg models.BaseMessage) string {
	switch msg.Type {
	case models.MessageTypeStartConversation:
		var payload models.StartConversationPayload
		if err := d.unmarshalPayload(msg.Payload, &payload); err != nil {
			return ""
		}
		return payload.ProcessedMessageID

	case models.MessageTypeUserMessage:
		var payload models.UserMessagePayload
		if err := d.unmarshalPayload(msg.Payload, &payload); err != nil {
			return ""
		}
		return payload.ProcessedMessageID

	default:
		return ""
	}
}

// maybeCleanupSeenMessages removes old entries from seenMessages if enough time has passed.
// Must be called with mutex held.
func (d *JobDispatcher) maybeCleanupSeenMessages() {
	now := time.Now()
	if now.Sub(d.lastCleanup) < cleanupInterval {
		return
	}

	d.lastCleanup = now
	cutoff := now.Add(-seenMessageTTL)
	for msgID, seenAt := range d.seenMessages {
		if seenAt.Before(cutoff) {
			delete(d.seenMessages, msgID)
		}
	}
	log.Info("🧹 Cleaned up seen messages, %d remaining", len(d.seenMessages))
}
