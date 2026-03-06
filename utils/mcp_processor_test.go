package utils

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

// Test GetMCPConfigFiles

func TestGetMCPConfigFiles_EmptyDirectory(t *testing.T) {
	// Create temporary eksecd MCP directory
	tempDir := t.TempDir()
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Get MCP config files
	files, err := GetMCPConfigFiles()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("Expected 0 files, got: %d", len(files))
	}
}

func TestGetMCPConfigFiles_WithJSONFiles(t *testing.T) {
	// Create temporary eksecd MCP directory
	tempDir := t.TempDir()
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create test JSON files
	testFiles := []string{"github.json", "postgres.json", "server.JSON"}
	for _, file := range testFiles {
		filePath := filepath.Join(mcpDir, file)
		if err := os.WriteFile(filePath, []byte("{}"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Create non-JSON file (should be ignored)
	if err := os.WriteFile(filepath.Join(mcpDir, "readme.txt"), []byte("text"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Get MCP config files
	files, err := GetMCPConfigFiles()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(files) != 3 {
		t.Errorf("Expected 3 files, got: %d", len(files))
	}
}

func TestGetMCPConfigFiles_NonexistentDirectory(t *testing.T) {
	// Use temporary directory that doesn't have MCP directory
	tempDir := t.TempDir()

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Get MCP config files (should return empty list, not error)
	files, err := GetMCPConfigFiles()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("Expected 0 files, got: %d", len(files))
	}
}

// Test CleanEksecdMCPDir

func TestCleanEksecdMCPDir_WithExistingConfigs(t *testing.T) {
	// Create temporary eksecd MCP directory with files
	tempDir := t.TempDir()
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create test MCP config files
	testFiles := []string{"github.json", "postgres.json", "stale-server.json"}
	for _, file := range testFiles {
		filePath := filepath.Join(mcpDir, file)
		if err := os.WriteFile(filePath, []byte("{}"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Clean the MCP directory
	if err := CleanEksecdMCPDir(); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify directory exists but is empty
	entries, err := os.ReadDir(mcpDir)
	if err != nil {
		t.Fatalf("Failed to read MCP directory: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("Expected empty directory, found %d files", len(entries))
	}
}

func TestCleanEksecdMCPDir_NonexistentDirectory(t *testing.T) {
	// Use temporary directory that doesn't have MCP directory
	tempDir := t.TempDir()

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Clean should succeed even if directory doesn't exist
	if err := CleanEksecdMCPDir(); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

func TestCleanEksecdMCPDir_RecreatesDirectory(t *testing.T) {
	// Create temporary eksecd MCP directory
	tempDir := t.TempDir()
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Clean the MCP directory
	if err := CleanEksecdMCPDir(); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify directory still exists (was recreated)
	if _, err := os.Stat(mcpDir); os.IsNotExist(err) {
		t.Errorf("Expected MCP directory to be recreated")
	}
}

// Test MergeMCPConfigs

func TestMergeMCPConfigs_EmptyDirectory(t *testing.T) {
	// Create temporary eksecd MCP directory
	tempDir := t.TempDir()
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Merge MCP configs
	merged, err := MergeMCPConfigs()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(merged) != 0 {
		t.Errorf("Expected empty map, got: %d entries", len(merged))
	}
}

func TestMergeMCPConfigs_WithMultipleConfigs(t *testing.T) {
	// Create temporary eksecd MCP directory
	tempDir := t.TempDir()
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create test MCP config files with top-level mcpServers key
	file1Config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"github": map[string]interface{}{
				"command": "uvx",
				"args":    []string{"mcp-server-github"},
				"env": map[string]interface{}{
					"GITHUB_TOKEN": "token123",
				},
			},
		},
	}

	file2Config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"postgres": map[string]interface{}{
				"command": "docker",
				"args":    []string{"run", "postgres-mcp"},
				"env": map[string]interface{}{
					"DB_URL": "postgres://localhost",
				},
			},
		},
	}

	file1JSON, _ := json.Marshal(file1Config)
	file2JSON, _ := json.Marshal(file2Config)

	if err := os.WriteFile(filepath.Join(mcpDir, "github.json"), file1JSON, 0644); err != nil {
		t.Fatalf("Failed to create github config: %v", err)
	}

	if err := os.WriteFile(filepath.Join(mcpDir, "postgres.json"), file2JSON, 0644); err != nil {
		t.Fatalf("Failed to create postgres config: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Merge MCP configs
	merged, err := MergeMCPConfigs()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(merged) != 2 {
		t.Errorf("Expected 2 servers, got: %d", len(merged))
	}

	// Verify github server exists
	if _, ok := merged["github"]; !ok {
		t.Errorf("Expected 'github' server to exist")
	}

	// Verify postgres server exists
	if _, ok := merged["postgres"]; !ok {
		t.Errorf("Expected 'postgres' server to exist")
	}
}

func TestMergeMCPConfigs_WithDuplicateServerNames(t *testing.T) {
	// Create temporary eksecd MCP directory
	tempDir := t.TempDir()
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create test MCP config files with duplicate server names
	file1Config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"shared-server": map[string]interface{}{
				"command": "server-v1",
				"args":    []string{},
			},
			"unique-server-1": map[string]interface{}{
				"command": "unique1",
				"args":    []string{},
			},
		},
	}

	file2Config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"shared-server": map[string]interface{}{
				"command": "server-v2",
				"args":    []string{},
			},
			"unique-server-2": map[string]interface{}{
				"command": "unique2",
				"args":    []string{},
			},
		},
	}

	file3Config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"shared-server": map[string]interface{}{
				"command": "server-v3",
				"args":    []string{},
			},
		},
	}

	file1JSON, _ := json.Marshal(file1Config)
	file2JSON, _ := json.Marshal(file2Config)
	file3JSON, _ := json.Marshal(file3Config)

	if err := os.WriteFile(filepath.Join(mcpDir, "file1.json"), file1JSON, 0644); err != nil {
		t.Fatalf("Failed to create file1 config: %v", err)
	}

	if err := os.WriteFile(filepath.Join(mcpDir, "file2.json"), file2JSON, 0644); err != nil {
		t.Fatalf("Failed to create file2 config: %v", err)
	}

	if err := os.WriteFile(filepath.Join(mcpDir, "file3.json"), file3JSON, 0644); err != nil {
		t.Fatalf("Failed to create file3 config: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Merge MCP configs
	merged, err := MergeMCPConfigs()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Expect 5 servers total: shared-server, shared-server-2, shared-server-3, unique-server-1, unique-server-2
	if len(merged) != 5 {
		t.Errorf("Expected 5 servers, got: %d", len(merged))
	}

	// Verify first instance has original name
	if _, ok := merged["shared-server"]; !ok {
		t.Errorf("Expected 'shared-server' to exist")
	}

	// Verify second instance has suffix -2
	if _, ok := merged["shared-server-2"]; !ok {
		t.Errorf("Expected 'shared-server-2' to exist")
	}

	// Verify third instance has suffix -3
	if _, ok := merged["shared-server-3"]; !ok {
		t.Errorf("Expected 'shared-server-3' to exist")
	}

	// Verify unique servers exist
	if _, ok := merged["unique-server-1"]; !ok {
		t.Errorf("Expected 'unique-server-1' to exist")
	}

	if _, ok := merged["unique-server-2"]; !ok {
		t.Errorf("Expected 'unique-server-2' to exist")
	}
}

func TestMergeMCPConfigs_WithInvalidJSON(t *testing.T) {
	// Create temporary eksecd MCP directory
	tempDir := t.TempDir()
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create invalid JSON file
	invalidJSON := []byte("{invalid json content")
	if err := os.WriteFile(filepath.Join(mcpDir, "invalid.json"), invalidJSON, 0644); err != nil {
		t.Fatalf("Failed to create invalid JSON file: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Merge MCP configs - should return error
	_, err := MergeMCPConfigs()
	if err == nil {
		t.Errorf("Expected error for invalid JSON, got nil")
	}
}

func TestMergeMCPConfigs_WithMissingMCPServersKey(t *testing.T) {
	// Create temporary eksecd MCP directory
	tempDir := t.TempDir()
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create config file without mcpServers key
	configWithoutKey := map[string]interface{}{
		"someOtherKey": "value",
	}
	configJSON, _ := json.Marshal(configWithoutKey)
	if err := os.WriteFile(filepath.Join(mcpDir, "no-key.json"), configJSON, 0644); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Merge MCP configs - should succeed but return empty map
	merged, err := MergeMCPConfigs()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(merged) != 0 {
		t.Errorf("Expected empty map when mcpServers key is missing, got: %d entries", len(merged))
	}
}

func TestMergeMCPConfigs_WithEmptyMCPServersObject(t *testing.T) {
	// Create temporary eksecd MCP directory
	tempDir := t.TempDir()
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create config file with empty mcpServers object
	emptyConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{},
	}
	configJSON, _ := json.Marshal(emptyConfig)
	if err := os.WriteFile(filepath.Join(mcpDir, "empty.json"), configJSON, 0644); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Merge MCP configs - should succeed but return empty map
	merged, err := MergeMCPConfigs()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(merged) != 0 {
		t.Errorf("Expected empty map when mcpServers is empty, got: %d entries", len(merged))
	}
}

func TestMergeMCPConfigs_WithMultipleServersInSingleFile(t *testing.T) {
	// Create temporary eksecd MCP directory
	tempDir := t.TempDir()
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create config file with multiple servers
	multiServerConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"github": map[string]interface{}{
				"command": "uvx",
				"args":    []string{"mcp-server-github"},
			},
			"postgres": map[string]interface{}{
				"command": "docker",
				"args":    []string{"run", "postgres-mcp"},
			},
			"filesystem": map[string]interface{}{
				"command": "node",
				"args":    []string{"mcp-server-filesystem"},
			},
		},
	}
	configJSON, _ := json.Marshal(multiServerConfig)
	if err := os.WriteFile(filepath.Join(mcpDir, "multiple.json"), configJSON, 0644); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Merge MCP configs
	merged, err := MergeMCPConfigs()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should have all 3 servers
	if len(merged) != 3 {
		t.Errorf("Expected 3 servers, got: %d", len(merged))
	}

	// Verify all servers exist
	expectedServers := []string{"github", "postgres", "filesystem"}
	for _, serverName := range expectedServers {
		if _, ok := merged[serverName]; !ok {
			t.Errorf("Expected '%s' server to exist", serverName)
		}
	}
}

// Test ClaudeCodeMCPProcessor

func TestClaudeCodeMCPProcessor_NoConfigs(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process MCP configs (should succeed with no configs)
	processor := NewClaudeCodeMCPProcessor(workDir)
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify .claude.json was not created when no configs
	claudeConfigPath := filepath.Join(tempDir, ".claude.json")
	if _, err := os.Stat(claudeConfigPath); !os.IsNotExist(err) {
		t.Errorf("Expected .claude.json not to exist when no configs")
	}
}

func TestClaudeCodeMCPProcessor_WithConfigs(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create test MCP config file with top-level mcpServers key
	fileConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"github": map[string]interface{}{
				"command": "uvx",
				"args":    []string{"mcp-server-github"},
			},
		},
	}

	fileJSON, _ := json.Marshal(fileConfig)
	if err := os.WriteFile(filepath.Join(mcpDir, "github.json"), fileJSON, 0644); err != nil {
		t.Fatalf("Failed to create github config: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process MCP configs
	processor := NewClaudeCodeMCPProcessor(workDir)
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify .claude.json was created with mcpServers key
	claudeConfigPath := filepath.Join(tempDir, ".claude.json")
	content, err := os.ReadFile(claudeConfigPath)
	if err != nil {
		t.Fatalf("Expected .claude.json to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse .claude.json: %v", err)
	}

	// Verify mcpServers key exists
	mcpServers, ok := config["mcpServers"]
	if !ok {
		t.Fatalf("Expected mcpServers key to exist")
	}

	// Verify github server is in mcpServers
	serversMap, ok := mcpServers.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected mcpServers to be a map")
	}

	if _, ok := serversMap["github"]; !ok {
		t.Errorf("Expected 'github' server to exist in mcpServers")
	}
}

func TestClaudeCodeMCPProcessor_WithMultipleMCPConfigs(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create multiple MCP config files
	file1Config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"github": map[string]interface{}{
				"command": "uvx",
				"args":    []string{"mcp-server-github"},
			},
		},
	}

	file2Config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"postgres": map[string]interface{}{
				"command": "docker",
				"args":    []string{"run", "postgres-mcp"},
			},
		},
	}

	file1JSON, _ := json.Marshal(file1Config)
	file2JSON, _ := json.Marshal(file2Config)

	if err := os.WriteFile(filepath.Join(mcpDir, "github.json"), file1JSON, 0644); err != nil {
		t.Fatalf("Failed to create github config: %v", err)
	}

	if err := os.WriteFile(filepath.Join(mcpDir, "postgres.json"), file2JSON, 0644); err != nil {
		t.Fatalf("Failed to create postgres config: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process MCP configs
	processor := NewClaudeCodeMCPProcessor(workDir)
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify .claude.json was created with both servers
	claudeConfigPath := filepath.Join(tempDir, ".claude.json")
	content, err := os.ReadFile(claudeConfigPath)
	if err != nil {
		t.Fatalf("Expected .claude.json to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse .claude.json: %v", err)
	}

	// Verify mcpServers key exists
	mcpServers, ok := config["mcpServers"]
	if !ok {
		t.Fatalf("Expected mcpServers key to exist")
	}

	serversMap, ok := mcpServers.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected mcpServers to be a map")
	}

	// Verify both servers are present
	if len(serversMap) != 2 {
		t.Errorf("Expected 2 servers, got: %d", len(serversMap))
	}

	if _, ok := serversMap["github"]; !ok {
		t.Errorf("Expected 'github' server to exist in mcpServers")
	}

	if _, ok := serversMap["postgres"]; !ok {
		t.Errorf("Expected 'postgres' server to exist in mcpServers")
	}
}

