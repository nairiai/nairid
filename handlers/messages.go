package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"eksecd/clients"
	"eksecd/core"
	"eksecd/core/env"
	"eksecd/core/log"
	"eksecd/models"
	"eksecd/services"
	"eksecd/usecases"
	"eksecd/utils"
)

// JobEvictor is an interface for evicting jobs from the dispatcher.
// This allows the message handler to signal the dispatcher to stop
// processing a job when an unrecoverable error occurs.
type JobEvictor interface {
	EvictJob(jobID string)
}

type MessageHandler struct {
	claudeService   services.CLIAgent
	gitUseCase      *usecases.GitUseCase
	appState        *models.AppState
	envManager      *env.EnvManager
	messageSender   *MessageSender
	agentsApiClient *clients.AgentsApiClient
	jobEvictor      JobEvictor
}

func NewMessageHandler(
	claudeService services.CLIAgent,
	gitUseCase *usecases.GitUseCase,
	appState *models.AppState,
	envManager *env.EnvManager,
	messageSender *MessageSender,
	agentsApiClient *clients.AgentsApiClient,
) *MessageHandler {
	return &MessageHandler{
		claudeService:   claudeService,
		gitUseCase:      gitUseCase,
		appState:        appState,
		envManager:      envManager,
		messageSender:   messageSender,
		agentsApiClient: agentsApiClient,
	}
}

// SetJobEvictor sets the job evictor for the message handler.
// This must be called after both the MessageHandler and JobDispatcher are created.
func (mh *MessageHandler) SetJobEvictor(evictor JobEvictor) {
	mh.jobEvictor = evictor
}

// processAttachmentsForPrompt fetches attachments from API and returns file paths and formatted text
func (mh *MessageHandler) processAttachmentsForPrompt(
	attachmentIDs []string,
	sessionID string,
) (filePaths []string, appendText string, err error) {
	if len(attachmentIDs) == 0 {
		return nil, "", nil
	}

	var paths []string
	for i, attachmentID := range attachmentIDs {
		filePath, err := utils.FetchAndStoreAttachment(mh.agentsApiClient, attachmentID, sessionID, i)
		if err != nil {
			return nil, "", fmt.Errorf("failed to fetch and store attachment %s: %w", attachmentID, err)
		}
		paths = append(paths, filePath)
	}

	// Format attachment text
	attachmentText := utils.FormatAttachmentsText(paths)

	return paths, attachmentText, nil
}

// formatThreadContext creates a preamble for the prompt that includes previous messages from the thread
func (mh *MessageHandler) formatThreadContext(
	previousMessages []models.PreviousMessage,
	currentMessage string,
	currentAttachments []models.MessageAttachment,
	sessionID string,
) (fullPrompt string, allAttachmentPaths []string, err error) {
	if len(previousMessages) == 0 {
		// No thread context, just process current message attachments
		var attachmentText string
		var attachmentIDs []string
		for _, att := range currentAttachments {
			attachmentIDs = append(attachmentIDs, att.AttachmentID)
		}

		allAttachmentPaths, attachmentText, err = mh.processAttachmentsForPrompt(attachmentIDs, sessionID)
		if err != nil {
			return "", nil, fmt.Errorf("failed to process current message attachments: %w", err)
		}

		if attachmentText != "" {
			fullPrompt = currentMessage + "\n" + attachmentText
		} else {
			fullPrompt = currentMessage
		}
		return fullPrompt, allAttachmentPaths, nil
	}

	// Build thread context with previous messages
	var builder strings.Builder
	builder.WriteString("THREAD CONTEXT: You are being asked to respond in the context of an ongoing conversation thread. ")
	builder.WriteString("Below are the previous messages in this thread for context, followed by the latest message you should respond to.\n\n")

	// Add previous messages
	builder.WriteString("Previous messages in thread:\n")
	builder.WriteString("---\n")

	attachmentIndex := 0
	for i, prevMsg := range previousMessages {
		builder.WriteString(fmt.Sprintf("Message %d:\n", i+1))
		builder.WriteString(prevMsg.Message)
		builder.WriteString("\n")

		// Process attachments for previous messages
		if len(prevMsg.Attachments) > 0 {
			var prevAttachmentIDs []string
			for _, att := range prevMsg.Attachments {
				prevAttachmentIDs = append(prevAttachmentIDs, att.AttachmentID)
			}

			prevPaths, prevAttachmentText, err := mh.processAttachmentsForPrompt(prevAttachmentIDs, sessionID)
			if err != nil {
				return "", nil, fmt.Errorf("failed to process attachments for previous message %d: %w", i+1, err)
			}

			if prevAttachmentText != "" {
				builder.WriteString(prevAttachmentText)
			}

			allAttachmentPaths = append(allAttachmentPaths, prevPaths...)
			attachmentIndex += len(prevPaths)
		}

		builder.WriteString("\n")
	}

	builder.WriteString("---\n\n")

	// Add current message
	builder.WriteString("LATEST MESSAGE (respond to this):\n")
	builder.WriteString(currentMessage)
	builder.WriteString("\n")

	// Process current message attachments
	if len(currentAttachments) > 0 {
		var currentAttachmentIDs []string
		for _, att := range currentAttachments {
			currentAttachmentIDs = append(currentAttachmentIDs, att.AttachmentID)
		}

		currentPaths, currentAttachmentText, err := mh.processAttachmentsForPrompt(currentAttachmentIDs, sessionID)
		if err != nil {
			return "", nil, fmt.Errorf("failed to process current message attachments: %w", err)
		}

		if currentAttachmentText != "" {
			builder.WriteString(currentAttachmentText)
		}

		allAttachmentPaths = append(allAttachmentPaths, currentPaths...)
	}

	builder.WriteString("\n")
	builder.WriteString("Please respond to the LATEST MESSAGE above, using the previous messages as context only if they are relevant to your response.")

	return builder.String(), allAttachmentPaths, nil
}

