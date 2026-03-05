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
			name: "all fields",
			metadata: &UserMetadata{
				ID:       strPtr("U999"),
				Name:     strPtr("Charlie"),
				Email:    strPtr("charlie@example.com"),
				Platform: &slackPlatform,
			},
			expected: "Charlie via slack",
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

func strPtr(s string) *string {
	return &s
}