func TestClaudeCodeMCPProcessor_PreservesExistingConfig(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create existing .claude.json with other keys
	claudeConfigPath := filepath.Join(tempDir, ".claude.json")
	existingConfig := map[string]interface{}{
		"theme":    "dark",
		"fontSize": 14,
	}
	existingJSON, _ := json.Marshal(existingConfig)
	if err := os.WriteFile(claudeConfigPath, existingJSON, 0644); err != nil {
		t.Fatalf("Failed to create existing .claude.json: %v", err)
	}

	// Create test MCP config with top-level mcpServers key
	fileConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"github": map[string]interface{}{
				"command": "uvx",
				"args":    []string{"mcp-server-github"},
			},
		},
	}
	fileJSON, _ := json.Marshal(fileConfig)
	if err := os.WriteFile(filepath.Join(mcpDir, "github.json"), fileJSON, 0644); err != nil {
		t.Fatalf("Failed to create github config: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process MCP configs
	processor := NewClaudeCodeMCPProcessor(workDir)
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify .claude.json preserves existing keys
	content, err := os.ReadFile(claudeConfigPath)
	if err != nil {
		t.Fatalf("Expected .claude.json to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse .claude.json: %v", err)
	}

	// Verify existing keys are preserved
	if theme, ok := config["theme"]; !ok || theme != "dark" {
		t.Errorf("Expected theme to be preserved")
	}

	if fontSize, ok := config["fontSize"]; !ok || fontSize.(float64) != 14 {
		t.Errorf("Expected fontSize to be preserved")
	}

	// Verify mcpServers was added
	if _, ok := config["mcpServers"]; !ok {
		t.Errorf("Expected mcpServers key to be added")
	}
}

// Test OpenCodeMCPProcessor

func TestOpenCodeMCPProcessor_NoConfigs(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process MCP configs (should succeed with no configs)
	processor := NewOpenCodeMCPProcessor(workDir)
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify opencode.json was not created when no configs
	opencodeConfigPath := filepath.Join(tempDir, ".config", "opencode", "opencode.json")
	if _, err := os.Stat(opencodeConfigPath); !os.IsNotExist(err) {
		t.Errorf("Expected opencode.json not to exist when no configs")
	}
}

func TestOpenCodeMCPProcessor_WithConfigs(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create test MCP config file with top-level mcpServers key
	fileConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"github": map[string]interface{}{
				"command": "uvx",
				"args":    []string{"mcp-server-github"},
			},
		},
	}

	fileJSON, _ := json.Marshal(fileConfig)
	if err := os.WriteFile(filepath.Join(mcpDir, "github.json"), fileJSON, 0644); err != nil {
		t.Fatalf("Failed to create github config: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process MCP configs
	processor := NewOpenCodeMCPProcessor(workDir)
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify opencode.json was created with mcp key
	opencodeConfigPath := filepath.Join(tempDir, ".config", "opencode", "opencode.json")
	content, err := os.ReadFile(opencodeConfigPath)
	if err != nil {
		t.Fatalf("Expected opencode.json to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse opencode.json: %v", err)
	}

	// Verify mcp key exists
	mcpServers, ok := config["mcp"]
	if !ok {
		t.Fatalf("Expected mcp key to exist")
	}

	// Verify github server is in mcp
	serversMap, ok := mcpServers.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected mcp to be a map")
	}

	if _, ok := serversMap["github"]; !ok {
		t.Errorf("Expected 'github' server to exist in mcp")
	}

	// Verify OpenCode format transformation
	githubConfig, ok := serversMap["github"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected github config to be a map")
	}

	// Verify type field is set to "local"
	if serverType, ok := githubConfig["type"].(string); !ok || serverType != "local" {
		t.Errorf("Expected type to be 'local', got: %v", githubConfig["type"])
	}

	// Verify command is an array with command + args merged
	command, ok := githubConfig["command"].([]interface{})
	if !ok {
		t.Fatalf("Expected command to be an array, got: %T", githubConfig["command"])
	}

	expectedCommand := []string{"uvx", "mcp-server-github"}
	if len(command) != len(expectedCommand) {
		t.Errorf("Expected command array length %d, got: %d", len(expectedCommand), len(command))
	}

	for i, expected := range expectedCommand {
		if i >= len(command) {
			break
		}
		if actual, ok := command[i].(string); !ok || actual != expected {
			t.Errorf("Expected command[%d] to be '%s', got: %v", i, expected, command[i])
		}
	}

	// Verify enabled field is set to true
	if enabled, ok := githubConfig["enabled"].(bool); !ok || !enabled {
		t.Errorf("Expected enabled to be true, got: %v", githubConfig["enabled"])
	}
}

