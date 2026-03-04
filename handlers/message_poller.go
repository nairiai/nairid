package handlers

import (
	"encoding/json"
	"time"

	"eksecd/clients"
	"eksecd/core/log"
	"eksecd/models"
)

// MessagePoller periodically polls the backend HTTP API for pending messages
// and dispatches them through the JobDispatcher. WebSocket nudge events
// can trigger an immediate poll.
type MessagePoller struct {
	apiClient    *clients.AgentsApiClient
	dispatcher   *JobDispatcher
	pollInterval time.Duration
	nudgeChan    chan struct{}
	stopChan     chan struct{}
}

// NewMessagePoller creates a new MessagePoller
func NewMessagePoller(
	apiClient *clients.AgentsApiClient,
	dispatcher *JobDispatcher,
	pollInterval time.Duration,
) *MessagePoller {
	return &MessagePoller{
		apiClient:    apiClient,
		dispatcher:   dispatcher,
		pollInterval: pollInterval,
		nudgeChan:    make(chan struct{}, 1),
		stopChan:     make(chan struct{}),
	}
}

// Run starts the polling loop. It blocks until Stop() is called.
func (mp *MessagePoller) Run() {
	log.Info("📡 MessagePoller: Started with %v poll interval", mp.pollInterval)
	ticker := time.NewTicker(mp.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			mp.pollAndDispatch()
		case <-mp.nudgeChan:
			mp.pollAndDispatch()
			ticker.Reset(mp.pollInterval)
		case <-mp.stopChan:
			log.Info("📡 MessagePoller: Stopped")
			return
		}
	}
}

// Nudge triggers an immediate poll. Non-blocking — if a nudge is already pending, this is a no-op.
func (mp *MessagePoller) Nudge() {
	select {
	case mp.nudgeChan <- struct{}{}:
		log.Info("📡 MessagePoller: Nudge received, triggering immediate poll")
	default:
		// Already a nudge pending, skip
	}
}

// Stop stops the polling loop
func (mp *MessagePoller) Stop() {
	close(mp.stopChan)
}

func (mp *MessagePoller) pollAndDispatch() {
	resp, err := mp.apiClient.PollMessages()
	if err != nil {
		log.Warn("📡 MessagePoller: Failed to poll messages: %v", err)
		return
	}

	if len(resp.Messages) == 0 {
		return
	}

	log.Info("📡 MessagePoller: Received %d pending message(s)", len(resp.Messages))

	for _, pendingMsg := range resp.Messages {
		// Acknowledge the message so it won't be returned by future polls
		if err := mp.apiClient.AckMessage(pendingMsg.ID); err != nil {
			log.Warn("📡 MessagePoller: Failed to ack message %s: %v", pendingMsg.ID, err)
			// Continue anyway — dedup in JobDispatcher will handle re-delivery
		}

		// Unmarshal the BaseMessage from the payload
		var baseMsg models.BaseMessage
		if err := json.Unmarshal(pendingMsg.MessagePayload, &baseMsg); err != nil {
			log.Error("📡 MessagePoller: Failed to unmarshal message payload for %s: %v", pendingMsg.ID, err)
			continue
		}

		// Dispatch through the same JobDispatcher used by WebSocket messages
		mp.dispatcher.Dispatch(baseMsg)
	}
}
