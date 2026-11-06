package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"reflect"
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

const (
	firstPrompt  = "Org rule: never push to main."
	firstText    = "the first question"
	earlierText  = "earlier in the thread"
	senderLabel  = "[Sender: Sam Example]"
	followUpText = "the follow up"
)

type agentRun struct {
	prompt, systemPrompt, sessionID string
}

// recordingAgent finishes a turn at once, or fails it when told to, and records how it was run.
type recordingAgent struct {
	services.CLIAgent
	mu        sync.Mutex
	starts    []agentRun
	continues []agentRun
	session   string
	failNext  error
	onStart   func(workDir string) error
}

func (a *recordingAgent) StartNewConversationWithProgress(
	_ context.Context, prompt, systemPrompt, workDir, _ string, _ time.Duration, _ services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.starts = append(a.starts, agentRun{prompt: prompt, systemPrompt: systemPrompt})
	if a.onStart != nil {
		if err := a.onStart(workDir); err != nil {
			return nil, err
		}
	}
	if failure := a.failNext; failure != nil {
		a.failNext = nil
		return nil, failure
	}
	return &services.CLIAgentResult{Output: "the answer", SessionID: a.session}, nil
}

func (a *recordingAgent) ContinueConversationWithProgress(
	_ context.Context, sessionID, prompt, systemPrompt, _, _ string, _ time.Duration, _ services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.continues = append(a.continues, agentRun{prompt: prompt, systemPrompt: systemPrompt, sessionID: sessionID})
	return &services.CLIAgentResult{Output: "the answer", SessionID: sessionID}, nil
}

func (a *recordingAgent) Model() string     { return "" }
func (a *recordingAgent) AgentName() string { return "claude" }

func (a *recordingAgent) runs() (starts, continues []agentRun) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]agentRun{}, a.starts...), append([]agentRun{}, a.continues...)
}

// pollBackend offers a message until it is acked, as the backend does.
type pollBackend struct {
	*httptest.Server
	mu      sync.Mutex
	job     clients.AgentJob
	offered []clients.AgentJobMessage
	acked   map[string]bool
}

func newPollBackend(t *testing.T, offered ...clients.AgentJobMessage) *pollBackend {
	t.Helper()
	b := &pollBackend{
		job:     clients.AgentJob{ID: "j1", Mode: string(models.AgentModeAsk), SystemPrompt: firstPrompt},
		offered: offered,
		acked:   make(map[string]bool),
	}
	b.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/ack") {
			b.acked[path.Base(path.Dir(r.URL.Path))] = true
			return
		}
		job := b.job
		for _, message := range b.offered {
			if !b.acked[message.ID] {
				job.Messages = append(job.Messages, message)
			}
		}
		_ = json.NewEncoder(w).Encode(clients.AgentJobsResponse{Jobs: []clients.AgentJob{job}})
	}))
	t.Cleanup(b.Close)
	return b
}

func (b *pollBackend) ackedIDs() map[string]bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make(map[string]bool, len(b.acked))
	for id := range b.acked {
		ids[id] = true
	}
	return ids
}

// agentProcess is what one run of the binary holds. A restart is a new one on the same state file.
type agentProcess struct {
	statePath string
	state     *models.AppState
	handler   *MessageHandler
	git       *usecases.GitUseCase
	sender    *MessageSender
	http      *mockHTTP
	ws        *mockWS
	agent     *recordingAgent
	api       *clients.AgentsApiClient
}

func bootAgentProcess(t *testing.T, statePath, backendURL string) *agentProcess {
	t.Helper()
	appState, _, err := RestoreAppState(statePath)
	if err != nil {
		t.Fatalf("restore state: %v", err)
	}
	envManager, err := env.NewEnvManager()
	if err != nil {
		t.Fatalf("env manager: %v", err)
	}
	connection := NewConnectionState()
	connection.SetConnected(true)
	httpMock, wsMock := &mockHTTP{}, &mockWS{}
	sender := newMessageSenderForTest(connection, httpMock, wsMock)
	sender.SetOutbox(appState)
	go sender.processQueue()
	agent := &recordingAgent{session: "session-1"}
	api := clients.NewAgentsApiClient("k", backendURL, "agent")
	git := usecases.NewGitUseCase(nil, agent, appState)
	return &agentProcess{
		statePath: statePath,
		state:     appState,
		handler:   NewMessageHandler(agent, git, appState, envManager, sender, api),
		git:       git,
		sender:    sender,
		http:      httpMock,
		ws:        wsMock,
		agent:     agent,
		api:       api,
	}
}

