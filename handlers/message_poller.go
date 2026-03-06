package handlers

import (
	"encoding/json"
	"sync"
	"time"

	"nairid/clients"
	"nairid/core/log"
	"nairid/models"
)

const (
	// maxConsecutiveFailures caps the backoff multiplier
	maxConsecutiveFailures = 3 // 30s → 60s → 120s → 120s
)

// MessagePoller polls the HTTP API for unacked messages as a redundancy layer.
// WS remains the primary real-time channel; polling catches any missed messages.
type MessagePoller struct {
	apiClient           *clients.AgentsApiClient
	dispatcher          *JobDispatcher
	messageHandler      *MessageHandler
	appState            *models.AppState
	pollInterval        time.Duration
	stopChan            chan struct{}
	consecutiveFailures int
	once                sync.Once
}

// NewMessagePoller creates a new MessagePoller instance.
func NewMessagePoller(
	apiClient *clients.AgentsApiClient,
	dispatcher *JobDispatcher,
	messageHandler *MessageHandler,
	appState *models.AppState,
	pollInterval time.Duration,
) *MessagePoller {
	return &MessagePoller{
		apiClient:      apiClient,
		dispatcher:     dispatcher,
		messageHandler: messageHandler,
		appState:       appState,
		pollInterval:   pollInterval,
		stopChan:       make(chan struct{}),
	}
}

// Start begins the polling loop in a goroutine.
// Safe to call multiple times (e.g. on WS reconnect) — only the first call spawns the goroutine.
func (mp *MessagePoller) Start() {
	mp.once.Do(func() {
		go mp.run()
	})
}

func (mp *MessagePoller) run() {
	log.Info("📡 MessagePoller: Started polling every %s", mp.pollInterval)

	for {
		interval := mp.currentInterval()
		timer := time.NewTimer(interval)

		select {
		case <-timer.C:
			mp.pollAndDispatch()
		case <-mp.stopChan:
			timer.Stop()
			log.Info("📡 MessagePoller: Stopped")
			return
		}
	}
}

// currentInterval returns the poll interval with exponential backoff on failures.
func (mp *MessagePoller) currentInterval() time.Duration {
	if mp.consecutiveFailures == 0 {
		return mp.pollInterval
	}

	backoff := mp.pollInterval
	failures := mp.consecutiveFailures
	if failures > maxConsecutiveFailures {
		failures = maxConsecutiveFailures
	}
	for i := 0; i < failures; i++ {
		backoff *= 2
	}

	return backoff
}

func (mp *MessagePoller) pollAndDispatch() {
	log.Info("📡 MessagePoller: Polling for unacked messages...")

	resp, err := mp.apiClient.FetchAgentJobs()
	if err != nil {
		mp.consecutiveFailures++
		nextInterval := mp.currentInterval()
		log.Warn("📡 MessagePoller: Failed to fetch agent jobs (retry in %s): %v", nextInterval, err)
		return
	}

	mp.consecutiveFailures = 0

	totalMessages := 0
	for _, job := range resp.Jobs {
		for range job.Messages {
			totalMessages++
		}
	}
	log.Info("📡 MessagePoller: Fetched %d jobs, %d unacked messages", len(resp.Jobs), totalMessages)

	for _, job := range resp.Jobs {
		_, jobExists := mp.appState.GetJobData(job.ID)
		log.Info("📡 MessagePoller: Processing job %s (exists_locally=%v, messages=%d)", job.ID, jobExists, len(job.Messages))

		for _, msg := range job.Messages {
			if msg.Type == "" {
				continue
			}

			var baseMsg models.BaseMessage
			if !jobExists {
				// Job not in local state — upgrade to start_conversation
				log.Info("📡 MessagePoller: Upgrading message %s to start_conversation (job %s not in local state)", msg.ID, job.ID)

				var userPayload models.UserMessagePayload
				if err := json.Unmarshal(msg.Payload, &userPayload); err != nil {
					log.Error("📡 MessagePoller: Failed to unmarshal user payload for start_conversation upgrade: %v", err)
					continue
				}

				startPayload := models.StartConversationPayload{
					JobID:              userPayload.JobID,
					Message:            userPayload.Message,
					ProcessedMessageID: userPayload.ProcessedMessageID,
					MessageLink:        userPayload.MessageLink,
					Mode:               models.AgentMode(job.Mode),
					Attachments:        userPayload.Attachments,
					PreviousMessages:   userPayload.PreviousMessages,
					SenderMetadata:     userPayload.SenderMetadata,
					SystemPrompt:       job.SystemPrompt,
				}

				enrichedPayload, err := json.Marshal(startPayload)
				if err != nil {
					log.Error("📡 MessagePoller: Failed to marshal start_conversation payload: %v", err)
					continue
				}

				baseMsg = models.BaseMessage{
					ID:      msg.ID,
					Type:    models.MessageTypeStartConversation,
					Payload: json.RawMessage(enrichedPayload),
				}
				jobExists = true // subsequent messages in same batch are user_message
			} else {
				log.Info("📡 MessagePoller: Dispatching message %s as user_message (job %s exists locally)", msg.ID, job.ID)
				baseMsg = models.BaseMessage{
					ID:      msg.ID,
					Type:    models.MessageTypeUserMessage,
					Payload: msg.Payload,
				}
			}

			// Persist and dispatch through same pipeline as WS messages
			if err := mp.messageHandler.PersistQueuedMessage(baseMsg); err != nil {
				log.Error("📡 MessagePoller: Failed to persist queued message: %v", err)
			}
			mp.dispatcher.Dispatch(baseMsg)
			log.Info("📡 MessagePoller: Dispatched message %s (type=%s)", msg.ID, baseMsg.Type)

			// Ack the message — failure is non-fatal, will be re-polled
			if err := mp.apiClient.AckMessage(msg.ID); err != nil {
				log.Warn("📡 MessagePoller: Failed to ack message %s: %v", msg.ID, err)
			}
		}
	}
}

// Stop gracefully stops the polling loop.
func (mp *MessagePoller) Stop() {
	close(mp.stopChan)
}
