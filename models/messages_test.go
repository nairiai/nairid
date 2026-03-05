package models

import "testing"

func TestFormatSenderLabel(t *testing.T) {
	slackPlatform := PlatformSlack
	webPlatform := PlatformWeb

	tests := []struct {
		name     string
		metadata *UserMetadata
		expected string
	}{
		{
			name:     "nil metadata",
			metadata: nil,
			expected: "",
		},
		{
			name:     "empty metadata",
			metadata: &UserMetadata{},
			expected: "",
		},
		{
			name:     "id only",
			metadata: &UserMetadata{ID: strPtr("U12345")},
			expected: "",
		},
		{
			name:     "name only",
			metadata: &UserMetadata{Name: strPtr("Alice")},
			expected: "Alice",
		},
		{
			name:     "platform only",
			metadata: &UserMetadata{Platform: &slackPlatform},
			expected: "via slack",
		},
		{
			name: "name and platform",
			metadata: &UserMetadata{
				Name:     strPtr("Bob"),
				Platform: &webPlatform,
			},
			expected: "Bob via web",
		},
		{
			name: "all fields with plain email",
			metadata: &UserMetadata{
				ID:       strPtr("U999"),
				Name:     strPtr("Charlie"),
				Email:    strPtr("charlie@example.com"),
				Platform: &slackPlatform,
			},
			expected: "Charlie (charlie@example.com) via slack",
		},
		{
			name: "all fields with slack mrkdwn email",
			metadata: &UserMetadata{
				ID:       strPtr("U08S1TQ0QLR"),
				Name:     strPtr("Pres"),
				Email:    strPtr("<mailto:pmihaylov95@gmail.com|pmihaylov95@gmail.com>"),
				Platform: &slackPlatform,
			},
			expected: "Pres (pmihaylov95@gmail.com) via slack",
		},
		{
			name: "email only",
			metadata: &UserMetadata{
				Email: strPtr("alice@example.com"),
			},
			expected: "(alice@example.com)",
		},
		{
			name: "name and email without platform",
			metadata: &UserMetadata{
				Name:  strPtr("Dave"),
				Email: strPtr("dave@example.com"),
			},
			expected: "Dave (dave@example.com)",
		},
		{
			name:     "empty name string",
			metadata: &UserMetadata{Name: strPtr("")},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatSenderLabel(tt.metadata)
			if result != tt.expected {
				t.Errorf("FormatSenderLabel() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestCleanEmail(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain email",
			input:    "user@example.com",
			expected: "user@example.com",
		},
		{
			name:     "slack mrkdwn email",
			input:    "<mailto:pmihaylov95@gmail.com|pmihaylov95@gmail.com>",
			expected: "pmihaylov95@gmail.com",
		},
		{
			name:     "slack mrkdwn with different display",
			input:    "<mailto:real@company.com|display@company.com>",
			expected: "display@company.com",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "not an email",
			input:    "just some text",
			expected: "just some text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanEmail(tt.input)
			if result != tt.expected {
				t.Errorf("CleanEmail(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}