func (mh *MessageHandler) HandleMessage(msg models.BaseMessage) {
	switch msg.Type {
	case models.MessageTypeStartConversation:
		if err := mh.handleStartConversation(msg); err != nil {
			// Extract ProcessedMessageID and JobID from payload for error reporting
			var payload models.StartConversationPayload
			if unmarshalErr := unmarshalPayload(msg.Payload, &payload); unmarshalErr != nil {
				log.Error("Failed to unmarshal StartConversationPayload for error reporting: %v", unmarshalErr)
				return
			}
			if sendErr := mh.sendErrorMessage(err, payload.ProcessedMessageID, payload.JobID); sendErr != nil {
				log.Error("Failed to send error message: %v", sendErr)
			}
		}
	case models.MessageTypeUserMessage:
		if err := mh.handleUserMessage(msg); err != nil {
			// Extract ProcessedMessageID and JobID from payload for error reporting
			var payload models.UserMessagePayload
			if unmarshalErr := unmarshalPayload(msg.Payload, &payload); unmarshalErr != nil {
				log.Error("Failed to unmarshal UserMessagePayload for error reporting: %v", unmarshalErr)
				return
			}
			if sendErr := mh.sendErrorMessage(err, payload.ProcessedMessageID, payload.JobID); sendErr != nil {
				log.Error("Failed to send error message: %v", sendErr)
			}
		}
	case models.MessageTypeCheckIdleJobs:
		if err := mh.handleCheckIdleJobs(msg); err != nil {
			log.Info("❌ Error handling CheckIdleJobs message: %v", err)
		}
	default:
		log.Info("⚠️ Unhandled message type: %s", msg.Type)
	}
}

