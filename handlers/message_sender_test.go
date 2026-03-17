package handlers

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nairid/models"
)

// mockHTTP implements httpSubmitter for testing.
type mockHTTP struct {
	mu        sync.Mutex
	calls     []models.BaseMessage
	failCount int // number of times to fail before succeeding; -1 = always fail
	callCount int
}

func (m *mockHTTP) SubmitMessage(msg models.BaseMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	m.calls = append(m.calls, msg)
	if m.failCount == -1 || m.callCount <= m.failCount {
		return fmt.Errorf("API returned status 403: blocked by WAF")
	}
	return nil
}

func (m *mockHTTP) getCalls() []models.BaseMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]models.BaseMessage, len(m.calls))
	copy(result, m.calls)
	return result
}

// mockWS implements wsEmitter for testing.
type mockWS struct {
	mu        sync.Mutex
	calls     []mockWSCall
	failCount int // number of times to fail before succeeding; -1 = always fail
	callCount int
}

type mockWSCall struct {
	Event string
	Args  []any
}

func (m *mockWS) Emit(ev string, args ...any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	m.calls = append(m.calls, mockWSCall{Event: ev, Args: args})
	if m.failCount == -1 || m.callCount <= m.failCount {
		return fmt.Errorf("websocket emit failed")
	}
	return nil
}

func (m *mockWS) getCalls() []mockWSCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]mockWSCall, len(m.calls))
	copy(result, m.calls)
	return result
}

func makeTestMessage(msgType string) OutgoingMessage {
	return OutgoingMessage{
		Event: "cc_message",
		Data: models.BaseMessage{
			ID:   "msg_test123",
			Type: msgType,
			Payload: models.AssistantMessagePayload{
				JobID:   "job_test",
				Message: "hello world",
			},
		},
	}
}

func TestIsWSMessage(t *testing.T) {
	ms := newMessageSenderForTest(NewConnectionState(), &mockHTTP{}, &mockWS{})

	tests := []struct {
		name     string
		msgType  string
		expected bool
	}{
		{"processing message goes via WS", models.MessageTypeProcessingMessage, true},
		{"assistant message goes via HTTP", models.MessageTypeAssistantMessage, false},
		{"system message goes via HTTP", models.MessageTypeSystemMessage, false},
		{"job complete goes via HTTP", models.MessageTypeJobComplete, false},
		{"agent progress goes via HTTP", models.MessageTypeAgentProgress, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := makeTestMessage(tt.msgType)
			result := ms.isWSMessage(msg)
			if result != tt.expected {
				t.Errorf("isWSMessage(%s) = %v, want %v", tt.msgType, result, tt.expected)
			}
		})
	}
}

func TestSendHTTPWithRetry_Success(t *testing.T) {
	httpMock := &mockHTTP{failCount: 0}
	wsMock := &mockWS{}
	ms := newMessageSenderForTest(NewConnectionState(), httpMock, wsMock)

	msg := makeTestMessage(models.MessageTypeAssistantMessage)
	ms.sendHTTPWithRetry(msg)

	httpCalls := httpMock.getCalls()
	if len(httpCalls) != 1 {
		t.Fatalf("expected 1 HTTP call, got %d", len(httpCalls))
	}
	if httpCalls[0].Type != models.MessageTypeAssistantMessage {
		t.Errorf("expected message type %s, got %s", models.MessageTypeAssistantMessage, httpCalls[0].Type)
	}

	wsCalls := wsMock.getCalls()
	if len(wsCalls) != 0 {
		t.Errorf("expected no WS fallback calls, got %d", len(wsCalls))
	}
}

func TestSendHTTPWithRetry_FallsBackToWS(t *testing.T) {
	cs := NewConnectionState()
	cs.SetConnected(true)

	httpMock := &mockHTTP{failCount: -1} // always fail HTTP
	wsMock := &mockWS{failCount: 0}      // WS succeeds on first call
	ms := newMessageSenderForTest(cs, httpMock, wsMock)

	msg := makeTestMessage(models.MessageTypeAssistantMessage)
	ms.sendHTTPWithRetry(msg)

	// HTTP should have been attempted multiple times
	httpCalls := httpMock.getCalls()
	if len(httpCalls) < 2 {
		t.Fatalf("expected at least 2 HTTP attempts, got %d", len(httpCalls))
	}

	// WS fallback should have been called exactly once (successful on first try)
	wsCalls := wsMock.getCalls()
	if len(wsCalls) != 1 {
		t.Fatalf("expected 1 WS fallback call, got %d", len(wsCalls))
	}
	if wsCalls[0].Event != "cc_message" {
		t.Errorf("expected WS event 'cc_message', got '%s'", wsCalls[0].Event)
	}
}

