package handlers

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/zishang520/socket.io/clients/socket/v3"

	"nairid/clients"
	"nairid/core/log"
	"nairid/models"
)

// OutgoingMessage represents a message to be sent via Socket.IO or HTTP
type OutgoingMessage struct {
	Event string
	Data  any
}

// httpSubmitter sends messages via the HTTP API (for testability).
type httpSubmitter interface {
	SubmitMessage(msg models.BaseMessage) error
}

// wsEmitter sends messages via WebSocket (for testability).
type wsEmitter interface {
	Emit(ev string, args ...any) error
}

// MessageSender handles queuing and sending messages via WS or HTTP.
// processing_message_v1 is sent via WS (real-time UX signal).
// All other response types are sent via HTTP, with WebSocket fallback on failure.
type MessageSender struct {
	connectionState *ConnectionState
	messageQueue    chan OutgoingMessage
	progressQueue   chan OutgoingMessage
	socketClient    wsEmitter
	socketMu        sync.RWMutex
	apiClient       httpSubmitter
	once            sync.Once
}

// Compile-time interface assertions
var (
	_ httpSubmitter = (*clients.AgentsApiClient)(nil)
	_ wsEmitter     = (*socket.Socket)(nil)
)

// NewMessageSender creates a new MessageSender instance.
// The queue has a buffer of 1 message to ensure blocking until messages are sent.
// This guarantees that jobs are only marked complete after their messages are actually sent.
func NewMessageSender(connectionState *ConnectionState, apiClient *clients.AgentsApiClient) *MessageSender {
	return &MessageSender{
		connectionState: connectionState,
		messageQueue:    make(chan OutgoingMessage, 1),
		progressQueue:   make(chan OutgoingMessage, 1000),
		socketClient:    nil, // Set later via Run()
		apiClient:       apiClient,
	}
}

// newMessageSenderForTest creates a MessageSender with injectable dependencies (for testing).
func newMessageSenderForTest(connectionState *ConnectionState, apiClient httpSubmitter, ws wsEmitter) *MessageSender {
	return &MessageSender{
		connectionState: connectionState,
		messageQueue:    make(chan OutgoingMessage, 1),
		progressQueue:   make(chan OutgoingMessage, 1000),
		socketClient:    ws,
		apiClient:       apiClient,
	}
}

// Run starts the message sender goroutines that process the queues.
// Safe to call multiple times (e.g. on WS reconnect) — only the first call spawns goroutines.
// Subsequent calls update the socket client reference for the existing goroutines.
func (ms *MessageSender) Run(socketClient *socket.Socket) {
	ms.SetSocketClient(socketClient)

	ms.once.Do(func() {
		go ms.processQueue()
		go ms.runProgressSender()
	})
}

// SetSocketClient updates the socket client reference (thread-safe).
func (ms *MessageSender) SetSocketClient(socketClient *socket.Socket) {
	ms.socketMu.Lock()
	ms.socketClient = socketClient
	ms.socketMu.Unlock()
}

// getSocketClient returns the current socket client reference (thread-safe).
func (ms *MessageSender) getSocketClient() wsEmitter {
	ms.socketMu.RLock()
	defer ms.socketMu.RUnlock()
	return ms.socketClient
}

func (ms *MessageSender) processQueue() {
	log.Info("📤 MessageSender: Started processing queue")

	for msg := range ms.messageQueue {
		if ms.isWSMessage(msg) {
			ms.connectionState.WaitForConnection()
			ms.sendWSWithRetry(msg)
		} else {
			ms.sendHTTPWithRetry(msg)
		}
	}

	log.Info("📤 MessageSender: Queue closed, exiting")
}

// isWSMessage checks if the message should be sent via WS (processing_message_v1 only)
func (ms *MessageSender) isWSMessage(msg OutgoingMessage) bool {
	msgBytes, err := json.Marshal(msg.Data)
	if err != nil {
		return true // fallback to WS on marshal error
	}

	var baseMsg models.BaseMessage
	if err := json.Unmarshal(msgBytes, &baseMsg); err != nil {
		return true // fallback to WS on unmarshal error
	}

	return baseMsg.Type == models.MessageTypeProcessingMessage
}