func (mh *MessageHandler) handleStartConversation(msg models.BaseMessage) error {
	log.Info("📋 Starting to handle start conversation message")
	var payload models.StartConversationPayload
	if err := unmarshalPayload(msg.Payload, &payload); err != nil {
		log.Info("❌ Failed to unmarshal start conversation payload: %v", err)
		return fmt.Errorf("failed to unmarshal start conversation payload: %w", err)
	}

	// Send processing message notification that agent is starting to process
	if err := mh.sendProcessingMessage(payload.ProcessedMessageID, payload.JobID); err != nil {
		log.Info("❌ Failed to send processing message notification: %v", err)
		return fmt.Errorf("failed to send processing message notification: %w", err)
	}

	log.Info("🚀 Starting new conversation with message: %s", payload.Message)

	// Prepare Git environment for new conversation - FAIL if this doesn't work
	// Use worktrees if MAX_CONCURRENCY > 1 for concurrent job processing
	var branchName, worktreePath string
	var err error

	if mh.gitUseCase.ShouldUseWorktrees() {
		log.Info("🌳 Using worktree mode for concurrent job processing")
		branchName, worktreePath, err = mh.gitUseCase.PrepareForNewConversationWithWorktree(payload.JobID, payload.Message)
	} else {
		branchName, err = mh.gitUseCase.PrepareForNewConversation(payload.Message)
	}

	if err != nil {
		log.Error("❌ Failed to prepare Git environment: %v", err)
		return fmt.Errorf("failed to prepare Git environment: %w", err)
	}

	// Refresh environment variables before starting conversation
	if err := mh.envManager.Reload(); err != nil {
		log.Error("❌ Failed to refresh environment variables: %v", err)
		return fmt.Errorf("failed to refresh environment variables: %w", err)
	}
	log.Info("🔄 Refreshed environment variables before starting conversation")

	// Persist job state with message BEFORE calling Claude
	// This enables crash recovery and future reprocessing
	if err := mh.appState.UpdateJobData(payload.JobID, models.JobData{
		JobID:              payload.JobID,
		BranchName:         branchName,
		WorktreePath:       worktreePath, // Will be empty if not using worktrees
		ClaudeSessionID:    "", // No session yet
		PullRequestID:      "",
		LastMessage:        payload.Message,
		ProcessedMessageID: payload.ProcessedMessageID,
		MessageLink:        payload.MessageLink,
		Status:             models.JobStatusInProgress,
		Mode:               payload.Mode,
		UpdatedAt:          time.Now(),
	}); err != nil {
		log.Error("❌ Failed to persist job state before Claude call: %v", err)
		return fmt.Errorf("failed to persist job state before Claude call: %w", err)
	}
	log.Info("💾 Persisted job state with in_progress status before calling Claude")

	// Remove from queued messages now that we're processing
	if err := mh.appState.RemoveQueuedMessage(payload.ProcessedMessageID); err != nil {
		log.Warn("⚠️ Failed to remove queued message %s: %v", payload.ProcessedMessageID, err)
		// Don't fail - message will be deduplicated during recovery
	}

	// Get repository context
	repoContext := mh.appState.GetRepositoryContext()

	// Get appropriate system prompt based on agent type and mode
	// Pass worktreePath so Claude knows to work in the worktree directory
	systemPrompt := GetClaudeSystemPrompt(payload.Mode, repoContext, worktreePath)
	if mh.claudeService.AgentName() == "cursor" {
		systemPrompt = GetCursorSystemPrompt(payload.Mode, repoContext, worktreePath)
	}

	// Process thread context (previous messages) and attachments
	attachmentSessionID := fmt.Sprintf("job_%s", payload.JobID)

	finalPrompt, attachmentPaths, err := mh.formatThreadContext(
		payload.PreviousMessages,
		payload.Message,
		payload.Attachments,
		attachmentSessionID,
	)
	if err != nil {
		log.Error("❌ Failed to format thread context: %v", err)
		return fmt.Errorf("failed to format thread context: %w", err)
	}

	if len(attachmentPaths) > 0 {
		log.Info("✅ Processed %d attachments: %v", len(attachmentPaths), attachmentPaths)
	}

	if len(payload.PreviousMessages) > 0 {
		log.Info("📝 Formatted thread context with %d previous messages", len(payload.PreviousMessages))
	}

	// Start Claude session - use worktree directory if in worktree mode
	var claudeResult *services.CLIAgentResult
	if worktreePath != "" {
		log.Info("🌳 Starting Claude session in worktree: %s", worktreePath)
		claudeResult, err = mh.claudeService.StartNewConversationWithSystemPromptInDir(finalPrompt, systemPrompt, worktreePath)
	} else {
		claudeResult, err = mh.claudeService.StartNewConversationWithSystemPrompt(finalPrompt, systemPrompt)
	}

	if err != nil {
		log.Info("❌ Error starting Claude session: %v", err)
		systemErr := mh.sendSystemMessage(
			fmt.Sprintf("eksecd encountered error: %v", err),
			payload.ProcessedMessageID,
			payload.JobID,
		)
		if systemErr != nil {
			log.Error("❌ Failed to send system message for Claude error: %v", systemErr)
		}

		// Mark job as failed so the processor goroutine can exit and free up a worker slot
		if updateErr := mh.appState.UpdateJobData(payload.JobID, models.JobData{
			JobID:              payload.JobID,
			BranchName:         branchName,
			WorktreePath:       worktreePath,
			ClaudeSessionID:    "",
			PullRequestID:      "",
			LastMessage:        payload.Message,
			ProcessedMessageID: payload.ProcessedMessageID,
			MessageLink:        payload.MessageLink,
			Status:             models.JobStatusFailed,
			Mode:               payload.Mode,
			UpdatedAt:          time.Now(),
		}); updateErr != nil {
			log.Error("❌ Failed to mark job as failed: %v", updateErr)
		} else {
			log.Info("💾 Marked job %s as failed due to Claude session error", payload.JobID)
		}

		// Evict the job from dispatcher to immediately free up the worker slot
		if mh.jobEvictor != nil {
			mh.jobEvictor.EvictJob(payload.JobID)
		}

		return fmt.Errorf("error starting Claude session: %w", err)
	}

	// Auto-commit changes if needed (skip in ask mode)
	var commitResult *usecases.AutoCommitResult
	if payload.Mode != models.AgentModeAsk {
		var err error
		if worktreePath != "" {
			// Use worktree-aware auto-commit
			commitResult, err = mh.gitUseCase.AutoCommitChangesInWorktreeIfNeeded(payload.MessageLink, claudeResult.SessionID, worktreePath)
		} else {
			commitResult, err = mh.gitUseCase.AutoCommitChangesIfNeeded(payload.MessageLink, claudeResult.SessionID)
		}
		if err != nil {
			log.Info("❌ Auto-commit failed: %v", err)
			return fmt.Errorf("auto-commit failed: %w", err)
		}
	} else {
		log.Info("📋 Skipping auto-commit in ask mode")
	}

	// Update JobData with conversation info (use commitResult.BranchName if available, otherwise branchName)
	finalBranchName := branchName
	if commitResult != nil && commitResult.BranchName != "" {
		finalBranchName = commitResult.BranchName
	}

	// Extract PR ID from commit result if available
	prID := ""
	if commitResult != nil && commitResult.PullRequestID != "" {
		prID = commitResult.PullRequestID
	}

	// Send assistant response back first
	assistantPayload := models.AssistantMessagePayload{
		JobID:              payload.JobID,
		Message:            claudeResult.Output,
		ProcessedMessageID: payload.ProcessedMessageID,
	}

	assistantMsg := models.BaseMessage{
		ID:      core.NewID("msg"),
		Type:    models.MessageTypeAssistantMessage,
		Payload: assistantPayload,
	}
	mh.messageSender.QueueMessage("cc_message", assistantMsg)
	log.Info("🤖 Queued assistant response (message ID: %s)", assistantMsg.ID)

	// Persist final job state with "completed" status after successful message send
	if err := mh.appState.UpdateJobData(payload.JobID, models.JobData{
		JobID:              payload.JobID,
		BranchName:         finalBranchName,
		WorktreePath:       worktreePath, // Preserve worktree path for concurrent job mode
		ClaudeSessionID:    claudeResult.SessionID,
		PullRequestID:      prID,
		LastMessage:        payload.Message,
		ProcessedMessageID: payload.ProcessedMessageID,
		MessageLink:        payload.MessageLink,
		Status:             models.JobStatusCompleted,
		Mode:               payload.Mode,
		UpdatedAt:          time.Now(),
	}); err != nil {
		log.Error("❌ Failed to persist final job state: %v", err)
		return fmt.Errorf("failed to persist final job state: %w", err)
	}
	log.Info("💾 Persisted final job state with completed status")

	// Add delay to ensure git activity message comes after assistant message
	time.Sleep(200 * time.Millisecond)

	// Send system message after assistant message for git activity
	if err := mh.sendGitActivitySystemMessage(commitResult, payload.ProcessedMessageID, payload.JobID); err != nil {
		log.Info("❌ Failed to send git activity system message: %v", err)
		return fmt.Errorf("failed to send git activity system message: %w", err)
	}

	// Validate and restore PR description footer if needed.
	// Pass branch name from commitResult to skip redundant HasExistingPR API calls.
	knownBranch := ""
	if commitResult != nil && commitResult.PullRequestID != "" {
		knownBranch = commitResult.BranchName
	}
	if worktreePath != "" {
		if err := mh.gitUseCase.ValidateAndRestorePRDescriptionFooterInWorktree(payload.MessageLink, worktreePath, knownBranch); err != nil {
			log.Info("❌ Failed to validate PR description footer in worktree: %v", err)
			return fmt.Errorf("failed to validate PR description footer in worktree: %w", err)
		}
	} else {
		if err := mh.gitUseCase.ValidateAndRestorePRDescriptionFooter(payload.MessageLink, knownBranch); err != nil {
			log.Info("❌ Failed to validate PR description footer: %v", err)
			return fmt.Errorf("failed to validate PR description footer: %w", err)
		}
	}

	log.Info("📋 Completed successfully - handled start conversation message")
	return nil
}