func TestSendHTTPWithRetry_FallbackWaitsForConnection(t *testing.T) {
	cs := NewConnectionState()
	// Connection starts as disconnected

	httpMock := &mockHTTP{failCount: -1}
	wsMock := &mockWS{failCount: 0}
	ms := newMessageSenderForTest(cs, httpMock, wsMock)

	var done atomic.Bool
	go func() {
		msg := makeTestMessage(models.MessageTypeAssistantMessage)
		ms.sendHTTPWithRetry(msg)
		done.Store(true)
	}()

	// Wait for HTTP retries to exhaust (max elapsed ~10s), then check WS hasn't been called
	time.Sleep(12 * time.Second)

	wsCalls := wsMock.getCalls()
	if len(wsCalls) != 0 {
		t.Fatalf("expected no WS calls while disconnected, got %d", len(wsCalls))
	}
	if done.Load() {
		t.Fatal("sendHTTPWithRetry should still be blocked waiting for WS connection")
	}

	// Establish the connection
	cs.SetConnected(true)

	// Wait for the fallback to complete
	deadline := time.After(15 * time.Second)
	for {
		if done.Load() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for WS fallback to complete after connection established")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	wsCalls = wsMock.getCalls()
	if len(wsCalls) != 1 {
		t.Fatalf("expected 1 WS call after connection, got %d", len(wsCalls))
	}
}

func TestSendHTTPWithRetry_HTTPSucceedsAfterRetries(t *testing.T) {
	cs := NewConnectionState()
	cs.SetConnected(true)

	httpMock := &mockHTTP{failCount: 2} // fail twice, succeed on third
	wsMock := &mockWS{}
	ms := newMessageSenderForTest(cs, httpMock, wsMock)

	msg := makeTestMessage(models.MessageTypeAssistantMessage)
	ms.sendHTTPWithRetry(msg)

	httpCalls := httpMock.getCalls()
	if len(httpCalls) != 3 {
		t.Fatalf("expected 3 HTTP calls, got %d", len(httpCalls))
	}

	// WS should NOT have been called since HTTP eventually succeeded
	wsCalls := wsMock.getCalls()
	if len(wsCalls) != 0 {
		t.Errorf("expected no WS fallback calls, got %d", len(wsCalls))
	}
}

func TestProcessQueue_HTTPMessageFallsBackToWS(t *testing.T) {
	cs := NewConnectionState()
	cs.SetConnected(true)

	httpMock := &mockHTTP{failCount: -1}
	wsMock := &mockWS{failCount: 0}
	ms := newMessageSenderForTest(cs, httpMock, wsMock)

	go ms.processQueue()

	msg := models.BaseMessage{
		ID:   "msg_queue_test",
		Type: models.MessageTypeAssistantMessage,
		Payload: models.AssistantMessagePayload{
			JobID:   "job_test",
			Message: "queued message",
		},
	}
	ms.QueueMessage("cc_message", msg)

	// Wait for processing (HTTP retries + WS fallback)
	time.Sleep(15 * time.Second)

	httpCalls := httpMock.getCalls()
	if len(httpCalls) < 2 {
		t.Fatalf("expected at least 2 HTTP attempts, got %d", len(httpCalls))
	}

	wsCalls := wsMock.getCalls()
	if len(wsCalls) != 1 {
		t.Fatalf("expected 1 WS fallback call, got %d", len(wsCalls))
	}

	ms.Close()
}

func TestSendWSWithRetry_Success(t *testing.T) {
	wsMock := &mockWS{failCount: 0}
	ms := newMessageSenderForTest(NewConnectionState(), &mockHTTP{}, wsMock)

	msg := makeTestMessage(models.MessageTypeProcessingMessage)
	ms.sendWSWithRetry(msg)

	wsCalls := wsMock.getCalls()
	if len(wsCalls) != 1 {
		t.Fatalf("expected 1 WS call, got %d", len(wsCalls))
	}
	if wsCalls[0].Event != "cc_message" {
		t.Errorf("expected event 'cc_message', got '%s'", wsCalls[0].Event)
	}
}

func TestSendWSWithRetry_RetriesOnFailure(t *testing.T) {
	wsMock := &mockWS{failCount: 2} // fail twice, succeed on third
	ms := newMessageSenderForTest(NewConnectionState(), &mockHTTP{}, wsMock)

	msg := makeTestMessage(models.MessageTypeProcessingMessage)
	ms.sendWSWithRetry(msg)

	wsCalls := wsMock.getCalls()
	if len(wsCalls) != 3 {
		t.Fatalf("expected 3 WS calls (2 failures + 1 success), got %d", len(wsCalls))
	}
}
