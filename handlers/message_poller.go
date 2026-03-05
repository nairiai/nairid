package handlers

import (
	"time"

	"nairid/clients"
	"nairid/core/log"
	"nairid/models"
)

// MessagePoller polls the HTTP API for unacked messages as a redundancy layer.
// WS remains the primary real-time channel; polling catches any missed messages.
type MessagePoller struct {
	apiClient      *clients.AgentsApiClient
	dispatcher     *JobDispatcher
	messageHandler *MessageHandler
	pollInterval   time.Duration
	stopChan       chan struct{}
}

// NewMessagePoller creates a new MessagePoller instance.
func NewMessagePoller(
	apiClient *clients.AgentsApiClient,
	dispatcher *JobDispatcher,
	messageHandler *MessageHandler,
	pollInterval time.Duration,
) *MessagePoller {
	return &MessagePoller{
		apiClient:      apiClient,
		dispatcher:     dispatcher,
		messageHandler: messageHandler,
		pollInterval:   pollInterval,
		stopChan:       make(chan struct{}),
	}
}

// Start begins the polling loop in a goroutine.
func (mp *MessagePoller) Start() {
	go mp.run()
}

func (mp *MessagePoller) run() {
	ticker := time.NewTicker(mp.pollInterval)
	defer ticker.Stop()

	log.Info("📡 MessagePoller: Started polling every %s", mp.pollInterval)

	for {
		select {
		case <-ticker.C:
			mp.pollAndDispatch()
		case <-mp.stopChan:
			log.Info("📡 MessagePoller: Stopped")
			return
		}
	}
}

func (mp *MessagePoller) pollAndDispatch() {
	resp, err := mp.apiClient.FetchAgentJobs()
	if err != nil {
		log.Warn("📡 MessagePoller: Failed to fetch agent jobs: %v", err)
		return
	}

	for _, job := range resp.Jobs {
		for _, msg := range job.Messages {
			if msg.Type == "" {
				continue
			}

			baseMsg := models.BaseMessage{
				ID:      msg.ID,
				Type:    msg.Type,
				Payload: msg.Payload,
			}

			// Persist and dispatch through same pipeline as WS messages
			switch baseMsg.Type {
			case models.MessageTypeStartConversation, models.MessageTypeUserMessage:
				if err := mp.messageHandler.PersistQueuedMessage(baseMsg); err != nil {
					log.Error("📡 MessagePoller: Failed to persist queued message: %v", err)
				}
				mp.dispatcher.Dispatch(baseMsg)
			default:
				mp.dispatcher.Dispatch(baseMsg)
			}

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