func (mh *MessageHandler) handleUserMessage(msg models.BaseMessage) error {
	log.Info("📋 Starting to handle user message")
	var payload models.UserMessagePayload
	if err := unmarshalPayload(msg.Payload, &payload); err != nil {
		log.Info("❌ Failed to unmarshal user message payload: %v", err)
		return fmt.Errorf("failed to unmarshal user message payload: %w", err)
	}

	// Send processing message notification that agent is starting to process
	if err := mh.sendProcessingMessage(payload.ProcessedMessageID, payload.JobID); err != nil {
		log.Info("❌ Failed to send processing message notification: %v", err)
		return fmt.Errorf("failed to send processing message notification: %w", err)
	}

	log.Info("💬 Continuing conversation with message: %s", payload.Message)

	// Get the current job data to retrieve the Claude session ID and branch
	jobData, exists := mh.appState.GetJobData(payload.JobID)
	if !exists {
		log.Info("❌ JobID %s not found in AppState", payload.JobID)
		return fmt.Errorf("job %s not found - conversation may have been started elsewhere", payload.JobID)
	}

	sessionID := jobData.ClaudeSessionID
	if sessionID == "" {
		log.Info("❌ No Claude session ID found for job %s", payload.JobID)
		return fmt.Errorf("no active Claude session found for job %s", payload.JobID)
	}

	// Get repository context to check if we're in repo mode
	repoContext := mh.appState.GetRepositoryContext()

	// Assert that BranchName is never empty (only in repo mode)
	if repoContext.IsRepoMode {
		utils.AssertInvariant(jobData.BranchName != "", "BranchName must not be empty for job "+payload.JobID)

		// Handle worktree mode vs regular branch mode
		if jobData.WorktreePath != "" {
			// Worktree mode: prepare the existing worktree for continuing the job
			log.Info("🌳 Using worktree mode for job: %s", jobData.WorktreePath)
			if err := mh.gitUseCase.PrepareWorktreeForJob(jobData.WorktreePath, jobData.BranchName); err != nil {
				// Check if error is due to remote branch being deleted
				if strings.Contains(err.Error(), "remote branch deleted") {
					log.Warn("⚠️ Remote branch deleted for job %s in worktree - abandoning job", payload.JobID)

					// Cleanup worktree and abandon job
					cleanupErr := mh.gitUseCase.CleanupJobWorktree(jobData.WorktreePath, jobData.BranchName)
					abandonErr := mh.appState.RemoveJob(payload.JobID)

					var systemMessage string
					if cleanupErr != nil || abandonErr != nil {
						systemMessage = fmt.Sprintf("Job failed: Remote branch '%s' was deleted (likely merged), but cleanup failed. Please check repository state.", jobData.BranchName)
					} else {
						systemMessage = fmt.Sprintf("Job abandoned: Remote branch '%s' was deleted (likely merged). Please start a new conversation.", jobData.BranchName)
					}

					systemErr := mh.sendSystemMessage(systemMessage, payload.ProcessedMessageID, payload.JobID)
					if systemErr != nil {
						log.Error("❌ Failed to send system message for abandoned job: %v", systemErr)
					}

					return fmt.Errorf("job abandoned: remote branch deleted")
				}

				log.Error("❌ Failed to prepare worktree for job: %v", err)
				return fmt.Errorf("failed to prepare worktree for job: %w", err)
			}
			log.Info("✅ Successfully prepared worktree for job: %s", jobData.WorktreePath)
		} else {
			// Regular branch mode: switch to the job's branch before continuing
			if err := mh.gitUseCase.SwitchToJobBranch(jobData.BranchName); err != nil {
				log.Error("❌ Failed to switch to job branch %s: %v", jobData.BranchName, err)
				return fmt.Errorf("failed to switch to job branch %s: %w", jobData.BranchName, err)
			}
			log.Info("✅ Successfully switched to job branch: %s", jobData.BranchName)
		}
	}

	// Pull latest changes before continuing conversation (only in non-worktree mode)
	// In worktree mode, PrepareWorktreeForJob already pulls latest changes
	if jobData.WorktreePath == "" {
		if err := mh.gitUseCase.PullLatestChanges(); err != nil {
			// Check if error is due to remote branch being deleted
			if strings.Contains(err.Error(), "remote branch deleted") {
				log.Warn("⚠️ Remote branch deleted for job %s - abandoning job and cleaning up", payload.JobID)

				// Abandon the job and cleanup
				abandonErr := mh.gitUseCase.AbandonJobAndCleanup(payload.JobID, jobData.BranchName)

				// Send system message to notify user (try even if cleanup fails)
				var systemMessage string
				if abandonErr != nil {
					systemMessage = fmt.Sprintf("Job failed: Remote branch '%s' was deleted (likely merged), but cleanup failed: %v. Please check repository state.", jobData.BranchName, abandonErr)
				} else {
					systemMessage = fmt.Sprintf("Job abandoned: Remote branch '%s' was deleted (likely merged). Please start a new conversation.", jobData.BranchName)
				}

				systemErr := mh.sendSystemMessage(systemMessage, payload.ProcessedMessageID, payload.JobID)
				if systemErr != nil {
					log.Error("❌ Failed to send system message for abandoned job: %v", systemErr)
				}

				// Return the cleanup error if it occurred, otherwise return abandoned error
				if abandonErr != nil {
					log.Error("❌ Failed to abandon job and cleanup: %v", abandonErr)
					return fmt.Errorf("failed to abandon job: %w", abandonErr)
				}

				return fmt.Errorf("job abandoned: remote branch deleted")
			}

			log.Error("❌ Failed to pull latest changes: %v", err)
			return fmt.Errorf("failed to pull latest changes: %w", err)
		}
		log.Info("✅ Pulled latest changes from remote")
	}

	// Refresh environment variables before continuing conversation
	if err := mh.envManager.Reload(); err != nil {
		log.Error("❌ Failed to refresh environment variables: %v", err)
		return fmt.Errorf("failed to refresh environment variables: %w", err)
	}
	log.Info("🔄 Refreshed environment variables before continuing conversation")

	// Persist updated message BEFORE calling Claude
	// This enables crash recovery and future reprocessing
	if err := mh.appState.UpdateJobData(payload.JobID, models.JobData{
		JobID:              payload.JobID,
		BranchName:         jobData.BranchName,
		WorktreePath:       jobData.WorktreePath, // Preserve worktree path
		ClaudeSessionID:    jobData.ClaudeSessionID,
		PullRequestID:      jobData.PullRequestID,
		LastMessage:        payload.Message,
		ProcessedMessageID: payload.ProcessedMessageID,
		MessageLink:        payload.MessageLink,
		Status:             models.JobStatusInProgress,
		UpdatedAt:          time.Now(),
	}); err != nil {
		log.Error("❌ Failed to persist job state before Claude call: %v", err)
		return fmt.Errorf("failed to persist job state before Claude call: %w", err)
	}
	log.Info("💾 Persisted job state with in_progress status before calling Claude")

	// Remove from queued messages now that we're processing
	if err := mh.appState.RemoveQueuedMessage(payload.ProcessedMessageID); err != nil {
		log.Warn("⚠️ Failed to remove queued message %s: %v", payload.ProcessedMessageID, err)
		// Don't fail - message will be deduplicated during recovery
	}

	// Process attachments and build final prompt
	attachmentSessionID := fmt.Sprintf("job_%s", payload.JobID)

	// Extract attachment IDs from MessageAttachment array
	var attachmentIDs []string
	for _, att := range payload.Attachments {
		attachmentIDs = append(attachmentIDs, att.AttachmentID)
	}

	attachmentPaths, attachmentText, err := mh.processAttachmentsForPrompt(
		attachmentIDs,
		attachmentSessionID,
	)
	if err != nil {
		log.Error("❌ Failed to process attachments: %v", err)
		return fmt.Errorf("failed to process attachments: %w", err)
	}

	if len(attachmentPaths) > 0 {
		log.Info("✅ Processed %d attachments: %v", len(attachmentPaths), attachmentPaths)
	}

	finalPrompt := payload.Message
	if attachmentText != "" {
		finalPrompt = payload.Message + "\n" + attachmentText
	}

	// Continue Claude session - use worktree directory if in worktree mode
	var claudeResult *services.CLIAgentResult
	if jobData.WorktreePath != "" {
		log.Info("🌳 Continuing Claude session in worktree: %s", jobData.WorktreePath)
		claudeResult, err = mh.claudeService.ContinueConversationInDir(sessionID, finalPrompt, jobData.WorktreePath)
	} else {
		claudeResult, err = mh.claudeService.ContinueConversation(sessionID, finalPrompt)
	}
	if err != nil {
		log.Info("❌ Error continuing Claude session: %v", err)
		systemErr := mh.sendSystemMessage(
			fmt.Sprintf("eksecd encountered error: %v", err),
			payload.ProcessedMessageID,
			payload.JobID,
		)
		if systemErr != nil {
			log.Error("❌ Failed to send system message for Claude error: %v", systemErr)
		}

		// Mark job as failed so the processor goroutine can exit and free up a worker slot
		if updateErr := mh.appState.UpdateJobData(payload.JobID, models.JobData{
			JobID:              payload.JobID,
			BranchName:         jobData.BranchName,
			WorktreePath:       jobData.WorktreePath,
			ClaudeSessionID:    jobData.ClaudeSessionID,
			PullRequestID:      jobData.PullRequestID,
			LastMessage:        payload.Message,
			ProcessedMessageID: payload.ProcessedMessageID,
			MessageLink:        payload.MessageLink,
			Status:             models.JobStatusFailed,
			Mode:               jobData.Mode,
			UpdatedAt:          time.Now(),
		}); updateErr != nil {
			log.Error("❌ Failed to mark job as failed: %v", updateErr)
		} else {
			log.Info("💾 Marked job %s as failed due to Claude session error", payload.JobID)
		}

		// Evict the job from dispatcher to immediately free up the worker slot
		if mh.jobEvictor != nil {
			mh.jobEvictor.EvictJob(payload.JobID)
		}

		return fmt.Errorf("error continuing Claude session: %w", err)
	}

	// Auto-commit changes if needed (skip in ask mode)
	var commitResult *usecases.AutoCommitResult
	if jobData.Mode != models.AgentModeAsk {
		var err error
		if jobData.WorktreePath != "" {
			// Use worktree-aware auto-commit
			commitResult, err = mh.gitUseCase.AutoCommitChangesInWorktreeIfNeeded(payload.MessageLink, claudeResult.SessionID, jobData.WorktreePath)
		} else {
			commitResult, err = mh.gitUseCase.AutoCommitChangesIfNeeded(payload.MessageLink, claudeResult.SessionID)
		}
		if err != nil {
			log.Info("❌ Auto-commit failed: %v", err)
			return fmt.Errorf("auto-commit failed: %w", err)
		}
	} else {
		log.Info("📋 Skipping auto-commit in ask mode")
	}

	// Update JobData with latest session ID and branch name from commit result
	finalBranchName := jobData.BranchName
	if commitResult != nil && commitResult.BranchName != "" {
		finalBranchName = commitResult.BranchName
	}

	// Extract PR ID from existing job data or commit result
	prID := jobData.PullRequestID
	if commitResult != nil && commitResult.PullRequestID != "" {
		prID = commitResult.PullRequestID
	}

	// Send assistant response back first
	assistantPayload := models.AssistantMessagePayload{
		JobID:              payload.JobID,
		Message:            claudeResult.Output,
		ProcessedMessageID: payload.ProcessedMessageID,
	}

	assistantMsg := models.BaseMessage{
		ID:      core.NewID("msg"),
		Type:    models.MessageTypeAssistantMessage,
		Payload: assistantPayload,
	}
	mh.messageSender.QueueMessage("cc_message", assistantMsg)
	log.Info("🤖 Queued assistant response (message ID: %s)", assistantMsg.ID)

	// Persist final job state with "completed" status after successful message send
	if err := mh.appState.UpdateJobData(payload.JobID, models.JobData{
		JobID:              payload.JobID,
		BranchName:         finalBranchName,
		WorktreePath:       jobData.WorktreePath, // Preserve worktree path
		Mode:               jobData.Mode,
		ClaudeSessionID:    claudeResult.SessionID,
		PullRequestID:      prID,
		LastMessage:        payload.Message,
		ProcessedMessageID: payload.ProcessedMessageID,
		MessageLink:        payload.MessageLink,
		Status:             models.JobStatusCompleted,
		UpdatedAt:          time.Now(),
	}); err != nil {
		log.Error("❌ Failed to persist final job state: %v", err)
		return fmt.Errorf("failed to persist final job state: %w", err)
	}
	log.Info("💾 Persisted final job state with completed status")

	// Add delay to ensure git activity message comes after assistant message
	time.Sleep(200 * time.Millisecond)

	// Send system message after assistant message for git activity
	if err := mh.sendGitActivitySystemMessage(commitResult, payload.ProcessedMessageID, payload.JobID); err != nil {
		log.Info("❌ Failed to send git activity system message: %v", err)
		return fmt.Errorf("failed to send git activity system message: %w", err)
	}

	// Validate and restore PR description footer if needed.
	// Pass branch name from commitResult to skip redundant HasExistingPR API calls.
	knownBranch := ""
	if commitResult != nil && commitResult.PullRequestID != "" {
		knownBranch = commitResult.BranchName
	}
	if jobData.WorktreePath != "" {
		if err := mh.gitUseCase.ValidateAndRestorePRDescriptionFooterInWorktree(payload.MessageLink, jobData.WorktreePath, knownBranch); err != nil {
			log.Info("❌ Failed to validate PR description footer in worktree: %v", err)
			return fmt.Errorf("failed to validate PR description footer in worktree: %w", err)
		}
	} else {
		if err := mh.gitUseCase.ValidateAndRestorePRDescriptionFooter(payload.MessageLink, knownBranch); err != nil {
			log.Info("❌ Failed to validate PR description footer: %v", err)
			return fmt.Errorf("failed to validate PR description footer: %w", err)
		}
	}

	log.Info("📋 Completed successfully - handled user message")
	return nil
}