// sendWSWithRetry sends a message via WebSocket with exponential backoff retry logic.
func (ms *MessageSender) sendWSWithRetry(msg OutgoingMessage) {
	expBackoff := backoff.NewExponentialBackOff()
	expBackoff.InitialInterval = 1 * time.Second
	expBackoff.MaxInterval = 4 * time.Second
	expBackoff.MaxElapsedTime = 10 * time.Second
	expBackoff.Multiplier = 2

	attempt := 0
	operation := func() error {
		attempt++
		err := ms.getSocketClient().Emit(msg.Event, msg.Data)
		if err != nil {
			log.Warn("⚠️ MessageSender: Failed to emit message on event '%s' (attempt %d): %v", msg.Event, attempt, err)
			return err
		}
		log.Info("📤 MessageSender: Successfully sent message via WS on event '%s' (attempt %d)", msg.Event, attempt)
		return nil
	}

	err := backoff.Retry(operation, expBackoff)
	if err != nil {
		log.Error("❌ MessageSender: Failed to emit message on event '%s' after %d attempts: %v. Message lost.", msg.Event, attempt, err)
	}
}

// sendHTTPWithRetry sends a message via HTTP with exponential backoff retry logic.
// If all HTTP attempts fail, it falls back to sending via WebSocket.
func (ms *MessageSender) sendHTTPWithRetry(msg OutgoingMessage) {
	msgBytes, err := json.Marshal(msg.Data)
	if err != nil {
		log.Error("❌ MessageSender: Failed to marshal message for HTTP: %v", err)
		return
	}

	var baseMsg models.BaseMessage
	if err := json.Unmarshal(msgBytes, &baseMsg); err != nil {
		log.Error("❌ MessageSender: Failed to unmarshal BaseMessage for HTTP: %v", err)
		return
	}

	expBackoff := backoff.NewExponentialBackOff()
	expBackoff.InitialInterval = 1 * time.Second
	expBackoff.MaxInterval = 4 * time.Second
	expBackoff.MaxElapsedTime = 10 * time.Second
	expBackoff.Multiplier = 2

	attempt := 0
	operation := func() error {
		attempt++
		err := ms.apiClient.SubmitMessage(baseMsg)
		if err != nil {
			log.Warn("⚠️ MessageSender: Failed to submit message via HTTP (attempt %d): %v", attempt, err)
			return err
		}
		log.Info("📤 MessageSender: Successfully sent message via HTTP (attempt %d, type: %s)", attempt, baseMsg.Type)
		return nil
	}

	err = backoff.Retry(operation, expBackoff)
	if err != nil {
		log.Warn("⚠️ MessageSender: HTTP failed after %d attempts: %v. Falling back to WebSocket.", attempt, err)
		ms.sendWSFallback(msg)
	}
}

// sendWSFallback attempts to deliver a message via WebSocket after HTTP delivery has failed.
// It waits for an active WebSocket connection and retries with exponential backoff.
func (ms *MessageSender) sendWSFallback(msg OutgoingMessage) {
	ms.connectionState.WaitForConnection()
	ms.sendWSWithRetry(msg)
}

// runProgressSender processes the progress queue in a separate goroutine.
func (ms *MessageSender) runProgressSender() {
	for msg := range ms.progressQueue {
		ms.sendHTTPWithRetry(msg)
	}
}

// QueueProgressMessage sends a progress message without blocking.
// If the channel is full, the message is dropped (acceptable for progress).
func (ms *MessageSender) QueueProgressMessage(event string, data any) {
	select {
	case ms.progressQueue <- OutgoingMessage{Event: event, Data: data}:
	default:
		log.Warn("⚠️ MessageSender: Progress queue full, dropping message")
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
	close(ms.progressQueue)
}
