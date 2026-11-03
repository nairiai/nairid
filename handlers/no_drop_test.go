package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gammazero/workerpool"

	"nairid/clients"
	"nairid/core/env"
	"nairid/models"
	"nairid/usecases"
)

// A reply queued just before the container stops is still sent by the next container.
func TestMessageSender_ReplyQueuedBeforeRestartIsSentAfterIt(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	appState, _, err := RestoreAppState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	before := newMessageSenderForTest(NewConnectionState(), &mockHTTP{}, &mockWS{})
	before.SetOutbox(appState)
	// Nothing consumes the queue: the container stops before the send.
	before.QueueMessage("cc_message", makeTestMessage(models.MessageTypeAssistantMessage).Data)

	restored, _, err := RestoreAppState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(restored.GetAllOutboxMessages()); got != 1 {
		t.Fatalf("outbox after restart has %d replies, want 1", got)
	}
	httpMock := &mockHTTP{}
	after := newMessageSenderForTest(NewConnectionState(), httpMock, &mockWS{})
	after.SetOutbox(restored)
	go after.processQueue()
	t.Cleanup(after.Close)

	waitUntil(t, func() bool { return len(restored.GetAllOutboxMessages()) == 0 })
	calls := httpMock.getCalls()
	if len(calls) != 1 || calls[0].ID != "msg_test123" {
		t.Errorf("sent %+v, want the one queued reply", calls)
	}
}

// The reply is on disk before the send starts, and gone once the backend has it.
func TestMessageSender_PersistsBeforeSendAndRemovesAfter(t *testing.T) {
	appState, _, err := RestoreAppState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	httpMock := &outboxCheckingHTTP{appState: appState}
	ms := newMessageSenderForTest(NewConnectionState(), httpMock, &mockWS{})
	ms.SetOutbox(appState)
	go ms.processQueue()
	t.Cleanup(ms.Close)

	ms.QueueMessage("cc_message", makeTestMessage(models.MessageTypeAssistantMessage).Data)

	waitUntil(t, func() bool { return len(appState.GetAllOutboxMessages()) == 0 })
	if !httpMock.sawItPersisted() {
		t.Error("the reply was sent before it was persisted")
	}
}

// A periodic resend must not send a reply a second time while it waits in the queue.
func TestMessageSender_ResendSkipsRepliesAlreadyQueued(t *testing.T) {
	appState, _, err := RestoreAppState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	httpMock := &mockHTTP{}
	ms := newMessageSenderForTest(NewConnectionState(), httpMock, &mockWS{})
	ms.SetOutbox(appState)
	ms.QueueMessage("cc_message", makeTestMessage(models.MessageTypeAssistantMessage).Data)

	ms.resendOutbox()
	if got := len(httpMock.getCalls()); got != 0 {
		t.Errorf("resend sent %d replies that were already queued", got)
	}
}

type outboxCheckingHTTP struct {
	mu        sync.Mutex
	appState  *models.AppState
	persisted bool
}

func (h *outboxCheckingHTTP) SubmitMessage(msg models.BaseMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, entry := range h.appState.GetAllOutboxMessages() {
		if entry.ID == msg.ID {
			h.persisted = true
		}
	}
	return nil
}

func (h *outboxCheckingHTTP) sawItPersisted() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.persisted
}

// A failed turn is finished: its message is done, so a redelivery is acked instead of rerun,
// and a follow-up waiting behind it stays queued.
func TestRunTurn_FailedTurnIsDoneAndKeepsFollowUps(t *testing.T) {
	f := newStopFixture(t)
	f.mustHandle(t, startMessage("0"))
	appState := f.handler.appState
	queueFollowUp(t, appState, "cmsg_1")
	queueFollowUp(t, appState, "cmsg_2")

	f.handler.runTurn("j1", "cmsg_1", func(ctx context.Context) error { return errors.New("cli crashed") })

	if !appState.IsMessageDone("cmsg_1") {
		t.Error("a failed turn's message must be done so it is acked, not redelivered for an hour")
	}
	if queuedIDs(appState)["cmsg_1"] {
		t.Error("the failed message must leave the queue")
	}
	if !queuedIDs(appState)["cmsg_2"] {
		t.Error("the follow-up behind the failed turn was dropped")
	}
}