func (mh *MessageHandler) handleCheckIdleJobs(msg models.BaseMessage) error {
	log.Info("📋 Starting to handle check idle jobs message")
	var payload models.CheckIdleJobsPayload
	if err := unmarshalPayload(msg.Payload, &payload); err != nil {
		log.Info("❌ Failed to unmarshal check idle jobs payload: %v", err)
		return fmt.Errorf("failed to unmarshal check idle jobs payload: %w", err)
	}

	log.Info("🔍 Checking all assigned jobs for idleness")

	// Get all job data from app state
	allJobData := mh.appState.GetAllJobs()
	if len(allJobData) == 0 {
		log.Info("📋 No jobs assigned to this agent")
		return nil
	}

	log.Info("🔍 Found %d jobs assigned to this agent", len(allJobData))

	// Check each job for idleness
	for jobID, jobData := range allJobData {
		log.Info("🔍 Checking job %s on branch %s", jobID, jobData.BranchName)

		if err := mh.checkJobIdleness(jobID, jobData); err != nil {
			log.Info("❌ Failed to check idleness for job %s: %v", jobID, err)
			// Continue checking other jobs even if one fails
			continue
		}
	}

	log.Info("📋 Completed successfully - checked all jobs for idleness")
	return nil
}

func (mh *MessageHandler) checkJobIdleness(jobID string, jobData models.JobData) error {
	log.Info("📋 Starting to check idleness for job %s", jobID)

	var prStatus string
	var err error

	// Use stored PR ID if available, otherwise fall back to branch-based check
	if jobData.PullRequestID != "" {
		log.Info("ℹ️ Using stored PR ID %s for job %s", jobData.PullRequestID, jobID)
		prStatus, err = mh.gitUseCase.CheckPRStatusByID(jobData.PullRequestID)
		if err != nil {
			log.Error("❌ Failed to check PR status by ID %s: %v", jobData.PullRequestID, err)
			return fmt.Errorf("failed to check PR status by ID %s: %w", jobData.PullRequestID, err)
		}
	} else {
		log.Info("ℹ️ No stored PR ID for job %s, using branch-based check", jobID)
		prStatus, err = mh.gitUseCase.CheckPRStatus(jobData.BranchName)
		if err != nil {
			log.Error("❌ Failed to check PR status for branch %s: %v", jobData.BranchName, err)
			return fmt.Errorf("failed to check PR status for branch %s: %w", jobData.BranchName, err)
		}
	}

	var reason string
	var shouldComplete bool

	// First check if job has been inactive for 25 hours (regardless of PR status)
	inactivityThreshold := 25 * time.Hour
	if time.Since(jobData.UpdatedAt) > inactivityThreshold {
		log.Info("⏰ Job %s has been inactive for more than 25 hours - marking as complete", jobID)
		reason = "Job complete - Thread is inactive"
		shouldComplete = true
	} else {
		// Job is still within active window, check PR status
		switch prStatus {
		case "merged":
			reason = "Job complete - Pull request was merged"
			shouldComplete = true
			log.Info("✅ Job %s PR was merged - marking as complete", jobID)
		case "closed":
			reason = "Job complete - Pull request was closed"
			shouldComplete = true
			log.Info("✅ Job %s PR was closed - marking as complete", jobID)
		case "open":
			log.Info("ℹ️ Job %s has open PR - not marking as complete", jobID)
			shouldComplete = false
		case "no_pr":
			log.Info("ℹ️ Job %s has no PR - not marking as complete (still within 25-hour activity window)", jobID)
			shouldComplete = false
		default:
			log.Info("ℹ️ Job %s PR status unclear (%s) - keeping active", jobID, prStatus)
			shouldComplete = false
		}
	}

	if shouldComplete {
		if err := mh.sendJobCompleteMessage(jobID, reason); err != nil {
			log.Error("❌ Failed to send job complete message for job %s: %v", jobID, err)
			return fmt.Errorf("failed to send job complete message: %w", err)
		}

		// Remove job from app state since it's complete
		if err := mh.appState.RemoveJob(jobID); err != nil {
			log.Error("❌ Failed to remove job from app state: %v", err)
			return fmt.Errorf("failed to remove job from app state: %w", err)
		}
		log.Info("🗑️ Removed completed job %s from app state", jobID)
	}

	log.Info("📋 Completed successfully - checked idleness for job %s", jobID)
	return nil
}

