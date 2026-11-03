package handlers

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"nairid/models"
)

// jobFailingHTTP refuses every message of one job and accepts the rest.
type jobFailingHTTP struct {
	mu      sync.Mutex
	failJob string
	sent    []string
}

func (h *jobFailingHTTP) SubmitMessage(msg models.BaseMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sent = append(h.sent, msg.ID)
	if outboxJobID(mustJSON(msg)) == h.failJob {
		return fmt.Errorf("API returned status 500: failed to process message")
	}
	return nil
}

func (h *jobFailingHTTP) sentIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.sent...)
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func replyFor(id, jobID string) models.BaseMessage {
	return models.BaseMessage{
		ID:      id,
		Type:    models.MessageTypeAssistantMessage,
		Payload: models.AssistantMessagePayload{JobID: jobID, Message: "hi"},
	}
}

func newOutboxSender(t *testing.T, api httpSubmitter) (*MessageSender, *models.AppState) {
	t.Helper()
	appState, _, err := RestoreAppState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	ms := newMessageSenderForTest(NewConnectionState(), api, &mockWS{})
	ms.SetOutbox(appState)
	return ms, appState
}

// Replies the backend keeps refusing for one job must not hold up a reply for another job.
func TestMessageSender_StuckRepliesDoNotDelayOtherJobs(t *testing.T) {
	api := &jobFailingHTTP{failJob: "job_stuck"}
	ms, appState := newOutboxSender(t, api)
	for i := range 5 {
		err := appState.AddOutboxMessage(models.OutboxMessage{
			ID: fmt.Sprintf("msg_stuck%d", i), Event: "cc_message",
			Data: mustJSON(replyFor(fmt.Sprintf("msg_stuck%d", i), "job_stuck")), QueuedAt: time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	go ms.processQueue()
	t.Cleanup(ms.Close)

	start := time.Now()
	ms.QueueMessage("cc_message", replyFor("msg_fresh", "job_live"))
	waitUntil(t, func() bool {
		for _, id := range api.sentIDs() {
			if id == "msg_fresh" {
				return true
			}
		}
		return false
	})
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("a fresh reply waited %s behind another job's stuck replies", took)
	}
}

// A failed reply is not retried again until its backoff has passed.
func TestMessageSender_FailedReplyBacksOff(t *testing.T) {
	api := &jobFailingHTTP{failJob: "job_stuck"}
	ms, appState := newOutboxSender(t, api)
	if err := appState.AddOutboxMessage(models.OutboxMessage{
		ID: "msg_stuck", Event: "cc_message", Data: mustJSON(replyFor("msg_stuck", "job_stuck")), QueuedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	ms.resendOutbox()
	ms.resendOutbox()

	if got := len(api.sentIDs()); got != 1 {
		t.Errorf("sent %d times in two back-to-back passes, want 1", got)
	}
	if got := len(appState.GetAllOutboxMessages()); got != 1 {
		t.Errorf("outbox has %d replies, want the failed one kept", got)
	}
	if wait := ms.backOff("msg_stuck"); wait != 2*outboxRetryInterval {
		t.Errorf("second backoff = %s, want %s", wait, 2*outboxRetryInterval)
	}
}

// An older reply of the same job goes out before a newer one, and the send loop leaves both to the retrier.
func TestMessageSender_OlderReplyOfSameJobGoesFirst(t *testing.T) {
	api := &jobFailingHTTP{}
	ms, appState := newOutboxSender(t, api)
	if err := appState.AddOutboxMessage(models.OutboxMessage{
		ID: "msg_older", Event: "cc_message", Data: mustJSON(replyFor("msg_older", "job_a")), QueuedAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	newer := replyFor("msg_newer", "job_a")
	id := ms.persistToOutbox("cc_message", newer)

	ms.send(OutgoingMessage{Event: "cc_message", Data: newer, outboxID: id})
	if sent := api.sentIDs(); len(sent) != 0 {
		t.Fatalf("send loop sent %v, want nothing while an older reply of the job is waiting", sent)
	}
	ms.resendOutbox()

	sent := api.sentIDs()
	if len(sent) != 2 || sent[0] != "msg_older" || sent[1] != "msg_newer" {
		t.Errorf("sent %v, want [msg_older msg_newer]", sent)
	}
}

// gatedHTTP holds the listed messages until release is closed, and records when each one is accepted.
type gatedHTTP struct {
	mu       sync.Mutex
	gated    map[string]bool
	started  chan string
	release  chan struct{}
	accepted []string
}

func newGatedHTTP(ids ...string) *gatedHTTP {
	h := &gatedHTTP{gated: map[string]bool{}, started: make(chan string, 10), release: make(chan struct{})}
	for _, id := range ids {
		h.gated[id] = true
	}
	return h
}

func (h *gatedHTTP) SubmitMessage(msg models.BaseMessage) error {
	if h.gated[msg.ID] {
		h.started <- msg.ID
		<-h.release
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.accepted = append(h.accepted, msg.ID)
	return nil
}

func (h *gatedHTTP) acceptedIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.accepted...)
}

func addOld(t *testing.T, appState *models.AppState, id, jobID string, age time.Duration) {
	t.Helper()
	if err := appState.AddOutboxMessage(models.OutboxMessage{
		ID: id, Event: "cc_message", Data: mustJSON(replyFor(id, jobID)), QueuedAt: time.Now().Add(-age),
	}); err != nil {
		t.Fatal(err)
	}
}

// A completion queued while the retrier is still sending the job's last reply must not reach the backend first.
func TestMessageSender_CompletionWaitsForInFlightReply(t *testing.T) {
	api := newGatedHTTP("msg_reply")
	ms, appState := newOutboxSender(t, api)
	addOld(t, appState, "msg_reply", "job_a", time.Minute)
	go ms.processQueue()
	t.Cleanup(ms.Close)
	<-api.started

	ms.QueueMessage("cc_message", models.BaseMessage{
		ID: "msg_done", Type: models.MessageTypeJobComplete, Payload: models.JobCompletePayload{JobID: "job_a"},
	})
	time.Sleep(200 * time.Millisecond)
	if got := api.acceptedIDs(); len(got) != 0 {
		t.Fatalf("accepted %v while the older reply was still in flight", got)
	}
	close(api.release)

	waitUntil(t, func() bool { return len(api.acceptedIDs()) == 2 })
	if got := api.acceptedIDs(); got[0] != "msg_reply" || got[1] != "msg_done" {
		t.Errorf("accepted %v, want [msg_reply msg_done]", got)
	}
}

// Slow retries of one job's backlog must not hold up a fresh reply for another job.
func TestMessageSender_SlowBacklogDoesNotBlockOtherJobs(t *testing.T) {
	api := newGatedHTTP("msg_old1", "msg_old2")
	ms, appState := newOutboxSender(t, api)
	addOld(t, appState, "msg_old1", "job_a", 2*time.Minute)
	addOld(t, appState, "msg_old2", "job_a", time.Minute)
	go ms.processQueue()
	t.Cleanup(ms.Close)
	<-api.started

	start := time.Now()
	ms.QueueMessage("cc_message", replyFor("msg_fresh_a", "job_a"))
	ms.QueueMessage("cc_message", replyFor("msg_fresh_b", "job_b"))
	waitUntil(t, func() bool {
		for _, id := range api.acceptedIDs() {
			if id == "msg_fresh_b" {
				return true
			}
		}
		return false
	})
	if took := time.Since(start); took > time.Second {
		t.Errorf("job_b's reply waited %s behind job_a's slow backlog", took)
	}
	for _, id := range api.acceptedIDs() {
		if id == "msg_fresh_a" {
			t.Errorf("job_a's fresh reply overtook its undelivered backlog")
		}
	}

	close(api.release)
	waitUntil(t, func() bool { return len(appState.GetAllOutboxMessages()) == 0 })
	got := fmt.Sprint(api.acceptedIDs())
	if want := "[msg_fresh_b msg_old1 msg_old2 msg_fresh_a]"; got != want {
		t.Errorf("accepted %s, want %s", got, want)
	}
}
