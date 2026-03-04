package clients

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPollMessages_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/agents/messages" {
			t.Errorf("expected /api/agents/messages, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-AGENT-ID") != "agent-123" {
			t.Errorf("expected X-AGENT-ID agent-123, got %s", r.Header.Get("X-AGENT-ID"))
		}

		payload := json.RawMessage(`{"type":"start_conversation_v1","payload":{"job_id":"j_1"}}`)
		resp := PollMessagesResponse{
			Messages: []PendingMessage{
				{ID: "pm_1", MessagePayload: payload},
				{ID: "pm_2", MessagePayload: payload},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewAgentsApiClient("test-key", server.URL, "agent-123")
	resp, err := client.PollMessages()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(resp.Messages))
	}
	if resp.Messages[0].ID != "pm_1" {
		t.Errorf("expected pm_1, got %s", resp.Messages[0].ID)
	}
}

func TestPollMessages_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := PollMessagesResponse{Messages: []PendingMessage{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewAgentsApiClient("test-key", server.URL, "")
	resp, err := client.PollMessages()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(resp.Messages))
	}
}

func TestPollMessages_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	client := NewAgentsApiClient("test-key", server.URL, "")
	_, err := client.PollMessages()
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestPollMessages_NoAgentID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-AGENT-ID") != "" {
			t.Error("expected no X-AGENT-ID header when agentID is empty")
		}
		resp := PollMessagesResponse{Messages: []PendingMessage{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewAgentsApiClient("test-key", server.URL, "")
	_, err := client.PollMessages()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAckMessage_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/agents/messages/pm_test/ack" {
			t.Errorf("expected /api/agents/messages/pm_test/ack, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewAgentsApiClient("test-key", server.URL, "agent-123")
	err := client.AckMessage("pm_test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAckMessage_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer server.Close()

	client := NewAgentsApiClient("test-key", server.URL, "")
	err := client.AckMessage("pm_nonexistent")
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestSubmitMessage_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/agents/messages" {
			t.Errorf("expected /api/agents/messages, got %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json content type, got %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("X-AGENT-ID") != "agent-123" {
			t.Errorf("expected X-AGENT-ID agent-123, got %s", r.Header.Get("X-AGENT-ID"))
		}

		body, _ := io.ReadAll(r.Body)
		var msg map[string]any
		if err := json.Unmarshal(body, &msg); err != nil {
			t.Errorf("invalid JSON body: %v", err)
		}
		if msg["type"] != "assistant_message_v1" {
			t.Errorf("expected type assistant_message_v1, got %v", msg["type"])
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewAgentsApiClient("test-key", server.URL, "agent-123")
	msg := map[string]any{
		"id":   "msg_1",
		"type": "assistant_message_v1",
	}
	err := client.SubmitMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSubmitMessage_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer server.Close()

	client := NewAgentsApiClient("test-key", server.URL, "")
	err := client.SubmitMessage(map[string]any{"type": "test"})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestSubmitMessage_MarshalError(t *testing.T) {
	client := NewAgentsApiClient("test-key", "http://localhost", "")
	// Channel values can't be marshaled to JSON
	err := client.SubmitMessage(make(chan int))
	if err == nil {
		t.Fatal("expected marshal error")
	}
}