func (p *agentProcess) waitForOutbox(t *testing.T) {
	t.Helper()
	waitUntil(t, func() bool { return len(p.state.GetAllOutboxMessages()) == 0 })
}

// recover starts the process the way main does, and returns once messageID has run and been handled.
func (p *agentProcess) recoverAndPoll(t *testing.T, messageID string) {
	t.Helper()
	pool := workerpool.New(2)
	dispatcher := NewJobDispatcher(p.handler, pool, p.state)
	RecoverJobs(p.state, p.git, dispatcher, p.handler)
	poller := NewMessagePoller(p.api, dispatcher, p.handler, p.state, time.Hour)
	poller.pollAndDispatch()
	waitUntil(t, func() bool { return p.state.IsMessageDone(messageID) })
	// The ack of the finished turn may not have landed, so the message can still be offered.
	poller.pollAndDispatch()
	pool.StopWait()
	p.waitForOutbox(t)
	p.sender.Close()
}

// repliesTo lists the distinct replies among the submitted messages. The outbox resends with the same
// id, so only a turn that ran twice shows up as two.
func repliesTo(t *testing.T, calls []models.BaseMessage, processedMessageID string) map[string]bool {
	t.Helper()
	ids := make(map[string]bool)
	for _, call := range calls {
		if call.Type != models.MessageTypeAssistantMessage {
			continue
		}
		var reply models.AssistantMessagePayload
		if err := unmarshalPayload(call.Payload, &reply); err != nil {
			t.Fatalf("read reply: %v", err)
		}
		if reply.ProcessedMessageID == processedMessageID {
			ids[call.ID] = true
		}
	}
	return ids
}

func firstMessagePayload() models.StartConversationPayload {
	name := "Sam Example"
	return models.StartConversationPayload{
		JobID:              "j1",
		Message:            firstText,
		ProcessedMessageID: "cmsg_1",
		Mode:               models.AgentModeAsk,
		SystemPrompt:       firstPrompt,
		PreviousMessages:   []models.PreviousMessage{{Message: earlierText}},
		SenderMetadata:     &models.UserMetadata{Name: &name},
	}
}

func firstMessage() models.BaseMessage {
	return models.BaseMessage{ID: "msg_1", Type: models.MessageTypeStartConversation, Payload: firstMessagePayload()}
}

func followUpMessage() models.BaseMessage {
	return models.BaseMessage{
		ID:      "msg_2",
		Type:    models.MessageTypeUserMessage,
		Payload: models.UserMessagePayload{JobID: "j1", Message: followUpText, ProcessedMessageID: "cmsg_2"},
	}
}

func offerOf(t *testing.T, payload models.UserMessagePayload) clients.AgentJobMessage {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return clients.AgentJobMessage{ID: payload.ProcessedMessageID, Type: models.MessageTypeUserMessage, Payload: raw}
}

func firstMessageOffer(t *testing.T) clients.AgentJobMessage {
	t.Helper()
	first := firstMessagePayload()
	return offerOf(t, models.UserMessagePayload{
		JobID:              first.JobID,
		Message:            first.Message,
		ProcessedMessageID: first.ProcessedMessageID,
		PreviousMessages:   first.PreviousMessages,
		SenderMetadata:     first.SenderMetadata,
	})
}

func assertRanWithWholeFirstMessage(t *testing.T, run agentRun) {
	t.Helper()
	if !strings.Contains(run.systemPrompt, firstPrompt) {
		t.Errorf("system prompt lost the customer's prompt:\n%s", run.systemPrompt)
	}
	if !strings.Contains(run.systemPrompt, "ASK mode") {
		t.Errorf("system prompt lost ask mode:\n%s", run.systemPrompt)
	}
	for _, want := range []string{firstText, earlierText, senderLabel} {
		if !strings.Contains(run.prompt, want) {
			t.Errorf("prompt lost %q:\n%s", want, run.prompt)
		}
	}
}

// crashPoint is one state file a crash could leave, and the replies the backend had by then.
type crashPoint struct {
	state   []byte
	replies []models.BaseMessage
}

