package handlers

import "testing"

func TestResolveReasoningEffort(t *testing.T) {
	tests := []struct {
		name         string
		level        string
		checkedModel string
		ownModel     string
		want         string
	}{
		{"empty level is model default", "", "claude-opus-4-8", "claude-opus-4-8", ""},
		{"default keyword is model default", "default", "claude-opus-4-8", "claude-opus-4-8", ""},
		{"matching model applies level", "high", "gpt-5.5", "gpt-5.5", "high"},
		{"max applies", "max", "opencode/claude-opus-5-5", "opencode/claude-opus-5-5", "max"},
		{"unknown level dropped", "turbo", "gpt-5.5", "gpt-5.5", ""},
		{"wrong case dropped", "High", "gpt-5.5", "gpt-5.5", ""},
		{"model mismatch dropped", "max", "claude-opus-4-8", "gpt-5.5", ""},
		{"no own model dropped", "high", "claude-opus-4-8", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveReasoningEffort(tt.level, tt.checkedModel, tt.ownModel); got != tt.want {
				t.Fatalf("resolveReasoningEffort(%q, %q, %q) = %q, want %q", tt.level, tt.checkedModel, tt.ownModel, got, tt.want)
			}
		})
	}
}
