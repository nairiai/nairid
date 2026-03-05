package handlers

import (
	"eksecd/models"
	"testing"
)

func TestStripAccessTokenFromURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "URL with x-access-token",
			input:    "https://x-access-token:ghs_1234567890abcdefghijklmnop@github.com/owner/repo",
			expected: "https://github.com/owner/repo",
		},
		{
			name:     "URL without x-access-token",
			input:    "https://github.com/owner/repo",
			expected: "https://github.com/owner/repo",
		},
		{
			name:     "Empty URL",
			input:    "",
			expected: "",
		},
		{
			name:     "URL with x-access-token and path",
			input:    "https://x-access-token:token123@github.com/owner/repo/commit/abc123",
			expected: "https://github.com/owner/repo/commit/abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripAccessTokenFromURL(tt.input)
			if result != tt.expected {
				t.Errorf("stripAccessTokenFromURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestPrependSenderMetadata(t *testing.T) {
	slackPlatform := models.PlatformSlack

	tests := []struct {
		name     string
		message  string
		metadata *models.UserMetadata
		expected string
	}{
		{
			name:     "nil metadata returns original message",
			message:  "hello world",
			metadata: nil,
			expected: "hello world",
		},
		{
			name:     "empty metadata returns original message",
			message:  "hello world",
			metadata: &models.UserMetadata{},
			expected: "hello world",
		},
		{
			name:    "name and platform prepends sender header",
			message: "review my PR",
			metadata: &models.UserMetadata{
				Name:     strPtr("Alice"),
				Platform: &slackPlatform,
			},
			expected: "[Sender: Alice via slack]\n\nreview my PR",
		},
		{
			name:    "name only prepends sender header",
			message: "check this",
			metadata: &models.UserMetadata{
				Name: strPtr("Bob"),
			},
			expected: "[Sender: Bob]\n\ncheck this",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := prependSenderMetadata(tt.message, tt.metadata)
			if result != tt.expected {
				t.Errorf("prependSenderMetadata() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}
