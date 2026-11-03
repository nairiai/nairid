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

// An older reply of the same job goes out before a newer one.
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

	sent := api.sentIDs()
	if len(sent) != 2 || sent[0] != "msg_older" || sent[1] != "msg_newer" {
		t.Errorf("sent %v, want [msg_older msg_newer]", sent)
	}
}
