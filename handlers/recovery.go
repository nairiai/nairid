package handlers

import (
	"sort"
	"time"

	"nairid/core"
	"nairid/core/log"
	"nairid/models"
	"nairid/usecases"
)

// RecoverJobs recovers both in-progress jobs and queued messages after agent restart
// Phase 1: Recovers jobs that were in_progress when the agent restarted
// Phase 2: Recovers messages that were queued but not yet started
// It removes stale items (>24h old) and requeues valid items for processing
func RecoverJobs(
	appState *models.AppState,
	gitUseCase *usecases.GitUseCase,
	dispatcher *JobDispatcher,
	messageHandler *MessageHandler,
) {
	log.Info("🔄 Starting job and message recovery process")

	allJobs := appState.GetAllJobs()
	allQueuedMessages := appState.GetAllQueuedMessages()

	if len(allJobs) == 0 && len(allQueuedMessages) == 0 {
		log.Info("✅ No jobs or queued messages to recover")
		return
	}

	recoveredJobsCount := 0
	removedJobsCount := 0
	recoveredQueuedCount := 0
	removedQueuedCount := 0
	now := time.Now()

	// Phase 1: Recover in-progress jobs

	// Get repository context to check if we're in repo mode
	repoContext := appState.GetRepositoryContext()

	for jobID, jobData := range allJobs {
		// Only process jobs that were in_progress
		if jobData.Status != models.JobStatusInProgress {
			continue
		}

		// Check staleness - remove jobs older than 24h
		jobAge := now.Sub(jobData.UpdatedAt)
		if jobAge > 24*time.Hour {
			log.Info("🗑️ Removing stale job %s (age: %v)", jobID, jobAge)
			// Clean up worktree if present for stale jobs
			if jobData.WorktreePath != "" {
				if err := gitUseCase.CleanupJobWorktree(jobData.WorktreePath, jobData.BranchName); err != nil {
					log.Warn("⚠️ Failed to cleanup worktree for stale job %s: %v", jobID, err)
				}
			}
			if err := appState.RemoveJob(jobID); err != nil {
				log.Error("❌ Failed to remove stale job %s: %v", jobID, err)
			} else {
				removedJobsCount++
			}
			messageHandler.notifyNotResumed(jobData)
			continue
		}

		// Validate branch/worktree exists (only in repo mode)
		if repoContext.IsRepoMode {
			// For worktree-based jobs, validate worktree exists
			if jobData.WorktreePath != "" {
				if !gitUseCase.WorktreeExists(jobData.WorktreePath) {
					log.Warn("⚠️ Worktree %s for job %s no longer exists, removing job", jobData.WorktreePath, jobID)
					if err := appState.RemoveJob(jobID); err != nil {
						log.Error("❌ Failed to remove job with missing worktree %s: %v", jobID, err)
					} else {
						removedJobsCount++
					}
					messageHandler.notifyNotResumed(jobData)
					continue
				}
			} else {
				// For branch-based jobs, validate branch exists
				branchExists, err := gitUseCase.BranchExists(jobData.BranchName)
				if err != nil {
					log.Error("❌ Failed to check if branch %s exists for job %s: %v", jobData.BranchName, jobID, err)
					continue
				}
				if !branchExists {
					log.Warn("⚠️ Branch %s for job %s no longer exists, removing job", jobData.BranchName, jobID)
					if err := appState.RemoveJob(jobID); err != nil {
						log.Error("❌ Failed to remove job with missing branch %s: %v", jobID, err)
					} else {
						removedJobsCount++
					}
					messageHandler.notifyNotResumed(jobData)
					continue
				}
			}
		}

		// Determine message type based on ClaudeSessionID
		var msg models.BaseMessage
		if jobData.ClaudeSessionID == "" {
			// No session = StartConversation (crashed before Claude)
			log.Info("🔄 Recovering StartConversation job %s (age: %v)", jobID, jobAge)
			msg = models.BaseMessage{
				ID:   core.NewID("msg"),
				Type: models.MessageTypeStartConversation,
				Payload: models.StartConversationPayload{
					JobID:                jobID,
					Message:              jobData.LastMessage,
					ProcessedMessageID:   jobData.ProcessedMessageID,
					MessageLink:          jobData.MessageLink,
					Mode:                 jobData.Mode,
					ReasoningEffort:      jobData.ReasoningEffort,
					ReasoningEffortModel: jobData.EffortModel,
					SessionTimeoutMs:     jobData.SessionTimeoutMs,
				},
			}
		} else {
			// Has session = UserMessage (crashed during continuation)
			log.Info("🔄 Recovering UserMessage job %s (age: %v)", jobID, jobAge)
			msg = models.BaseMessage{
				ID:   core.NewID("msg"),
				Type: models.MessageTypeUserMessage,
				Payload: models.UserMessagePayload{
					JobID:              jobID,
					Message:            jobData.LastMessage,
					ProcessedMessageID: jobData.ProcessedMessageID,
					MessageLink:        jobData.MessageLink,
				},
			}
		}

		// Route through dispatcher for per-job sequential processing
		dispatcher.Dispatch(msg)

		recoveredJobsCount++
	}

	// Phase 2: Recover queued messages
	if len(allQueuedMessages) > 0 {
		log.Info("🔄 Recovering %d queued messages", len(allQueuedMessages))

		// Sort queued messages by QueuedAt timestamp (FIFO order)
		sort.Slice(allQueuedMessages, func(i, j int) bool {
			return allQueuedMessages[i].QueuedAt.Before(allQueuedMessages[j].QueuedAt)
		})

		for _, queuedMsg := range allQueuedMessages {
			if appState.IsMessageDone(queuedMsg.ProcessedMessageID) {
				log.Info("🗑️ Removing queued message %s, its turn already finished", queuedMsg.ProcessedMessageID)
				if err := appState.RemoveQueuedMessage(queuedMsg.ProcessedMessageID); err != nil {
					log.Error("❌ Failed to remove queued message %s: %v", queuedMsg.ProcessedMessageID, err)
				}
				removedQueuedCount++
				continue
			}
			if len(queuedMsg.Payload) > 0 {
				if recoverQueuedMessage(appState, dispatcher, queuedMsg, now) {
					recoveredQueuedCount++
				} else {
					removedQueuedCount++
				}
				continue
			}

			// Entries written before the payload was stored lack mode and history, so they
			// keep the old rules. Skip messages for non-existent jobs (orphaned — job was removed by idle check)
			jobData, exists := appState.GetJobData(queuedMsg.JobID)
			if !exists {
				log.Info("🗑️ Removing orphaned queued message %s (job %s no longer exists)", queuedMsg.ProcessedMessageID, queuedMsg.JobID)
				if err := appState.RemoveQueuedMessage(queuedMsg.ProcessedMessageID); err != nil {
					log.Error("❌ Failed to remove orphaned queued message %s: %v", queuedMsg.ProcessedMessageID, err)
				}
				removedQueuedCount++
				continue
			}

			// Skip messages for completed/failed jobs — don't replay old conversations
			if jobData.Status == models.JobStatusCompleted || jobData.Status == models.JobStatusFailed {
				log.Info("🗑️ Removing queued message %s for %s job %s", queuedMsg.ProcessedMessageID, jobData.Status, queuedMsg.JobID)
				if err := appState.RemoveQueuedMessage(queuedMsg.ProcessedMessageID); err != nil {
					log.Error("❌ Failed to remove queued message %s: %v", queuedMsg.ProcessedMessageID, err)
				}
				removedQueuedCount++
				continue
			}

			// Deduplication check: if a job with the same ProcessedMessageID exists, skip
			if jobData.ProcessedMessageID == queuedMsg.ProcessedMessageID {
				log.Info("🔄 Queued message %s already being processed/completed, removing from queue", queuedMsg.ProcessedMessageID)
				if err := appState.RemoveQueuedMessage(queuedMsg.ProcessedMessageID); err != nil {
					log.Error("❌ Failed to remove duplicate queued message %s: %v", queuedMsg.ProcessedMessageID, err)
				}
				continue
			}

			// Check staleness - remove messages older than 24h (same as jobs)
			msgAge := now.Sub(queuedMsg.QueuedAt)
			if msgAge > 24*time.Hour {
				log.Info("🗑️ Removing stale queued message %s (age: %v)", queuedMsg.ProcessedMessageID, msgAge)
				if err := appState.RemoveQueuedMessage(queuedMsg.ProcessedMessageID); err != nil {
					log.Error("❌ Failed to remove stale queued message %s: %v", queuedMsg.ProcessedMessageID, err)
				} else {
					removedQueuedCount++
				}
				continue
			}

			// Reconstruct message based on message type
			var msg models.BaseMessage
			switch queuedMsg.MessageType {
			case models.MessageTypeStartConversation:
				log.Info("🔄 Recovering queued StartConversation message %s (age: %v)", queuedMsg.ProcessedMessageID, msgAge)
				msg = models.BaseMessage{
					ID:   core.NewID("msg"),
					Type: models.MessageTypeStartConversation,
					Payload: models.StartConversationPayload{
						JobID:              queuedMsg.JobID,
						Message:            queuedMsg.Message,
						ProcessedMessageID: queuedMsg.ProcessedMessageID,
						MessageLink:        queuedMsg.MessageLink,
					},
				}
			case models.MessageTypeUserMessage:
				log.Info("🔄 Recovering queued UserMessage %s (age: %v)", queuedMsg.ProcessedMessageID, msgAge)
				msg = models.BaseMessage{
					ID:   core.NewID("msg"),
					Type: models.MessageTypeUserMessage,
					Payload: models.UserMessagePayload{
						JobID:              queuedMsg.JobID,
						Message:            queuedMsg.Message,
						ProcessedMessageID: queuedMsg.ProcessedMessageID,
						MessageLink:        queuedMsg.MessageLink,
					},
				}
			default:
				log.Warn("⚠️ Unknown message type %s for queued message %s, skipping", queuedMsg.MessageType, queuedMsg.ProcessedMessageID)
				continue
			}

			// Route through dispatcher for per-job sequential processing
			dispatcher.Dispatch(msg)

			recoveredQueuedCount++
		}
	}

	// Summary
	totalRecovered := recoveredJobsCount + recoveredQueuedCount
	totalRemoved := removedJobsCount + removedQueuedCount
	if totalRecovered > 0 || totalRemoved > 0 {
		log.Info("✅ Recovery complete: %d jobs recovered, %d jobs removed, %d queued recovered, %d queued removed",
			recoveredJobsCount, removedJobsCount, recoveredQueuedCount, removedQueuedCount)
	} else {
		log.Info("✅ No items to recover")
	}
}

