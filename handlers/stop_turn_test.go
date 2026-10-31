package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gammazero/workerpool"

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
	mu                 sync.Mutex
	blockNext          bool
	sessionWhenDone    string
	sessionWhenStopped string
	started            chan struct{}
	startSystemPrompts []string
	name               string
	failWith           error
}

func (a *stoppableAgent) run(ctx context.Context, sessionID string) (*services.CLIAgentResult, error) {
	a.mu.Lock()
	block := a.blockNext
	a.blockNext = false
	failWith := a.failWith
	a.mu.Unlock()
	if failWith != nil {
		return nil, failWith
	}
	if !block {
		return &services.CLIAgentResult{Output: "done", SessionID: sessionID}, nil
	}
	close(a.started)
	<-ctx.Done()
	return nil, &core.ErrTurnStopped{SessionID: a.sessionWhenStopped}
}

func (a *stoppableAgent) StartNewConversationWithProgress(
	ctx context.Context, prompt, systemPrompt, workDir, effort string, _ time.Duration, emitter services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	a.mu.Lock()
	a.startSystemPrompts = append(a.startSystemPrompts, systemPrompt)
	a.mu.Unlock()
	return a.run(ctx, a.sessionWhenDone)
}

func (a *stoppableAgent) ContinueConversationWithProgress(
	ctx context.Context, sessionID, prompt, systemPrompt, workDir, effort string, _ time.Duration, emitter services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	return a.run(ctx, sessionID)
}

func (a *stoppableAgent) Model() string { return "" }

func (a *stoppableAgent) AgentName() string {
	if a.name != "" {
		return a.name
	}
	return "claude"
}

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
}

func newStopFixture(t *testing.T) *stopFixture {
	t.Helper()
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	return &stopFixture{handler: handler, agent: agent, outbox: box}
}

// mustHandle runs the message through the real entry point and fails on an error report.
func (f *stopFixture) mustHandle(t *testing.T, msg models.BaseMessage) {
	t.Helper()
	before := len(f.outbox.ofType(models.MessageTypeSystemMessage))
	f.handler.HandleMessage(msg)
	if after := f.outbox.ofType(models.MessageTypeSystemMessage); len(after) > before {
		t.Fatalf("handling %s reported an error: %+v", msg.Type, after[len(after)-1].Payload)
	}
}