func (mh *MessageHandler) sendJobCompleteMessage(jobID, reason string) error {
	log.Info("📋 Sending job complete message for job %s with reason: %s", jobID, reason)

	payload := models.JobCompletePayload{
		JobID:  jobID,
		Reason: reason,
	}

	jobMsg := models.BaseMessage{
		ID:      core.NewID("msg"),
		Type:    models.MessageTypeJobComplete,
		Payload: payload,
	}
	mh.messageSender.QueueMessage("cc_message", jobMsg)
	log.Info("📤 Queued job complete message for job: %s (message ID: %s)", jobID, jobMsg.ID)

	return nil
}

func (mh *MessageHandler) sendSystemMessage(message, slackMessageID, jobID string) error {
	payload := models.SystemMessagePayload{
		Message:            message,
		ProcessedMessageID: slackMessageID,
		JobID:              jobID,
	}

	sysMsg := models.BaseMessage{
		ID:      core.NewID("msg"),
		Type:    models.MessageTypeSystemMessage,
		Payload: payload,
	}
	mh.messageSender.QueueMessage("cc_message", sysMsg)
	log.Info("⚙️ Queued system message: %s (message ID: %s)", message, sysMsg.ID)

	return nil
}

// sendErrorMessage sends an error as a system message. The Claude service handles
// all error processing internally, so we just need to format and send the error.
func (mh *MessageHandler) sendErrorMessage(err error, slackMessageID, jobID string) error {
	messageToSend := fmt.Sprintf("eksecd encountered error: %v", err)
	return mh.sendSystemMessage(messageToSend, slackMessageID, jobID)
}

