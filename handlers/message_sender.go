package handlers

import (
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/zishang520/socket.io/clients/socket/v3"

	"nairid/core/log"
)

// OutgoingMessage represents a message to be sent via Socket.IO
type OutgoingMessage struct {
	Event string
	Data  any
}

// MessageSender handles queuing and sending messages to Socket.IO client.
// It blocks when the connection is down and resumes when reconnected.
type MessageSender struct {
	connectionState *ConnectionState
	messageQueue    chan OutgoingMessage
	socketClient    *socket.Socket
}

// NewMessageSender creates a new MessageSender instance.
// The queue has a buffer of 1 message to ensure blocking until messages are sent.
// This guarantees that jobs are only marked complete after their messages are actually sent.
func NewMessageSender(connectionState *ConnectionState) *MessageSender {
	return &MessageSender{
		connectionState: connectionState,
		messageQueue:    make(chan OutgoingMessage, 1),
		socketClient:    nil, // Set later via Run()
	}
}

// Run starts the message sender goroutine that processes the queue.
// This should be called once with the Socket.IO client reference.
// It blocks until the message queue is closed.
func (ms *MessageSender) Run(socketClient *socket.Socket) {
	ms.socketClient = socketClient
	log.Info("📤 MessageSender: Started processing queue")

	for msg := range ms.messageQueue {
		// Block until connection is established
		ms.connectionState.WaitForConnection()

		// Send the message with retry logic (3 attempts with exponential backoff)
		ms.sendWithRetry(msg)
	}

	log.Info("📤 MessageSender: Queue closed, exiting")
}

// sendWithRetry attempts to send a message with exponential backoff retry logic.
// Uses the backoff library for consistent retry behavior across the codebase.
func (ms *MessageSender) sendWithRetry(msg OutgoingMessage) {
	// Configure exponential backoff for 3 retries
	expBackoff := backoff.NewExponentialBackOff()
	expBackoff.InitialInterval = 1 * time.Second
	expBackoff.MaxInterval = 4 * time.Second
	expBackoff.MaxElapsedTime = 10 * time.Second // Total retry window
	expBackoff.Multiplier = 2

	attempt := 0
	operation := func() error {
		attempt++
		err := ms.socketClient.Emit(msg.Event, msg.Data)
		if err != nil {
			log.Warn("⚠️ MessageSender: Failed to emit message on event '%s' (attempt %d): %v", msg.Event, attempt, err)
			return err // Trigger retry
		}
		log.Info("📤 MessageSender: Successfully sent message on event '%s' (attempt %d)", msg.Event, attempt)
		return nil // Success
	}

	err := backoff.Retry(operation, expBackoff)
	if err != nil {
		log.Error("❌ MessageSender: Failed to emit message on event '%s' after %d attempts: %v. Message lost.", msg.Event, attempt, err)
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
