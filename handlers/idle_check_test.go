package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gammazero/workerpool"

	"nairid/clients"
	"nairid/models"
	"nairid/services"
)

const inactiveForTooLong = 26 * time.Hour

// The two rules that complete a job.
var completionRules = []struct {
	name       string
	fixture    func(t *testing.T) *idleFixture
	age        time.Duration
	wantReason string
}{
	{"merged PR", func(t *testing.T) *idleFixture { return newRepoIdleFixture(t, 1) }, 0, "merged"},
	{"inactive for 25 hours", func(t *testing.T) *idleFixture { return newIdleFixture(t, 1, nil) }, inactiveForTooLong, "inactive"},
}

// scriptedAgent reports each turn as it begins, then waits for the test to end it.
type scriptedAgent struct {
	services.CLIAgent
	mu         sync.Mutex
	running    int
	maxRunning int
	entered    chan struct{}
	release    chan error
}

func newScriptedAgent() *scriptedAgent {
	return &scriptedAgent{entered: make(chan struct{}, 16), release: make(chan error, 16)}
}

func (a *scriptedAgent) run() (*services.CLIAgentResult, error) {
	a.mu.Lock()
	a.running++
	a.maxRunning = max(a.maxRunning, a.running)
	a.mu.Unlock()
	a.entered <- struct{}{}
	err := <-a.release
	a.mu.Lock()
	a.running--
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &services.CLIAgentResult{Output: "done", SessionID: "session-1"}, nil
}

func (a *scriptedAgent) StartNewConversationWithProgress(
	_ context.Context, _, _, _, _ string, _ time.Duration, _ services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	return a.run()
}

func (a *scriptedAgent) ContinueConversationWithProgress(
	_ context.Context, _, _, _, _, _ string, _ time.Duration, _ services.ProgressEmitter,
) (*services.CLIAgentResult, error) {
	return a.run()
}

func (a *scriptedAgent) Model() string     { return "" }
func (a *scriptedAgent) AgentName() string { return "claude" }

func (a *scriptedAgent) awaitTurn(t *testing.T) {
	t.Helper()
	select {
	case <-a.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no turn started")
	}
}

type idleFixture struct {
	handler    *MessageHandler
	agent      *scriptedAgent
	box        *outbox
	pool       *workerpool.WorkerPool
	dispatcher *JobDispatcher
	// Replies pull in worktree mode, so the job's branch must exist on the remote.
	publishBranch func(job *models.JobData)
	// A reply on a job whose pushed branch is gone from the remote abandons the job.
	deleteRemoteBranch func(job *models.JobData)
}

func newIdleFixture(t *testing.T, workers int, gitClient *clients.GitClient) *idleFixture {
	t.Helper()
	agent := newScriptedAgent()
	handler, box := handlerWithOutbox(t, agent, gitClient)
	pool := workerpool.New(workers)
	dispatcher := NewJobDispatcher(handler, pool, handler.appState)
	handler.SetJobEvictor(dispatcher)
	t.Cleanup(func() {
		close(agent.release)
		pool.Stop()
	})
	return &idleFixture{
		handler: handler, agent: agent, box: box, pool: pool, dispatcher: dispatcher,
		publishBranch: func(*models.JobData) {},
	}
}

// newRepoIdleFixture is a worktree-mode agent with a local remote, where gh reports every PR merged.
func newRepoIdleFixture(t *testing.T, workers int) *idleFixture {
	t.Helper()
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

	ghDir := t.TempDir()
	gh := `#!/bin/sh
case "$*" in
*mergedBy*) echo '{"mergedBy":{"login":"octocat","name":"Octo Cat"}}' ;;
*"--json state"*) echo MERGED ;;
"pr list"*) echo '[]' ;;
esac
`
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENT_EXEC_USER", "")
	t.Setenv("MAX_CONCURRENCY", "2")

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return repo })
	f := newIdleFixture(t, workers, gitClient)
	f.handler.appState.SetRepositoryContext(&models.RepositoryContext{IsRepoMode: true, RepoPath: repo})
	f.publishBranch = func(job *models.JobData) {
		git(job.WorktreePath, "push", "-q", "-u", "origin", job.BranchName)
	}
	// On the remote itself, as a merge does: the worktree keeps its upstream tracking.
	f.deleteRemoteBranch = func(job *models.JobData) {
		git(remote, "branch", "-q", "-D", job.BranchName)
	}
	return f
}

