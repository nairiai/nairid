package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"nairid/clients"
	"nairid/core/env"
	"nairid/models"
	"nairid/services"
	"nairid/usecases"
)

// effortRecordingAgent stands in for a CLI: it records the effort each turn was run with.
type effortRecordingAgent struct {
	services.CLIAgent
	model          string
	turns          []string
	failsNextStart bool
	duringTurn     func()
}

func (a *effortRecordingAgent) StartNewConversationWithProgress(
	prompt, systemPrompt, workDir, effort string, emitter services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	a.turns = append(a.turns, "start:"+effort)
	if a.duringTurn != nil {
		a.duringTurn()
	}
	if a.failsNextStart {
		a.failsNextStart = false
		return nil, errors.New("the CLI could not start a session")
	}
	return &services.CLIAgentResult{Output: "done", SessionID: "session-1"}, nil
}

func (a *effortRecordingAgent) ContinueConversationWithProgress(
	sessionID, prompt, systemPrompt, workDir, effort string, emitter services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	a.turns = append(a.turns, "continue:"+effort)
	if a.duringTurn != nil {
		a.duringTurn()
	}
	return &services.CLIAgentResult{Output: "done", SessionID: sessionID}, nil
}

func (a *effortRecordingAgent) Model() string     { return a.model }
func (a *effortRecordingAgent) AgentName() string { return "codex" }

// startHandlerLikeAFreshProcess builds a handler the way a starting nairid does: from the state file.
func startHandlerLikeAFreshProcess(t *testing.T, agent services.CLIAgent, statePath, backendURL string) *MessageHandler {
	t.Helper()
	appState, _, err := RestoreAppState(statePath)
	if err != nil {
		t.Fatalf("restore state: %v", err)
	}
	envManager, err := env.NewEnvManager()
	if err != nil {
		t.Fatalf("env manager: %v", err)
	}
	apiClient := clients.NewAgentsApiClient("test-key", backendURL, "agent")
	sender := NewMessageSender(NewConnectionState(), apiClient)
	go func() {
		for range sender.messageQueue {
		}
	}()
	t.Cleanup(sender.Close)
	return NewMessageHandler(agent, usecases.NewGitUseCase(nil, agent, appState), appState, envManager, sender, apiClient)
}