func startMessage(id string) models.BaseMessage {
	return models.BaseMessage{
		ID:   "msg_" + id,
		Type: models.MessageTypeStartConversation,
		Payload: models.StartConversationPayload{
			JobID: "j1", Message: "hello", ProcessedMessageID: "cmsg_" + id, Mode: models.AgentModeAsk,
			SystemPrompt: "Org rule: never push to main.",
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

// The outbox is filled by a goroutine, so a message queued by the handler shows up a moment later.
func waitFor(t *testing.T, box *outbox, messageType string) []models.BaseMessage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if found := box.ofType(messageType); len(found) > 0 {
			return found
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %s message was sent", messageType)
	return nil
}

func waitForStop(t *testing.T, box *outbox) models.TurnStoppedPayload {
	t.Helper()
	stops := waitFor(t, box, models.MessageTypeTurnStopped)
	payload, ok := stops[len(stops)-1].Payload.(models.TurnStoppedPayload)
	if !ok {
		t.Fatalf("turn stopped payload is a %T", stops[len(stops)-1].Payload)
	}
	return payload
}

func TestStopTurn_EndsAFirstTurnAndKeepsItsSession(t *testing.T) {
	f := newStopFixture(t)
	f.agent.blockNext = true
	f.agent.sessionWhenStopped = "session-partial"

	done := make(chan struct{})
	go func() { f.handler.HandleMessage(startMessage("0")); close(done) }()
	<-f.agent.started
	f.handler.stopTurn("j1", "U1", "2026-10-21T20:00:00Z")

	<-done
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

	// The conversation goes on from the session the stopped turn left behind.
	f.mustHandle(t, replyMessage("1"))
	job, _ = f.handler.appState.GetJobData("j1")
	if job.ClaudeSessionID != "session-partial" || job.Status != models.JobStatusCompleted {
		t.Errorf("job after the next reply = status %q session %q", job.Status, job.ClaudeSessionID)
	}
}

func TestStopTurn_KeepsThePreviousSessionWhenTheStoppedTurnHasNone(t *testing.T) {
	f := newStopFixture(t)
	f.mustHandle(t, startMessage("0"))
	f.agent.blockNext = true

	done := make(chan struct{})
	go func() { f.handler.HandleMessage(replyMessage("1")); close(done) }()
	<-f.agent.started
	f.handler.stopTurn("j1", "U1", "")

	<-done
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
	f.mustHandle(t, startMessage("0"))

	f.handler.stopTurn("j1", "U1", "2026-10-21T20:00:00Z")
	stop := waitForStop(t, f.outbox)
	if stop.Outcome != models.TurnStopOutcomeNotRunning || stop.SessionID != "session-1" {
		t.Errorf("turn_stopped = %+v, want not_running with session-1", stop)
	}

	// The same request delivered again (socket, then poll) is answered once.
	f.handler.stopTurn("j1", "U1", "2026-10-21T20:00:00Z")
	time.Sleep(50 * time.Millisecond)
	if got := f.outbox.ofType(models.MessageTypeTurnStopped); len(got) != 1 {
		t.Errorf("sent %d turn_stopped messages, want 1", len(got))
	}
}

func TestStopTurn_KeepsAJobStoppedBeforeItHadASessionInItsMode(t *testing.T) {
	f := newStopFixture(t)
	f.agent.blockNext = true
	f.agent.sessionWhenStopped = ""

	done := make(chan struct{})
	go func() { f.handler.HandleMessage(startMessage("0")); close(done) }()
	<-f.agent.started
	f.handler.stopTurn("j1", "U1", "2026-10-22T10:00:00Z")

	<-done
	stop := waitForStop(t, f.outbox)
	if stop.Outcome != models.TurnStopOutcomeStopped || stop.SessionID != "" || stop.ProcessedMessageID != "cmsg_0" {
		t.Errorf("turn_stopped = %+v, want stopped with no session for cmsg_0", stop)
	}
	job, exists := f.handler.appState.GetJobData("j1")
	if !exists || job.ClaudeSessionID != "" || job.Status != models.JobStatusCompleted {
		t.Fatalf("job after stop = %+v, want it kept, completed, without a session", job)
	}

	// The backend sends the next message as a reply; it starts the conversation over in ask
	// mode with the organization's instructions.
	f.mustHandle(t, replyMessage("1"))
	assertStartedOverInAskMode(t, f)
}

func TestStopTurn_KeepsTheModeOfAFirstMessageDroppedBeforeItRan(t *testing.T) {
	f := newStopFixture(t)
	pool := workerpool.New(1)
	busy := make(chan struct{})
	pool.Submit(func() { <-busy })
	defer close(busy)
	dispatcher := NewJobDispatcher(f.handler, pool, f.handler.appState)
	f.handler.SetJobEvictor(dispatcher)
	dispatcher.Dispatch(startMessage("0"))

	f.handler.stopTurn("j1", "U1", "2026-10-22T10:00:00Z")

	if stop := waitForStop(t, f.outbox); stop.Outcome != models.TurnStopOutcomeStopped || stop.ProcessedMessageID != "cmsg_0" {
		t.Errorf("turn_stopped = %+v, want stopped for cmsg_0", stop)
	}
	f.mustHandle(t, replyMessage("1"))
	assertStartedOverInAskMode(t, f)
}

func assertStartedOverInAskMode(t *testing.T, f *stopFixture) {
	t.Helper()
	job, _ := f.handler.appState.GetJobData("j1")
	if job.Mode != models.AgentModeAsk || job.ClaudeSessionID != "session-1" {
		t.Errorf("job after the reply = mode %q session %q, want ask / session-1", job.Mode, job.ClaudeSessionID)
	}
	prompts := f.agent.startSystemPrompts
	if len(prompts) == 0 || !strings.Contains(prompts[len(prompts)-1], "Org rule: never push to main.") {
		t.Errorf("the conversation started over without the organization's system prompt: %q", prompts)
	}
}

func TestStopTurn_DropsAMessageStillWaitingForAWorker(t *testing.T) {
	f := newStopFixture(t)
	f.mustHandle(t, startMessage("0"))
	pool := workerpool.New(1)
	busy := make(chan struct{})
	pool.Submit(func() { <-busy })
	defer close(busy)
	dispatcher := NewJobDispatcher(f.handler, pool, f.handler.appState)
	f.handler.SetJobEvictor(dispatcher)
	dispatcher.Dispatch(replyMessage("1"))

	f.handler.stopTurn("j1", "U1", "2026-10-22T10:00:00Z")

	stop := waitForStop(t, f.outbox)
	if stop.Outcome != models.TurnStopOutcomeStopped || stop.ProcessedMessageID != "cmsg_1" || stop.SessionID != "session-1" {
		t.Errorf("turn_stopped = %+v, want stopped for cmsg_1 with session-1", stop)
	}
	if _, exists := dispatcher.activeJobs["j1"]; exists {
		t.Error("the job's queue must be gone so its worker exits instead of waiting forever")
	}
}

func TestStopTurn_ReportsAFailureTheStopDidNotCause(t *testing.T) {
	f := newStopFixture(t)
	f.mustHandle(t, startMessage("0"))

	// The push after a finished CLI run fails while a stop is pending: the failure is reported
	// and the stop is answered as not running.
	f.handler.runTurn("j1", "cmsg_1", func(ctx context.Context) error {
		f.handler.stopTurn("j1", "U1", "2026-10-22T11:00:00Z")
		if got := f.outbox.ofType(models.MessageTypeTurnStopped); len(got) != 0 {
			t.Fatalf("a running turn was answered before it ended: %+v", got)
		}
		return errors.New("git push failed")
	})

	stop := waitForStop(t, f.outbox)
	if stop.Outcome != models.TurnStopOutcomeNotRunning || stop.ProcessedMessageID != "cmsg_1" {
		t.Errorf("turn_stopped = %+v, want not_running for cmsg_1", stop)
	}
	errorsSent := waitFor(t, f.outbox, models.MessageTypeSystemMessage)
	if len(errorsSent) != 1 || !strings.Contains(fmt.Sprint(errorsSent[0].Payload), "git push failed") {
		t.Errorf("the failure must still reach the user, sent %+v", errorsSent)
	}
}

func TestPolledStop_StillRunsAReplySentAfterThePress(t *testing.T) {
	f := newStopFixture(t)
	f.mustHandle(t, startMessage("0"))
	pool := workerpool.New(2)
	defer pool.Stop()
	dispatcher := NewJobDispatcher(f.handler, pool, f.handler.appState)
	f.handler.SetJobEvictor(dispatcher)

	f.agent.blockNext = true
	dispatcher.Dispatch(replyMessage("1"))
	<-f.agent.started
	// The socket send of the stop failed; the user's next message still came over the socket.
	dispatcher.Dispatch(replyMessage("9"))

	// The backend ended and acked cmsg_1 at the press, so the poll lists only cmsg_9.
	newer, _ := json.Marshal(models.UserMessagePayload{JobID: "j1", Message: "do Y instead", ProcessedMessageID: "cmsg_9"})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ack") {
			return
		}
		_ = json.NewEncoder(w).Encode(clients.AgentJobsResponse{Jobs: []clients.AgentJob{{
			ID: "j1", Mode: "ask", StopRequested: true, StopRequestedBy: "U1", StopRequestedAt: "2026-10-23T10:00:00.000000Z",
			Messages: []clients.AgentJobMessage{{ID: "msg_9", Type: models.MessageTypeUserMessage, Payload: newer}},
		}}})
	}))
	defer backend.Close()
	poller := NewMessagePoller(clients.NewAgentsApiClient("k", backend.URL, "agent"), dispatcher, f.handler, f.handler.appState, time.Hour)
	poller.pollAndDispatch()

	if stop := waitForStop(t, f.outbox); stop.Outcome != models.TurnStopOutcomeStopped || stop.ProcessedMessageID != "cmsg_1" {
		t.Errorf("turn_stopped = %+v, want stopped for cmsg_1", stop)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !answered(f.outbox, "cmsg_9") {
		if time.Now().After(deadline) {
			t.Fatal("the reply sent after the press was dropped with the stopped turn")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func answered(box *outbox, processedMessageID string) bool {
	for _, msg := range box.ofType(models.MessageTypeAssistantMessage) {
		if payload, ok := msg.Payload.(models.AssistantMessagePayload); ok && payload.ProcessedMessageID == processedMessageID {
			return true
		}
	}
	return false
}

func TestStopTurn_AnswersNotRunningWhenTheTurnFinishedFirst(t *testing.T) {
	f := newStopFixture(t)
	f.mustHandle(t, startMessage("0"))

	// The stop lands while the finished turn is still committing and posting its answer.
	f.handler.runTurn("j1", "cmsg_1", func(ctx context.Context) error {
		f.handler.stopTurn("j1", "U1", "2026-10-22T12:00:00Z")
		return nil
	})

	if stop := waitForStop(t, f.outbox); stop.Outcome != models.TurnStopOutcomeNotRunning || stop.ProcessedMessageID != "cmsg_1" {
		t.Errorf("turn_stopped = %+v, want not_running for cmsg_1", stop)
	}
}
