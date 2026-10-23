package handlers

import (
	"context"
	"fmt"
	"time"

	"nairid/clients"
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
	mh.stopTurn(payload.JobID, payload.RequestedBy, payload.RequestedAt)
	return nil
}

// A running turn is answered by runTurn once its handler returns; anything else at once.
func (mh *MessageHandler) stopTurn(jobID, requestedBy, requestedAt string) (droppedIDs []string) {
	if !mh.turns.claimStopRequest(jobID, requestedAt) {
		log.Info("🛑 Stop request for job %s already handled (requested at %s)", jobID, requestedAt)
		return nil
	}
	running := mh.turns.Stop(jobID)
	dropped := mh.dropQueuedMessages(jobID)
	if running {
		log.Info("🛑 Stopping the running turn of job %s (requested by %q)", jobID, requestedBy)
		return dropped
	}
	if len(dropped) == 0 {
		log.Info("🛑 Stop requested for job %s but no turn is running", jobID)
		mh.sendTurnStopped(jobID, "", models.TurnStopOutcomeNotRunning)
		return nil
	}
	log.Info("🛑 Stop requested for job %s before its turn started; %d waiting message(s) dropped", jobID, len(dropped))
	mh.jobEvictor.EvictJob(jobID)
	mh.sendTurnStopped(jobID, dropped[0], models.TurnStopOutcomeStopped)
	return dropped
}

// The backend ended the messages that were waiting when the stop was pressed; the job poll
// brings back any sent after it.
func (mh *MessageHandler) dropQueuedMessages(jobID string) (droppedIDs []string) {
	if mh.jobEvictor == nil {
		return nil
	}
	for _, queued := range mh.jobEvictor.DropQueuedMessages(jobID) {
		switch queued.Type {
		case models.MessageTypeStartConversation:
			var start models.StartConversationPayload
			if err := unmarshalPayload(queued.Payload, &start); err != nil {
				log.Warn("⚠️ Failed to read dropped message of job %s: %v", jobID, err)
				continue
			}
			droppedIDs = append(droppedIDs, start.ProcessedMessageID)
			if err := mh.keepJobWithoutSession(jobDataForStart(start)); err != nil {
				log.Error("❌ %v", err)
			}
		case models.MessageTypeUserMessage:
			var reply models.UserMessagePayload
			if err := unmarshalPayload(queued.Payload, &reply); err != nil {
				log.Warn("⚠️ Failed to read dropped message of job %s: %v", jobID, err)
				continue
			}
			droppedIDs = append(droppedIDs, reply.ProcessedMessageID)
		}
	}
	// Their handlers would have taken them off the crash-recovery queue; nothing else will.
	if err := mh.appState.RemoveQueuedMessagesForJob(jobID); err != nil {
		log.Warn("⚠️ Failed to clean up queued messages for job %s: %v", jobID, err)
	}
	return droppedIDs
}

// The turn's CLI was cut short or never ran: the session it printed is kept for the next
// message, and the partial work is committed (no Claude call, no PR) so the next turn starts
// from a clean tree.
func (mh *MessageHandler) finishStoppedTurn(
	jobData models.JobData,
	result *services.CLIAgentResult,
	runErr error,
) error {
	jobData.ClaudeSessionID = sessionIDAfterStop(jobData.ClaudeSessionID, result, runErr)
	log.Info("🛑 Turn of job %s stopped (session %q)", jobData.JobID, jobData.ClaudeSessionID)
	if jobData.ClaudeSessionID == "" {
		if jobData.WorktreePath != "" {
			if err := mh.gitUseCase.CleanupJobWorktree(jobData.WorktreePath, jobData.BranchName); err != nil {
				log.Warn("⚠️ Failed to clean up the worktree of stopped job %s: %v", jobData.JobID, err)
			}
		}
		if err := mh.keepJobWithoutSession(jobData); err != nil {
			return err
		}
		return &core.ErrTurnStopped{}
	}

	jobData.Status = models.JobStatusCompleted
	jobData.UpdatedAt = time.Now()
	if err := mh.appState.UpdateJobData(jobData.JobID, jobData); err != nil {
		return fmt.Errorf("failed to persist job state after stop: %w", err)
	}
	if jobData.Mode != models.AgentModeAsk {
		if err := mh.gitUseCase.CommitStoppedWork(jobData.WorktreePath, jobData.BranchName); err != nil {
			log.Error("❌ Failed to save the work of stopped job %s: %v", jobData.JobID, err)
			notice := fmt.Sprintf(
				"Stopped, but saving the work so far failed: %v",
				clients.RedactURLCredentials(err.Error()),
			)
			if sendErr := mh.sendSystemMessage(notice, jobData.ProcessedMessageID, jobData.JobID); sendErr != nil {
				log.Error("❌ Failed to report the unsaved work of job %s: %v", jobData.JobID, sendErr)
			}
		}
	}
	return &core.ErrTurnStopped{}
}