func TestOpenCodeMCPProcessor_WithMultipleMCPConfigs(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create multiple MCP config files
	file1Config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"github": map[string]interface{}{
				"command": "uvx",
				"args":    []string{"mcp-server-github"},
			},
		},
	}

	file2Config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"postgres": map[string]interface{}{
				"command": "docker",
				"args":    []string{"run", "postgres-mcp"},
			},
		},
	}

	file1JSON, _ := json.Marshal(file1Config)
	file2JSON, _ := json.Marshal(file2Config)

	if err := os.WriteFile(filepath.Join(mcpDir, "github.json"), file1JSON, 0644); err != nil {
		t.Fatalf("Failed to create github config: %v", err)
	}

	if err := os.WriteFile(filepath.Join(mcpDir, "postgres.json"), file2JSON, 0644); err != nil {
		t.Fatalf("Failed to create postgres config: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process MCP configs
	processor := NewOpenCodeMCPProcessor(workDir)
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify opencode.json was created with both servers
	opencodeConfigPath := filepath.Join(tempDir, ".config", "opencode", "opencode.json")
	content, err := os.ReadFile(opencodeConfigPath)
	if err != nil {
		t.Fatalf("Expected opencode.json to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse opencode.json: %v", err)
	}

	// Verify mcp key exists
	mcpServers, ok := config["mcp"]
	if !ok {
		t.Fatalf("Expected mcp key to exist")
	}

	serversMap, ok := mcpServers.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected mcp to be a map")
	}

	// Verify both servers are present
	if len(serversMap) != 2 {
		t.Errorf("Expected 2 servers, got: %d", len(serversMap))
	}

	if _, ok := serversMap["github"]; !ok {
		t.Errorf("Expected 'github' server to exist in mcp")
	}

	if _, ok := serversMap["postgres"]; !ok {
		t.Errorf("Expected 'postgres' server to exist in mcp")
	}
}

