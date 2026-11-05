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
	// doneMessageTTL outlives the backend's redelivery window (1h from creation) with margin.
	doneMessageTTL = 2 * time.Hour
	// cleanupInterval is how often we forget old done messages
	cleanupInterval = 10 * time.Minute
)

// JobDispatcher routes messages to per-job channels to ensure sequential processing
// for the same job while allowing different jobs to process in parallel.
type JobDispatcher struct {
	activeJobs map[string]chan models.BaseMessage
	// Messages dispatched in this process whose turn has not finished. Kept in memory only:
	// after a restart they are not done, so recovery or the poller runs them again.
	inFlight    map[string]time.Time
	lastCleanup time.Time
	mutex       sync.Mutex
	handler     *MessageHandler
	workerPool  *workerpool.WorkerPool
	appState    *models.AppState
}

// NewJobDispatcher creates a new JobDispatcher instance.
func NewJobDispatcher(
	handler *MessageHandler,
	workerPool *workerpool.WorkerPool,
	appState *models.AppState,
) *JobDispatcher {
	return &JobDispatcher{
		activeJobs:  make(map[string]chan models.BaseMessage),
		inFlight:    make(map[string]time.Time),
		lastCleanup: time.Now(),
		handler:     handler,
		workerPool:  workerPool,
		appState:    appState,
	}
}

// Dispatch routes a message to the appropriate per-job channel.
// If no channel exists for the job, it creates one and starts a worker goroutine.
// A message whose turn already finished, or that is already queued or running, is skipped.
func (d *JobDispatcher) Dispatch(msg models.BaseMessage) {
	processedMsgID := d.extractProcessedMessageID(msg)
	if processedMsgID != "" {
		d.maybeCleanupDoneMessages()
		if d.appState.IsMessageDone(processedMsgID) {
			log.Info("🔁 Message %s already handled, skipping", processedMsgID)
			return
		}
		d.mutex.Lock()
		if _, running := d.inFlight[processedMsgID]; running {
			d.mutex.Unlock()
			log.Info("🔁 Duplicate message %s, skipping", processedMsgID)
			return
		}
		d.inFlight[processedMsgID] = time.Now()
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

	// The send is non-blocking and happens under the lock, so a finishing worker cannot close
	// the channel in between.
	d.mutex.Lock()
	ch, exists := d.activeJobs[jobID]
	if !exists {
		ch = make(chan models.BaseMessage, 100)
		d.activeJobs[jobID] = ch
		log.Info("🔀 Created per-job channel for job %s", jobID)
	}
	select {
	case ch <- msg:
		log.Info("📥 Queued message to job %s channel", jobID)
	default:
		// Not acked, so the poller offers it again once the job has room.
		delete(d.inFlight, processedMsgID)
		log.Error("❌ Job %s channel is full, message %s left for the next poll", jobID, processedMsgID)
	}
	d.mutex.Unlock()

	if !exists {
		d.workerPool.Submit(func() {
			d.processJobMessages(jobID, ch)
		})
	}
}

// processJobMessages processes messages sequentially for a specific job
func (d *JobDispatcher) processJobMessages(jobID string, ch chan models.BaseMessage) {
	log.Info("🔄 Started message processor for job %s", jobID)

	defer d.cleanup(jobID, ch)

	for msg := range ch {
		log.Info("🔧 Processing message for job %s", jobID)
		d.handler.HandleMessage(msg)
		d.ForgetMessage(d.extractProcessedMessageID(msg))

		// Check if job was removed from AppState
		jobData, exists := d.appState.GetJobData(jobID)
		if !exists {
			log.Info("✅ Job %s removed, exiting processor", jobID)
			return
		}

		// If job is completed or failed AND no more messages buffered, exit
		// This ensures we process all queued messages before exiting
		if (jobData.Status == models.JobStatusCompleted || jobData.Status == models.JobStatusFailed) &&
			d.closeIfEmpty(jobID, ch) {
			log.Info("✅ Job %s %s and channel empty, exiting processor", jobID, jobData.Status)
			return
		}
	}

	log.Info("📤 Message processor for job %s exited (channel closed)", jobID)
}

func (d *JobDispatcher) cleanup(jobID string, ch chan models.BaseMessage) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.closeLocked(jobID, ch)
}

// closeIfEmpty checks and closes under the same lock Dispatch sends under, so a message
// queued while the processor decides to exit is processed instead of closed in.
func (d *JobDispatcher) closeIfEmpty(jobID string, ch chan models.BaseMessage) bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if len(ch) > 0 {
		return false
	}
	d.closeLocked(jobID, ch)
	return true
}

// closeLocked closes the job's channel only while it is still ch: a processor of an evicted
// job may exit after a newer channel for the same job was created.
func (d *JobDispatcher) closeLocked(jobID string, ch chan models.BaseMessage) {
	if d.activeJobs[jobID] != ch {
		return
	}
	delete(d.activeJobs, jobID)
	close(ch)
	log.Info("🧹 Cleaned up channel for job %s", jobID)
}

// IsMessageDone reports whether the message's turn already finished.
func (d *JobDispatcher) IsMessageDone(processedMsgID string) bool {
	return d.appState.IsMessageDone(processedMsgID)
}

// IsMessageInFlight reports whether the message is queued or running in this process.
func (d *JobDispatcher) IsMessageInFlight(processedMsgID string) bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	_, running := d.inFlight[processedMsgID]
	return running
}

// ForgetMessage clears the in-flight mark, so a later delivery of the message can run it.
func (d *JobDispatcher) ForgetMessage(processedMsgID string) {
	if processedMsgID == "" {
		return
	}
	d.mutex.Lock()
	defer d.mutex.Unlock()
	delete(d.inFlight, processedMsgID)
}

// EvictJob removes a job from the dispatcher, closing its channel so an idle processor exits and
// frees its worker slot. Call it from outside the job's processor: a processor evicting its own job
// keeps draining the closed channel while the next message starts a second processor.
func (d *JobDispatcher) EvictJob(jobID string) {
	log.Info("🚫 Evicting job %s from dispatcher", jobID)
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if ch, exists := d.activeJobs[jobID]; exists {
		d.closeLocked(jobID, ch)
	}
}

// HasQueuedMessages reports whether a message is waiting in the job's channel. A processor blocked
// on an empty channel does not count: that job has nothing left to do.
func (d *JobDispatcher) HasQueuedMessages(jobID string) bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	ch, exists := d.activeJobs[jobID]
	return exists && len(ch) > 0
}

// DropQueuedMessages empties the job's channel and returns what was waiting in it.
func (d *JobDispatcher) DropQueuedMessages(jobID string) []models.BaseMessage {
	d.mutex.Lock()
	ch, exists := d.activeJobs[jobID]
	d.mutex.Unlock()
	if !exists {
		return nil
	}
	var dropped []models.BaseMessage
	for {
		select {
		case msg, open := <-ch:
			if !open {
				return dropped
			}
			// The backend ended it; a later delivery of the same id must be able to run.
			d.ForgetMessage(d.extractProcessedMessageID(msg))
			dropped = append(dropped, msg)
		default:
			return dropped
		}
	}
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

func (d *JobDispatcher) maybeCleanupDoneMessages() {
	d.mutex.Lock()
	now := time.Now()
	if now.Sub(d.lastCleanup) < cleanupInterval {
		d.mutex.Unlock()
		return
	}
	d.lastCleanup = now
	d.mutex.Unlock()

	if err := d.appState.CleanupDoneMessages(now.Add(-doneMessageTTL)); err != nil {
		log.Warn("⚠️ Failed to persist done messages cleanup: %v", err)
	}
}