func sessionIDAfterStop(known string, result *services.CLIAgentResult, runErr error) string {
	if result != nil && services.SessionIDIfKnown(result.SessionID) != "" {
		return result.SessionID
	}
	if stopped, ok := core.IsTurnStopped(runErr); ok && stopped.SessionID != "" {
		return stopped.SessionID
	}
	return known
}

// A turn stopped before its CLI printed a session id has nothing to resume. The job stays,
// without a session or worktree, so the next message starts it over in the same mode and
// with the same instructions.
func (mh *MessageHandler) keepJobWithoutSession(jobData models.JobData) error {
	jobData.BranchName = ""
	jobData.WorktreePath = ""
	jobData.ClaudeSessionID = ""
	jobData.PullRequestID = "none"
	jobData.Status = models.JobStatusCompleted
	jobData.UpdatedAt = time.Now()
	if err := mh.appState.UpdateJobData(jobData.JobID, jobData); err != nil {
		return fmt.Errorf("failed to keep stopped job %s: %w", jobData.JobID, err)
	}
	return nil
}

func jobDataForStart(start models.StartConversationPayload) models.JobData {
	return models.JobData{
		JobID:              start.JobID,
		LastMessage:        start.Message,
		ProcessedMessageID: start.ProcessedMessageID,
		MessageLink:        start.MessageLink,
		Mode:               start.Mode,
		SystemPrompt:       start.SystemPrompt,
		ReasoningEffort:    start.ReasoningEffort,
		EffortModel:        start.ReasoningEffortModel,
	}
}

// The turn is registered before its handler prepares the worktree, so a stop that lands
// during the preparation still ends it.
func (mh *MessageHandler) runTurn(jobID, processedMessageID string, handle func(ctx context.Context) error) {
	turn := mh.turns.Begin(jobID)
	err := handle(turn.ctx)
	stopPressed := mh.turns.End(jobID, turn)
	if _, stopped := core.IsTurnStopped(err); stopped {
		log.Info("🛑 Answering the stop of job %s: stopped", jobID)
		mh.sendTurnStopped(jobID, processedMessageID, models.TurnStopOutcomeStopped)
		return
	}
	if stopPressed {
		// The stop came after the CLI had finished, or the turn failed on its own; either way
		// the turn is reported as it went.
		log.Info("🛑 Answering the stop of job %s: not running (%v)", jobID, err)
		mh.sendTurnStopped(jobID, processedMessageID, models.TurnStopOutcomeNotRunning)
	}
	if err == nil {
		return
	}
	if sendErr := mh.sendErrorMessage(err, processedMessageID, jobID); sendErr != nil {
		log.Error("Failed to send error message: %v", sendErr)
	}
}

func (mh *MessageHandler) sendTurnStopped(jobID, processedMessageID, outcome string) {
	sessionID := ""
	if jobData, exists := mh.appState.GetJobData(jobID); exists {
		sessionID = jobData.ClaudeSessionID
	}
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
