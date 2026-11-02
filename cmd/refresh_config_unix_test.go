//go:build !windows

package main

import (
	"testing"
	"time"

	ioserver "github.com/zishang520/socket.io/servers/socket/v3"

	"nairid/models"
)

func TestRefreshConfigMessageDownloadsArtifactsAgain(t *testing.T) {
	backend := newShutdownBackend(t, true)
	backend.onConnect = func(client *ioserver.Socket) {
		msg := models.BaseMessage{ID: "msg_refresh", Type: models.MessageTypeRefreshConfig, Payload: struct{}{}}
		if err := client.Emit("cc_message", msg); err != nil {
			t.Errorf("failed to send the refresh: %v", err)
		}
	}
	daemon := startNairid(t, backend.srv.URL)

	deadline := time.Now().Add(30 * time.Second)
	for backend.artifactCalls.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("nairid downloaded artifacts %d time(s), want a second download after the refresh:\n%s",
				backend.artifactCalls.Load(), daemon.output())
		}
		time.Sleep(50 * time.Millisecond)
	}
	daemon.terminateAndWait(t)
}