// A turn that ended in success used to clear every queued message of its job.
func TestHandleMessage_SuccessKeepsFollowUpsQueued(t *testing.T) {
	f := newStopFixture(t)
	queueFollowUp(t, f.handler.appState, "cmsg_7")

	f.mustHandle(t, startMessage("0"))

	if !queuedIDs(f.handler.appState)["cmsg_7"] {
		t.Error("a follow-up queued during the turn was dropped when the turn succeeded")
	}
}

// An error caused by the container stopping leaves the turn for recovery on the next start.
func TestRunTurn_ErrorDuringShutdownIsLeftForRecovery(t *testing.T) {
	f := newStopFixture(t)
	f.mustHandle(t, startMessage("0"))
	MarkShuttingDown()
	t.Cleanup(func() { shuttingDown.Store(false) })

	f.handler.runTurn("j1", "cmsg_1", func(ctx context.Context) error { return errors.New("signal: terminated") })

	if f.handler.appState.IsMessageDone("cmsg_1") {
		t.Error("a turn cut by shutdown must not be marked done")
	}
	if got := len(f.outbox.ofType(models.MessageTypeSystemMessage)); got != 0 {
		t.Errorf("a turn cut by shutdown reported %d errors to the user", got)
	}
}

// The poller acks a finished message, skips one already in flight without acking it, and
// persists a new one before dispatching it.
func TestPoller_AcksDoneSkipsInFlightPersistsNew(t *testing.T) {
	f := newStopFixture(t)
	appState := f.handler.appState
	if err := appState.FinishMessage("cmsg_done", time.Now()); err != nil {
		t.Fatal(err)
	}
	pool := workerpool.New(1)
	defer pool.Stop()
	dispatcher := NewJobDispatcher(f.handler, pool, appState)
	ch := make(chan models.BaseMessage, 10)
	dispatcher.activeJobs["j1"] = ch
	dispatcher.Dispatch(replyMessage("busy"))

	var mu sync.Mutex
	var acked []string
	offered := []clients.AgentJobMessage{pollMessage(t, "done"), pollMessage(t, "busy"), pollMessage(t, "new")}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ack") {
			mu.Lock()
			acked = append(acked, r.URL.Path)
			mu.Unlock()
			return
		}
		_ = json.NewEncoder(w).Encode(clients.AgentJobsResponse{Jobs: []clients.AgentJob{{ID: "j1", Mode: "ask", Messages: offered}}})
	}))
	defer backend.Close()
	poller := NewMessagePoller(clients.NewAgentsApiClient("k", backend.URL, "agent"), dispatcher, f.handler, appState, time.Hour)
	poller.pollAndDispatch()

	mu.Lock()
	defer mu.Unlock()
	if len(acked) != 1 || !strings.Contains(acked[0], "cmsg_done") {
		t.Errorf("acked %v, want only cmsg_done", acked)
	}
	if len(ch) != 2 {
		t.Errorf("job channel has %d messages, want busy + new", len(ch))
	}
	if !queuedIDs(appState)["cmsg_new"] {
		t.Error("a polled message must be persisted before it is dispatched")
	}
}

