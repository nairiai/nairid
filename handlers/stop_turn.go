package handlers

import (
	"fmt"
	"time"

	"nairid/core"
	"nairid/core/log"
	"nairid/models"
	"nairid/services"
)

func (mh *MessageHandler) handleStopTurn(msg models.BaseMessage) error {
	var payload models.StopTurnPayload
	if err := unmarshalPayload(msg.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal stop turn payload: %w", err)
	}
	if payload.JobID == "" {
		return fmt.Errorf("stop turn message has no job id")
	}
	mh.StopTurn(payload.JobID, payload.RequestedBy, payload.RequestedAt)
	return nil
}

// StopTurn ends the turn the job is running. A job with no running turn is answered
// at once; a running one is answered by its handler once the CLI has exited.
func (mh *MessageHandler) StopTurn(jobID, requestedBy, requestedAt string) {
	if !mh.turns.FirstSightOf(jobID, requestedAt) {
		log.Info("🛑 Stop request for job %s already handled (requested at %s)", jobID, requestedAt)
		return
	}
	processedMessageID, running := mh.turns.Stop(jobID, requestedBy)
	if !running {
		log.Info("🛑 Stop requested for job %s but no turn is running", jobID)
		sessionID := ""
		if jobData, exists := mh.appState.GetJobData(jobID); exists {
			sessionID = jobData.ClaudeSessionID
		}
		mh.sendTurnStopped(jobID, "", models.TurnStopOutcomeNotRunning, sessionID)
		return
	}
	log.Info("🛑 Stopping the running turn of job %s (message %s, requested by %q)", jobID, processedMessageID, requestedBy)
	for _, dropped := range mh.dropQueuedTurns(jobID) {
		log.Info("🛑 Dropped queued message %s for job %s", dropped, jobID)
		if err := mh.agentsApiClient.AckMessage(dropped); err != nil {
			log.Warn("⚠️ Failed to ack dropped message %s: %v", dropped, err)
		}
	}
}

// dropQueuedTurns takes the follow-ups waiting behind the running turn out of the
// job's queue, so a stop is not followed at once by the next turn.
func (mh *MessageHandler) dropQueuedTurns(jobID string) []string {
	if mh.jobEvictor == nil {
		return nil
	}
	var dropped []string
	for _, queued := range mh.jobEvictor.DropQueuedMessages(jobID) {
		if id := processedMessageIDOf(queued); id != "" {
			dropped = append(dropped, id)
		}
	}
	return dropped
}

func processedMessageIDOf(msg models.BaseMessage) string {
	switch msg.Type {
	case models.MessageTypeStartConversation:
		var payload models.StartConversationPayload
		if err := unmarshalPayload(msg.Payload, &payload); err == nil {
			return payload.ProcessedMessageID
		}
	case models.MessageTypeUserMessage:
		var payload models.UserMessagePayload
		if err := unmarshalPayload(msg.Payload, &payload); err == nil {
			return payload.ProcessedMessageID
		}
	}
	return ""
}

// finishStoppedTurn closes a turn whose CLI run was ended on request: the job stays
// open with the best session id known, nothing is committed or posted as an answer,
// and the backend hears that the stop happened.
func (mh *MessageHandler) finishStoppedTurn(
	jobData models.JobData,
	result *services.CLIAgentResult,
	runErr error,
	stoppedBy string,
) error {
	sessionID := jobData.ClaudeSessionID
	if result != nil && result.SessionID != "" {
		sessionID = result.SessionID
	} else if stopped, ok := core.IsTurnStopped(runErr); ok && stopped.SessionID != "" {
		sessionID = stopped.SessionID
	}
	log.Info("🛑 Turn of job %s stopped by %q (session %q)", jobData.JobID, stoppedBy, sessionID)

	jobData.ClaudeSessionID = sessionID
	jobData.Status = models.JobStatusCompleted
	jobData.UpdatedAt = time.Now()
	if err := mh.appState.UpdateJobData(jobData.JobID, jobData); err != nil {
		return fmt.Errorf("failed to persist job state after stop: %w", err)
	}
	if err := mh.agentsApiClient.AckMessage(jobData.ProcessedMessageID); err != nil {
		log.Warn("⚠️ Failed to ack stopped message %s: %v", jobData.ProcessedMessageID, err)
	}
	if err := mh.appState.RemoveQueuedMessagesForJob(jobData.JobID); err != nil {
		log.Warn("⚠️ Failed to clean up queued messages for job %s: %v", jobData.JobID, err)
	}
	mh.sendTurnStopped(jobData.JobID, jobData.ProcessedMessageID, models.TurnStopOutcomeStopped, sessionID)
	return nil
}

func (mh *MessageHandler) sendTurnStopped(jobID, processedMessageID, outcome, sessionID string) {
	stoppedMsg := models.BaseMessage{
		ID:   core.NewID("msg"),
		Type: models.MessageTypeTurnStopped,
		Payload: models.TurnStoppedPayload{
			JobID:              jobID,
			ProcessedMessageID: processedMessageID,
			Outcome:            outcome,
			SessionID:          sessionID,
		},
	}
	mh.messageSender.QueueMessage("cc_message", stoppedMsg)
	log.Info("📤 Queued turn stopped message for job %s (outcome: %s, message ID: %s)", jobID, outcome, stoppedMsg.ID)
}
