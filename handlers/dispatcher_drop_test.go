package handlers

import (
	"path/filepath"
	"testing"
	"time"

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
	first, _ := dropped[0].Payload.(models.UserMessagePayload)
	second, _ := dropped[1].Payload.(models.UserMessagePayload)
	if first.ProcessedMessageID != "cmsg_1" || second.ProcessedMessageID != "cmsg_2" {
		t.Errorf("dropped %v", dropped)
	}
	if again := dispatcher.DropQueuedMessages("j1"); len(again) != 0 {
		t.Errorf("a second drop found %d messages", len(again))
	}
	if dispatcher.DropQueuedMessages("unknown") != nil {
		t.Error("a job with no channel has nothing to drop")
	}
}

func TestDropQueuedMessages_ReturnsWhenTheChannelIsClosed(t *testing.T) {
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	dispatcher := NewJobDispatcher(&MessageHandler{}, workerpool.New(1), appState)
	closed := make(chan models.BaseMessage, 1)
	close(closed)
	dispatcher.activeJobs["j1"] = closed

	done := make(chan []models.BaseMessage, 1)
	go func() { done <- dispatcher.DropQueuedMessages("j1") }()
	select {
	case dropped := <-done:
		if len(dropped) != 0 {
			t.Errorf("a closed channel has nothing to drop, got %d", len(dropped))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DropQueuedMessages did not return on a closed channel")
	}
}

func TestCleanup_LeavesANewerChannelOfTheSameJobOpen(t *testing.T) {
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	dispatcher := NewJobDispatcher(&MessageHandler{}, workerpool.New(1), appState)
	evicted := make(chan models.BaseMessage, 1)
	newer := make(chan models.BaseMessage, 1)
	dispatcher.activeJobs["j1"] = newer

	// The processor of the evicted channel exits late and cleans up after itself.
	dispatcher.cleanup("j1", evicted)

	if dispatcher.activeJobs["j1"] != newer {
		t.Fatal("the newer channel must stay registered")
	}
	newer <- replyMessage("1") // panics if cleanup closed it
}
