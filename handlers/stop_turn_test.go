package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nairid/clients"
	"nairid/core"
	"nairid/core/env"
	"nairid/models"
	"nairid/services"
	"nairid/usecases"
)

// stoppableAgent stands in for a CLI service: a turn either finishes at once or
// waits to be stopped, and a stopped turn reports what the CLI printed by then.
type stoppableAgent struct {
	services.CLIAgent
	mu               sync.Mutex
	blockNext        bool
	sessionWhenDone  string
	sessionWhenStopd string
	started          chan struct{}
}

func (a *stoppableAgent) run(ctx context.Context, sessionID string) (*services.CLIAgentResult, error) {
	a.mu.Lock()
	block := a.blockNext
	a.blockNext = false
	a.mu.Unlock()
	if !block {
		return &services.CLIAgentResult{Output: "done", SessionID: sessionID}, nil
	}
	close(a.started)
	<-ctx.Done()
	return nil, &core.ErrTurnStopped{SessionID: a.sessionWhenStopd}
}

func (a *stoppableAgent) StartNewConversationWithProgress(
	ctx context.Context, prompt, systemPrompt, workDir, effort string, emitter services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	return a.run(ctx, a.sessionWhenDone)
}

func (a *stoppableAgent) ContinueConversationWithProgress(
	ctx context.Context, sessionID, prompt, systemPrompt, workDir, effort string, emitter services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	return a.run(ctx, sessionID)
}

func (a *stoppableAgent) Model() string     { return "" }
func (a *stoppableAgent) AgentName() string { return "claude" }

type outbox struct {
	mu       sync.Mutex
	messages []models.BaseMessage
}

func (o *outbox) ofType(messageType string) []models.BaseMessage {
	o.mu.Lock()
	defer o.mu.Unlock()
	var found []models.BaseMessage
	for _, msg := range o.messages {
		if msg.Type == messageType {
			found = append(found, msg)
		}
	}
	return found
}

type stopFixture struct {
	handler *MessageHandler
	agent   *stoppableAgent
	outbox  *outbox
	acked   func() []string
}

func newStopFixture(t *testing.T) *stopFixture {
	t.Helper()
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	var ackMu sync.Mutex
	var acked []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ack") {
			parts := strings.Split(r.URL.Path, "/")
			ackMu.Lock()
			acked = append(acked, parts[len(parts)-2])
			ackMu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
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

	agent := &stoppableAgent{sessionWhenDone: "session-1", started: make(chan struct{})}
	handler := NewMessageHandler(agent, usecases.NewGitUseCase(nil, agent, appState), appState, envManager, sender, apiClient)
	return &stopFixture{
		handler: handler,
		agent:   agent,
		outbox:  box,
		acked: func() []string {
			ackMu.Lock()
			defer ackMu.Unlock()
			return append([]string(nil), acked...)
		},
	}
}

func startMessage(id string) models.BaseMessage {
	return models.BaseMessage{
		ID:   "msg_" + id,
		Type: models.MessageTypeStartConversation,
		Payload: models.StartConversationPayload{
			JobID: "j1", Message: "hello", ProcessedMessageID: "cmsg_" + id, Mode: models.AgentModeAsk,
		},
	}
}

func replyMessage(id string) models.BaseMessage {
	return models.BaseMessage{
		ID:      "msg_" + id,
		Type:    models.MessageTypeUserMessage,
		Payload: models.UserMessagePayload{JobID: "j1", Message: "and again", ProcessedMessageID: "cmsg_" + id},
	}
}