func (f *idleFixture) runIdleCheck() {
	f.handler.HandleMessage(models.BaseMessage{
		ID: "msg_idle", Type: models.MessageTypeCheckIdleJobs, Payload: models.CheckIdleJobsPayload{},
	})
}

func (f *idleFixture) hasChannel(jobID string) bool {
	f.dispatcher.mutex.Lock()
	defer f.dispatcher.mutex.Unlock()
	_, exists := f.dispatcher.activeJobs[jobID]
	return exists
}

// finishFirstTurn leaves j1 answered, with no turn running and its processor gone.
func (f *idleFixture) finishFirstTurn(t *testing.T) {
	t.Helper()
	f.dispatcher.Dispatch(startMessage("0"))
	f.agent.awaitTurn(t)
	f.agent.release <- nil
	eventually(t, "the first turn to finish", func() bool {
		return answered(f.box, "cmsg_0") && !f.handler.turns.IsRunning("j1") && !f.hasChannel("j1")
	})
}

// finishJobAged is finishFirstTurn on a job that one completion rule would now complete.
func (f *idleFixture) finishJobAged(t *testing.T, age time.Duration) {
	t.Helper()
	f.finishFirstTurn(t)
	job, _ := f.handler.appState.GetJobData("j1")
	f.publishBranch(job)
	aged := *job
	aged.UpdatedAt = time.Now().Add(-age)
	if err := f.handler.appState.UpdateJobData("j1", aged); err != nil {
		t.Fatalf("age job: %v", err)
	}
}

func (f *idleFixture) expectNotCompleted(t *testing.T) {
	t.Helper()
	if sent := f.box.ofType(models.MessageTypeJobComplete); len(sent) > 0 {
		t.Fatalf("job_complete was sent: %+v", sent[0].Payload)
	}
	if _, exists := f.handler.appState.GetJobData("j1"); !exists {
		t.Fatal("the job was removed")
	}
}

func (f *idleFixture) expectCompleted(t *testing.T, wantReason string) {
	t.Helper()
	sent := waitFor(t, f.box, models.MessageTypeJobComplete)
	payload, ok := sent[0].Payload.(models.JobCompletePayload)
	if !ok || !strings.Contains(payload.Reason, wantReason) {
		t.Fatalf("job_complete = %+v, want a reason containing %q", sent[0].Payload, wantReason)
	}
	if _, exists := f.handler.appState.GetJobData("j1"); exists {
		t.Fatal("a completed job must be removed")
	}
}

