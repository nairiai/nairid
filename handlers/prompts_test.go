package handlers

import (
	"strings"
	"testing"
)

func TestAppendOutboundAttachmentInstructions_WithDir(t *testing.T) {
	base := "You are an AI assistant."
	dir := "/home/user/.config/eksecd/attachments/job-123"

	result := AppendOutboundAttachmentInstructions(base, dir)

	if !strings.Contains(result, dir) {
		t.Errorf("Expected result to contain attachments dir '%s'", dir)
	}
	if !strings.Contains(result, "Outbound Attachments") {
		t.Error("Expected result to contain 'Outbound Attachments' section header")
	}
	if !strings.Contains(result, "50 MB") {
		t.Error("Expected result to contain file size limit")
	}
	if !strings.HasPrefix(result, base) {
		t.Error("Expected result to start with original base prompt")
	}
}

func TestAppendOutboundAttachmentInstructions_EmptyDir(t *testing.T) {
	base := "You are an AI assistant."
	result := AppendOutboundAttachmentInstructions(base, "")
	if result != base {
		t.Errorf("Expected unchanged base when dir is empty, got: %s", result)
	}
}
