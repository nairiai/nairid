package models

import (
	"regexp"
	"strings"
)

// AgentMode represents the mode of a conversation
type AgentMode string

const (
	AgentModeExecute AgentMode = "execute"
	AgentModeAsk     AgentMode = "ask"
)

// Platform represents the source platform of a message
type Platform string

const (
	PlatformSlack   Platform = "slack"
	PlatformDiscord Platform = "discord"
	PlatformWeb     Platform = "web"
)

// UserMetadata represents optional user information from incoming messages
type UserMetadata struct {
	ID       *string   `json:"id,omitempty"`
	Name     *string   `json:"name,omitempty"`
	Email    *string   `json:"email,omitempty"`
	Platform *Platform `json:"platform,omitempty"`
}

// slackEmailRegex matches Slack's mrkdwn email format: <mailto:email@example.com|email@example.com>
var slackEmailRegex = regexp.MustCompile(`<mailto:[^|]+\|([^>]+)>`)

// CleanEmail extracts a plain email address from Slack's mrkdwn format.
// Input like "<mailto:user@example.com|user@example.com>" returns "user@example.com".
// Plain email addresses are returned as-is.
func CleanEmail(email string) string {
	if m := slackEmailRegex.FindStringSubmatch(email); len(m) == 2 {
		return m[1]
	}
	return email
}

// FormatSenderLabel returns a formatted string identifying the sender,
// or empty string if no metadata is available.
// Example output: "Pres (pmihaylov95@gmail.com) via slack"
func FormatSenderLabel(metadata *UserMetadata) string {
	if metadata == nil {
		return ""
	}

	var parts []string
	if metadata.Name != nil && *metadata.Name != "" {
		parts = append(parts, *metadata.Name)
	}
	if metadata.Email != nil && *metadata.Email != "" {
		email := CleanEmail(*metadata.Email)
		parts = append(parts, "("+email+")")
	}
	if metadata.Platform != nil {
		parts = append(parts, "via "+string(*metadata.Platform))
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, " ")
}

// Message types
const (
	MessageTypeStartConversation         = "start_conversation_v1"
	MessageTypeStartConversationResponse = "start_conversation_response_v1"
	MessageTypeUserMessage               = "user_message_v1"
	MessageTypeAssistantMessage          = "assistant_message_v1"
	MessageTypeSystemMessage             = "system_message_v1"
	MessageTypeProcessingMessage         = "processing_message_v1"
	MessageTypeCheckIdleJobs             = "check_idle_jobs_v1"
	MessageTypeJobComplete               = "job_complete_v1"
	MessageTypeAgentProgress             = "agent_progress_v1"
)

type BaseMessage struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

// MessageAttachment represents an attachment in a message
type MessageAttachment struct {
	AttachmentID string `json:"attachment_id"` // ID of attachment stored in database
}

// PreviousMessage represents a message from the thread history
type PreviousMessage struct {
	Message     string              `json:"message"`
	Attachments []MessageAttachment `json:"attachments,omitempty"`
}

type StartConversationPayload struct {
	JobID              string              `json:"job_id"`
	Message            string              `json:"message"`
	ProcessedMessageID string              `json:"processed_message_id"`
	MessageLink        string              `json:"message_link"`
	Mode               AgentMode           `json:"mode"`
	SystemPrompt       string              `json:"system_prompt"`
	Attachments        []MessageAttachment `json:"attachments,omitempty"`
	PreviousMessages   []PreviousMessage   `json:"previous_messages,omitempty"`
	SenderMetadata     *UserMetadata       `json:"sender_metadata,omitempty"`
}

type StartConversationResponsePayload struct {
	SessionID string `json:"sessionID"`
	Message   string `json:"message"`
}

type UserMessagePayload struct {
	JobID              string              `json:"job_id"`
	Message            string              `json:"message"`
	ProcessedMessageID string              `json:"processed_message_id"`
	MessageLink        string              `json:"message_link"`
	Attachments        []MessageAttachment `json:"attachments,omitempty"`
	PreviousMessages   []PreviousMessage   `json:"previous_messages,omitempty"`
	SenderMetadata     *UserMetadata       `json:"sender_metadata,omitempty"`
}

type AssistantMessagePayload struct {
	JobID              string `json:"job_id"`
	Message            string `json:"message"`
	ProcessedMessageID string `json:"processed_message_id"`
}

type SystemMessagePayload struct {
	Message            string `json:"message"`
	ProcessedMessageID string `json:"processed_message_id"`
	JobID              string `json:"job_id"`
}

type ProcessingMessagePayload struct {
	ProcessedMessageID string `json:"processed_message_id"`
	JobID              string `json:"job_id"`
}

type CheckIdleJobsPayload struct {
	// Empty payload - agent checks all its jobs
}

type JobCompletePayload struct {
	JobID  string `json:"job_id"`
	Reason string `json:"reason"`
}

// AgentProgressType identifies the kind of progress event
type AgentProgressType string

const (
	ProgressTypeToolUse       AgentProgressType = "tool_use"
	ProgressTypeText          AgentProgressType = "text"
	ProgressTypeStep          AgentProgressType = "step"
	ProgressTypeToolHeartbeat AgentProgressType = "tool_heartbeat"
	ProgressTypeSubagent      AgentProgressType = "subagent"
)

// AgentProgressPayload is a uniform format for agent progress events from all CLI agents
type AgentProgressPayload struct {
	JobID              string            `json:"job_id"`
	ProcessedMessageID string            `json:"processed_message_id"`
	ProgressType       AgentProgressType `json:"progress_type"`
	ToolName           string            `json:"tool_name,omitempty"`
	ToolInput          string            `json:"tool_input,omitempty"`
	ToolStatus         string            `json:"tool_status,omitempty"`
	TextDelta          string            `json:"text_delta,omitempty"`
	Summary            string            `json:"summary,omitempty"`
}