func TestOpenCodeMCPProcessor_PreservesExistingConfig(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")
	opencodeConfigDir := filepath.Join(tempDir, ".config", "opencode")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	if err := os.MkdirAll(opencodeConfigDir, 0755); err != nil {
		t.Fatalf("Failed to create opencode config directory: %v", err)
	}

	// Create existing opencode.json with other keys (e.g., instructions)
	opencodeConfigPath := filepath.Join(opencodeConfigDir, "opencode.json")
	existingConfig := map[string]interface{}{
		"instructions": []string{"~/.config/eksecd/rules/*.md"},
		"theme":        "dark",
	}
	existingJSON, _ := json.Marshal(existingConfig)
	if err := os.WriteFile(opencodeConfigPath, existingJSON, 0644); err != nil {
		t.Fatalf("Failed to create existing opencode.json: %v", err)
	}

	// Create test MCP config with top-level mcpServers key
	fileConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"github": map[string]interface{}{
				"command": "uvx",
				"args":    []string{"mcp-server-github"},
			},
		},
	}
	fileJSON, _ := json.Marshal(fileConfig)
	if err := os.WriteFile(filepath.Join(mcpDir, "github.json"), fileJSON, 0644); err != nil {
		t.Fatalf("Failed to create github config: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process MCP configs
	processor := NewOpenCodeMCPProcessor(workDir)
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify opencode.json preserves existing keys
	content, err := os.ReadFile(opencodeConfigPath)
	if err != nil {
		t.Fatalf("Expected opencode.json to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse opencode.json: %v", err)
	}

	// Verify existing keys are preserved
	if instructions, ok := config["instructions"]; !ok {
		t.Errorf("Expected instructions to be preserved")
	} else {
		instructionsArr, ok := instructions.([]interface{})
		if !ok || len(instructionsArr) != 1 {
			t.Errorf("Expected instructions array to be preserved")
		}
	}

	if theme, ok := config["theme"]; !ok || theme != "dark" {
		t.Errorf("Expected theme to be preserved")
	}

	// Verify mcp was added
	if _, ok := config["mcp"]; !ok {
		t.Errorf("Expected mcp key to be added")
	}
}

// Test OpenCode MCP Format Transformation

func TestOpenCodeMCPProcessor_LocalServerTransformation(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create test MCP config with local server (command + args + env)
	fileConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"postgres": map[string]interface{}{
				"command": "npx",
				"args":    []string{"-y", "@modelcontextprotocol/server-postgres", "postgresql://localhost/db"},
				"env": map[string]interface{}{
					"DB_HOST": "localhost",
					"DB_PORT": "5432",
				},
			},
		},
	}

	fileJSON, _ := json.Marshal(fileConfig)
	if err := os.WriteFile(filepath.Join(mcpDir, "postgres.json"), fileJSON, 0644); err != nil {
		t.Fatalf("Failed to create postgres config: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process MCP configs
	processor := NewOpenCodeMCPProcessor(workDir)
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Read and verify the output
	opencodeConfigPath := filepath.Join(tempDir, ".config", "opencode", "opencode.json")
	content, err := os.ReadFile(opencodeConfigPath)
	if err != nil {
		t.Fatalf("Expected opencode.json to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse opencode.json: %v", err)
	}

	mcpServers, ok := config["mcp"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected mcp to be a map")
	}

	postgresConfig, ok := mcpServers["postgres"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected postgres config to be a map")
	}

	// Verify type is "local"
	if serverType, ok := postgresConfig["type"].(string); !ok || serverType != "local" {
		t.Errorf("Expected type to be 'local', got: %v", postgresConfig["type"])
	}

	// Verify command array merges command + args
	command, ok := postgresConfig["command"].([]interface{})
	if !ok {
		t.Fatalf("Expected command to be an array, got: %T", postgresConfig["command"])
	}

	expectedCommand := []string{"npx", "-y", "@modelcontextprotocol/server-postgres", "postgresql://localhost/db"}
	if len(command) != len(expectedCommand) {
		t.Errorf("Expected command array length %d, got: %d", len(expectedCommand), len(command))
	}

	for i, expected := range expectedCommand {
		if i >= len(command) {
			break
		}
		if actual, ok := command[i].(string); !ok || actual != expected {
			t.Errorf("Expected command[%d] to be '%s', got: %v", i, expected, command[i])
		}
	}

	// Verify env was renamed to environment
	environment, ok := postgresConfig["environment"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected environment to be a map, got: %T", postgresConfig["environment"])
	}

	if dbHost, ok := environment["DB_HOST"].(string); !ok || dbHost != "localhost" {
		t.Errorf("Expected DB_HOST to be 'localhost', got: %v", environment["DB_HOST"])
	}

	if dbPort, ok := environment["DB_PORT"].(string); !ok || dbPort != "5432" {
		t.Errorf("Expected DB_PORT to be '5432', got: %v", environment["DB_PORT"])
	}

	// Verify enabled is true
	if enabled, ok := postgresConfig["enabled"].(bool); !ok || !enabled {
		t.Errorf("Expected enabled to be true, got: %v", postgresConfig["enabled"])
	}

	// Verify no "args" or "env" fields remain (should be transformed)
	if _, hasArgs := postgresConfig["args"]; hasArgs {
		t.Errorf("Expected 'args' field to be removed after transformation")
	}

	if _, hasEnv := postgresConfig["env"]; hasEnv {
		t.Errorf("Expected 'env' field to be removed after transformation")
	}
}

func TestOpenCodeMCPProcessor_RemoteServerTransformation(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create test MCP config with remote server (url + headers)
	fileConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"api-server": map[string]interface{}{
				"url": "https://api.example.com/mcp",
				"headers": map[string]interface{}{
					"Authorization": "Bearer token123",
					"X-API-Key":     "key456",
				},
			},
		},
	}

	fileJSON, _ := json.Marshal(fileConfig)
	if err := os.WriteFile(filepath.Join(mcpDir, "api-server.json"), fileJSON, 0644); err != nil {
		t.Fatalf("Failed to create api-server config: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process MCP configs
	processor := NewOpenCodeMCPProcessor(workDir)
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Read and verify the output
	opencodeConfigPath := filepath.Join(tempDir, ".config", "opencode", "opencode.json")
	content, err := os.ReadFile(opencodeConfigPath)
	if err != nil {
		t.Fatalf("Expected opencode.json to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse opencode.json: %v", err)
	}

	mcpServers, ok := config["mcp"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected mcp to be a map")
	}

	apiServerConfig, ok := mcpServers["api-server"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected api-server config to be a map")
	}

	// Verify type is "remote"
	if serverType, ok := apiServerConfig["type"].(string); !ok || serverType != "remote" {
		t.Errorf("Expected type to be 'remote', got: %v", apiServerConfig["type"])
	}

	// Verify url is preserved
	if url, ok := apiServerConfig["url"].(string); !ok || url != "https://api.example.com/mcp" {
		t.Errorf("Expected url to be 'https://api.example.com/mcp', got: %v", apiServerConfig["url"])
	}

	// Verify headers are preserved
	headers, ok := apiServerConfig["headers"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected headers to be a map, got: %T", apiServerConfig["headers"])
	}

	if auth, ok := headers["Authorization"].(string); !ok || auth != "Bearer token123" {
		t.Errorf("Expected Authorization header, got: %v", headers["Authorization"])
	}

	if apiKey, ok := headers["X-API-Key"].(string); !ok || apiKey != "key456" {
		t.Errorf("Expected X-API-Key header, got: %v", headers["X-API-Key"])
	}

	// Verify enabled is true
	if enabled, ok := apiServerConfig["enabled"].(bool); !ok || !enabled {
		t.Errorf("Expected enabled to be true, got: %v", apiServerConfig["enabled"])
	}

	// Verify no "command" field exists for remote servers
	if _, hasCommand := apiServerConfig["command"]; hasCommand {
		t.Errorf("Expected 'command' field not to exist for remote server")
	}
}

func TestOpenCodeMCPProcessor_MixedLocalAndRemoteServers(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create test MCP config with both local and remote servers
	fileConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"github": map[string]interface{}{
				"command": "uvx",
				"args":    []string{"mcp-server-github"},
				"env": map[string]interface{}{
					"GITHUB_TOKEN": "ghp_123",
				},
			},
			"remote-api": map[string]interface{}{
				"url": "https://remote.example.com",
				"headers": map[string]interface{}{
					"X-API-Key": "secret",
				},
			},
		},
	}

	fileJSON, _ := json.Marshal(fileConfig)
	if err := os.WriteFile(filepath.Join(mcpDir, "mixed.json"), fileJSON, 0644); err != nil {
		t.Fatalf("Failed to create mixed config: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process MCP configs
	processor := NewOpenCodeMCPProcessor(workDir)
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Read and verify the output
	opencodeConfigPath := filepath.Join(tempDir, ".config", "opencode", "opencode.json")
	content, err := os.ReadFile(opencodeConfigPath)
	if err != nil {
		t.Fatalf("Expected opencode.json to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse opencode.json: %v", err)
	}

	mcpServers, ok := config["mcp"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected mcp to be a map")
	}

	// Verify both servers exist
	if len(mcpServers) != 2 {
		t.Errorf("Expected 2 servers, got: %d", len(mcpServers))
	}

	// Verify local server (github)
	githubConfig, ok := mcpServers["github"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected github config to be a map")
	}

	if serverType, ok := githubConfig["type"].(string); !ok || serverType != "local" {
		t.Errorf("Expected github type to be 'local', got: %v", githubConfig["type"])
	}

	if command, ok := githubConfig["command"].([]interface{}); !ok || len(command) != 2 {
		t.Errorf("Expected github command to be array with 2 elements, got: %v", githubConfig["command"])
	}

	// Verify remote server (remote-api)
	remoteConfig, ok := mcpServers["remote-api"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected remote-api config to be a map")
	}

	if serverType, ok := remoteConfig["type"].(string); !ok || serverType != "remote" {
		t.Errorf("Expected remote-api type to be 'remote', got: %v", remoteConfig["type"])
	}

	if url, ok := remoteConfig["url"].(string); !ok || url != "https://remote.example.com" {
		t.Errorf("Expected remote-api url to be 'https://remote.example.com', got: %v", remoteConfig["url"])
	}
}

func TestOpenCodeMCPProcessor_LocalServerWithoutEnv(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create test MCP config with local server without env
	fileConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"simple": map[string]interface{}{
				"command": "node",
				"args":    []string{"server.js"},
			},
		},
	}

	fileJSON, _ := json.Marshal(fileConfig)
	if err := os.WriteFile(filepath.Join(mcpDir, "simple.json"), fileJSON, 0644); err != nil {
		t.Fatalf("Failed to create simple config: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process MCP configs
	processor := NewOpenCodeMCPProcessor(workDir)
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Read and verify the output
	opencodeConfigPath := filepath.Join(tempDir, ".config", "opencode", "opencode.json")
	content, err := os.ReadFile(opencodeConfigPath)
	if err != nil {
		t.Fatalf("Expected opencode.json to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse opencode.json: %v", err)
	}

	mcpServers, ok := config["mcp"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected mcp to be a map")
	}

	simpleConfig, ok := mcpServers["simple"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected simple config to be a map")
	}

	// Verify type is "local"
	if serverType, ok := simpleConfig["type"].(string); !ok || serverType != "local" {
		t.Errorf("Expected type to be 'local', got: %v", simpleConfig["type"])
	}

	// Verify command array
	command, ok := simpleConfig["command"].([]interface{})
	if !ok {
		t.Fatalf("Expected command to be an array, got: %T", simpleConfig["command"])
	}

	expectedCommand := []string{"node", "server.js"}
	if len(command) != len(expectedCommand) {
		t.Errorf("Expected command array length %d, got: %d", len(expectedCommand), len(command))
	}

	// Verify no environment field (since env wasn't provided)
	if _, hasEnv := simpleConfig["environment"]; hasEnv {
		t.Errorf("Expected no 'environment' field when env is not provided")
	}

	// Verify enabled is true
	if enabled, ok := simpleConfig["enabled"].(bool); !ok || !enabled {
		t.Errorf("Expected enabled to be true, got: %v", simpleConfig["enabled"])
	}
}

func TestOpenCodeMCPProcessor_RemoteServerWithoutHeaders(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	// Create test MCP config with remote server without headers
	fileConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"public-api": map[string]interface{}{
				"url": "https://public.example.com",
			},
		},
	}

	fileJSON, _ := json.Marshal(fileConfig)
	if err := os.WriteFile(filepath.Join(mcpDir, "public.json"), fileJSON, 0644); err != nil {
		t.Fatalf("Failed to create public config: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process MCP configs
	processor := NewOpenCodeMCPProcessor(workDir)
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Read and verify the output
	opencodeConfigPath := filepath.Join(tempDir, ".config", "opencode", "opencode.json")
	content, err := os.ReadFile(opencodeConfigPath)
	if err != nil {
		t.Fatalf("Expected opencode.json to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse opencode.json: %v", err)
	}

	mcpServers, ok := config["mcp"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected mcp to be a map")
	}

	publicConfig, ok := mcpServers["public-api"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected public-api config to be a map")
	}

	// Verify type is "remote"
	if serverType, ok := publicConfig["type"].(string); !ok || serverType != "remote" {
		t.Errorf("Expected type to be 'remote', got: %v", publicConfig["type"])
	}

	// Verify url is preserved
	if url, ok := publicConfig["url"].(string); !ok || url != "https://public.example.com" {
		t.Errorf("Expected url to be 'https://public.example.com', got: %v", publicConfig["url"])
	}

	// Verify no headers field (since headers weren't provided)
	if _, hasHeaders := publicConfig["headers"]; hasHeaders {
		t.Errorf("Expected no 'headers' field when headers are not provided")
	}

	// Verify enabled is true
	if enabled, ok := publicConfig["enabled"].(bool); !ok || !enabled {
		t.Errorf("Expected enabled to be true, got: %v", publicConfig["enabled"])
	}
}

// Test CodexMCPProcessor

func TestCodexMCPProcessor_NoConfigs(t *testing.T) {
	tempDir := t.TempDir()

	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	processor := NewCodexMCPProcessor()
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// config.toml should still be created with shell_environment_policy
	codexConfigPath := filepath.Join(tempDir, ".codex", "config.toml")
	content, err := os.ReadFile(codexConfigPath)
	if err != nil {
		t.Fatalf("Expected config.toml to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := toml.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse config.toml: %v", err)
	}

	// Verify shell_environment_policy is set
	policy, ok := config["shell_environment_policy"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected shell_environment_policy to exist")
	}

	if policy["inherit"] != "core" {
		t.Errorf("Expected inherit to be 'core', got: %v", policy["inherit"])
	}

	if policy["ignore_default_excludes"] != true {
		t.Errorf("Expected ignore_default_excludes to be true, got: %v", policy["ignore_default_excludes"])
	}
}

func TestCodexMCPProcessor_WithConfigs(t *testing.T) {
	tempDir := t.TempDir()
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	fileConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"github": map[string]interface{}{
				"command": "uvx",
				"args":    []string{"mcp-server-github"},
				"env": map[string]interface{}{
					"GITHUB_TOKEN": "ghp_test",
				},
			},
		},
	}

	fileJSON, _ := json.Marshal(fileConfig)
	if err := os.WriteFile(filepath.Join(mcpDir, "github.json"), fileJSON, 0644); err != nil {
		t.Fatalf("Failed to create github config: %v", err)
	}

	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	processor := NewCodexMCPProcessor()
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	codexConfigPath := filepath.Join(tempDir, ".codex", "config.toml")
	content, err := os.ReadFile(codexConfigPath)
	if err != nil {
		t.Fatalf("Expected config.toml to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := toml.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse config.toml: %v", err)
	}

	// Verify mcp_servers key exists
	mcpServers, ok := config["mcp_servers"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected mcp_servers key to exist")
	}

	githubConfig, ok := mcpServers["github"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected github server to exist in mcp_servers")
	}

	if githubConfig["command"] != "uvx" {
		t.Errorf("Expected command to be 'uvx', got: %v", githubConfig["command"])
	}

	if githubConfig["enabled"] != true {
		t.Errorf("Expected enabled to be true, got: %v", githubConfig["enabled"])
	}

	// Verify shell_environment_policy is also set
	if _, ok := config["shell_environment_policy"]; !ok {
		t.Error("Expected shell_environment_policy to be set")
	}
}