func TestReasoningEffortReachesEveryTurnOfAConversation(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	statePath := filepath.Join(t.TempDir(), "state.json")
	agent := &effortRecordingAgent{model: "gpt-5.5"}

	messages := 0
	reply := func(handler *MessageHandler) {
		t.Helper()
		messages++
		err := handler.handleUserMessage(models.BaseMessage{
			ID:   fmt.Sprintf("msg_%d", messages),
			Type: models.MessageTypeUserMessage,
			Payload: models.UserMessagePayload{
				JobID: "j1", Message: "and again", ProcessedMessageID: fmt.Sprintf("cmsg_%d", messages),
			},
		})
		if err != nil {
			t.Fatalf("reply %d: %v", messages, err)
		}
	}

	handler := startHandlerLikeAFreshProcess(t, agent, statePath, backend.URL)
	err := handler.handleStartConversation(models.BaseMessage{
		ID:   "msg_0",
		Type: models.MessageTypeStartConversation,
		Payload: models.StartConversationPayload{
			JobID: "j1", Message: "hello", ProcessedMessageID: "cmsg_0",
			ReasoningEffort: "high", ReasoningEffortModel: "gpt-5.5",
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	reply(handler)
	reply(handler)

	handler = startHandlerLikeAFreshProcess(t, agent, statePath, backend.URL)
	reply(handler)

	agent.model = "gpt-5.4"
	handler = startHandlerLikeAFreshProcess(t, agent, statePath, backend.URL)
	reply(handler)

	agent.model = "gpt-5.5"
	handler = startHandlerLikeAFreshProcess(t, agent, statePath, backend.URL)
	reply(handler)

	want := []string{
		"start:high",
		"continue:high",
		"continue:high",
		"continue:high", // after a restart
		"continue:",     // the agent now runs a model the level was not checked for
		"continue:high", // back on the model it was checked for
	}
	if !reflect.DeepEqual(agent.turns, want) {
		t.Errorf("turns ran with %v, want %v", agent.turns, want)
	}
}

func TestReasoningEffortSurvivesAFailedFirstTurn(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	agent := &effortRecordingAgent{model: "gpt-5.5", failsNextStart: true}
	handler := startHandlerLikeAFreshProcess(t, agent, filepath.Join(t.TempDir(), "state.json"), backend.URL)

	err := handler.handleStartConversation(models.BaseMessage{
		ID:   "msg_0",
		Type: models.MessageTypeStartConversation,
		Payload: models.StartConversationPayload{
			JobID: "j1", Message: "hello", ProcessedMessageID: "cmsg_0",
			ReasoningEffort: "high", ReasoningEffortModel: "gpt-5.5",
		},
	})
	if err == nil {
		t.Fatal("the first turn was meant to fail")
	}
	err = handler.handleUserMessage(models.BaseMessage{
		ID:      "msg_1",
		Type:    models.MessageTypeUserMessage,
		Payload: models.UserMessagePayload{JobID: "j1", Message: "try again", ProcessedMessageID: "cmsg_1"},
	})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}

	want := []string{"start:high", "start:high"}
	if !reflect.DeepEqual(agent.turns, want) {
		t.Errorf("turns ran with %v, want %v", agent.turns, want)
	}
}

// The mode saved while a turn runs is what crash recovery restarts the turn with.
func TestAskModeStaysOnEveryTurnOfAConversation(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	agent := &effortRecordingAgent{model: "gpt-5.5"}
	handler := startHandlerLikeAFreshProcess(t, agent, filepath.Join(t.TempDir(), "state.json"), backend.URL)
	var modesSavedDuringTurns []models.AgentMode
	agent.duringTurn = func() {
		job, _ := handler.appState.GetJobData("j1")
		modesSavedDuringTurns = append(modesSavedDuringTurns, job.Mode)
	}
	reply := func(id string) {
		t.Helper()
		err := handler.handleUserMessage(models.BaseMessage{
			ID:      "msg_" + id,
			Type:    models.MessageTypeUserMessage,
			Payload: models.UserMessagePayload{JobID: "j1", Message: "and again", ProcessedMessageID: "cmsg_" + id},
		})
		if err != nil {
			t.Fatalf("reply %s: %v", id, err)
		}
	}

	err := handler.handleStartConversation(models.BaseMessage{
		ID:   "msg_0",
		Type: models.MessageTypeStartConversation,
		Payload: models.StartConversationPayload{
			JobID: "j1", Message: "what does this repo do?", ProcessedMessageID: "cmsg_0", Mode: models.AgentModeAsk,
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	reply("1")

	job, _ := handler.appState.GetJobData("j1")
	job.ClaudeSessionID = ""
	if err := handler.appState.UpdateJobData("j1", *job); err != nil {
		t.Fatalf("lose the session: %v", err)
	}
	reply("2")

	wantTurns := []string{"start:", "continue:", "start:"}
	if !reflect.DeepEqual(agent.turns, wantTurns) {
		t.Fatalf("turns ran as %v, want %v", agent.turns, wantTurns)
	}
	wantModes := []models.AgentMode{models.AgentModeAsk, models.AgentModeAsk, models.AgentModeAsk}
	if !reflect.DeepEqual(modesSavedDuringTurns, wantModes) {
		t.Errorf("modes saved while the turns ran: %q, want %q", modesSavedDuringTurns, wantModes)
	}
	if job, _ := handler.appState.GetJobData("j1"); job.Mode != models.AgentModeAsk {
		t.Errorf("mode after the restarted turn = %q, want ask", job.Mode)
	}
}