func (mh *MessageHandler) sendProcessingMessage(processedMessageID, jobID string) error {
	processingMessageMsg := models.BaseMessage{
		ID:   core.NewID("msg"),
		Type: models.MessageTypeProcessingMessage,
		Payload: models.ProcessingMessagePayload{
			ProcessedMessageID: processedMessageID,
			JobID:              jobID,
		},
	}

	mh.messageSender.QueueMessage("cc_message", processingMessageMsg)
	log.Info("🔔 Queued processing message notification for message: %s", processedMessageID)
	return nil
}

func extractPRNumber(prURL string) string {
	if prURL == "" {
		return ""
	}

	// Extract PR number from URL like https://github.com/user/repo/pull/1234
	parts := strings.Split(prURL, "/")
	if len(parts) > 0 && parts[len(parts)-1] != "" {
		return "#" + parts[len(parts)-1]
	}

	return ""
}

// stripAccessTokenFromURL removes x-access-token authentication from URLs
// Converts: https://x-access-token:TOKEN@github.com/owner/repo
// To: https://github.com/owner/repo
func stripAccessTokenFromURL(url string) string {
	if url == "" {
		return ""
	}

	// Check if URL contains x-access-token
	if strings.Contains(url, "x-access-token") && strings.Contains(url, "@") {
		// Split by @ to separate credentials from host
		parts := strings.Split(url, "@")
		if len(parts) >= 2 {
			// Take everything after the last @ symbol (handles multiple @ symbols)
			hostAndPath := parts[len(parts)-1]
			// Reconstruct URL with https://
			return "https://" + hostAndPath
		}
	}

	return url
}

