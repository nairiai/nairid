package handlers

import "nairid/core/log"

var validReasoningEfforts = map[string]bool{
	"low":    true,
	"medium": true,
	"high":   true,
	"xhigh":  true,
	"max":    true,
}

// resolveReasoningEffort decides which effort level a conversation runs at.
// It returns "" (model default) rather than failing, because a wrong level must
// never cost a job: Codex sends an unsupported level to the API as-is and the
// turn fails.
//
// The backend checks the level against the model it believes the agent runs,
// and sends that model id as checkedModel. ownModel is what this container was
// actually started with. They differ while a model switch is still deploying or
// after a failed deploy, and then the level is dropped.
func resolveReasoningEffort(level, checkedModel, ownModel string) string {
	if level == "" || level == "default" {
		return ""
	}
	if !validReasoningEfforts[level] {
		log.Warn("⚠️ Ignoring unknown reasoning effort %q", level)
		return ""
	}
	if checkedModel != ownModel {
		log.Warn("⚠️ Ignoring reasoning effort %q: checked for model %q but this agent runs %q", level, checkedModel, ownModel)
		return ""
	}
	return level
}
