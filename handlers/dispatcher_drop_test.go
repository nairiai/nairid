package handlers

import (
	"path/filepath"
	"testing"

	"github.com/gammazero/workerpool"

	"nairid/models"
)

func TestDropQueuedMessages_EmptiesTheJobQueue(t *testing.T) {
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	// One worker, already busy: the job's messages pile up in its channel.
	pool := workerpool.New(1)
	busy := make(chan struct{})
	pool.Submit(func() { <-busy })
	defer close(busy)
	dispatcher := NewJobDispatcher(&MessageHandler{}, pool, appState)

	dispatcher.Dispatch(replyMessage("1"))
	dispatcher.Dispatch(replyMessage("2"))

	dropped := dispatcher.DropQueuedMessages("j1")
	if len(dropped) != 2 {
		t.Fatalf("dropped %d messages, want 2", len(dropped))
	}
	if processedMessageIDOf(dropped[0]) != "cmsg_1" || processedMessageIDOf(dropped[1]) != "cmsg_2" {
		t.Errorf("dropped %v", dropped)
	}
	if again := dispatcher.DropQueuedMessages("j1"); len(again) != 0 {
		t.Errorf("a second drop found %d messages", len(again))
	}
	if dispatcher.DropQueuedMessages("unknown") != nil {
		t.Error("a job with no channel has nothing to drop")
	}
}
