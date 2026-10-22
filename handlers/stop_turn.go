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

// The stop is answered first; the partial work is then kept as a local WIP commit (nothing is
// pushed, no PR is opened), so the next reply's pull --rebase finds a clean tree.
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

	jobData.ClaudeSessionID = sessionID
	jobData.Status = models.JobStatusCompleted
	jobData.UpdatedAt = time.Now()
	if err := mh.appState.UpdateJobData(jobData.JobID, jobData); err != nil {
		return fmt.Errorf("failed to persist job state after stop: %w", err)
	}
	mh.ackStoppedMessage(jobData)
	mh.sendTurnStopped(jobData.JobID, jobData.ProcessedMessageID, models.TurnStopOutcomeStopped, sessionID)

	if jobData.Mode == models.AgentModeAsk {
		return nil
	}
	if err := mh.gitUseCase.CommitStoppedWork(jobData.WorktreePath, stoppedBy); err != nil {
		log.Error("❌ Failed to commit the work of stopped job %s: %v", jobData.JobID, err)
		notice := fmt.Sprintf("Stopped, but the unsaved changes could not be committed: %v", clients.RedactURLCredentials(err.Error()))
		if sendErr := mh.sendSystemMessage(notice, jobData.ProcessedMessageID, jobData.JobID); sendErr != nil {
			log.Error("❌ Failed to report the uncommitted work of job %s: %v", jobData.JobID, sendErr)
		}
	}
	return nil
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

// The turn counts as running from the moment its message leaves the queue, before anything
// that can block, so a stop that lands while the worktree is being prepared still ends it.
// A turn that failed before its CLI ran while a stop was pending owes the backend the stop
// answer, not an error report.
func (mh *MessageHandler) runTurn(jobID, processedMessageID string, handle func(ctx context.Context) error) {
	ctx := mh.turns.Begin(jobID, processedMessageID)
	err := handle(ctx)
	if stopped, stoppedBy := mh.turns.End(jobID); stopped {
		sessionID := ""
		if jobData, exists := mh.appState.GetJobData(jobID); exists {
			sessionID = jobData.ClaudeSessionID
		}
		log.Info("🛑 Turn of job %s ended before the CLI ran (%v); answering the stop by %q", jobID, err, stoppedBy)
		mh.sendTurnStopped(jobID, processedMessageID, models.TurnStopOutcomeStopped, sessionID)
		return
	}
	if err == nil {
		return
	}
	if sendErr := mh.sendErrorMessage(err, processedMessageID, jobID); sendErr != nil {
		log.Error("Failed to send error message: %v", sendErr)
	}
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