func TestCodexMCPProcessor_RemoteServer(t *testing.T) {
	tempDir := t.TempDir()
	mcpDir := filepath.Join(tempDir, ".config", "eksecd", "mcp")

	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatalf("Failed to create MCP directory: %v", err)
	}

	fileConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"api-server": map[string]interface{}{
				"url": "https://api.example.com/mcp",
				"headers": map[string]interface{}{
					"Authorization": "Bearer token123",
				},
			},
		},
	}

	fileJSON, _ := json.Marshal(fileConfig)
	if err := os.WriteFile(filepath.Join(mcpDir, "api.json"), fileJSON, 0644); err != nil {
		t.Fatalf("Failed to create api config: %v", err)
	}

	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	processor := NewCodexMCPProcessor()
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	codexConfigPath := filepath.Join(tempDir, ".codex", "config.toml")
	content, err := os.ReadFile(codexConfigPath)
	if err != nil {
		t.Fatalf("Expected config.toml to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := toml.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse config.toml: %v", err)
	}

	mcpServers, ok := config["mcp_servers"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected mcp_servers key to exist")
	}

	apiConfig, ok := mcpServers["api-server"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected api-server to exist in mcp_servers")
	}

	if apiConfig["url"] != "https://api.example.com/mcp" {
		t.Errorf("Expected url to be 'https://api.example.com/mcp', got: %v", apiConfig["url"])
	}

	// Verify headers were mapped to http_headers
	headers, ok := apiConfig["http_headers"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected http_headers to be a map")
	}

	if headers["Authorization"] != "Bearer token123" {
		t.Errorf("Expected Authorization header, got: %v", headers["Authorization"])
	}
}