func waitForStop(t *testing.T, box *outbox) models.TurnStoppedPayload {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if stops := box.ofType(models.MessageTypeTurnStopped); len(stops) > 0 {
			payload, ok := stops[len(stops)-1].Payload.(models.TurnStoppedPayload)
			if !ok {
				t.Fatalf("turn stopped payload is a %T", stops[len(stops)-1].Payload)
			}
			return payload
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no turn_stopped_v1 message was sent")
	return models.TurnStoppedPayload{}
}

func TestStopTurn_EndsAFirstTurnAndKeepsItsSession(t *testing.T) {
	f := newStopFixture(t)
	f.agent.blockNext = true
	f.agent.sessionWhenStopd = "session-partial"

	done := make(chan error, 1)
	go func() { done <- f.handler.handleStartConversation(startMessage("0")) }()
	<-f.agent.started
	f.handler.StopTurn("j1", "U1", "2026-10-21T20:00:00Z")

	if err := <-done; err != nil {
		t.Fatalf("a stopped turn must not be reported as an error, got: %v", err)
	}
	stop := waitForStop(t, f.outbox)
	if stop.Outcome != models.TurnStopOutcomeStopped || stop.SessionID != "session-partial" || stop.ProcessedMessageID != "cmsg_0" {
		t.Errorf("turn_stopped = %+v", stop)
	}
	job, _ := f.handler.appState.GetJobData("j1")
	if job.Status != models.JobStatusCompleted || job.ClaudeSessionID != "session-partial" {
		t.Errorf("job after stop = status %q session %q, want completed / session-partial", job.Status, job.ClaudeSessionID)
	}
	if got := f.outbox.ofType(models.MessageTypeSystemMessage); len(got) != 0 {
		t.Errorf("no system message belongs in a stopped turn, got %+v", got[0].Payload)
	}
	if got := f.outbox.ofType(models.MessageTypeAssistantMessage); len(got) != 0 {
		t.Errorf("no answer belongs in a stopped turn, got %+v", got[0].Payload)
	}
	if acked := f.acked(); len(acked) != 1 || acked[0] != "cmsg_0" {
		t.Errorf("acked %v, want [cmsg_0]", acked)
	}

	// The conversation goes on from the session the stopped turn left behind.
	if err := f.handler.handleUserMessage(replyMessage("1")); err != nil {
		t.Fatalf("reply after stop: %v", err)
	}
	job, _ = f.handler.appState.GetJobData("j1")
	if job.ClaudeSessionID != "session-partial" || job.Status != models.JobStatusCompleted {
		t.Errorf("job after the next reply = status %q session %q", job.Status, job.ClaudeSessionID)
	}
}

func TestStopTurn_KeepsThePreviousSessionWhenTheStoppedTurnHasNone(t *testing.T) {
	f := newStopFixture(t)
	if err := f.handler.handleStartConversation(startMessage("0")); err != nil {
		t.Fatalf("start: %v", err)
	}
	f.agent.blockNext = true

	done := make(chan error, 1)
	go func() { done <- f.handler.handleUserMessage(replyMessage("1")) }()
	<-f.agent.started
	f.handler.StopTurn("j1", "U1", "")

	if err := <-done; err != nil {
		t.Fatalf("stopped reply: %v", err)
	}
	stop := waitForStop(t, f.outbox)
	if stop.SessionID != "session-1" || stop.ProcessedMessageID != "cmsg_1" {
		t.Errorf("turn_stopped = %+v, want session-1 / cmsg_1", stop)
	}
	job, _ := f.handler.appState.GetJobData("j1")
	if job.ClaudeSessionID != "session-1" || job.Status != models.JobStatusCompleted {
		t.Errorf("job after stop = status %q session %q", job.Status, job.ClaudeSessionID)
	}
}

func TestStopTurn_AnswersNotRunningWhenNothingRuns(t *testing.T) {
	f := newStopFixture(t)
	if err := f.handler.handleStartConversation(startMessage("0")); err != nil {
		t.Fatalf("start: %v", err)
	}

	f.handler.StopTurn("j1", "U1", "2026-10-21T20:00:00Z")
	stop := waitForStop(t, f.outbox)
	if stop.Outcome != models.TurnStopOutcomeNotRunning || stop.SessionID != "session-1" {
		t.Errorf("turn_stopped = %+v, want not_running with session-1", stop)
	}

	// The same request delivered again (socket, then poll) is answered once.
	f.handler.StopTurn("j1", "U1", "2026-10-21T20:00:00Z")
	time.Sleep(50 * time.Millisecond)
	if got := f.outbox.ofType(models.MessageTypeTurnStopped); len(got) != 1 {
		t.Errorf("sent %d turn_stopped messages, want 1", len(got))
	}
}
