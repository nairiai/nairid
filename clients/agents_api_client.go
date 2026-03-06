package clients

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nairid/models"
)

// AttachmentResponse represents the API response for fetching an attachment
type AttachmentResponse struct {
	ID   string `json:"id"`
	Data string `json:"data"` // Base64-encoded content
}

// ArtifactFile represents a file within an artifact
type ArtifactFile struct {
	Location     string `json:"location"`
	AttachmentID string `json:"attachmentId"`
}

// Artifact represents an agent artifact (rule, guideline, instruction)
type Artifact struct {
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Type        string         `json:"type"`
	Files       []ArtifactFile `json:"files"`
}

// TokenResponse represents the API response for token operations
type TokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	EnvKey    string    `json:"env_key"`
}

// AgentsApiClient handles API requests to the Claude Control agents API
type AgentsApiClient struct {
	apiKey  string
	baseURL string
	agentID string
	client  *http.Client
}

// NewAgentsApiClient creates a new agents API client
func NewAgentsApiClient(apiKey, baseURL, agentID string) *AgentsApiClient {
	return &AgentsApiClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		agentID: agentID,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// IsSelfHosted returns true if the API key indicates a self-hosted installation
// Self-hosted installations use API keys with the "nairid_", legacy "eksecd_", or legacy "ccagent_" prefix
func (c *AgentsApiClient) IsSelfHosted() bool {
	return strings.HasPrefix(c.apiKey, "nairid_") || strings.HasPrefix(c.apiKey, "eksecd_") || strings.HasPrefix(c.apiKey, "ccagent_")
}

// FetchAttachment fetches an attachment by ID from the Claude Control API
func (c *AgentsApiClient) FetchAttachment(attachmentID string) (*AttachmentResponse, error) {
	url := fmt.Sprintf("%s/api/agents/attachments/%s", c.baseURL, attachmentID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add Bearer token authentication header
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var attachmentResp AttachmentResponse
	if err := json.NewDecoder(resp.Body).Decode(&attachmentResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &attachmentResp, nil
}

// UploadAttachmentResponse represents the response from the upload attachment endpoint
type UploadAttachmentResponse struct {
	AttachmentID string `json:"attachment_id"`
}

// UploadAttachment uploads a file to the backend and returns the attachment ID
func (c *AgentsApiClient) UploadAttachment(filePath string) (*UploadAttachmentResponse, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer file.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("failed to copy file content: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	url := fmt.Sprintf("%s/api/agents/attachments", c.baseURL)
	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if c.agentID != "" {
		req.Header.Set("X-AGENT-ID", c.agentID)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var uploadResp UploadAttachmentResponse
	if err := json.NewDecoder(resp.Body).Decode(&uploadResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &uploadResp, nil
}

// FetchToken retrieves the current Anthropic token for the authenticated organization
func (c *AgentsApiClient) FetchToken() (*TokenResponse, error) {
	url := fmt.Sprintf("%s/api/agents/token", c.baseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add Bearer token authentication header
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &tokenResp, nil
}

// EnvVarEntry represents a key-value environment variable from the API
type EnvVarEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// EnvVarsResponse represents the API response for environment variables
type EnvVarsResponse struct {
	EnvVars []EnvVarEntry `json:"env_vars"`
}

// FetchEnvVars retrieves environment variables for the authenticated container
func (c *AgentsApiClient) FetchEnvVars() ([]EnvVarEntry, error) {
	url := fmt.Sprintf("%s/api/agents/env", c.baseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	req.Header.Set("Accept", "application/json")
	if c.agentID != "" {
		req.Header.Set("X-AGENT-ID", c.agentID)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var envVarsResp EnvVarsResponse
	if err := json.NewDecoder(resp.Body).Decode(&envVarsResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return envVarsResp.EnvVars, nil
}

// FetchArtifacts retrieves the list of agent artifacts from the API
func (c *AgentsApiClient) FetchArtifacts() ([]Artifact, error) {
	url := fmt.Sprintf("%s/api/agents/artifacts", c.baseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add Bearer token authentication header
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	req.Header.Set("Accept", "application/json")
	// Add X-AGENT-ID header for precise container lookup when multiple containers share API key
	if c.agentID != "" {
		req.Header.Set("X-AGENT-ID", c.agentID)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var artifacts []Artifact
	if err := json.NewDecoder(resp.Body).Decode(&artifacts); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return artifacts, nil
}

// AgentJobsResponse represents the response from GET /api/agents/jobs
type AgentJobsResponse struct {
	Jobs []AgentJob `json:"jobs"`
}

// AgentJob represents a job assigned to the agent
type AgentJob struct {
	ID           string            `json:"id"`
	JobType      string            `json:"job_type"`
	Mode         string            `json:"mode"`
	Title        *string           `json:"title"`
	CreatedAt    time.Time         `json:"created_at"`
	Messages     []AgentJobMessage `json:"messages"`
	SystemPrompt string            `json:"system_prompt,omitempty"`
}

// AgentJobMessage represents a message within an agent job
type AgentJobMessage struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// FetchAgentJobs retrieves active jobs and unacked messages for the authenticated agent
func (c *AgentsApiClient) FetchAgentJobs() (*AgentJobsResponse, error) {
	url := fmt.Sprintf("%s/api/agents/jobs", c.baseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	req.Header.Set("Accept", "application/json")
	if c.agentID != "" {
		req.Header.Set("X-AGENT-ID", c.agentID)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var jobsResp AgentJobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&jobsResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &jobsResp, nil
}

// AckMessage acknowledges a message has been processed
func (c *AgentsApiClient) AckMessage(messageID string) error {
	url := fmt.Sprintf("%s/api/agents/messages/%s/ack", c.baseURL, messageID)

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	if c.agentID != "" {
		req.Header.Set("X-AGENT-ID", c.agentID)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// SubmitMessage sends an agent response message via HTTP
func (c *AgentsApiClient) SubmitMessage(msg models.BaseMessage) error {
	url := fmt.Sprintf("%s/api/agents/messages", c.baseURL)

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	req.Header.Set("Content-Type", "application/json")
	if c.agentID != "" {
		req.Header.Set("X-AGENT-ID", c.agentID)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}
