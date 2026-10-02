package handlers

import (
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
	model string
	turns []string
}

func (a *effortRecordingAgent) StartNewConversationWithProgress(
	prompt, systemPrompt, workDir, effort string, emitter services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	a.turns = append(a.turns, "start:"+effort)
	return &services.CLIAgentResult{Output: "done", SessionID: "session-1"}, nil
}

func (a *effortRecordingAgent) ContinueConversationWithProgress(
	sessionID, prompt, systemPrompt, workDir, effort string, emitter services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	a.turns = append(a.turns, "continue:"+effort)
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