func recordCrashPoints(t *testing.T, live *agentProcess, turn func()) []crashPoint {
	t.Helper()
	var points []crashPoint
	restore := models.SetAfterPersistForTest(func(statePath string) {
		if statePath != live.statePath {
			return
		}
		data, err := os.ReadFile(statePath)
		if err != nil {
			t.Errorf("read state: %v", err)
			return
		}
		points = append(points, crashPoint{state: data, replies: live.http.getCalls()})
	})
	turn()
	live.waitForOutbox(t)
	live.sender.Close()
	restore()
	return points
}

// restoreCopy reads a state file as a restart would, without touching the original.
func restoreCopy(t *testing.T, state []byte) (*models.AppState, string) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(statePath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	appState, _, err := RestoreAppState(statePath)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	return appState, statePath
}

// Any state file holding the reply also says the turn finished: that is what stops the replay.
func assertReplyAndFinishAreOneWrite(t *testing.T, points []crashPoint, jobID, processedMessageID string) {
	t.Helper()
	sawReply := false
	for i, point := range points {
		state, _ := restoreCopy(t, point.state)
		if len(state.GetAllOutboxMessages()) == 0 {
			continue
		}
		sawReply = true
		job, exists := state.GetJobData(jobID)
		if !exists || job.Status != models.JobStatusCompleted || job.ClaudeSessionID == "" {
			t.Errorf("write %d holds the reply, but the job is %+v", i, job)
		}
		if !state.IsMessageDone(processedMessageID) {
			t.Errorf("write %d holds the reply, but the message is not done", i)
		}
	}
	if !sawReply {
		t.Fatal("no write held the reply, so the windows were not tested")
	}
}

// A restart after any state write of a first turn gives one reply, and a turn that runs again has the
// whole first message.
func TestRestartAfterAnyWriteOfAFirstTurn(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	live := bootAgentProcess(t, filepath.Join(t.TempDir(), "state.json"), newPollBackend(t).URL)
	points := recordCrashPoints(t, live, func() {
		if err := live.handler.PersistQueuedMessage(firstMessage()); err != nil {
			t.Fatal(err)
		}
		live.handler.HandleMessage(firstMessage())
	})
	assertReplyAndFinishAreOneWrite(t, points, "j1", "cmsg_1")

	for i, point := range points {
		t.Run(fmt.Sprintf("after write %d of %d", i+1, len(points)), func(t *testing.T) {
			backend := newPollBackend(t, firstMessageOffer(t))
			_, statePath := restoreCopy(t, point.state)
			restarted := bootAgentProcess(t, statePath, backend.URL)
			restarted.recoverAndPoll(t, "cmsg_1")

			replies := repliesTo(t, point.replies, "cmsg_1")
			for id := range repliesTo(t, restarted.http.getCalls(), "cmsg_1") {
				replies[id] = true
			}
			if len(replies) != 1 {
				t.Errorf("got %d replies, want exactly 1: %v", len(replies), replies)
			}
			starts, continues := restarted.agent.runs()
			if len(starts) > 1 || len(continues) != 0 {
				t.Errorf("turn ran %d starts and %d continues", len(starts), len(continues))
			}
			for _, run := range starts {
				assertRanWithWholeFirstMessage(t, run)
			}
			if !backend.ackedIDs()["cmsg_1"] {
				t.Error("first message was never acked")
			}
			job, exists := restarted.state.GetJobData("j1")
			if !exists || job.Status != models.JobStatusCompleted || job.ClaudeSessionID != "session-1" {
				t.Errorf("job ended as %+v", job)
			}
			if len(job.StartPayload) != 0 {
				t.Error("job with a session still stores its first message")
			}
		})
	}
}

