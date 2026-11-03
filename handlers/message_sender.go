package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/zishang520/socket.io/clients/socket/v3"

	"nairid/clients"
	"nairid/core"
	"nairid/core/log"
	"nairid/models"
)

// OutgoingMessage represents a message to be sent via Socket.IO or HTTP
type OutgoingMessage struct {
	Event string
	Data  any
	// outboxID is set when the message is persisted in the outbox and must be removed once delivered.
	outboxID string
}

// outboxStore persists replies so a container restart cannot lose them before delivery.
type outboxStore interface {
	AddOutboxMessage(msg models.OutboxMessage) error
	RemoveOutboxMessage(id string) error
	GetAllOutboxMessages() []models.OutboxMessage
}

const outboxRetryInterval = 60 * time.Second

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

	outbox        outboxStore
	pendingMu     sync.Mutex
	pendingOutbox map[string]bool
	wsTried       map[string]bool
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
		pendingOutbox:   make(map[string]bool),
		wsTried:         make(map[string]bool),
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
		pendingOutbox:   make(map[string]bool),
		wsTried:         make(map[string]bool),
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

// SetOutbox enables durable delivery. Must be called before Run.
func (ms *MessageSender) SetOutbox(store outboxStore) {
	ms.outbox = store
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

	// Replies left over from a previous run go out before new ones, keeping their order.
	ms.resendOutbox()
	ticker := time.NewTicker(outboxRetryInterval)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-ms.messageQueue:
			if !ok {
				log.Info("📤 MessageSender: Queue closed, exiting")
				return
			}
			ms.send(msg)
		case <-ticker.C:
			ms.resendOutbox()
		}
	}
}

func (ms *MessageSender) send(msg OutgoingMessage) {
	if ms.isWSMessage(msg) {
		ms.connectionState.WaitForConnection()
		ms.sendWSWithRetry(msg)
		return
	}
	if msg.outboxID == "" {
		ms.sendHTTPWithRetry(msg)
		return
	}
	// Older replies that failed earlier go first, so a job's replies keep their order.
	ms.resendOutbox()
	ms.deliverFromOutbox(msg)
}

// deliverFromOutbox submits an outbox reply over HTTP only: a WS emit is not acknowledged
// by the backend, so it cannot prove delivery. A failed reply stays for the next resend.
func (ms *MessageSender) deliverFromOutbox(msg OutgoingMessage) {
	err := ms.submitHTTPWithRetry(msg)
	var rejected *clients.StatusError
	if errors.As(err, &rejected) && rejected.Permanent() {
		log.Error("❌ MessageSender: Backend rejected outbox message %s, keeping it: %v", msg.outboxID, err)
		ms.emitRejectedOnce(msg)
	}
	if err != nil {
		log.Warn("⚠️ MessageSender: Outbox message %s not delivered, retrying in %s: %v", msg.outboxID, outboxRetryInterval, err)
	}
	ms.settleOutbox(msg.outboxID, err == nil)
}

// emitRejectedOnce sends a reply the HTTP endpoint refused over WebSocket, as the pre-outbox
// fallback did. Once per entry per process, since an emit is not a backend ack and the entry stays.
func (ms *MessageSender) emitRejectedOnce(msg OutgoingMessage) {
	if !ms.connectionState.IsConnected() {
		return
	}
	ms.pendingMu.Lock()
	tried := ms.wsTried[msg.outboxID]
	ms.wsTried[msg.outboxID] = true
	ms.pendingMu.Unlock()
	if tried {
		return
	}
	ms.sendWSWithRetry(msg)
}

// persistToOutbox stores the message before it is queued. Returns "" when there is no outbox or the write failed.
func (ms *MessageSender) persistToOutbox(event string, data any) string {
	if ms.outbox == nil {
		return ""
	}
	raw, err := json.Marshal(data)
	if err != nil {
		log.Warn("⚠️ MessageSender: Failed to marshal message for outbox: %v", err)
		return ""
	}
	var baseMsg models.BaseMessage
	_ = json.Unmarshal(raw, &baseMsg)
	id := baseMsg.ID
	if id == "" {
		id = core.NewID("out")
	}
	ms.markPending(id)
	entry := models.OutboxMessage{ID: id, Event: event, Data: raw, QueuedAt: time.Now()}
	if err := ms.outbox.AddOutboxMessage(entry); err != nil {
		log.Warn("⚠️ MessageSender: Failed to persist message %s to outbox: %v", id, err)
		ms.settleOutbox(id, false)
		return ""
	}
	return id
}

// markPending returns false if the outbox entry is already queued or being sent.
func (ms *MessageSender) markPending(id string) bool {
	ms.pendingMu.Lock()
	defer ms.pendingMu.Unlock()
	if ms.pendingOutbox[id] {
		return false
	}
	ms.pendingOutbox[id] = true
	return true
}

