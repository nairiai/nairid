package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"nairid/clients"
	"nairid/core"
	"nairid/models"
	"nairid/services"
)

// timingOutAgent is a CLI killed at its deadline on every turn, after it printed session-1.
// Its first turn leaves an edited tracked file and a new file in the working tree.
type timingOutAgent struct {
	services.CLIAgent
	turns    []string
	workDirs []string
}

func (a *timingOutAgent) killed(turn, workDir string) error {
	a.turns = append(a.turns, turn)
	a.workDirs = append(a.workDirs, workDir)
	return fmt.Errorf("failed to run session: %w", &core.ErrClaudeCommandErr{
		Err: &core.ErrSessionTimeout{Timeout: time.Hour, SessionID: "session-1", Err: errors.New("signal: killed")},
	})
}

func (a *timingOutAgent) StartNewConversationWithProgress(
	_ context.Context, _, _, workDir, _ string, _ time.Duration, _ services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	if len(a.turns) == 0 {
		for name, content := range map[string]string{"README.md": "edited", "half-done.txt": "partial"} {
			if err := os.WriteFile(filepath.Join(workDir, name), []byte(content), 0o600); err != nil {
				return nil, err
			}
		}
	}
	return nil, a.killed("start", workDir)
}

func (a *timingOutAgent) ContinueConversationWithProgress(
	_ context.Context, sessionID, _, _, workDir, _ string, _ time.Duration, _ services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	return nil, a.killed("continue "+sessionID, workDir)
}

func (a *timingOutAgent) Model() string     { return "" }
func (a *timingOutAgent) AgentName() string { return "claude" }

// gitWithoutUserConfig runs git with no user or system config, so signing or hooks on the
// machine running the tests cannot change the result.
func gitWithoutUserConfig(t *testing.T) func(dir string, args ...string) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, name := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(name, "test")
	}
	for _, name := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(name, "test@example.com")
	}
	return func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
}

// Reviewer's P1: a first turn killed at its deadline in a worktree must be resumable. Without its
// session the reply starts over and removes the worktree; with uncommitted edits the resume's
// `git pull --rebase` fails.
func TestATimedOutTurnInARepoIsResumedWithItsWork(t *testing.T) {
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
	agent := &timingOutAgent{}
	handler, _ := handlerWithOutbox(t, agent, gitClient)
	handler.appState.SetRepositoryContext(&models.RepositoryContext{IsRepoMode: true, RepoPath: repo})

	err := handler.handleStartConversation(context.Background(), models.BaseMessage{
		ID:   "msg_0",
		Type: models.MessageTypeStartConversation,
		Payload: models.StartConversationPayload{
			JobID: "j1", Message: "hello", ProcessedMessageID: "cmsg_0", Mode: models.AgentModeExecute,
		},
	})
	if _, ok := core.IsSessionTimeout(err); !ok {
		t.Fatalf("start returned %v, want the session timeout", err)
	}
	job, _ := handler.appState.GetJobData("j1")
	pushed := git(remote, "for-each-ref", "--format=%(contents:subject)", "refs/heads/"+job.BranchName)
	if pushed != "WIP: turn stopped" {
		t.Errorf("last commit on the job's branch is %q, want the WIP commit", pushed)
	}

	err = handler.handleUserMessage(context.Background(), models.BaseMessage{
		ID:      "msg_1",
		Type:    models.MessageTypeUserMessage,
		Payload: models.UserMessagePayload{JobID: "j1", Message: "continue", ProcessedMessageID: "cmsg_1"},
	})
	if _, ok := core.IsSessionTimeout(err); !ok {
		t.Fatalf("reply returned %v, want the second session timeout", err)
	}

	if want := []string{"start", "continue session-1"}; !reflect.DeepEqual(agent.turns, want) {
		t.Fatalf("turns = %v, want %v", agent.turns, want)
	}
	if agent.workDirs[1] != agent.workDirs[0] {
		t.Errorf("the resumed turn ran in %s, want the first turn's worktree %s", agent.workDirs[1], agent.workDirs[0])
	}
	content, err := os.ReadFile(filepath.Join(agent.workDirs[1], "half-done.txt"))
	if err != nil || string(content) != "partial" {
		t.Errorf("the first turn's half-done.txt is gone from the worktree: %q, %v", content, err)
	}
}