// The same for a follow-up: one reply, on the session the first turn made.
func TestRestartAfterAnyWriteOfAFollowUp(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	live := bootAgentProcess(t, filepath.Join(t.TempDir(), "state.json"), newPollBackend(t).URL)
	live.handler.HandleMessage(firstMessage())
	live.waitForOutbox(t)
	points := recordCrashPoints(t, live, func() {
		if err := live.handler.PersistQueuedMessage(followUpMessage()); err != nil {
			t.Fatal(err)
		}
		live.handler.HandleMessage(followUpMessage())
	})
	assertReplyAndFinishAreOneWrite(t, points, "j1", "cmsg_2")

	for i, point := range points {
		t.Run(fmt.Sprintf("after write %d of %d", i+1, len(points)), func(t *testing.T) {
			backend := newPollBackend(t, offerOf(t, models.UserMessagePayload{
				JobID: "j1", Message: followUpText, ProcessedMessageID: "cmsg_2",
			}))
			_, statePath := restoreCopy(t, point.state)
			restarted := bootAgentProcess(t, statePath, backend.URL)
			restarted.recoverAndPoll(t, "cmsg_2")

			replies := repliesTo(t, point.replies, "cmsg_2")
			for id := range repliesTo(t, restarted.http.getCalls(), "cmsg_2") {
				replies[id] = true
			}
			if len(replies) != 1 {
				t.Errorf("got %d replies, want exactly 1: %v", len(replies), replies)
			}
			starts, continues := restarted.agent.runs()
			if len(starts) != 0 || len(continues) > 1 {
				t.Errorf("turn ran %d starts and %d continues", len(starts), len(continues))
			}
			for _, run := range continues {
				if run.sessionID != "session-1" || !strings.Contains(run.prompt, followUpText) {
					t.Errorf("follow-up ran as %+v", run)
				}
			}
		})
	}
}

func TestAFinishedFirstTurnLeavesNoStoredMessage(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	live := bootAgentProcess(t, filepath.Join(t.TempDir(), "state.json"), newPollBackend(t).URL)
	live.handler.HandleMessage(firstMessage())
	live.waitForOutbox(t)
	live.sender.Close()

	state, err := os.ReadFile(live.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), "start_payload") || strings.Contains(string(state), firstPrompt) {
		t.Errorf("state still holds the first message:\n%s", state)
	}
}

func TestRecoverJobs_RunsTheStoredFirstMessageAsItCame(t *testing.T) {
	first := firstMessagePayload()
	first.Attachments = []models.MessageAttachment{{AttachmentID: "att_1"}}
	stored, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	if err := appState.UpdateJobData("j1", models.JobData{
		JobID: "j1", LastMessage: first.Message, ProcessedMessageID: first.ProcessedMessageID,
		Status: models.JobStatusInProgress, UpdatedAt: time.Now(), StartPayload: stored,
	}); err != nil {
		t.Fatal(err)
	}

	got := recoveredStart(t, appState)
	if !reflect.DeepEqual(got, first) {
		t.Errorf("recovered start is\n%+v\nwant\n%+v", got, first)
	}
}

