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
					JobID:              jobID,
					Message:            jobData.LastMessage,
					ProcessedMessageID: jobData.ProcessedMessageID,
					MessageLink:        jobData.MessageLink,
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
			// Deduplication check: if a job with the same ProcessedMessageID exists, skip
			jobData, exists := appState.GetJobData(queuedMsg.JobID)
			if exists && jobData.ProcessedMessageID == queuedMsg.ProcessedMessageID {
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

	// Restore jobs if state was loaded
	if loadedState.Loaded && loadedState.Jobs != nil {
		for jobID, jobData := range loadedState.Jobs {
			if err := appState.UpdateJobData(jobID, *jobData); err != nil {
				log.Warn("⚠️ Failed to restore job state for %s: %v", jobID, err)
				continue
			}
			log.Info("📥 Restored job state: %s (branch: %s, session: %s)", jobID, jobData.BranchName, jobData.ClaudeSessionID)
		}
	}

	// Restore queued messages if state was loaded
	if loadedState.Loaded && loadedState.QueuedMessages != nil {
		for _, queuedMsg := range loadedState.QueuedMessages {
			if err := appState.AddQueuedMessage(*queuedMsg); err != nil {
				log.Warn("⚠️ Failed to restore queued message for %s: %v", queuedMsg.ProcessedMessageID, err)
				continue
			}
			log.Info("📥 Restored queued message: %s (job: %s, type: %s)", queuedMsg.ProcessedMessageID, queuedMsg.JobID, queuedMsg.MessageType)
		}
	}

	// Restore seen messages for deduplication if state was loaded
	if loadedState.Loaded && loadedState.SeenMessages != nil {
		restoredCount := 0
		for msgID, seenAt := range loadedState.SeenMessages {
			if err := appState.MarkMessageSeen(msgID, seenAt); err != nil {
				log.Warn("⚠️ Failed to restore seen message %s: %v", msgID, err)
				continue
			}
			restoredCount++
		}
		if restoredCount > 0 {
			log.Info("📥 Restored %d seen messages for deduplication", restoredCount)
		}
	}

	return appState, agentID, nil
}
