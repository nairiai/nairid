package utils

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCodePermissionsProcessor_ProcessPermissions(t *testing.T) {
	// Create a temporary home directory
	tmpHome, err := os.MkdirTemp("", "opencode-permissions-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	// Override HOME for this test
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", originalHome)

	// Create the processor and run it
	processor := NewOpenCodePermissionsProcessor("/tmp/workdir")
	err = processor.ProcessPermissions("")
	if err != nil {
		t.Fatalf("ProcessPermissions failed: %v", err)
	}

	// Read the generated config file
	configPath := filepath.Join(tmpHome, ".config", "opencode", "opencode.json")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read generated config: %v", err)
	}

	// Parse and verify the config
	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse config JSON: %v", err)
	}

	// Verify permissions key exists
	permissions, ok := config["permission"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected 'permission' key in config, got: %v", config)
	}

	// Verify all expected permissions are set to "allow"
	expectedPermissions := []string{
		"bash", "edit", "write", "read", "glob", "grep",
		"webfetch", "task", "skill", "doom_loop", "external_directory",
	}

	for _, perm := range expectedPermissions {
		value, ok := permissions[perm]
		if !ok {
			t.Errorf("Expected permission '%s' to be set, but it's missing", perm)
			continue
		}
		if value != "allow" {
			t.Errorf("Expected permission '%s' to be 'allow', got '%v'", perm, value)
		}
	}
}

func TestOpenCodePermissionsProcessor_PreservesExistingConfig(t *testing.T) {
	// Create a temporary home directory
	tmpHome, err := os.MkdirTemp("", "opencode-permissions-preserve-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	// Override HOME for this test
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", originalHome)

	// Create an existing config with some settings
	configDir := filepath.Join(tmpHome, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	existingConfig := map[string]interface{}{
		"mcp": map[string]interface{}{
			"postgres": map[string]interface{}{
				"type":    "local",
				"enabled": true,
			},
		},
		"instructions": []string{"~/.config/eksecd/rules/*.md"},
	}

	existingJSON, _ := json.MarshalIndent(existingConfig, "", "  ")
	configPath := filepath.Join(configDir, "opencode.json")
	if err := os.WriteFile(configPath, existingJSON, 0644); err != nil {
		t.Fatalf("Failed to write existing config: %v", err)
	}

	// Create the processor and run it
	processor := NewOpenCodePermissionsProcessor("/tmp/workdir")
	err = processor.ProcessPermissions("")
	if err != nil {
		t.Fatalf("ProcessPermissions failed: %v", err)
	}

	// Read the updated config file
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read updated config: %v", err)
	}

	// Parse and verify the config
	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse config JSON: %v", err)
	}

	// Verify existing MCP config is preserved
	mcp, ok := config["mcp"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected 'mcp' key to be preserved, got: %v", config)
	}
	if _, ok := mcp["postgres"]; !ok {
		t.Error("Expected 'postgres' MCP config to be preserved")
	}

	// Verify existing instructions are preserved
	instructions, ok := config["instructions"].([]interface{})
	if !ok {
		t.Fatalf("Expected 'instructions' key to be preserved, got: %v", config)
	}
	if len(instructions) == 0 || instructions[0] != "~/.config/eksecd/rules/*.md" {
		t.Error("Expected instructions to be preserved")
	}

	// Verify permissions were added
	permissions, ok := config["permission"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected 'permission' key to be added, got: %v", config)
	}
	if permissions["bash"] != "allow" {
		t.Error("Expected bash permission to be 'allow'")
	}
}

func TestNoOpPermissionsProcessor_ProcessPermissions(t *testing.T) {
	processor := NewNoOpPermissionsProcessor()
	err := processor.ProcessPermissions("")
	if err != nil {
		t.Errorf("NoOpPermissionsProcessor should not return an error, got: %v", err)
	}
}
