package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gammazero/workerpool"

	"eksecd/clients"
	"eksecd/models"
)

func newTestPollerWithDispatcher(t *testing.T, handler http.HandlerFunc, pollInterval time.Duration) (*MessagePoller, *httptest.Server, *JobDispatcher) {
	t.Helper()
	server := httptest.NewServer(handler)
	apiClient := clients.NewAgentsApiClient("test-key", server.URL, "test-agent")

	wp := workerpool.New(4)
	t.Cleanup(func() { wp.StopWait() })
	appState := createTestAppStateNoPath()
	// Pass nil handler — we'll observe dispatched messages via channels
	dispatcher := NewJobDispatcher(nil, wp, appState)

	poller := NewMessagePoller(apiClient, dispatcher, pollInterval)
	return poller, server, dispatcher
}

func TestMessagePoller_PollAndDispatch_Success(t *testing.T) {
	msg := models.BaseMessage{
		ID:   "msg_001",
		Type: models.MessageTypeStartConversation,
		Payload: map[string]any{
			"job_id":               "j_001",
			"message":              "hello",
			"processed_message_id": "pm_001",
		},
	}
	msgPayload, _ := json.Marshal(msg)

	var ackCount atomic.Int32
	var pollCount atomic.Int32

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/agents/messages":
			count := pollCount.Add(1)
			// Only return message on first poll to avoid repeated dispatches
			if count == 1 {
				resp := clients.PollMessagesResponse{
					Messages: []clients.PendingMessage{
						{
							ID:             "pm_test_001",
							MessagePayload: msgPayload,
						},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			} else {
				resp := clients.PollMessagesResponse{Messages: []clients.PendingMessage{}}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			}
		case r.Method == "POST" && r.URL.Path == "/api/agents/messages/pm_test_001/ack":
			ackCount.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	poller, server, dispatcher := newTestPollerWithDispatcher(t, handler, 100*time.Millisecond)
	defer server.Close()

	// Pre-create a job channel so we can observe the dispatched message
	ch := make(chan models.BaseMessage, 100)
	dispatcher.mutex.Lock()
	dispatcher.activeJobs["j_001"] = ch
	dispatcher.mutex.Unlock()

	go poller.Run()
	defer poller.Stop()

	// Wait for message to be dispatched to job channel
	select {
	case received := <-ch:
		if received.ID != "msg_001" {
			t.Errorf("expected message ID msg_001, got %s", received.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for dispatched message")
	}

	// Verify ack was called
	if ackCount.Load() == 0 {
		t.Error("expected ack to be called at least once")
	}
}

func TestMessagePoller_PollAndDispatch_NoMessages(t *testing.T) {
	var pollCount atomic.Int32

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/agents/messages" {
			pollCount.Add(1)
			resp := clients.PollMessagesResponse{Messages: []clients.PendingMessage{}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	poller, server, _ := newTestPollerWithDispatcher(t, handler, 50*time.Millisecond)
	defer server.Close()

	go poller.Run()
	defer poller.Stop()

	// Wait for a few polls
	time.Sleep(200 * time.Millisecond)

	if pollCount.Load() < 2 {
		t.Errorf("expected at least 2 polls, got %d", pollCount.Load())
	}
}

func TestMessagePoller_Nudge_TriggersImmediatePoll(t *testing.T) {
	pollChan := make(chan struct{}, 10)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/agents/messages" {
			pollChan <- struct{}{}
			resp := clients.PollMessagesResponse{Messages: []clients.PendingMessage{}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	// Use a very long poll interval so only nudge triggers polls
	poller, server, _ := newTestPollerWithDispatcher(t, handler, 10*time.Minute)
	defer server.Close()

	go poller.Run()
	defer poller.Stop()

	// Nudge should trigger an immediate poll
	poller.Nudge()

	select {
	case <-pollChan:
		// Poll triggered by nudge
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for nudge-triggered poll")
	}
}

func TestMessagePoller_Nudge_NonBlocking(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := clients.PollMessagesResponse{Messages: []clients.PendingMessage{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	poller, server, _ := newTestPollerWithDispatcher(t, handler, 10*time.Minute)
	defer server.Close()

	// Don't start Run() — nudge channel should still accept without blocking
	poller.Nudge() // First nudge fills the buffer
	poller.Nudge() // Second nudge should be a no-op, not block
	// If we reach here without hanging, the test passes
}

func TestMessagePoller_Stop(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := clients.PollMessagesResponse{Messages: []clients.PendingMessage{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	poller, server, _ := newTestPollerWithDispatcher(t, handler, 50*time.Millisecond)
	defer server.Close()

	done := make(chan struct{})
	go func() {
		poller.Run()
		close(done)
	}()

	poller.Stop()

	select {
	case <-done:
		// Run() exited cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for poller to stop")
	}
}

func TestMessagePoller_PollError_ContinuesPolling(t *testing.T) {
	var callCount atomic.Int32

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := callCount.Add(1)

		if count == 1 {
			// First call returns error
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Subsequent calls succeed
		resp := clients.PollMessagesResponse{Messages: []clients.PendingMessage{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	poller, server, _ := newTestPollerWithDispatcher(t, handler, 50*time.Millisecond)
	defer server.Close()

	go poller.Run()
	defer poller.Stop()

	// Wait for multiple polls
	time.Sleep(300 * time.Millisecond)

	if callCount.Load() < 3 {
		t.Errorf("expected at least 3 poll attempts (including error recovery), got %d", callCount.Load())
	}
}

func TestMessagePoller_AckFailure_StillDispatches(t *testing.T) {
	msg := models.BaseMessage{
		ID:   "msg_002",
		Type: models.MessageTypeUserMessage,
		Payload: map[string]any{
			"job_id":               "j_002",
			"message":              "test",
			"processed_message_id": "pm_002",
		},
	}
	msgPayload, _ := json.Marshal(msg)

	var pollCount atomic.Int32
	mu := sync.Mutex{}
	_ = mu

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/agents/messages":
			count := pollCount.Add(1)
			if count == 1 {
				resp := clients.PollMessagesResponse{
					Messages: []clients.PendingMessage{
						{
							ID:             "pm_test_002",
							MessagePayload: msgPayload,
						},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			} else {
				resp := clients.PollMessagesResponse{Messages: []clients.PendingMessage{}}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			}
		case r.Method == "POST":
			// Ack fails
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	poller, server, dispatcher := newTestPollerWithDispatcher(t, handler, 100*time.Millisecond)
	defer server.Close()

	// Pre-create a job channel to capture dispatched messages
	ch := make(chan models.BaseMessage, 100)
	dispatcher.mutex.Lock()
	dispatcher.activeJobs["j_002"] = ch
	dispatcher.mutex.Unlock()

	go poller.Run()
	defer poller.Stop()

	// Wait for message to be dispatched despite ack failure
	select {
	case received := <-ch:
		if received.ID != "msg_002" {
			t.Errorf("expected message ID msg_002, got %s", received.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected message to be dispatched even when ack fails")
	}
}

func TestMessagePoller_InvalidPayload_SkipsMessage(t *testing.T) {
	var pollCount atomic.Int32

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/agents/messages" {
			count := pollCount.Add(1)
			if count == 1 {
				resp := clients.PollMessagesResponse{
					Messages: []clients.PendingMessage{
						{
							ID:             "pm_bad",
							MessagePayload: json.RawMessage(`not valid json`),
						},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			} else {
				resp := clients.PollMessagesResponse{Messages: []clients.PendingMessage{}}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			}
			return
		}
		// Ack should still be called even for bad payload
		w.WriteHeader(http.StatusOK)
	})

	poller, server, _ := newTestPollerWithDispatcher(t, handler, 100*time.Millisecond)
	defer server.Close()

	go poller.Run()
	defer poller.Stop()

	// Wait a bit — should not panic on invalid payload
	time.Sleep(300 * time.Millisecond)
}