func (ms *MessageSender) settleOutbox(id string, delivered bool) {
	if id == "" || ms.outbox == nil {
		return
	}
	if delivered {
		if err := ms.outbox.RemoveOutboxMessage(id); err != nil {
			log.Warn("⚠️ MessageSender: Failed to remove delivered message %s from outbox: %v", id, err)
		}
	}
	ms.pendingMu.Lock()
	delete(ms.pendingOutbox, id)
	if delivered {
		delete(ms.wsTried, id)
	}
	ms.pendingMu.Unlock()
}

// resendOutbox retries every persisted reply that is not already on its way.
func (ms *MessageSender) resendOutbox() {
	if ms.outbox == nil {
		return
	}
	for _, entry := range ms.outbox.GetAllOutboxMessages() {
		if !ms.markPending(entry.ID) {
			continue
		}
		var data models.BaseMessage
		if err := json.Unmarshal(entry.Data, &data); err != nil {
			log.Error("❌ MessageSender: Dropping unreadable outbox message %s: %v", entry.ID, err)
			ms.settleOutbox(entry.ID, true)
			continue
		}
		log.Info("📤 MessageSender: Resending outbox message %s (queued %s)", entry.ID, entry.QueuedAt.Format(time.RFC3339))
		ms.deliverFromOutbox(OutgoingMessage{Event: entry.Event, Data: data, outboxID: entry.ID})
	}
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
// Returns true if the message was emitted.
func (ms *MessageSender) sendWSWithRetry(msg OutgoingMessage) bool {
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
		log.Error("❌ MessageSender: Failed to emit message on event '%s' after %d attempts: %v", msg.Event, attempt, err)
		return false
	}
	return true
}

// sendHTTPWithRetry sends a message via HTTP with exponential backoff retry logic.
// If all HTTP attempts fail, it falls back to sending via WebSocket.
func (ms *MessageSender) sendHTTPWithRetry(msg OutgoingMessage) {
	if err := ms.submitHTTPWithRetry(msg); err != nil {
		log.Warn("⚠️ MessageSender: HTTP failed: %v. Falling back to WebSocket.", err)
		ms.sendWSFallback(msg)
	}
}

// submitHTTPWithRetry submits a message via HTTP with exponential backoff. A malformed
// message returns a permanent error, since no retry can deliver it.
func (ms *MessageSender) submitHTTPWithRetry(msg OutgoingMessage) error {
	msgBytes, err := json.Marshal(msg.Data)
	if err != nil {
		return &clients.StatusError{Code: http.StatusBadRequest, Body: "marshal: " + err.Error()}
	}

	var baseMsg models.BaseMessage
	if err := json.Unmarshal(msgBytes, &baseMsg); err != nil {
		return &clients.StatusError{Code: http.StatusBadRequest, Body: "unmarshal: " + err.Error()}
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
		var rejected *clients.StatusError
		if errors.As(err, &rejected) && rejected.Permanent() {
			return backoff.Permanent(err)
		}
		if err != nil {
			log.Warn("⚠️ MessageSender: Failed to submit message via HTTP (attempt %d): %v", attempt, err)
			return err
		}
		log.Info("📤 MessageSender: Successfully sent message via HTTP (attempt %d, type: %s)", attempt, baseMsg.Type)
		return nil
	}

	if err := backoff.Retry(operation, expBackoff); err != nil {
		return fmt.Errorf("after %d attempts: %w", attempt, err)
	}
	return nil
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
	msg := OutgoingMessage{Event: event, Data: data}
	if !ms.isWSMessage(msg) {
		msg.outboxID = ms.persistToOutbox(event, data)
	}
	ms.messageQueue <- msg
	log.Info("📤 MessageSender: Message for event '%s' has been consumed by sender", event)
}

// QueueMessageLater persists a reply for the sender to deliver once it runs, without waiting.
// For callers that run before the sender starts, such as startup recovery.
func (ms *MessageSender) QueueMessageLater(event string, data any) {
	msg := OutgoingMessage{Event: event, Data: data}
	if !ms.isWSMessage(msg) {
		if id := ms.persistToOutbox(event, data); id != "" {
			// Not pending: the sender's first resend picks it up.
			ms.pendingMu.Lock()
			delete(ms.pendingOutbox, id)
			ms.pendingMu.Unlock()
			return
		}
	}
	go ms.QueueMessage(event, data)
}

// Close closes the message queue, causing Run() to exit.
// Should be called during graceful shutdown.
func (ms *MessageSender) Close() {
	close(ms.messageQueue)
	close(ms.progressQueue)
}
