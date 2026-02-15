package clients

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
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
// Self-hosted installations use API keys with the "eksecd_" or legacy "ccagent_" prefix
func (c *AgentsApiClient) IsSelfHosted() bool {
	return strings.HasPrefix(c.apiKey, "eksecd_") || strings.HasPrefix(c.apiKey, "ccagent_")
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

