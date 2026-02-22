package clients

import (
	"strings"
	"testing"
)

func TestSanitizePRTitle(t *testing.T) {
	tests := []struct {
		name, input, expected string
	}{
		{"clean title", "feat: add email validation", "feat: add email validation"},
		{"trims whitespace", "  feat: add feature  ", "feat: add feature"},
		{"first line only", "feat: add feature\n\nDescription", "feat: add feature"},
		{"strips ## prefix", "## feat: add feature", "feat: add feature"},
		{"strips # prefix", "# feat: add feature", "feat: add feature"},
		{"strips bullet", "- feat: add feature", "feat: add feature"},
		{"strips asterisk", "* feat: add feature", "feat: add feature"},
		{"strips numbered", "1. feat: add feature", "feat: add feature"},
		{"strips double quotes", `"feat: add feature"`, "feat: add feature"},
		{"strips backticks", "`feat: add feature`", "feat: add feature"},
		{"strips preamble", "Here is the PR title: feat: add feature", "feat: add feature"},
		{"strips pr title preamble", "PR title: feat: add feature", "feat: add feature"},
		{"strips title preamble", "Title: feat: add feature", "feat: add feature"},
		{"truncates at ## Summary", "feat: add job type ## Summary - Added support", "feat: add job type"},
		{"truncates at # marker", "feat: add feature # Some heading", "feat: add feature"},
		{"removes trailing period", "feat: add feature.", "feat: add feature"},
		{"handles empty", "", ""},
		{"handles whitespace only", "   ", ""},
		{"real-world broken", "feat: add job type ## Summary - Added support for app", "feat: add job type"},
		{"multiline body", "feat: add validation\n## Summary\n- Added stuff", "feat: add validation"},
		{"preserves #42", "fix: handle issue #42 properly", "fix: handle issue #42 properly"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizePRTitle(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizePRTitle(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestValidateAndTruncatePRTitle_WithinLimit(t *testing.T) {
	tests := []struct {
		name, title, description string
	}{
		{"short title", "feat: add new feature", "This is a test description"},
		{"empty title", "", "Description"},
		{"exactly at limit", strings.Repeat("a", MaxGitHubPRTitleLength), "Description"},
		{"one character", "x", "Description"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateAndTruncatePRTitle(tt.title, tt.description)
			if result.Title != tt.title {
				t.Errorf("Expected title unchanged. Got: %q, Want: %q", result.Title, tt.title)
			}
			if result.DescriptionPrefix != "" {
				t.Errorf("Expected empty description prefix. Got: %q", result.DescriptionPrefix)
			}
		})
	}
}

func TestValidateAndTruncatePRTitle_ExceedsLimit(t *testing.T) {
	title := strings.Repeat("a", MaxGitHubPRTitleLength+50)
	result := ValidateAndTruncatePRTitle(title, "desc")
	if len(result.Title) != MaxGitHubPRTitleLength {
		t.Errorf("Expected title length %d, got %d", MaxGitHubPRTitleLength, len(result.Title))
	}
	if !strings.HasSuffix(result.Title, "...") {
		t.Errorf("Expected title to end with '...'")
	}
	if result.DescriptionPrefix == "" {
		t.Errorf("Expected non-empty description prefix")
	}
}

func TestMaxGitHubPRTitleLength_Constant(t *testing.T) {
	if MaxGitHubPRTitleLength != 256 {
		t.Errorf("MaxGitHubPRTitleLength should be 256, got %d", MaxGitHubPRTitleLength)
	}
}
