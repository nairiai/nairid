package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"nairid/clients"
	"nairid/core"
	"nairid/core/env"
	"nairid/models"
	"nairid/usecases"

	"github.com/gammazero/workerpool"
)

const hourMs = int64(time.Hour / time.Millisecond)

func killedAtDeadline(timeout time.Duration) error {
	return fmt.Errorf("failed to continue Claude session: %w", &core.ErrClaudeCommandErr{
		Err:    &core.ErrSessionTimeout{Timeout: timeout, Err: errors.New("signal: killed")},
		Output: "Now I'll loop on the review findings until the reviewer is clean.",
	})
}

func TestLatestSessionTimeoutMs(t *testing.T) {
	tests := []struct {
		name            string
		message, stored int64
		want            int64
	}{
		{"a value on the message replaces the stored one", 2 * hourMs, hourMs, 2 * hourMs},
		{"no value on the message keeps the stored one", 0, hourMs, hourMs},
		{"no value anywhere stays none", 0, 0, 0},
	}
	for _, tt := range tests {
		if got := latestSessionTimeoutMs(tt.message, tt.stored); got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestErrorMessageText(t *testing.T) {
	got := errorMessageText(fmt.Errorf("error continuing Claude session: %w", killedAtDeadline(time.Hour)))
	want := "nairid encountered error: nairid stopped this run after it hit the 1h session timeout. Raise it in the agent's Advanced settings."
	if got != want {
		t.Errorf("timeout text = %q, want %q", got, want)
	}

	got = errorMessageText(errors.New("git push failed"))
	if got != "nairid encountered error: git push failed" {
		t.Errorf("other errors keep their text: got %q", got)
	}
}

func TestStartPayloadForUnstartedJob_SessionTimeout(t *testing.T) {
	stored := &models.JobData{JobID: "j1", SessionTimeoutMs: hourMs}
	tests := []struct {
		name  string
		reply int64
		job   *models.JobData
		want  int64
	}{
		{"the job's stored value when the reply has none", 0, stored, hourMs},
		{"the reply's value over the stored one", 2 * hourMs, stored, 2 * hourMs},
		{"the reply's value for a job this agent never saw", 2 * hourMs, nil, 2 * hourMs},
	}
	for _, tt := range tests {
		reply := models.UserMessagePayload{JobID: "j1", Message: "continue", ProcessedMessageID: "cmsg_1", SessionTimeoutMs: tt.reply}
		if got := startPayloadForUnstartedJob(reply, tt.job).SessionTimeoutMs; got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}

// The agent's setting rides the start and every later reply that carries it, survives a
// restart through the state file, and a new value on a reply replaces the stored one.
func TestSessionTimeoutReachesEveryTurnOfAConversation(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	t.Setenv("NAIRI_MAX_SESSION_MS", "")
	t.Setenv("EKSEC_MAX_SESSION_MS", "")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()
	statePath := filepath.Join(t.TempDir(), "state.json")
	agent := &effortRecordingAgent{model: "gpt-5.5"}

	messages := 0
	reply := func(handler *MessageHandler, sessionTimeoutMs int64) {
		t.Helper()
		messages++
		err := handler.handleUserMessage(context.Background(), models.BaseMessage{
			ID:   fmt.Sprintf("msg_%d", messages),
			Type: models.MessageTypeUserMessage,
			Payload: models.UserMessagePayload{
				JobID: "j1", Message: "and again", ProcessedMessageID: fmt.Sprintf("cmsg_%d", messages),
				SessionTimeoutMs: sessionTimeoutMs,
			},
		})
		if err != nil {
			t.Fatalf("reply %d: %v", messages, err)
		}
	}

	handler := startHandlerLikeAFreshProcess(t, agent, statePath, backend.URL)
	err := handler.handleStartConversation(context.Background(), models.BaseMessage{
		ID:   "msg_0",
		Type: models.MessageTypeStartConversation,
		Payload: models.StartConversationPayload{
			JobID: "j1", Message: "hello", ProcessedMessageID: "cmsg_0", SessionTimeoutMs: 2 * hourMs,
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	reply(handler, 0)

	handler = startHandlerLikeAFreshProcess(t, agent, statePath, backend.URL)
	reply(handler, 0)
	reply(handler, 4*hourMs)
	reply(handler, 0)
	reply(handler, int64(time.Minute/time.Millisecond))

	want := []time.Duration{
		2 * time.Hour,
		2 * time.Hour,
		2 * time.Hour, // after a restart
		4 * time.Hour, // the agent's setting was raised
		4 * time.Hour,
		10 * time.Minute, // clamped up to the minimum
	}
	if !reflect.DeepEqual(agent.timeouts, want) {
		t.Errorf("turns ran with timeouts %v, want %v", agent.timeouts, want)
	}
	if job, _ := handler.appState.GetJobData("j1"); job.SessionTimeoutMs != int64(time.Minute/time.Millisecond) {
		t.Errorf("stored timeout = %dms, want the latest value the backend sent", job.SessionTimeoutMs)
	}
}

func TestSessionTimeoutWithoutAnAgentValueUsesTheMachineSetting(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	t.Setenv("NAIRI_MAX_SESSION_MS", "1800000")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()
	agent := &effortRecordingAgent{model: "gpt-5.5"}
	handler := startHandlerLikeAFreshProcess(t, agent, filepath.Join(t.TempDir(), "state.json"), backend.URL)

	err := handler.handleStartConversation(context.Background(), models.BaseMessage{
		ID:      "msg_0",
		Type:    models.MessageTypeStartConversation,
		Payload: models.StartConversationPayload{JobID: "j1", Message: "hello", ProcessedMessageID: "cmsg_0"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if !reflect.DeepEqual(agent.timeouts, []time.Duration{30 * time.Minute}) {
		t.Errorf("turn ran with timeouts %v, want [30m] from NAIRI_MAX_SESSION_MS", agent.timeouts)
	}
}

// "Raise it, then reply continue": the retry of a first turn that hit the timeout runs on the new value.
func TestRaisingTheTimeoutAfterAFirstTurnTimeoutAppliesToTheRetry(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()
	agent := &effortRecordingAgent{model: "gpt-5.5", nextTurnErr: killedAtDeadline(time.Hour)}
	handler := startHandlerLikeAFreshProcess(t, agent, filepath.Join(t.TempDir(), "state.json"), backend.URL)

	err := handler.handleStartConversation(context.Background(), models.BaseMessage{
		ID:      "msg_0",
		Type:    models.MessageTypeStartConversation,
		Payload: models.StartConversationPayload{JobID: "j1", Message: "hello", ProcessedMessageID: "cmsg_0", SessionTimeoutMs: hourMs},
	})
	if _, ok := core.IsSessionTimeout(err); !ok {
		t.Fatalf("start returned %v, want the session timeout", err)
	}
	err = handler.handleUserMessage(context.Background(), models.BaseMessage{
		ID:      "msg_1",
		Type:    models.MessageTypeUserMessage,
		Payload: models.UserMessagePayload{JobID: "j1", Message: "continue", ProcessedMessageID: "cmsg_1", SessionTimeoutMs: 4 * hourMs},
	})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}

	if !reflect.DeepEqual(agent.timeouts, []time.Duration{time.Hour, 4 * time.Hour}) {
		t.Errorf("turns ran with timeouts %v, want [1h 4h]", agent.timeouts)
	}
	if job, _ := handler.appState.GetJobData("j1"); job.SessionTimeoutMs != 4*hourMs {
		t.Errorf("stored timeout = %dms, want %d", job.SessionTimeoutMs, 4*hourMs)
	}
}

// handlerWithOutbox is startHandlerLikeAFreshProcess, keeping what the handler sends.
func handlerWithOutbox(t *testing.T, agent *effortRecordingAgent) (*MessageHandler, *outbox) {
	t.Helper()
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(backend.Close)
	appState, _, err := RestoreAppState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("restore state: %v", err)
	}
	envManager, err := env.NewEnvManager()
	if err != nil {
		t.Fatalf("env manager: %v", err)
	}
	apiClient := clients.NewAgentsApiClient("test-key", backend.URL, "agent")
	sender := NewMessageSender(NewConnectionState(), apiClient)
	box := &outbox{}
	go func() {
		for outgoing := range sender.messageQueue {
			if msg, ok := outgoing.Data.(models.BaseMessage); ok {
				box.mu.Lock()
				box.messages = append(box.messages, msg)
				box.mu.Unlock()
			}
		}
	}()
	t.Cleanup(sender.Close)
	return NewMessageHandler(agent, usecases.NewGitUseCase(nil, agent, appState), appState, envManager, sender, apiClient), box
}

// systemMessagesAfter waits for the turn's report and gives a second post the time to show up.
func systemMessagesAfter(t *testing.T, box *outbox, processedMessageID string) []string {
	t.Helper()
	waitFor(t, box, models.MessageTypeSystemMessage)
	time.Sleep(200 * time.Millisecond)
	var texts []string
	for _, msg := range box.ofType(models.MessageTypeSystemMessage) {
		if payload, ok := msg.Payload.(models.SystemMessagePayload); ok && payload.ProcessedMessageID == processedMessageID {
			texts = append(texts, payload.Message)
		}
	}
	return texts
}

func TestASessionTimeoutIsPostedOnceAndSaysWhatHappened(t *testing.T) {
	want := []string{"nairid encountered error: nairid stopped this run after it hit the 2h session timeout. Raise it in the agent's Advanced settings."}

	t.Run("first turn", func(t *testing.T) {
		agent := &effortRecordingAgent{model: "gpt-5.5", nextTurnErr: killedAtDeadline(2 * time.Hour)}
		handler, box := handlerWithOutbox(t, agent)

		handler.HandleMessage(models.BaseMessage{
			ID:      "msg_0",
			Type:    models.MessageTypeStartConversation,
			Payload: models.StartConversationPayload{JobID: "j1", Message: "hello", ProcessedMessageID: "cmsg_0", SessionTimeoutMs: 2 * hourMs},
		})

		if got := systemMessagesAfter(t, box, "cmsg_0"); !reflect.DeepEqual(got, want) {
			t.Errorf("thread got %q, want %q", got, want)
		}
	})

	t.Run("later turn", func(t *testing.T) {
		agent := &effortRecordingAgent{model: "gpt-5.5"}
		handler, box := handlerWithOutbox(t, agent)
		handler.HandleMessage(models.BaseMessage{
			ID:      "msg_0",
			Type:    models.MessageTypeStartConversation,
			Payload: models.StartConversationPayload{JobID: "j1", Message: "hello", ProcessedMessageID: "cmsg_0", SessionTimeoutMs: 2 * hourMs},
		})
		agent.nextTurnErr = killedAtDeadline(2 * time.Hour)

		handler.HandleMessage(models.BaseMessage{
			ID:      "msg_1",
			Type:    models.MessageTypeUserMessage,
			Payload: models.UserMessagePayload{JobID: "j1", Message: "and again", ProcessedMessageID: "cmsg_1"},
		})

		got := systemMessagesAfter(t, box, "cmsg_1")
		if !reflect.DeepEqual(got, want) {
			t.Errorf("thread got %q, want %q", got, want)
		}
		for _, text := range got {
			if strings.Contains(text, "loop on the review findings") {
				t.Errorf("the post quotes the agent's last progress note: %q", text)
			}
		}
	})
}

// A job this agent has never seen is started from the poll with the job's stored value,
// unless the message carries a newer one.
func TestPolledJobStartsWithItsSessionTimeout(t *testing.T) {
	tests := []struct {
		name      string
		onMessage int64
		want      time.Duration
	}{
		{"the job's value", 0, 2 * time.Hour},
		{"the message's newer value", 4 * hourMs, 4 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &effortRecordingAgent{model: "gpt-5.5"}
			handler, box := handlerWithOutbox(t, agent)
			pool := workerpool.New(1)
			defer pool.Stop()
			dispatcher := NewJobDispatcher(handler, pool, handler.appState)
			handler.SetJobEvictor(dispatcher)

			payload, _ := json.Marshal(models.UserMessagePayload{
				JobID: "j1", Message: "hello", ProcessedMessageID: "cmsg_0", SessionTimeoutMs: tt.onMessage,
			})
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/ack") {
					return
				}
				_ = json.NewEncoder(w).Encode(clients.AgentJobsResponse{Jobs: []clients.AgentJob{{
					ID: "j1", Mode: "execute", SessionTimeoutMs: 2 * hourMs,
					Messages: []clients.AgentJobMessage{{ID: "msg_0", Type: models.MessageTypeUserMessage, Payload: payload}},
				}}})
			}))
			defer backend.Close()

			NewMessagePoller(clients.NewAgentsApiClient("k", backend.URL, "agent"), dispatcher, handler, handler.appState, time.Hour).pollAndDispatch()

			deadline := time.Now().Add(5 * time.Second)
			for !answered(box, "cmsg_0") {
				if time.Now().After(deadline) {
					t.Fatal("the polled message was not answered")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !reflect.DeepEqual(agent.timeouts, []time.Duration{tt.want}) {
				t.Errorf("turn ran with timeouts %v, want [%s]", agent.timeouts, tt.want)
			}
		})
	}
}