// RestoreAppState loads persisted state from disk and restores jobs and queued messages
// Returns the initialized AppState and agent ID
func RestoreAppState(statePath string) (*models.AppState, string, error) {
	// Try to load existing state
	loadedState, loadErr := models.LoadState(statePath)
	if loadErr != nil {
		log.Warn("⚠️ Failed to load persisted state: %v (will start fresh)", loadErr)
	}

	// Determine agent ID (use loaded or generate new)
	var agentID string
	if loadedState.Loaded && loadedState.AgentID != "" {
		agentID = loadedState.AgentID
		log.Info("🔄 Restored agent ID from persisted state: %s", agentID)
	} else {
		agentID = core.NewID("ccaid")
		log.Info("🆔 Generated new agent ID: %s", agentID)
	}

	// Create app state with agent ID and state path
	appState := models.NewAppState(agentID, statePath)
	if !loadedState.Loaded {
		return appState, agentID, nil
	}

	for jobID, jobData := range loadedState.Jobs {
		log.Info("📥 Restored job state: %s (branch: %s, session: %s)", jobID, jobData.BranchName, jobData.ClaudeSessionID)
	}
	for _, queuedMsg := range loadedState.QueuedMessages {
		log.Info("📥 Restored queued message: %s (job: %s, type: %s)", queuedMsg.ProcessedMessageID, queuedMsg.JobID, queuedMsg.MessageType)
	}

	done := make(map[string]time.Time, len(loadedState.DoneMessages)+len(loadedState.SeenMessages))
	for msgID, doneAt := range loadedState.DoneMessages {
		done[msgID] = doneAt
	}
	// Older versions only recorded "seen". A seen message still queued, or still the turn of
	// an in-progress job, is unfinished and recovery runs it; the rest count as done from now.
	unfinished := make(map[string]bool)
	for _, jobData := range loadedState.Jobs {
		if jobData.Status == models.JobStatusInProgress && jobData.ProcessedMessageID != "" {
			unfinished[jobData.ProcessedMessageID] = true
		}
	}
	for msgID := range loadedState.SeenMessages {
		if _, queued := loadedState.QueuedMessages[msgID]; queued || unfinished[msgID] {
			continue
		}
		if _, exists := done[msgID]; !exists {
			done[msgID] = time.Now()
		}
	}
	// One write for everything: a crash mid-restore must not leave a file missing any part.
	if err := appState.Restore(loadedState.Jobs, loadedState.QueuedMessages, done, loadedState.Outbox); err != nil {
		log.Warn("⚠️ Failed to persist restored state: %v", err)
	}
	log.Info("📥 Restored %d handled messages for deduplication, %d unsent replies", len(done), len(loadedState.Outbox))

	return appState, agentID, nil
}

