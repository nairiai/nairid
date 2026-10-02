package handlers

import (
	"nairid/core/log"
	"nairid/models"
)

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
	if checkedModel == "" || checkedModel != ownModel {
		log.Warn("⚠️ Ignoring reasoning effort %q: checked for model %q but this agent runs %q", level, checkedModel, ownModel)
		return ""
	}
	return level
}

// effortForTurn is the level one CLI run gets. It is resolved again on every
// turn: a conversation can outlive a model switch, and Codex fails a turn that
// asks for a level the new model does not have.
func effortForTurn(jobID, level, checkedModel, ownModel string) string {
	effort := resolveReasoningEffort(level, checkedModel, ownModel)
	if effort == "" {
		log.Info("🧠 Reasoning effort for job %s: model default", jobID)
	} else {
		log.Info("🧠 Reasoning effort for job %s: %s", jobID, effort)
	}
	return effort
}

// startPayloadForUnstartedJob turns a reply into a conversation start, for a job
// that has no CLI session yet. job is nil when this agent has never seen the job.
func startPayloadForUnstartedJob(reply models.UserMessagePayload, job *models.JobData) models.StartConversationPayload {
	start := models.StartConversationPayload{
		JobID:              reply.JobID,
		Message:            reply.Message,
		ProcessedMessageID: reply.ProcessedMessageID,
		MessageLink:        reply.MessageLink,
		Attachments:        reply.Attachments,
		PreviousMessages:   reply.PreviousMessages,
		SenderMetadata:     reply.SenderMetadata,
	}
	if job != nil {
		start.ReasoningEffort = job.ReasoningEffort
		start.ReasoningEffortModel = job.EffortModel
	}
	return start
}
