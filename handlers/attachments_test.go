package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"nairid/clients"
	"nairid/models"
	"nairid/utils"
)

// All the contents have the same file type on purpose, since they then get the same extension.
func handlerServingAttachments(t *testing.T, contents map[string]string) *MessageHandler {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := path.Base(r.URL.Path)
		content, ok := contents[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":   id,
			"data": base64.StdEncoding.EncodeToString([]byte(content)),
		})
	}))
	t.Cleanup(backend.Close)
	return &MessageHandler{agentsApiClient: clients.NewAgentsApiClient("k", backend.URL, "agent")}
}

func attachmentSession(t *testing.T) string {
	t.Helper()
	sessionID := fmt.Sprintf("job_%s_%d", t.Name(), os.Getpid())
	dir, err := utils.GetAttachmentsDir(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return sessionID
}

func attachmentsOf(ids ...string) []models.MessageAttachment {
	attachments := make([]models.MessageAttachment, 0, len(ids))
	for _, id := range ids {
		attachments = append(attachments, models.MessageAttachment{AttachmentID: id})
	}
	return attachments
}

func assertFileBytes(t *testing.T, filePath, want string) {
	t.Helper()
	got, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s holds %q, want %q", filepath.Base(filePath), got, want)
	}
}

func assertFileCount(t *testing.T, sessionID string, want int) {
	t.Helper()
	dir, err := utils.GetAttachmentsDir(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != want {
		t.Errorf("directory holds %d files, want %d", len(entries), want)
	}
}

func TestThreadHistoryAttachmentsKeepTheirOwnBytes(t *testing.T) {
	mh := handlerServingAttachments(t, map[string]string{
		"att_first":  "first bytes",
		"att_second": "second bytes",
		"att_third":  "third bytes",
	})
	sessionID := attachmentSession(t)

	prompt, paths, err := mh.formatThreadContext(
		[]models.PreviousMessage{
			{Message: "one", Attachments: attachmentsOf("att_first")},
			{Message: "two", Attachments: attachmentsOf("att_second")},
		},
		"three",
		attachmentsOf("att_third"),
		sessionID,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(paths) != 3 {
		t.Fatalf("got %d paths, want 3", len(paths))
	}
	assertFileBytes(t, paths[0], "first bytes")
	assertFileBytes(t, paths[1], "second bytes")
	assertFileBytes(t, paths[2], "third bytes")
	assertFileCount(t, sessionID, 3)
	for _, filePath := range paths {
		if !strings.Contains(prompt, filePath) {
			t.Errorf("prompt doesn't list %s", filePath)
		}
	}
}

func TestSeveralAttachmentsOnOneMessageKeepTheirOwnBytes(t *testing.T) {
	mh := handlerServingAttachments(t, map[string]string{
		"att_first":  "first bytes",
		"att_second": "second bytes",
	})
	sessionID := attachmentSession(t)

	_, paths, err := mh.formatThreadContext(nil, "one", attachmentsOf("att_first", "att_second"), sessionID)
	if err != nil {
		t.Fatal(err)
	}

	if len(paths) != 2 {
		t.Fatalf("got %d paths, want 2", len(paths))
	}
	assertFileBytes(t, paths[0], "first bytes")
	assertFileBytes(t, paths[1], "second bytes")
	assertFileCount(t, sessionID, 2)
}

func TestFollowUpTurnKeepsEarlierAttachment(t *testing.T) {
	mh := handlerServingAttachments(t, map[string]string{
		"att_first":  "first bytes",
		"att_second": "second bytes",
	})
	sessionID := attachmentSession(t)

	_, firstTurn, err := mh.formatThreadContext(nil, "one", attachmentsOf("att_first"), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	followUp, _, err := mh.processAttachmentsForPrompt([]string{"att_second"}, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	assertFileBytes(t, firstTurn[0], "first bytes")
	assertFileBytes(t, followUp[0], "second bytes")
	assertFileCount(t, sessionID, 2)
}