func TestCodexMCPProcessor_PreservesExistingConfig(t *testing.T) {
	tempDir := t.TempDir()
	codexConfigDir := filepath.Join(tempDir, ".codex")

	if err := os.MkdirAll(codexConfigDir, 0755); err != nil {
		t.Fatalf("Failed to create codex config dir: %v", err)
	}

	// Create existing config.toml with a custom setting
	existingTOML := `model = "gpt-5"
`
	codexConfigPath := filepath.Join(codexConfigDir, "config.toml")
	if err := os.WriteFile(codexConfigPath, []byte(existingTOML), 0644); err != nil {
		t.Fatalf("Failed to create existing config.toml: %v", err)
	}

	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	processor := NewCodexMCPProcessor()
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	content, err := os.ReadFile(codexConfigPath)
	if err != nil {
		t.Fatalf("Expected config.toml to exist, got error: %v", err)
	}

	var config map[string]interface{}
	if err := toml.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse config.toml: %v", err)
	}

	// Verify existing key is preserved
	if config["model"] != "gpt-5" {
		t.Errorf("Expected model to be preserved as 'gpt-5', got: %v", config["model"])
	}

	// Verify shell_environment_policy was added
	if _, ok := config["shell_environment_policy"]; !ok {
		t.Error("Expected shell_environment_policy to be added")
	}
}

