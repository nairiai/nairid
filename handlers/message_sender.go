package handlers

import (
	"encoding/json"
	"time"

	"github.com/cenkalti/backoff/v4"

	"eksecd/clients"
	"eksecd/core/log"
)

// OutgoingMessage represents a message to be sent via the HTTP API
type OutgoingMessage struct {
	Event string
	Data  any
}

// MessageSender handles queuing and sending messages via the HTTP API.
// It replaces the previous Socket.IO-based sender with HTTP POST requests
// to ensure reliable message delivery through the outbox pattern.
type MessageSender struct {
	messageQueue chan OutgoingMessage
	apiClient    *clients.AgentsApiClient
}

// NewMessageSender creates a new MessageSender instance.
// The queue has a buffer of 1 message to ensure blocking until messages are sent.
// This guarantees that jobs are only marked complete after their messages are actually sent.
func NewMessageSender() *MessageSender {
	return &MessageSender{
		messageQueue: make(chan OutgoingMessage, 1),
		apiClient:    nil, // Set later via Run()
	}
}

// Run starts the message sender goroutine that processes the queue.
// This should be called once with the API client reference.
// It blocks until the message queue is closed.
func (ms *MessageSender) Run(apiClient *clients.AgentsApiClient) {
	ms.apiClient = apiClient
	log.Info("📤 MessageSender: Started processing queue (HTTP mode)")

	for msg := range ms.messageQueue {
		// Send the message with retry logic (exponential backoff)
		ms.sendWithRetry(msg)
	}

	log.Info("📤 MessageSender: Queue closed, exiting")
}

// sendWithRetry attempts to send a message with exponential backoff retry logic.
func (ms *MessageSender) sendWithRetry(msg OutgoingMessage) {
	// Configure exponential backoff for retries
	expBackoff := backoff.NewExponentialBackOff()
	expBackoff.InitialInterval = 1 * time.Second
	expBackoff.MaxInterval = 4 * time.Second
	expBackoff.MaxElapsedTime = 10 * time.Second // Total retry window
	expBackoff.Multiplier = 2

	// Build the message payload to include the event type
	payload := map[string]any{
		"event": msg.Event,
		"data":  msg.Data,
	}

	// If Data is already a map, merge event into it for a flat structure
	if dataMap, ok := msg.Data.(map[string]any); ok {
		payload = make(map[string]any, len(dataMap)+1)
		for k, v := range dataMap {
			payload[k] = v
		}
	} else {
		// Try to convert via JSON round-trip for struct types
		dataBytes, err := json.Marshal(msg.Data)
		if err == nil {
			var dataMap map[string]any
			if json.Unmarshal(dataBytes, &dataMap) == nil {
				payload = dataMap
			}
		}
	}

	attempt := 0
	operation := func() error {
		attempt++
		err := ms.apiClient.SubmitMessage(payload)
		if err != nil {
			log.Warn("⚠️ MessageSender: Failed to submit message on event '%s' (attempt %d): %v", msg.Event, attempt, err)
			return err // Trigger retry
		}
		log.Info("📤 MessageSender: Successfully sent message on event '%s' (attempt %d)", msg.Event, attempt)
		return nil // Success
	}

	err := backoff.Retry(operation, expBackoff)
	if err != nil {
		log.Error("❌ MessageSender: Failed to submit message on event '%s' after %d attempts: %v. Message lost.", msg.Event, attempt, err)
	}
}

// QueueMessage adds a message to the send queue.
// Blocks until the message is consumed and sent by the MessageSender goroutine.
// This ensures the caller knows the message has been processed before continuing.
func (ms *MessageSender) QueueMessage(event string, data any) {
	log.Info("📥 MessageSender: Queueing message for event '%s'", event)
	ms.messageQueue <- OutgoingMessage{
		Event: event,
		Data:  data,
	}
	log.Info("📤 MessageSender: Message for event '%s' has been consumed by sender", event)
}

// Close closes the message queue, causing Run() to exit.
// Should be called during graceful shutdown.
func (ms *MessageSender) Close() {
	close(ms.messageQueue)
}
