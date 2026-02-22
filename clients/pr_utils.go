package clients

import (
	"regexp"
	"strings"
)

const (
	MaxGitHubPRTitleLength = 256
)

type PRTitleValidationResult struct {
	Title             string
	DescriptionPrefix string
}

func SanitizePRTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return title
	}
	if idx := strings.IndexAny(title, "\n\r"); idx != -1 {
		title = title[:idx]
	}
	title = regexp.MustCompile(`^#{1,6}\s+`).ReplaceAllString(title, "")
	title = regexp.MustCompile(`^[-*]\s+`).ReplaceAllString(title, "")
	title = regexp.MustCompile(`^\d+\.\s+`).ReplaceAllString(title, "")
	if len(title) >= 2 {
		first := title[0]
		last := title[len(title)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') || (first == '`' && last == '`') {
			title = title[1 : len(title)-1]
		}
	}
	preambles := []string{"here is the pr title:", "here's the pr title:", "pr title:", "the pr title is:", "updated title:", "suggested title:", "title:"}
	lowerTitle := strings.ToLower(title)
	for _, p := range preambles {
		if strings.HasPrefix(lowerTitle, p) {
			title = strings.TrimSpace(title[len(p):])
			lowerTitle = strings.ToLower(title)
		}
	}
	if idx := strings.Index(title, " ## "); idx != -1 {
		title = title[:idx]
	}
	if idx := strings.Index(title, " # "); idx != -1 {
		title = title[:idx]
	}
	title = strings.TrimRight(title, ".")
	return strings.TrimSpace(title)
}

func ValidateAndTruncatePRTitle(title, description string) PRTitleValidationResult {
	if len(title) <= MaxGitHubPRTitleLength {
		return PRTitleValidationResult{Title: title, DescriptionPrefix: ""}
	}
	truncateAt := MaxGitHubPRTitleLength - 3
	truncatedTitle := title[:truncateAt] + "..."
	overflowText := title[truncateAt:]
	var descriptionPrefix strings.Builder
	descriptionPrefix.WriteString(overflowText)
	descriptionPrefix.WriteString("\n\n---\n\n")
	return PRTitleValidationResult{Title: truncatedTitle, DescriptionPrefix: descriptionPrefix.String()}
}
