package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"nairid/core/log"
)

// PermissionsProcessor defines the interface for processing agent-specific permissions
type PermissionsProcessor interface {
	// ProcessPermissions configures permissions for the agent.
	// targetHomeDir specifies the home directory to deploy config to.
	// If empty, uses the current user's home directory.
	ProcessPermissions(targetHomeDir string) error
}

// OpenCodePermissionsProcessor handles permissions configuration for OpenCode
type OpenCodePermissionsProcessor struct {
	workDir string
}

// NewOpenCodePermissionsProcessor creates a new OpenCode permissions processor
func NewOpenCodePermissionsProcessor(workDir string) *OpenCodePermissionsProcessor {
	return &OpenCodePermissionsProcessor{
		workDir: workDir,
	}
}

// ProcessPermissions implements PermissionsProcessor for OpenCode
// It configures opencode.json to allow all tool operations without prompting,
// enabling "yolo mode" for automated/headless operation.
// This is required because OpenCode defaults to asking for permission on certain
// operations (like accessing paths outside the project directory), which blocks
// automated workflows.
// targetHomeDir specifies the home directory to deploy config to.
// If empty, uses the current user's home directory.
func (p *OpenCodePermissionsProcessor) ProcessPermissions(targetHomeDir string) error {
	log.Info("🔓 Processing permissions for OpenCode agent")

	// Determine home directory for OpenCode config
	homeDir := targetHomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
	}

	log.Info("🔓 Deploying OpenCode permissions to home directory: %s", homeDir)

	opencodeConfigDir := filepath.Join(homeDir, ".config", "opencode")
	opencodeConfigPath := filepath.Join(opencodeConfigDir, "opencode.json")

	// Ensure OpenCode config directory exists with correct ownership
	if err := mkdirAllAsTargetUser(opencodeConfigDir); err != nil {
		return fmt.Errorf("failed to create OpenCode config directory: %w", err)
	}

	// Read existing config if it exists
	var existingConfig map[string]interface{}
	if content, err := readFileAsTargetUser(opencodeConfigPath); err == nil {
		if err := json.Unmarshal(content, &existingConfig); err != nil {
			log.Info("⚠️  Failed to parse existing opencode.json, creating new config: %v", err)
			existingConfig = make(map[string]interface{})
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to read existing opencode.json: %w", err)
	} else {
		existingConfig = make(map[string]interface{})
	}

	// Configure permissions to allow all operations without prompting
	// This enables "yolo mode" for automated operation
	permissions := map[string]interface{}{
		// Core tool permissions
		"bash":     "allow",
		"edit":     "allow",
		"write":    "allow",
		"read":     "allow",
		"glob":     "allow",
		"grep":     "allow",
		"webfetch": "allow",
		"task":     "allow",
		"skill":    "allow",
		// Special permissions that default to "ask"
		"doom_loop":          "allow",
		"external_directory": "allow",
	}

	existingConfig["permission"] = permissions

	// Write updated config back
	configJSON, err := json.MarshalIndent(existingConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal opencode.json: %w", err)
	}

	log.Info("🔓 Updating opencode.json with permissions at: %s", opencodeConfigPath)

	if err := writeFileAsTargetUser(opencodeConfigPath, configJSON, 0644); err != nil {
		return fmt.Errorf("failed to write opencode.json: %w", err)
	}

	log.Info("✅ Successfully configured permissions for OpenCode (yolo mode enabled)")
	return nil
}

// NoOpPermissionsProcessor is a no-op implementation for agents that don't need permissions processing
type NoOpPermissionsProcessor struct{}

// NewNoOpPermissionsProcessor creates a new no-op permissions processor
func NewNoOpPermissionsProcessor() *NoOpPermissionsProcessor {
	return &NoOpPermissionsProcessor{}
}

// ProcessPermissions implements PermissionsProcessor with no operation
func (p *NoOpPermissionsProcessor) ProcessPermissions(targetHomeDir string) error {
	log.Info("🔓 Permissions processing not needed for this agent type")
	return nil
}