// Queued messages that carry their payload are replayed after a restart, even when the job
// they start does not exist yet or the job they follow has completed.
func TestRecoverJobs_ReplaysQueuedMessagesWithPayload(t *testing.T) {
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	now := time.Now()
	if err := appState.UpdateJobData("j_done", models.JobData{
		JobID: "j_done", ClaudeSessionID: "sess_1", ProcessedMessageID: "cmsg_old", Status: models.JobStatusCompleted, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	start, _ := json.Marshal(models.StartConversationPayload{JobID: "j_new", Message: "hi", ProcessedMessageID: "cmsg_start", Mode: models.AgentModeAsk})
	followUp, _ := json.Marshal(models.UserMessagePayload{JobID: "j_done", Message: "one more", ProcessedMessageID: "cmsg_follow"})
	orphan, _ := json.Marshal(models.UserMessagePayload{JobID: "j_gone", Message: "hello?", ProcessedMessageID: "cmsg_orphan"})
	for _, q := range []models.QueuedMessage{
		{ProcessedMessageID: "cmsg_start", JobID: "j_new", MessageType: models.MessageTypeStartConversation, QueuedAt: now, Payload: start},
		{ProcessedMessageID: "cmsg_follow", JobID: "j_done", MessageType: models.MessageTypeUserMessage, QueuedAt: now, Payload: followUp},
		{ProcessedMessageID: "cmsg_orphan", JobID: "j_gone", MessageType: models.MessageTypeUserMessage, QueuedAt: now, Payload: orphan},
	} {
		if err := appState.AddQueuedMessage(q); err != nil {
			t.Fatal(err)
		}
	}

	wp := workerpool.New(1)
	defer wp.StopWait()
	dispatcher := NewJobDispatcher(nil, wp, appState)
	jNew := make(chan models.BaseMessage, 10)
	jDone := make(chan models.BaseMessage, 10)
	dispatcher.activeJobs["j_new"] = jNew
	dispatcher.activeJobs["j_done"] = jDone

	RecoverJobs(appState, nil, dispatcher, nil)

	if len(jNew) != 1 || (<-jNew).Type != models.MessageTypeStartConversation {
		t.Error("a queued start for a job not created yet must be replayed")
	}
	if len(jDone) != 1 {
		t.Error("a queued follow-up for a completed job must be replayed")
	}
	if !queuedIDs(appState)["cmsg_orphan"] {
		t.Error("a reply whose job is unknown must stay queued for the poller, not be dropped")
	}
}

func queueFollowUp(t *testing.T, appState *models.AppState, processedMessageID string) {
	t.Helper()
	if err := appState.AddQueuedMessage(models.QueuedMessage{
		ProcessedMessageID: processedMessageID, JobID: "j1", MessageType: models.MessageTypeUserMessage, Message: "and again", QueuedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

func queuedIDs(appState *models.AppState) map[string]bool {
	ids := make(map[string]bool)
	for _, q := range appState.GetAllQueuedMessages() {
		ids[q.ProcessedMessageID] = true
	}
	return ids
}

func pollMessage(t *testing.T, id string) clients.AgentJobMessage {
	t.Helper()
	payload, err := json.Marshal(models.UserMessagePayload{JobID: "j1", Message: "msg " + id, ProcessedMessageID: "cmsg_" + id})
	if err != nil {
		t.Fatal(err)
	}
	return clients.AgentJobMessage{ID: "cmsg_" + id, Type: models.MessageTypeUserMessage, Payload: payload}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Recovery runs before the sender starts. Two jobs it cannot resume used to fill the one-slot
// queue and hang startup; their notices now wait in the outbox for the sender.
func TestRecoverJobs_NonResumableJobsDoNotBlockStartup(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	appState, _, err := RestoreAppState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-25 * time.Hour)
	for _, id := range []string{"1", "2"} {
		if err := appState.UpdateJobData("j"+id, models.JobData{
			JobID: "j" + id, ProcessedMessageID: "cmsg_" + id, Status: models.JobStatusInProgress, UpdatedAt: stale,
		}); err != nil {
			t.Fatal(err)
		}
	}
	envManager, err := env.NewEnvManager()
	if err != nil {
		t.Fatal(err)
	}
	apiClient := clients.NewAgentsApiClient("k", "http://127.0.0.1:1", "agent")
	sender := NewMessageSender(NewConnectionState(), apiClient)
	sender.SetOutbox(appState)
	agent := &stoppableAgent{started: make(chan struct{})}
	handler := NewMessageHandler(agent, usecases.NewGitUseCase(nil, agent, appState), appState, envManager, sender, apiClient)
	wp := workerpool.New(1)
	defer wp.StopWait()

	recovered := make(chan struct{})
	go func() {
		RecoverJobs(appState, usecases.NewGitUseCase(nil, agent, appState), NewJobDispatcher(handler, wp, appState), handler)
		close(recovered)
	}()
	select {
	case <-recovered:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery blocked before the sender started")
	}

	if got := len(appState.GetAllOutboxMessages()); got != 2 {
		t.Errorf("outbox holds %d restart notices, want 2", got)
	}
	for _, id := range []string{"cmsg_1", "cmsg_2"} {
		if !appState.IsMessageDone(id) {
			t.Errorf("%s should be done once its user is told", id)
		}
	}
}

// A WS emit is not acknowledged by the backend, so it cannot take a reply out of the outbox.
func TestMessageSender_FailedHTTPKeepsReplyInOutbox(t *testing.T) {
	appState, _, err := RestoreAppState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	ws := &mockWS{}
	ms := newMessageSenderForTest(NewConnectionState(), &mockHTTP{failCount: -1}, ws)
	ms.SetOutbox(appState)
	id := ms.persistToOutbox("cc_message", makeTestMessage(models.MessageTypeAssistantMessage).Data)

	ms.send(OutgoingMessage{Event: "cc_message", Data: makeTestMessage(models.MessageTypeAssistantMessage).Data, outboxID: id})

	if got := len(appState.GetAllOutboxMessages()); got != 1 {
		t.Errorf("outbox has %d replies after a failed send, want 1", got)
	}
	if len(ws.calls) != 0 {
		t.Errorf("an outbox reply went over WS %d times; only HTTP proves delivery", len(ws.calls))
	}
	if !ms.markPending(id) {
		t.Error("a failed reply must be free for the next resend")
	}
}

// Age alone never drops a reply: one left over from a long stop is still sent.
func TestMessageSender_OldReplyIsStillResent(t *testing.T) {
	appState, _, err := RestoreAppState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(makeTestMessage(models.MessageTypeAssistantMessage).Data)
	if err := appState.AddOutboxMessage(models.OutboxMessage{ID: "msg_old", Event: "cc_message", Data: raw, QueuedAt: time.Now().Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	httpMock := &mockHTTP{}
	ms := newMessageSenderForTest(NewConnectionState(), httpMock, &mockWS{})
	ms.SetOutbox(appState)

	ms.resendOutbox()

	if len(httpMock.getCalls()) != 1 || len(appState.GetAllOutboxMessages()) != 0 {
		t.Errorf("a 2-day-old reply was not delivered: %d sends, %d left", len(httpMock.getCalls()), len(appState.GetAllOutboxMessages()))
	}
}

// A reply the backend refuses for good (a 4xx such as 400) leaves the outbox instead of looping.
func TestMessageSender_PermanentRejectionLeavesOutbox(t *testing.T) {
	appState, _, err := RestoreAppState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	ms := newMessageSenderForTest(NewConnectionState(), rejectingHTTP{}, &mockWS{})
	ms.SetOutbox(appState)
	id := ms.persistToOutbox("cc_message", makeTestMessage(models.MessageTypeAssistantMessage).Data)

	ms.deliverFromOutbox(OutgoingMessage{Event: "cc_message", Data: makeTestMessage(models.MessageTypeAssistantMessage).Data, outboxID: id})

	if got := len(appState.GetAllOutboxMessages()); got != 0 {
		t.Errorf("outbox kept %d rejected replies", got)
	}
}

type rejectingHTTP struct{}

func (rejectingHTTP) SubmitMessage(models.BaseMessage) error {
	return &clients.StatusError{Code: http.StatusBadRequest, Body: "invalid"}
}