func eventually(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func queuedFor(f *idleFixture, jobID string) int {
	count := 0
	for _, queued := range f.handler.appState.GetAllQueuedMessages() {
		if queued.JobID == jobID {
			count++
		}
	}
	return count
}

func replyIDs(box *outbox) []string {
	var ids []string
	for _, msg := range box.ofType(models.MessageTypeAssistantMessage) {
		if payload, ok := msg.Payload.(models.AssistantMessagePayload); ok {
			ids = append(ids, payload.ProcessedMessageID)
		}
	}
	return ids
}

func TestIdleCheck_WaitsForARunningTurnOnAMergedPR(t *testing.T) {
	f := newRepoIdleFixture(t, 2)
	f.dispatcher.Dispatch(startMessage("0"))
	f.agent.awaitTurn(t)
	if err := f.handler.PersistQueuedMessage(replyMessage("1")); err != nil {
		t.Fatalf("persist follow-up: %v", err)
	}

	f.runIdleCheck()

	f.expectNotCompleted(t)
	if got := queuedFor(f, "j1"); got != 1 {
		t.Errorf("queued follow-ups = %d, want the 1 kept", got)
	}
	if err := f.handler.gitUseCase.CleanupOrphanedWorktrees(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	job, _ := f.handler.appState.GetJobData("j1")
	if _, err := os.Stat(job.WorktreePath); err != nil {
		t.Errorf("the running turn's worktree is gone: %v", err)
	}

	f.agent.release <- nil
	eventually(t, "the reply", func() bool { return answered(f.box, "cmsg_0") && !f.handler.turns.IsRunning("j1") })
	f.expectNotCompleted(t)

	f.runIdleCheck()

	f.expectCompleted(t, "merged")
	if got := queuedFor(f, "j1"); got != 0 {
		t.Errorf("queued follow-ups = %d, a completed job must leave none", got)
	}
	f.box.mu.Lock()
	defer f.box.mu.Unlock()
	replyAt, completeAt := -1, -1
	for i, msg := range f.box.messages {
		switch msg.Type {
		case models.MessageTypeAssistantMessage:
			replyAt = i
		case models.MessageTypeJobComplete:
			completeAt = i
		}
	}
	if replyAt < 0 || completeAt < replyAt {
		t.Errorf("reply at %d, job_complete at %d: the reply must go first", replyAt, completeAt)
	}
}

func TestIdleCheck_WaitsForAFollowUpInTheChannel(t *testing.T) {
	for _, rule := range completionRules {
		t.Run(rule.name, func(t *testing.T) {
			f := rule.fixture(t)
			f.finishJobAged(t, rule.age)

			// With the only worker taken, the follow-up stays in the channel and no turn runs.
			worker := make(chan struct{}, 1)
			t.Cleanup(func() { close(worker) })
			f.pool.Submit(func() { <-worker })
			f.dispatcher.Dispatch(replyMessage("1"))

			f.runIdleCheck()

			f.expectNotCompleted(t)

			worker <- struct{}{}
			f.agent.awaitTurn(t)
			f.agent.release <- nil
			eventually(t, "the follow-up's reply", func() bool { return answered(f.box, "cmsg_1") })
			f.expectNotCompleted(t)
		})
	}
}

// pausingPayload stops a processor that has taken its message from the channel and has not registered
// the turn yet: HandleMessage marshals the payload in between.
type pausingPayload struct {
	models.UserMessagePayload
	armed    atomic.Bool
	pause    sync.Once
	dequeued chan struct{}
	resume   chan struct{}
}

func (p *pausingPayload) MarshalJSON() ([]byte, error) {
	if p.armed.Load() {
		p.pause.Do(func() {
			close(p.dequeued)
			<-p.resume
		})
	}
	return json.Marshal(p.UserMessagePayload)
}

func TestIdleCheck_WaitsForAMessageBetweenItsChannelAndItsTurn(t *testing.T) {
	for _, rule := range completionRules {
		t.Run(rule.name, func(t *testing.T) {
			f := rule.fixture(t)
			f.finishJobAged(t, rule.age)

			payload := &pausingPayload{
				UserMessagePayload: models.UserMessagePayload{JobID: "j1", Message: "and again", ProcessedMessageID: "cmsg_1"},
				dequeued:           make(chan struct{}),
				resume:             make(chan struct{}),
			}
			resume := sync.OnceFunc(func() { close(payload.resume) })
			t.Cleanup(resume)

			// Dispatch marshals the payload too, so the processor is held back until it is armed.
			worker := make(chan struct{})
			f.pool.Submit(func() { <-worker })
			f.dispatcher.Dispatch(models.BaseMessage{ID: "msg_1", Type: models.MessageTypeUserMessage, Payload: payload})
			payload.armed.Store(true)
			close(worker)
			select {
			case <-payload.dequeued:
			case <-time.After(5 * time.Second):
				t.Fatal("the processor did not take the message")
			}
			if f.handler.turns.IsRunning("j1") {
				t.Fatal("the barrier must hold the message before its turn is registered")
			}

			f.runIdleCheck()

			f.expectNotCompleted(t)

			resume()
			f.agent.awaitTurn(t)
			f.agent.release <- nil
			eventually(t, "the message's reply", func() bool { return answered(f.box, "cmsg_1") })
			f.expectNotCompleted(t)
		})
	}
}

func TestDispatch_LeavesAMessageForTheNextPollWhileItsJobIsBeingCompleted(t *testing.T) {
	f := newIdleFixture(t, 1, nil)
	if !f.dispatcher.ClaimCompletion("j1") {
		t.Fatal("a job with no messages must be claimable")
	}
	if f.dispatcher.ClaimCompletion("j1") {
		t.Fatal("a second idle check must not complete the job as well")
	}

	f.dispatcher.Dispatch(startMessage("0"))

	if f.hasChannel("j1") || f.dispatcher.IsMessageInFlight("cmsg_0") {
		t.Fatal("the message must be left for the next poll")
	}

	f.dispatcher.ReleaseCompletion("j1")
	f.dispatcher.Dispatch(startMessage("0"))
	f.agent.awaitTurn(t)
	if f.dispatcher.ClaimCompletion("j1") {
		t.Fatal("a running message must refuse the claim")
	}
	f.agent.release <- nil
	eventually(t, "the claim once the message finished", func() bool { return f.dispatcher.ClaimCompletion("j1") })
}

// A reply on a job whose branch is gone removes the job, and its processor leaves. A message left
// holding its claim would keep any later job with this id from completing.
func TestProcessorLeavingAnAbandonedJob_ReleasesWhatItLeftBehind(t *testing.T) {
	f := newRepoIdleFixture(t, 1)
	f.finishFirstTurn(t)
	job, _ := f.handler.appState.GetJobData("j1")
	f.publishBranch(job)
	f.deleteRemoteBranch(job)

	worker := make(chan struct{})
	f.pool.Submit(func() { <-worker })
	f.dispatcher.Dispatch(replyMessage("1"))
	f.dispatcher.Dispatch(replyMessage("2"))
	close(worker)

	eventually(t, "the left-behind message to return to the poller", func() bool {
		return !f.hasChannel("j1") && !f.dispatcher.IsMessageInFlight("cmsg_2")
	})
	if _, exists := f.handler.appState.GetJobData("j1"); exists {
		t.Fatal("the reply must have abandoned the job")
	}
	if !f.dispatcher.ClaimCompletion("j1") {
		t.Error("the left-behind message must not hold up a completion")
	}
}

// The PR lookups can take minutes, and idle checks can overlap.
func TestIdleCheck_RechecksTheJobOnceItHoldsTheClaim(t *testing.T) {
	f := newIdleFixture(t, 1, nil)
	f.finishFirstTurn(t)
	job, _ := f.handler.appState.GetJobData("j1")
	snapshot := *job
	snapshot.UpdatedAt = time.Now().Add(-inactiveForTooLong)

	if err := f.handler.checkJobIdleness("j1", snapshot); err != nil {
		t.Fatalf("check on a job that moved on: %v", err)
	}
	f.expectNotCompleted(t)

	if err := f.handler.appState.RemoveJob("j1"); err != nil {
		t.Fatalf("remove job: %v", err)
	}
	if err := f.handler.checkJobIdleness("j1", snapshot); err != nil {
		t.Fatalf("check on a job already completed: %v", err)
	}
	if completes := f.box.ofType(models.MessageTypeJobComplete); len(completes) != 0 {
		t.Errorf("job_complete sent for a job that is gone: %d", len(completes))
	}
}

// A processor blocked on an empty channel and an in_progress status do not mean work is pending:
// treating them as such would keep a stuck job forever.
func TestIdleCheck_CompletesAStuckJobWithNothingPending(t *testing.T) {
	for _, rule := range completionRules {
		seed := func(t *testing.T) *idleFixture {
			t.Helper()
			f := rule.fixture(t)
			stuck := models.JobData{
				JobID: "j1", BranchName: "stuck-branch", Status: models.JobStatusInProgress,
				UpdatedAt: time.Now().Add(-rule.age),
			}
			if err := f.handler.appState.UpdateJobData("j1", stuck); err != nil {
				t.Fatalf("seed job: %v", err)
			}
			return f
		}

		t.Run(rule.name, func(t *testing.T) {
			f := seed(t)
			ch := make(chan models.BaseMessage, 100)
			f.dispatcher.mutex.Lock()
			f.dispatcher.activeJobs["j1"] = ch
			f.dispatcher.mutex.Unlock()
			exited := make(chan struct{})
			f.pool.Submit(func() {
				f.dispatcher.processJobMessages("j1", ch)
				close(exited)
			})

			f.runIdleCheck()

			f.expectCompleted(t, rule.wantReason)
			if f.hasChannel("j1") {
				t.Error("the job's channel must be gone")
			}
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				t.Fatal("the blocked processor must exit and free its worker")
			}
		})

		t.Run(rule.name+" with no dispatcher", func(t *testing.T) {
			f := seed(t)
			f.handler.SetJobEvictor(nil)

			f.runIdleCheck()

			f.expectCompleted(t, rule.wantReason)
		})
	}
}

func TestFailedTurn_KeepsOneProcessorForTheMessagesBehindIt(t *testing.T) {
	tests := []struct {
		name      string
		priorTurn bool
		failing   models.BaseMessage
		wantOrder []string
	}{
		{"start", false, startMessage("1"), []string{"cmsg_2", "cmsg_3"}},
		{"continue", true, replyMessage("1"), []string{"cmsg_0", "cmsg_2", "cmsg_3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newIdleFixture(t, 3, nil)
			if tt.priorTurn {
				f.finishFirstTurn(t)
			}

			f.dispatcher.Dispatch(tt.failing)
			f.agent.awaitTurn(t)
			f.dispatcher.Dispatch(replyMessage("2"))
			f.agent.release <- errors.New("claude exited")
			f.agent.awaitTurn(t)

			f.dispatcher.Dispatch(replyMessage("3"))
			select {
			case <-f.agent.entered:
				t.Fatal("a message started its turn while the job's previous turn was still running")
			case <-time.After(300 * time.Millisecond):
			}

			f.agent.release <- nil
			f.agent.awaitTurn(t)
			f.agent.release <- nil
			eventually(t, "the last reply", func() bool { return answered(f.box, "cmsg_3") })

			f.agent.mu.Lock()
			maxRunning := f.agent.maxRunning
			f.agent.mu.Unlock()
			if maxRunning != 1 {
				t.Errorf("%d turns ran at once, want 1", maxRunning)
			}
			if order := replyIDs(f.box); strings.Join(order, ",") != strings.Join(tt.wantOrder, ",") {
				t.Errorf("replies = %v, want %v", order, tt.wantOrder)
			}
		})
	}
}

func TestFailedTurn_WithNothingBehindItFreesTheWorker(t *testing.T) {
	tests := []struct {
		name      string
		priorTurn bool
		failing   models.BaseMessage
	}{
		{"start", false, startMessage("1")},
		{"continue", true, replyMessage("1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newIdleFixture(t, 1, nil)
			if tt.priorTurn {
				f.finishFirstTurn(t)
			}

			f.dispatcher.Dispatch(tt.failing)
			f.agent.awaitTurn(t)
			f.agent.release <- errors.New("claude exited")

			f.dispatcher.Dispatch(models.BaseMessage{
				ID:   "msg_other",
				Type: models.MessageTypeStartConversation,
				Payload: models.StartConversationPayload{
					JobID: "j2", Message: "hello", ProcessedMessageID: "cmsg_other", Mode: models.AgentModeAsk,
				},
			})
			f.agent.awaitTurn(t)
			f.agent.release <- nil

			eventually(t, "the other job's reply", func() bool { return answered(f.box, "cmsg_other") })
			if f.hasChannel("j1") {
				t.Error("the failed job's channel must be gone")
			}
		})
	}
}
