package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nairid/internal/testsupport"
	"nairid/models"
	"nairid/utils"
)

func TestTurnLogsCarryNoMessageText(t *testing.T) {
	f := newStopFixture(t)
	logs := testsupport.CaptureLog(t)
	const first = "rotate the acme staging password"
	const reply = "and the acme production one"
	start := startMessage("0")
	startPayload := start.Payload.(models.StartConversationPayload)
	startPayload.Message = first
	start.Payload = startPayload
	followUp := replyMessage("1")
	followUp.Payload = models.UserMessagePayload{JobID: "j1", Message: reply, ProcessedMessageID: "cmsg_1"}

	f.mustHandle(t, start)
	f.mustHandle(t, followUp)

	got := logs.String()
	for _, text := range []string{first, reply} {
		if strings.Contains(got, text) {
			t.Errorf("the log contains the message %q:\n%s", text, got)
		}
	}
	if !strings.Contains(got, fmt.Sprintf("(message %d chars)", len(first))) {
		t.Errorf("the log lacks the message size:\n%s", got)
	}
}

func TestFinishedJobKeepsNoLastMessage(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		f := newStopFixture(t)
		f.mustHandle(t, startMessage("0"))

		job, _ := f.handler.appState.GetJobData("j1")
		if job.Status != models.JobStatusCompleted || job.LastMessage != "" {
			t.Errorf("job = status %q, last message %q; want completed and empty", job.Status, job.LastMessage)
		}
	})

	t.Run("failed", func(t *testing.T) {
		f := newStopFixture(t)
		f.agent.failWith = codexCommandFailure("failed to start new Codex session")

		f.handler.HandleMessage(startMessage("0"))

		job, _ := f.handler.appState.GetJobData("j1")
		if job.Status != models.JobStatusFailed || job.LastMessage != "" {
			t.Errorf("job = status %q, last message %q; want failed and empty", job.Status, job.LastMessage)
		}
	})
}

func TestIdleCheckRemovesTheInboundAttachments(t *testing.T) {
	f := newStopFixture(t)
	inboundDir := utils.InboundAttachmentsDir
	utils.InboundAttachmentsDir = t.TempDir()
	t.Cleanup(func() { utils.InboundAttachmentsDir = inboundDir })
	jobDir := filepath.Join(utils.InboundAttachmentsDir, "job_j1")
	if err := os.WriteFile(filepath.Join(mustMkdir(t, jobDir), "attachment_0.png"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	job := models.JobData{JobID: "j1", PullRequestID: "none", Status: models.JobStatusCompleted, UpdatedAt: time.Now().Add(-26 * time.Hour)}
	if err := f.handler.appState.UpdateJobData("j1", job); err != nil {
		t.Fatal(err)
	}

	if err := f.handler.checkJobIdleness("j1", job); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Errorf("the inbound attachments dir of the finished job was kept (stat: %v)", err)
	}
	if _, exists := f.handler.appState.GetJobData("j1"); exists {
		t.Error("the idle job was kept")
	}
}

func mustMkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}