func TestCodexProxiedMCPProcessor_Success(t *testing.T) {
	servers := []MCPProxyServerInfo{
		{Name: "github", URL: "/mcp/github"},
		{Name: "postgres", URL: "/mcp/postgres"},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(servers)
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	processor := NewCodexProxiedMCPProcessor(ts.URL)

	if err := processor.ProcessMCPConfigs(tempDir); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tempDir, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("Failed to read config.toml: %v", err)
	}

	var config map[string]interface{}
	if err := toml.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse config.toml: %v", err)
	}

	mcpServers, ok := config["mcp_servers"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected mcp_servers key in config")
	}

	if len(mcpServers) != 2 {
		t.Errorf("Expected 2 MCP servers, got %d", len(mcpServers))
	}

	githubServer, ok := mcpServers["github"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected github server in config")
	}

	expectedURL := ts.URL + "/mcp/github"
	if githubServer["url"] != expectedURL {
		t.Errorf("Expected URL %s, got %s", expectedURL, githubServer["url"])
	}

	// Verify shell_environment_policy
	policy, ok := config["shell_environment_policy"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected shell_environment_policy to exist")
	}

	if policy["ignore_default_excludes"] != true {
		t.Errorf("Expected ignore_default_excludes to be true")
	}
}

func TestCodexProxiedMCPProcessor_EmptyServers(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	processor := NewCodexProxiedMCPProcessor(ts.URL)

	if err := processor.ProcessMCPConfigs(tempDir); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// config.toml should still be created with shell_environment_policy
	content, err := os.ReadFile(filepath.Join(tempDir, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("Expected config.toml to exist, got error: %v", err)
	}

	configStr := string(content)
	if !strings.Contains(configStr, "ignore_default_excludes") {
		t.Error("Expected config.toml to contain shell_environment_policy even with no servers")
	}
}

// Test NoOpMCPProcessor

func TestNoOpMCPProcessor(t *testing.T) {
	processor := NewNoOpMCPProcessor()
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatalf("Expected no error from NoOp processor, got: %v", err)
	}
}