// A job written by an older version stored no first message, so it runs from what the row has.
func TestRecoverJobs_RebuildsARowWithNoStoredMessage(t *testing.T) {
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	if err := appState.UpdateJobData("j1", models.JobData{
		JobID: "j1", LastMessage: firstText, ProcessedMessageID: "cmsg_1", Mode: models.AgentModeAsk,
		Status: models.JobStatusInProgress, UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	got := recoveredStart(t, appState)
	if got.Message != firstText || got.ProcessedMessageID != "cmsg_1" || got.Mode != models.AgentModeAsk {
		t.Errorf("recovered start is %+v", got)
	}
}

func recoveredStart(t *testing.T, appState *models.AppState) models.StartConversationPayload {
	t.Helper()
	pool := workerpool.New(1)
	defer pool.StopWait()
	dispatcher := NewJobDispatcher(nil, pool, appState)
	jobMessages := make(chan models.BaseMessage, 10)
	dispatcher.activeJobs["j1"] = jobMessages
	RecoverJobs(appState, nil, dispatcher, nil)
	if len(jobMessages) != 1 {
		t.Fatalf("recovery dispatched %d messages, want 1", len(jobMessages))
	}
	message := <-jobMessages
	if message.Type != models.MessageTypeStartConversation {
		t.Fatalf("recovery dispatched a %s", message.Type)
	}
	var start models.StartConversationPayload
	if err := unmarshalPayload(message.Payload, &start); err != nil {
		t.Fatal(err)
	}
	return start
}

func TestStartPayloadForUnstartedJobKeepsTheFirstMessage(t *testing.T) {
	first := firstMessagePayload()
	first.Attachments = []models.MessageAttachment{{AttachmentID: "att_first"}}
	stored, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	job := &models.JobData{JobID: "j1", Mode: models.AgentModeAsk, StartPayload: stored}
	name := "Lee Example"
	reply := models.UserMessagePayload{
		JobID:              "j1",
		Message:            "are you there?",
		ProcessedMessageID: "cmsg_2",
		Attachments:        []models.MessageAttachment{{AttachmentID: "att_reply"}},
		SenderMetadata:     &models.UserMetadata{Name: &name},
	}

	t.Run("a new reply keeps the first message as history", func(t *testing.T) {
		got := startPayloadForUnstartedJob(reply, job)
		if got.SystemPrompt != firstPrompt || got.Mode != models.AgentModeAsk {
			t.Errorf("lost prompt or mode: %+v", got)
		}
		wantHistory := []models.PreviousMessage{
			{Message: earlierText},
			{Message: firstText, Attachments: first.Attachments},
		}
		if !reflect.DeepEqual(got.PreviousMessages, wantHistory) {
			t.Errorf("history is %+v, want %+v", got.PreviousMessages, wantHistory)
		}
		if got.Message != reply.Message || got.ProcessedMessageID != "cmsg_2" {
			t.Errorf("reply is not what runs: %+v", got)
		}
		if !reflect.DeepEqual(got.Attachments, reply.Attachments) || got.SenderMetadata != reply.SenderMetadata {
			t.Errorf("lost the reply's attachments or sender: %+v", got)
		}
	})

	t.Run("the first message offered again runs as it came", func(t *testing.T) {
		got := startPayloadForUnstartedJob(models.UserMessagePayload{JobID: "j1", Message: firstText, ProcessedMessageID: "cmsg_1"}, job)
		if !reflect.DeepEqual(got, first) {
			t.Errorf("got %+v, want the stored first message", got)
		}
	})

	t.Run("a second failure keeps everything before it", func(t *testing.T) {
		merged, err := json.Marshal(startPayloadForUnstartedJob(reply, job))
		if err != nil {
			t.Fatal(err)
		}
		next := models.UserMessagePayload{JobID: "j1", Message: "hello?", ProcessedMessageID: "cmsg_3"}
		got := startPayloadForUnstartedJob(next, &models.JobData{JobID: "j1", Mode: models.AgentModeAsk, StartPayload: merged})
		if got.SystemPrompt != firstPrompt {
			t.Errorf("lost the prompt: %+v", got)
		}
		var history []string
		for _, message := range got.PreviousMessages {
			history = append(history, message.Message)
		}
		if want := []string{earlierText, firstText, reply.Message}; !reflect.DeepEqual(history, want) {
			t.Errorf("history is %v, want %v", history, want)
		}
	})

	t.Run("a repeated message stays in the history", func(t *testing.T) {
		repeat := firstMessagePayload()
		repeat.Message = earlierText
		raw, err := json.Marshal(repeat)
		if err != nil {
			t.Fatal(err)
		}
		got := startPayloadForUnstartedJob(reply, &models.JobData{JobID: "j1", StartPayload: raw})
		if len(got.PreviousMessages) != 2 {
			t.Errorf("history is %+v, want both copies", got.PreviousMessages)
		}
	})

	t.Run("a stored message it cannot read is ignored", func(t *testing.T) {
		got := startPayloadForUnstartedJob(reply, &models.JobData{
			JobID: "j1", Mode: models.AgentModeAsk, SystemPrompt: "from the row", StartPayload: json.RawMessage(`{"message":1}`),
		})
		if got.SystemPrompt != "from the row" || got.Message != reply.Message || len(got.PreviousMessages) != 0 {
			t.Errorf("got %+v", got)
		}
	})
}

func TestAReplyToAFailedFirstTurnRunsWithTheFirstMessage(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	live := bootAgentProcess(t, filepath.Join(t.TempDir(), "state.json"), newPollBackend(t).URL)
	t.Cleanup(live.sender.Close)
	live.agent.failNext = errors.New("Claude command failed: signal: killed")
	live.handler.HandleMessage(firstMessage())

	job, _ := live.state.GetJobData("j1")
	if job.Status != models.JobStatusFailed || len(job.StartPayload) == 0 {
		t.Fatalf("failed job is %+v", job)
	}

	live.handler.HandleMessage(followUpMessage())
	starts, continues := live.agent.runs()
	if len(starts) != 2 || len(continues) != 0 {
		t.Fatalf("ran %d starts and %d continues", len(starts), len(continues))
	}
	assertRanWithWholeFirstMessage(t, agentRun{
		prompt:       starts[1].prompt + "\n" + senderLabel,
		systemPrompt: starts[1].systemPrompt,
	})
	if !strings.Contains(starts[1].prompt, followUpText) {
		t.Errorf("the reply did not run:\n%s", starts[1].prompt)
	}
	job, _ = live.state.GetJobData("j1")
	if job.Status != models.JobStatusCompleted || job.ClaudeSessionID != "session-1" || len(job.StartPayload) != 0 {
		t.Errorf("job ended as %+v", job)
	}
}

func TestAFirstTurnKilledByShutdownRunsAgainAfterTheRestart(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	backend := newPollBackend(t, firstMessageOffer(t))
	live := bootAgentProcess(t, filepath.Join(t.TempDir(), "state.json"), backend.URL)
	MarkShuttingDown()
	t.Cleanup(func() { shuttingDown.Store(false) })
	live.agent.failNext = errors.New("Claude command failed: signal: terminated")
	if err := live.handler.PersistQueuedMessage(firstMessage()); err != nil {
		t.Fatal(err)
	}
	live.handler.HandleMessage(firstMessage())
	live.sender.Close()
	shuttingDown.Store(false)

	job, _ := live.state.GetJobData("j1")
	if job.Status != models.JobStatusInProgress || len(job.StartPayload) == 0 || live.state.IsMessageDone("cmsg_1") {
		t.Fatalf("job after shutdown is %+v", job)
	}
	if calls := live.http.getCalls(); len(calls) != 0 {
		t.Errorf("shutdown posted %+v", calls)
	}

	restarted := bootAgentProcess(t, live.statePath, backend.URL)
	restarted.recoverAndPoll(t, "cmsg_1")
	starts, _ := restarted.agent.runs()
	if len(starts) != 1 {
		t.Fatalf("ran %d starts after the restart, want 1", len(starts))
	}
	assertRanWithWholeFirstMessage(t, starts[0])
	if got := repliesTo(t, restarted.http.getCalls(), "cmsg_1"); len(got) != 1 {
		t.Errorf("got %d replies, want 1", len(got))
	}
}

// A timed out CLI leaves a session that holds the prompt, so the reply resumes it.
func TestATimedOutFirstTurnDropsTheStoredMessage(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	live := bootAgentProcess(t, filepath.Join(t.TempDir(), "state.json"), newPollBackend(t).URL)
	t.Cleanup(live.sender.Close)
	live.agent.failNext = fmt.Errorf("failed to run session: %w", &core.ErrClaudeCommandErr{
		Err: &core.ErrSessionTimeout{Timeout: time.Hour, SessionID: "session-1", Err: errors.New("signal: killed")},
	})
	live.handler.HandleMessage(firstMessage())

	job, _ := live.state.GetJobData("j1")
	if job.Status != models.JobStatusFailed || job.ClaudeSessionID != "session-1" || len(job.StartPayload) != 0 {
		t.Errorf("job is %+v", job)
	}
}

// A CLI that finished without printing a session id leaves nothing to resume.
func TestAFirstTurnWithNoSessionKeepsTheFirstMessage(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	live := bootAgentProcess(t, filepath.Join(t.TempDir(), "state.json"), newPollBackend(t).URL)
	t.Cleanup(live.sender.Close)
	live.agent.session = ""
	live.handler.HandleMessage(firstMessage())

	job, _ := live.state.GetJobData("j1")
	if job.Status != models.JobStatusCompleted || len(job.StartPayload) == 0 {
		t.Fatalf("job is %+v", job)
	}

	live.agent.session = "session-1"
	live.handler.HandleMessage(followUpMessage())
	starts, _ := live.agent.runs()
	if len(starts) != 2 || !strings.Contains(starts[1].systemPrompt, firstPrompt) || !strings.Contains(starts[1].prompt, firstText) {
		t.Errorf("reply ran as %+v", starts)
	}
}

func TestAReplyWithNoLocalJobIsLeftForThePoller(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	backend := newPollBackend(t, offerOf(t, models.UserMessagePayload{
		JobID: "j1", Message: followUpText, ProcessedMessageID: "cmsg_2",
	}))
	live := bootAgentProcess(t, filepath.Join(t.TempDir(), "state.json"), backend.URL)
	pool := workerpool.New(2)
	dispatcher := NewJobDispatcher(live.handler, pool, live.state)
	if err := live.handler.PersistQueuedMessage(followUpMessage()); err != nil {
		t.Fatal(err)
	}
	dispatcher.Dispatch(followUpMessage())
	waitUntil(t, func() bool { return !dispatcher.IsMessageInFlight("cmsg_2") })

	starts, continues := live.agent.runs()
	if len(starts)+len(continues) != 0 {
		t.Fatalf("the reply ran with no prompt or mode: %+v %+v", starts, continues)
	}
	if calls, events := live.http.getCalls(), live.ws.getCalls(); len(calls)+len(events) != 0 {
		t.Errorf("sent %+v and %+v for a reply left waiting", calls, events)
	}
	if live.state.IsMessageDone("cmsg_2") || len(backend.ackedIDs()) != 0 {
		t.Error("a reply left for the poller must stay unacked")
	}
	if !queuedIDs(live.state)["cmsg_2"] {
		t.Error("the reply should stay queued")
	}

	poller := NewMessagePoller(live.api, dispatcher, live.handler, live.state, time.Hour)
	poller.pollAndDispatch()
	waitUntil(t, func() bool { return live.state.IsMessageDone("cmsg_2") })
	pool.StopWait()
	live.waitForOutbox(t)
	live.sender.Close()

	starts, _ = live.agent.runs()
	if len(starts) != 1 {
		t.Fatalf("ran %d starts, want 1", len(starts))
	}
	if !strings.Contains(starts[0].systemPrompt, firstPrompt) || !strings.Contains(starts[0].systemPrompt, "ASK mode") {
		t.Errorf("system prompt is:\n%s", starts[0].systemPrompt)
	}
	job, _ := live.state.GetJobData("j1")
	if job.Mode != models.AgentModeAsk {
		t.Errorf("job mode is %q, want ask", job.Mode)
	}
	if got := repliesTo(t, live.http.getCalls(), "cmsg_2"); len(got) != 1 {
		t.Errorf("got %d replies, want 1", len(got))
	}
	if !backend.ackedIDs()["cmsg_2"] {
		t.Error("the reply was never acked")
	}
}

// Ask mode must survive the poller's start, or the turn's edits get committed and pushed.
func TestAReplyWithNoLocalJobInAskModeCommitsNothing(t *testing.T) {
	git := gitWithoutUserConfig(t)
	remote, repo := t.TempDir(), t.TempDir()
	git(remote, "init", "-q", "--bare", "-b", "main")
	git(repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", "README.md")
	git(repo, "commit", "-q", "-m", "base")
	git(repo, "remote", "add", "origin", remote)
	git(repo, "push", "-q", "-u", "origin", "main")

	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENT_EXEC_USER", "")
	t.Setenv("MAX_CONCURRENCY", "2")
	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return repo })
	agent := &recordingAgent{session: "session-1", onStart: func(workDir string) error {
		return os.WriteFile(filepath.Join(workDir, "scratch.txt"), []byte("not for a commit"), 0o600)
	}}
	handler, _ := handlerWithOutbox(t, agent, gitClient)
	handler.appState.SetRepositoryContext(&models.RepositoryContext{IsRepoMode: true, RepoPath: repo})

	backend := newPollBackend(t, offerOf(t, models.UserMessagePayload{
		JobID: "j1", Message: followUpText, ProcessedMessageID: "cmsg_2",
	}))
	pool := workerpool.New(2)
	dispatcher := NewJobDispatcher(handler, pool, handler.appState)
	poller := NewMessagePoller(clients.NewAgentsApiClient("k", backend.URL, "agent"), dispatcher, handler, handler.appState, time.Hour)
	poller.pollAndDispatch()
	waitUntil(t, func() bool { return handler.appState.IsMessageDone("cmsg_2") })
	pool.StopWait()

	if starts, _ := agent.runs(); len(starts) != 1 {
		t.Fatalf("ran %d starts, want 1", len(starts))
	}
	if refs := git(remote, "for-each-ref", "--format=%(refname)"); refs != "refs/heads/main" {
		t.Errorf("remote has %q, want only main", refs)
	}
	if count := git(remote, "rev-list", "--all", "--count"); count != "1" {
		t.Errorf("remote has %s commits, want the base commit only", count)
	}
}