func (mh *MessageHandler) sendGitActivitySystemMessage(
	commitResult *usecases.AutoCommitResult,
	slackMessageID string,
	jobID string,
) error {
	if commitResult == nil {
		return nil
	}

	if commitResult.JustCreatedPR && commitResult.PullRequestLink != "" {
		// New PR created
		message := fmt.Sprintf("Agent opened a [pull request](%s)", commitResult.PullRequestLink)
		if err := mh.sendSystemMessage(message, slackMessageID, jobID); err != nil {
			log.Info("❌ Failed to send PR creation system message: %v", err)
			return fmt.Errorf("failed to send PR creation system message: %w", err)
		}
	} else if !commitResult.JustCreatedPR && commitResult.CommitHash != "" && commitResult.RepositoryURL != "" {
		// Commit added to existing PR
		shortHash := commitResult.CommitHash
		if len(shortHash) > 7 {
			shortHash = shortHash[:7]
		}
		// Strip access token from repository URL before sending
		cleanRepoURL := stripAccessTokenFromURL(commitResult.RepositoryURL)
		commitURL := fmt.Sprintf("%s/commit/%s", cleanRepoURL, commitResult.CommitHash)
		message := fmt.Sprintf("New commit added: [%s](%s)", shortHash, commitURL)

		// Add PR link if available
		if commitResult.PullRequestLink != "" {
			prNumber := extractPRNumber(commitResult.PullRequestLink)
			if prNumber != "" {
				message += fmt.Sprintf(" in [%s](%s)", prNumber, commitResult.PullRequestLink)
			}
		}

		if err := mh.sendSystemMessage(message, slackMessageID, jobID); err != nil {
			log.Info("❌ Failed to send commit system message: %v", err)
			return fmt.Errorf("failed to send commit system message: %w", err)
		}
	}

	return nil
}

func unmarshalPayload(payload any, target any) error {
	if payload == nil {
		return nil
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return json.Unmarshal(payloadBytes, target)
}

// PersistQueuedMessage extracts payload from message and persists it to queue for crash recovery
func (mh *MessageHandler) PersistQueuedMessage(msg models.BaseMessage) error {
	// Extract payload based on message type and persist directly
	if msg.Type == models.MessageTypeStartConversation {
		var payload models.StartConversationPayload
		if err := unmarshalPayload(msg.Payload, &payload); err != nil {
			return fmt.Errorf("failed to unmarshal StartConversation payload: %w", err)
		}

		queuedMsg := models.QueuedMessage{
			ProcessedMessageID: payload.ProcessedMessageID,
			JobID:              payload.JobID,
			MessageType:        msg.Type,
			Message:            payload.Message,
			MessageLink:        payload.MessageLink,
			QueuedAt:           time.Now(),
		}
		if err := mh.appState.AddQueuedMessage(queuedMsg); err != nil {
			return fmt.Errorf("failed to persist queued message %s: %w", payload.ProcessedMessageID, err)
		}

		log.Info("💾 Persisted queued message: %s", payload.ProcessedMessageID)
		return nil
	}

	if msg.Type == models.MessageTypeUserMessage {
		var payload models.UserMessagePayload
		if err := unmarshalPayload(msg.Payload, &payload); err != nil {
			return fmt.Errorf("failed to unmarshal UserMessage payload: %w", err)
		}

		queuedMsg := models.QueuedMessage{
			ProcessedMessageID: payload.ProcessedMessageID,
			JobID:              payload.JobID,
			MessageType:        msg.Type,
			Message:            payload.Message,
			MessageLink:        payload.MessageLink,
			QueuedAt:           time.Now(),
		}
		if err := mh.appState.AddQueuedMessage(queuedMsg); err != nil {
			return fmt.Errorf("failed to persist queued message %s: %w", payload.ProcessedMessageID, err)
		}

		log.Info("💾 Persisted queued message: %s", payload.ProcessedMessageID)
		return nil
	}

	return fmt.Errorf("unsupported message type: %s", msg.Type)
}
