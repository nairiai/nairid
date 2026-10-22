package handlers

import (
	"fmt"
	"time"

	"nairid/core"
	"nairid/core/log"
	"nairid/models"
	"nairid/services"
	"nairid/usecases"
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

// A running turn is answered by its handler once the CLI has exited; anything else at once.
func (mh *MessageHandler) StopTurn(jobID, requestedBy, requestedAt string) {
	if mh.turns.recordStopRequest(jobID, requestedAt) {
		log.Info("🛑 Stop request for job %s already handled (requested at %s)", jobID, requestedAt)
		return
	}
	processedMessageID, running := mh.turns.Stop(jobID, requestedBy)
	dropped := mh.dropQueuedTurns(jobID)
	// The backend ended every message of the job; a restart must not replay them.
	if err := mh.appState.RemoveQueuedMessagesForJob(jobID); err != nil {
		log.Warn("⚠️ Failed to clean up queued messages for job %s: %v", jobID, err)
	}
	if running {
		log.Info("🛑 Stopping the running turn of job %s (message %s, requested by %q)", jobID, processedMessageID, requestedBy)
		return
	}
	sessionID := ""
	if jobData, exists := mh.appState.GetJobData(jobID); exists {
		sessionID = jobData.ClaudeSessionID
	}
	if len(dropped) == 0 {
		log.Info("🛑 Stop requested for job %s but no turn is running", jobID)
		mh.sendTurnStopped(jobID, "", models.TurnStopOutcomeNotRunning, sessionID)
		return
	}
	log.Info("🛑 Stop requested for job %s before its turn started; %d waiting message(s) dropped", jobID, len(dropped))
	if mh.jobEvictor != nil {
		mh.jobEvictor.EvictJob(jobID)
	}
	mh.sendTurnStopped(jobID, dropped[0], models.TurnStopOutcomeStopped, sessionID)
}

func (mh *MessageHandler) dropQueuedTurns(jobID string) []string {
	if mh.jobEvictor == nil {
		return nil
	}
	var dropped []string
	for _, queued := range mh.jobEvictor.DropQueuedMessages(jobID) {
		id := processedMessageIDOf(queued)
		if id == "" {
			continue
		}
		dropped = append(dropped, id)
		log.Info("🛑 Dropped queued message %s for job %s", id, jobID)
		if err := mh.agentsApiClient.AckMessage(id); err != nil {
			log.Warn("⚠️ Failed to ack dropped message %s: %v", id, err)
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

// Like a finished turn minus the answer: the partial work is committed, nothing is posted.
func (mh *MessageHandler) finishStoppedTurn(
	jobData models.JobData,
	result *services.CLIAgentResult,
	runErr error,
	stoppedBy string,
) error {
	sessionID := sessionIDAfterStop(jobData.ClaudeSessionID, result, runErr)
	log.Info("🛑 Turn of job %s stopped by %q (session %q)", jobData.JobID, stoppedBy, sessionID)
	if sessionID == "" {
		return mh.forgetUnstartedJob(jobData)
	}

	if jobData.Mode != models.AgentModeAsk {
		commitResult, err := mh.commitStoppedWork(jobData, sessionID)
		if err != nil {
			return fmt.Errorf("auto-commit after stop failed: %w", err)
		}
		if commitResult.BranchName != "" {
			jobData.BranchName = commitResult.BranchName
		}
		if commitResult.PullRequestID != "" {
			jobData.PullRequestID = commitResult.PullRequestID
		}
	}

	jobData.ClaudeSessionID = sessionID
	jobData.Status = models.JobStatusCompleted
	jobData.UpdatedAt = time.Now()
	if err := mh.appState.UpdateJobData(jobData.JobID, jobData); err != nil {
		return fmt.Errorf("failed to persist job state after stop: %w", err)
	}
	mh.ackStoppedMessage(jobData)
	mh.sendTurnStopped(jobData.JobID, jobData.ProcessedMessageID, models.TurnStopOutcomeStopped, sessionID)
	return nil
}

func sessionIDAfterStop(known string, result *services.CLIAgentResult, runErr error) string {
	if result != nil && result.SessionID != "" {
		return result.SessionID
	}
	if stopped, ok := core.IsTurnStopped(runErr); ok && stopped.SessionID != "" {
		return stopped.SessionID
	}
	return known
}

func (mh *MessageHandler) commitStoppedWork(jobData models.JobData, sessionID string) (*usecases.AutoCommitResult, error) {
	if jobData.WorktreePath != "" {
		return mh.gitUseCase.AutoCommitChangesInWorktreeIfNeeded(jobData.MessageLink, sessionID, jobData.WorktreePath)
	}
	return mh.gitUseCase.AutoCommitChangesIfNeeded(jobData.MessageLink, sessionID)
}

// A turn stopped before the CLI printed a session id has nothing to resume: the job is
// forgotten so the next message starts it over on a fresh worktree.
func (mh *MessageHandler) forgetUnstartedJob(jobData models.JobData) error {
	if jobData.WorktreePath != "" {
		if err := mh.gitUseCase.CleanupJobWorktree(jobData.WorktreePath, jobData.BranchName); err != nil {
			log.Warn("⚠️ Failed to clean up the worktree of unstarted job %s: %v", jobData.JobID, err)
		}
	}
	if err := mh.appState.RemoveJob(jobData.JobID); err != nil {
		return fmt.Errorf("failed to forget unstarted job: %w", err)
	}
	mh.ackStoppedMessage(jobData)
	mh.sendTurnStopped(jobData.JobID, jobData.ProcessedMessageID, models.TurnStopOutcomeStopped, "")
	return nil
}

func (mh *MessageHandler) ackStoppedMessage(jobData models.JobData) {
	if err := mh.agentsApiClient.AckMessage(jobData.ProcessedMessageID); err != nil {
		log.Warn("⚠️ Failed to ack stopped message %s: %v", jobData.ProcessedMessageID, err)
	}
}

// A turn that failed before its CLI ran (git prep, attachments) still owes the backend an answer.
func (mh *MessageHandler) answerStopIfTurnEndedEarly(jobID, processedMessageID string) {
	stopped, stoppedBy := mh.turns.End(jobID)
	if !stopped {
		return
	}
	sessionID := ""
	if jobData, exists := mh.appState.GetJobData(jobID); exists {
		sessionID = jobData.ClaudeSessionID
	}
	log.Info("🛑 Turn of job %s ended before the CLI ran; answering the stop by %q", jobID, stoppedBy)
	mh.sendTurnStopped(jobID, processedMessageID, models.TurnStopOutcomeStopped, sessionID)
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