// recoverQueuedMessage replays a queued message with its full payload. It is dropped only
// when its job is running it already or it is over a day old.
func recoverQueuedMessage(
	appState *models.AppState,
	dispatcher *JobDispatcher,
	queuedMsg models.QueuedMessage,
	now time.Time,
) bool {
	jobData, exists := appState.GetJobData(queuedMsg.JobID)
	if exists && jobData.ProcessedMessageID == queuedMsg.ProcessedMessageID {
		// In progress: Phase 1 reruns it. Completed or failed: its turn ended.
		log.Info("🔄 Queued message %s is the job's own message, removing from queue", queuedMsg.ProcessedMessageID)
		if err := appState.RemoveQueuedMessage(queuedMsg.ProcessedMessageID); err != nil {
			log.Error("❌ Failed to remove queued message %s: %v", queuedMsg.ProcessedMessageID, err)
		}
		return false
	}

	msgAge := now.Sub(queuedMsg.QueuedAt)
	if msgAge > 24*time.Hour {
		log.Warn("🗑️ Removing stale queued message %s (age: %v)", queuedMsg.ProcessedMessageID, msgAge)
		if err := appState.RemoveQueuedMessage(queuedMsg.ProcessedMessageID); err != nil {
			log.Error("❌ Failed to remove stale queued message %s: %v", queuedMsg.ProcessedMessageID, err)
		}
		return false
	}

	if !exists && queuedMsg.MessageType == models.MessageTypeUserMessage {
		// Started over here it would lose the job's mode; the poller offers it with the job.
		log.Info("🔄 Leaving queued reply %s for the poller, job %s is not known here", queuedMsg.ProcessedMessageID, queuedMsg.JobID)
		return true
	}

	// A start for a job that is not known yet creates it.
	log.Info("🔄 Recovering queued %s %s for job %s (age: %v, job known: %v)",
		queuedMsg.MessageType, queuedMsg.ProcessedMessageID, queuedMsg.JobID, msgAge, exists)
	dispatcher.Dispatch(models.BaseMessage{
		ID:      core.NewID("msg"),
		Type:    queuedMsg.MessageType,
		Payload: queuedMsg.Payload,
	})
	return true
}

// notifyNotResumed tells the user their in-progress turn is gone instead of leaving them waiting.
func (mh *MessageHandler) notifyNotResumed(jobData models.JobData) {
	if jobData.ProcessedMessageID == "" {
		return
	}
	// Recovery runs before the sender starts, so the notice waits in the outbox instead of the queue.
	mh.messageSender.QueueMessageLater("cc_message", models.BaseMessage{
		ID:   core.NewID("msg"),
		Type: models.MessageTypeSystemMessage,
		Payload: models.SystemMessagePayload{
			Message:            "The agent restarted and could not resume this conversation. Please send your message again.",
			ProcessedMessageID: jobData.ProcessedMessageID,
			JobID:              jobData.JobID,
		},
	})
	mh.finishMessage(jobData.ProcessedMessageID)
}
