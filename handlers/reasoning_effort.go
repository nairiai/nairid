package handlers

import (
	"cmp"
	"encoding/json"

	"nairid/core/log"
	"nairid/models"
)

var validReasoningEfforts = map[string]bool{
	"low":    true,
	"medium": true,
	"high":   true,
	"xhigh":  true,
	"max":    true,
	"ultra":  true,
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
// that has no CLI session yet. The first message it can't resume goes into the history.
func startPayloadForUnstartedJob(reply models.UserMessagePayload, job *models.JobData) models.StartConversationPayload {
	var first models.StartConversationPayload
	if len(job.StartPayload) > 0 {
		if err := json.Unmarshal(job.StartPayload, &first); err != nil {
			log.Warn("⚠️ Ignoring the stored first message of job %s: %v", reply.JobID, err)
			first = models.StartConversationPayload{}
		}
	}

	// The first message offered again rather than a new reply: run it as it came, or it lands in its own history.
	if first.ProcessedMessageID != "" && first.ProcessedMessageID == reply.ProcessedMessageID {
		return first
	}

	return models.StartConversationPayload{
		JobID:                reply.JobID,
		Message:              reply.Message,
		ProcessedMessageID:   reply.ProcessedMessageID,
		MessageLink:          reply.MessageLink,
		Mode:                 job.Mode,
		SystemPrompt:         cmp.Or(first.SystemPrompt, job.SystemPrompt),
		Attachments:          reply.Attachments,
		PreviousMessages:     historyBefore(first, reply),
		SenderMetadata:       reply.SenderMetadata,
		ReasoningEffort:      job.ReasoningEffort,
		ReasoningEffortModel: job.EffortModel,
		SessionTimeoutMs:     latestSessionTimeoutMs(reply.SessionTimeoutMs, job.SessionTimeoutMs),
	}
}

// historyBefore is what came before a reply to a first turn that never made a session: the first
// message's history, the first message, then whatever the reply brought.
func historyBefore(first models.StartConversationPayload, reply models.UserMessagePayload) []models.PreviousMessage {
	if first.Message == "" {
		return reply.PreviousMessages
	}
	history := append([]models.PreviousMessage{}, first.PreviousMessages...)
	history = append(history, models.PreviousMessage{Message: first.Message, Attachments: first.Attachments})
	return append(history, reply.PreviousMessages...)
}