// Test FetchMCPProxyServers

func TestFetchMCPProxyServers_Success(t *testing.T) {
	servers := []MCPProxyServerInfo{
		{Name: "github", URL: "/mcp/github"},
		{Name: "postgres", URL: "/mcp/postgres"},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/servers" {
			t.Errorf("Expected path /servers, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(servers)
	}))
	defer ts.Close()

	result, err := FetchMCPProxyServers(ts.URL)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("Expected 2 servers, got %d", len(result))
	}

	if result[0].Name != "github" || result[0].URL != "/mcp/github" {
		t.Errorf("Unexpected first server: %+v", result[0])
	}

	if result[1].Name != "postgres" || result[1].URL != "/mcp/postgres" {
		t.Errorf("Unexpected second server: %+v", result[1])
	}
}

func TestFetchMCPProxyServers_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer ts.Close()

	_, err := FetchMCPProxyServers(ts.URL)
	if err == nil {
		t.Fatal("Expected error for server error response")
	}
}

func TestFetchMCPProxyServers_EmptyList(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer ts.Close()

	result, err := FetchMCPProxyServers(ts.URL)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("Expected 0 servers, got %d", len(result))
	}
}

// Test ClaudeCodeProxiedMCPProcessor

func TestClaudeCodeProxiedMCPProcessor_Success(t *testing.T) {
	servers := []MCPProxyServerInfo{
		{Name: "github", URL: "/mcp/github"},
		{Name: "postgres", URL: "/mcp/postgres"},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(servers)
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	processor := NewClaudeCodeProxiedMCPProcessor(ts.URL)

	if err := processor.ProcessMCPConfigs(tempDir); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Read and verify .claude.json
	content, err := os.ReadFile(filepath.Join(tempDir, ".claude.json"))
	if err != nil {
		t.Fatalf("Failed to read .claude.json: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse .claude.json: %v", err)
	}

	mcpServers, ok := config["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected mcpServers key in config")
	}

	if len(mcpServers) != 2 {
		t.Errorf("Expected 2 MCP servers, got %d", len(mcpServers))
	}

	githubServer, ok := mcpServers["github"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected github server in config")
	}

	expectedURL := ts.URL + "/mcp/github"
	if githubServer["url"] != expectedURL {
		t.Errorf("Expected URL %s, got %s", expectedURL, githubServer["url"])
	}
}

func TestClaudeCodeProxiedMCPProcessor_EmptyServers(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	processor := NewClaudeCodeProxiedMCPProcessor(ts.URL)

	if err := processor.ProcessMCPConfigs(tempDir); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// .claude.json should not be created when there are no servers
	if _, err := os.Stat(filepath.Join(tempDir, ".claude.json")); !os.IsNotExist(err) {
		t.Error("Expected .claude.json to not exist when no servers available")
	}
}

// Test OpenCodeProxiedMCPProcessor

func TestOpenCodeProxiedMCPProcessor_Success(t *testing.T) {
	servers := []MCPProxyServerInfo{
		{Name: "github", URL: "/mcp/github"},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(servers)
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	processor := NewOpenCodeProxiedMCPProcessor(ts.URL)

	if err := processor.ProcessMCPConfigs(tempDir); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Read and verify opencode.json
	content, err := os.ReadFile(filepath.Join(tempDir, ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatalf("Failed to read opencode.json: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("Failed to parse opencode.json: %v", err)
	}

	mcpSection, ok := config["mcp"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected mcp key in config")
	}

	githubServer, ok := mcpSection["github"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected github server in mcp config")
	}

	if githubServer["type"] != "remote" {
		t.Errorf("Expected type 'remote', got %s", githubServer["type"])
	}

	expectedURL := ts.URL + "/mcp/github"
	if githubServer["url"] != expectedURL {
		t.Errorf("Expected URL %s, got %s", expectedURL, githubServer["url"])
	}

	if githubServer["enabled"] != true {
		t.Errorf("Expected enabled to be true")
	}
}

func TestOpenCodeProxiedMCPProcessor_EmptyServers(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	processor := NewOpenCodeProxiedMCPProcessor(ts.URL)

	if err := processor.ProcessMCPConfigs(tempDir); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// opencode.json should not be created when there are no servers
	if _, err := os.Stat(filepath.Join(tempDir, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Error("Expected opencode.json to not exist when no servers available")
	}
}

