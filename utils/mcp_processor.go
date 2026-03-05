package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"nairid/core/log"

	toml "github.com/pelletier/go-toml/v2"
)

// writeFileAsTargetUser writes content to a file, using sudo if necessary.
// When AGENT_EXEC_USER is set and the target path is in that user's home directory,
// the file is written via 'sudo -u <user> tee' to ensure proper ownership and permissions.
// This solves permission issues where nairid (running as 'nairid' user) needs to write
// files to the agent user's home directory (e.g., /home/agentrunner/.claude.json).
func writeFileAsTargetUser(filePath string, content []byte, perm os.FileMode) error {
	execUser := os.Getenv("AGENT_EXEC_USER")
	if execUser == "" {
		// Self-hosted mode: write directly
		return os.WriteFile(filePath, content, perm)
	}

	// Check if the target path is in the agent user's home directory
	agentHome := "/home/" + execUser
	if !strings.HasPrefix(filePath, agentHome) {
		// Not in agent's home, write directly
		return os.WriteFile(filePath, content, perm)
	}

	log.Info("🔌 Writing file as user '%s': %s", execUser, filePath)

	// Use sudo -u <user> tee to write the file with correct ownership
	// The tee command writes stdin to the file, and we redirect stdout to /dev/null
	cmd := exec.Command("sudo", "-u", execUser, "tee", filePath)
	cmd.Stdin = bytes.NewReader(content)
	cmd.Stdout = nil // Discard tee's stdout (it echoes the input)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to write file as user %s: %w (stderr: %s)", execUser, err, stderr.String())
	}

	return nil
}

// mkdirAllAsTargetUser creates a directory (and all parent directories), using sudo if necessary.
// When AGENT_EXEC_USER is set and the target path is in that user's home directory,
// the directory is created via 'sudo -u <user> mkdir -p' to ensure proper ownership.
// This solves permission issues where nairid (running as 'nairid' user) needs to create
// directories in the agent user's home directory (e.g., /home/agentrunner/.config/opencode).
// The directory is created with mode 0775 to allow group write access.
func mkdirAllAsTargetUser(dirPath string) error {
	execUser := os.Getenv("AGENT_EXEC_USER")
	if execUser == "" {
		// Self-hosted mode: create directly with 0755
		return os.MkdirAll(dirPath, 0755)
	}

	// Check if the target path is in the agent user's home directory
	agentHome := "/home/" + execUser
	if !strings.HasPrefix(dirPath, agentHome) {
		// Not in agent's home, create directly
		return os.MkdirAll(dirPath, 0755)
	}

	log.Info("📁 Creating directory as user '%s': %s", execUser, dirPath)

	// Use sudo -u <user> mkdir -p to create the directory with correct ownership
	// Use umask 002 to ensure directories are created with 775 permissions (group-writable)
	// This allows both the agent user (owner) and nairid (group member) to write to it
	cmd := exec.Command("sudo", "-u", execUser, "bash", "-c", fmt.Sprintf("umask 002 && mkdir -p '%s'", dirPath))

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create directory as user %s: %w (stderr: %s)", execUser, err, stderr.String())
	}

	return nil
}

// MCPProcessor defines the interface for processing agent-specific MCP configurations
type MCPProcessor interface {
	// ProcessMCPConfigs processes MCP configs from the eksecd MCP directory
	// and applies them to the agent-specific location.
	// targetHomeDir specifies the home directory to deploy configs to.
	// If empty, uses the current user's home directory.
	ProcessMCPConfigs(targetHomeDir string) error
}

// GetEksecdMCPDir returns the path to the eksecd MCP directory
func GetEksecdMCPDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(homeDir, ".config", "eksecd", "mcp"), nil
}

// GetMCPConfigFiles returns a list of JSON files in the eksecd MCP directory
func GetMCPConfigFiles() ([]string, error) {
	mcpDir, err := GetEksecdMCPDir()
	if err != nil {
		return nil, err
	}

	// Check if MCP directory exists
	if _, err := os.Stat(mcpDir); os.IsNotExist(err) {
		log.Info("🔌 MCP directory does not exist: %s", mcpDir)
		return []string{}, nil
	}

	// Read directory
	entries, err := os.ReadDir(mcpDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read MCP directory: %w", err)
	}

	// Filter JSON files
	var mcpFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			mcpFiles = append(mcpFiles, filepath.Join(mcpDir, entry.Name()))
		}
	}

	return mcpFiles, nil
}

// CleanEksecdMCPDir removes all files from the eksecd MCP directory
// This should be called before downloading new MCP configs from the server to ensure
// stale configs that were deleted on the server are also removed locally.
func CleanEksecdMCPDir() error {
	mcpDir, err := GetEksecdMCPDir()
	if err != nil {
		return err
	}

	// Check if MCP directory exists
	if _, err := os.Stat(mcpDir); os.IsNotExist(err) {
		log.Info("🔌 MCP directory does not exist, nothing to clean: %s", mcpDir)
		return nil
	}

	log.Info("🔌 Cleaning eksecd MCP directory: %s", mcpDir)

	// Remove and recreate the directory to ensure a clean state
	if err := os.RemoveAll(mcpDir); err != nil {
		return fmt.Errorf("failed to remove MCP directory: %w", err)
	}

	// Recreate empty directory
	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		return fmt.Errorf("failed to recreate MCP directory: %w", err)
	}

	log.Info("✅ Successfully cleaned eksecd MCP directory")
	return nil
}

// MergeMCPConfigs reads all MCP JSON files and merges them into a single mcpServers object
// Returns a map[string]interface{} representing the merged MCP server configurations
// Each file is expected to have a top-level "mcpServers" key containing server configurations.
// Duplicate server names across files are handled by adding numeric suffixes (e.g., "server-1", "server-2").
func MergeMCPConfigs() (map[string]interface{}, error) {
	mcpFiles, err := GetMCPConfigFiles()
	if err != nil {
		return nil, fmt.Errorf("failed to get MCP config files: %w", err)
	}

	if len(mcpFiles) == 0 {
		return map[string]interface{}{}, nil
	}

	log.Info("🔌 Merging %d MCP config file(s)", len(mcpFiles))

	mergedServers := make(map[string]interface{})

	for _, mcpFile := range mcpFiles {
		// Read file
		content, err := os.ReadFile(mcpFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read MCP config file %s: %w", mcpFile, err)
		}

		// Parse JSON - expect top-level mcpServers key
		var fileConfig struct {
			MCPServers map[string]interface{} `json:"mcpServers"`
		}
		if err := json.Unmarshal(content, &fileConfig); err != nil {
			return nil, fmt.Errorf("failed to parse MCP config file %s: %w", mcpFile, err)
		}

		// Merge servers from this file into the main map
		for serverName, serverConfig := range fileConfig.MCPServers {
			// Handle duplicate server names by adding numeric suffix
			finalName := serverName
			suffix := 1
			for {
				if _, exists := mergedServers[finalName]; !exists {
					break
				}
				suffix++
				finalName = fmt.Sprintf("%s-%d", serverName, suffix)
			}

			if finalName != serverName {
				log.Info("🔌 Duplicate server name '%s' detected, using '%s' instead", serverName, finalName)
			}

			mergedServers[finalName] = serverConfig
		}
	}

	return mergedServers, nil
}

// ClaudeCodeMCPProcessor handles MCP config processing for Claude Code
type ClaudeCodeMCPProcessor struct{}

// NewClaudeCodeMCPProcessor creates a new Claude Code MCP processor
func NewClaudeCodeMCPProcessor(workDir string) *ClaudeCodeMCPProcessor {
	return &ClaudeCodeMCPProcessor{}
}

// ProcessMCPConfigs implements MCPProcessor for Claude Code
// It reads all MCP configs, merges them, and updates ~/.claude.json
// targetHomeDir specifies the home directory to deploy configs to.
// If empty, uses the current user's home directory.
func (p *ClaudeCodeMCPProcessor) ProcessMCPConfigs(targetHomeDir string) error {
	log.Info("🔌 Processing MCP configs for Claude Code agent")

	// Get merged MCP server configs
	mcpServers, err := MergeMCPConfigs()
	if err != nil {
		return fmt.Errorf("failed to merge MCP configs: %w", err)
	}

	if len(mcpServers) == 0 {
		log.Info("🔌 No MCP configs found in eksecd MCP directory")
		return nil
	}

	log.Info("🔌 Found %d MCP server(s) to configure", len(mcpServers))

	// Determine home directory for Claude Code config
	homeDir := targetHomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
	}

	log.Info("🔌 Deploying MCP configs to home directory: %s", homeDir)

	claudeConfigPath := filepath.Join(homeDir, ".claude.json")

	// Read existing config if it exists
	var existingConfig map[string]interface{}
	if content, err := readFileAsTargetUser(claudeConfigPath); err == nil {
		if err := json.Unmarshal(content, &existingConfig); err != nil {
			log.Info("⚠️  Failed to parse existing .claude.json, creating new config: %v", err)
			existingConfig = make(map[string]interface{})
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to read existing .claude.json: %w", err)
	} else {
		existingConfig = make(map[string]interface{})
	}

	// Update mcpServers key with merged configs
	existingConfig["mcpServers"] = mcpServers

	// Write updated config back
	configJSON, err := json.MarshalIndent(existingConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal .claude.json: %w", err)
	}

	log.Info("🔌 Updating .claude.json at: %s", claudeConfigPath)

	if err := writeFileAsTargetUser(claudeConfigPath, configJSON, 0644); err != nil {
		return fmt.Errorf("failed to write .claude.json: %w", err)
	}

	log.Info("✅ Successfully configured %d MCP server(s) for Claude Code", len(mcpServers))
	return nil
}

// OpenCodeMCPProcessor handles MCP config processing for OpenCode
type OpenCodeMCPProcessor struct {
	workDir string
}

// NewOpenCodeMCPProcessor creates a new OpenCode MCP processor
func NewOpenCodeMCPProcessor(workDir string) *OpenCodeMCPProcessor {
	return &OpenCodeMCPProcessor{
		workDir: workDir,
	}
}

// ProcessMCPConfigs implements MCPProcessor for OpenCode
// It reads all MCP configs, merges them, transforms them to OpenCode format,
// and updates ~/.config/opencode/opencode.json
// targetHomeDir specifies the home directory to deploy configs to.
// If empty, uses the current user's home directory.
func (p *OpenCodeMCPProcessor) ProcessMCPConfigs(targetHomeDir string) error {
	log.Info("🔌 Processing MCP configs for OpenCode agent")

	// Get merged MCP server configs
	mcpServers, err := MergeMCPConfigs()
	if err != nil {
		return fmt.Errorf("failed to merge MCP configs: %w", err)
	}

	if len(mcpServers) == 0 {
		log.Info("🔌 No MCP configs found in eksecd MCP directory")
		return nil
	}

	log.Info("🔌 Found %d MCP server(s) to configure", len(mcpServers))

	// Transform Claude Code MCP format to OpenCode format
	opencodeMcpServers := make(map[string]interface{})
	for serverName, serverConfig := range mcpServers {
		configMap, ok := serverConfig.(map[string]interface{})
		if !ok {
			log.Info("⚠️  Skipping invalid MCP server config for %s", serverName)
			continue
		}

		opencodeConfig := make(map[string]interface{})

		// Check if this is a remote server (has "url" field)
		if url, hasURL := configMap["url"]; hasURL {
			opencodeConfig["type"] = "remote"
			opencodeConfig["url"] = url
			if headers, ok := configMap["headers"]; ok {
				opencodeConfig["headers"] = headers
			}
		} else {
			// Local server - transform command + args to command array
			opencodeConfig["type"] = "local"

			var commandArray []string

			// Get the command
			if cmd, ok := configMap["command"].(string); ok {
				commandArray = append(commandArray, cmd)
			}

			// Append args to command array
			if args, ok := configMap["args"].([]interface{}); ok {
				for _, arg := range args {
					if argStr, ok := arg.(string); ok {
						commandArray = append(commandArray, argStr)
					}
				}
			}

			opencodeConfig["command"] = commandArray

			// Transform env -> environment
			if env, ok := configMap["env"]; ok {
				opencodeConfig["environment"] = env
			}
		}

		// Always enable the server
		opencodeConfig["enabled"] = true

		opencodeMcpServers[serverName] = opencodeConfig
	}

	// Determine home directory for OpenCode config
	homeDir := targetHomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
	}

	log.Info("🔌 Deploying OpenCode MCP configs to home directory: %s", homeDir)

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

	// Update mcp key with transformed configs
	existingConfig["mcp"] = opencodeMcpServers

	// Write updated config back
	configJSON, err := json.MarshalIndent(existingConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal opencode.json: %w", err)
	}

	log.Info("🔌 Updating opencode.json at: %s", opencodeConfigPath)

	if err := writeFileAsTargetUser(opencodeConfigPath, configJSON, 0644); err != nil {
		return fmt.Errorf("failed to write opencode.json: %w", err)
	}

	log.Info("✅ Successfully configured %d MCP server(s) for OpenCode", len(opencodeMcpServers))
	return nil
}

// CodexMCPProcessor handles MCP config processing for Codex
type CodexMCPProcessor struct{}

// NewCodexMCPProcessor creates a new Codex MCP processor
func NewCodexMCPProcessor() *CodexMCPProcessor {
	return &CodexMCPProcessor{}
}

// ProcessMCPConfigs implements MCPProcessor for Codex
// It reads all MCP configs, merges them, transforms them to Codex TOML format,
// and updates ~/.codex/config.toml. Always sets shell_environment_policy to
// not filter env vars regardless of name patterns.
// targetHomeDir specifies the home directory to deploy configs to.
// If empty, uses the current user's home directory.
func (p *CodexMCPProcessor) ProcessMCPConfigs(targetHomeDir string) error {
	log.Info("🔌 Processing MCP configs for Codex agent")

	// Get merged MCP server configs
	mcpServers, err := MergeMCPConfigs()
	if err != nil {
		return fmt.Errorf("failed to merge MCP configs: %w", err)
	}

	// Determine home directory for Codex config
	homeDir := targetHomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
	}

	log.Info("🔌 Deploying Codex MCP configs to home directory: %s", homeDir)

	codexConfigDir := filepath.Join(homeDir, ".codex")
	codexConfigPath := filepath.Join(codexConfigDir, "config.toml")

	if err := mkdirAllAsTargetUser(codexConfigDir); err != nil {
		return fmt.Errorf("failed to create Codex config directory: %w", err)
	}

	// Read existing config if it exists (read-modify-write)
	existingConfig := make(map[string]interface{})
	if content, err := readFileAsTargetUser(codexConfigPath); err == nil {
		if err := toml.Unmarshal(content, &existingConfig); err != nil {
			log.Info("⚠️  Failed to parse existing config.toml, creating new config: %v", err)
			existingConfig = make(map[string]interface{})
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to read existing config.toml: %w", err)
	}

	// Always set shell_environment_policy to not filter env vars
	existingConfig["shell_environment_policy"] = map[string]interface{}{
		"inherit":                 "core",
		"ignore_default_excludes": true,
	}

	if len(mcpServers) == 0 {
		log.Info("🔌 No MCP configs found, writing config with shell_environment_policy only")
	} else {
		log.Info("🔌 Found %d MCP server(s) to configure", len(mcpServers))

		codexMCPServers := make(map[string]interface{})
		for serverName, serverConfig := range mcpServers {
			configMap, ok := serverConfig.(map[string]interface{})
			if !ok {
				log.Info("⚠️  Skipping invalid MCP server config for %s", serverName)
				continue
			}

			codexServerConfig := make(map[string]interface{})

			// Check if this is a remote server (has "url" field)
			if url, hasURL := configMap["url"]; hasURL {
				codexServerConfig["url"] = url
				if headers, ok := configMap["headers"]; ok {
					codexServerConfig["http_headers"] = headers
				}
			} else {
				// Local server - keep command/args/env format
				if cmd, ok := configMap["command"].(string); ok {
					codexServerConfig["command"] = cmd
				}

				if args, ok := configMap["args"].([]interface{}); ok {
					strArgs := make([]string, 0, len(args))
					for _, arg := range args {
						if argStr, ok := arg.(string); ok {
							strArgs = append(strArgs, argStr)
						}
					}
					codexServerConfig["args"] = strArgs
				}

				if env, ok := configMap["env"].(map[string]interface{}); ok {
					strEnv := make(map[string]string)
					for k, v := range env {
						strEnv[k] = fmt.Sprintf("%v", v)
					}
					codexServerConfig["env"] = strEnv
				}
			}

			codexServerConfig["enabled"] = true
			codexMCPServers[serverName] = codexServerConfig
		}

		existingConfig["mcp_servers"] = codexMCPServers
	}

	configTOML, err := toml.Marshal(existingConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal config.toml: %w", err)
	}

	log.Info("🔌 Updating config.toml at: %s", codexConfigPath)

	if err := writeFileAsTargetUser(codexConfigPath, configTOML, 0644); err != nil {
		return fmt.Errorf("failed to write config.toml: %w", err)
	}

	log.Info("✅ Successfully configured %d MCP server(s) for Codex", len(mcpServers))
	return nil
}

// CodexProxiedMCPProcessor handles MCP config processing for Codex
// when using an MCP proxy. Writes HTTP-type configs pointing to the MCP proxy.
type CodexProxiedMCPProcessor struct {
	mcpProxyURL string
}

// NewCodexProxiedMCPProcessor creates a new proxied Codex MCP processor
func NewCodexProxiedMCPProcessor(mcpProxyURL string) *CodexProxiedMCPProcessor {
	return &CodexProxiedMCPProcessor{mcpProxyURL: mcpProxyURL}
}

// ProcessMCPConfigs fetches server list from MCP proxy and writes URL-based configs to config.toml
func (p *CodexProxiedMCPProcessor) ProcessMCPConfigs(targetHomeDir string) error {
	log.Info("🔌 Processing MCP configs via MCP proxy for Codex agent")

	servers, err := FetchMCPProxyServers(p.mcpProxyURL)
	if err != nil {
		return fmt.Errorf("failed to fetch MCP proxy servers: %w", err)
	}

	// Determine home directory
	homeDir := targetHomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
	}

	codexConfigDir := filepath.Join(homeDir, ".codex")
	codexConfigPath := filepath.Join(codexConfigDir, "config.toml")

	if err := mkdirAllAsTargetUser(codexConfigDir); err != nil {
		return fmt.Errorf("failed to create Codex config directory: %w", err)
	}

	// Read existing config if it exists (read-modify-write)
	existingConfig := make(map[string]interface{})
	if content, err := readFileAsTargetUser(codexConfigPath); err == nil {
		if err := toml.Unmarshal(content, &existingConfig); err != nil {
			log.Info("⚠️  Failed to parse existing config.toml, creating new config: %v", err)
			existingConfig = make(map[string]interface{})
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to read existing config.toml: %w", err)
	}

	// Always set shell_environment_policy to not filter env vars
	existingConfig["shell_environment_policy"] = map[string]interface{}{
		"inherit":                 "core",
		"ignore_default_excludes": true,
	}

	if len(servers) == 0 {
		log.Info("🔌 No MCP servers available from proxy, writing config with shell_environment_policy only")
	} else {
		log.Info("🔌 Found %d MCP server(s) from proxy", len(servers))

		codexMCPServers := make(map[string]interface{})
		for _, server := range servers {
			codexMCPServers[server.Name] = map[string]interface{}{
				"url":     p.mcpProxyURL + server.URL,
				"enabled": true,
			}
		}

		existingConfig["mcp_servers"] = codexMCPServers
	}

	configTOML, err := toml.Marshal(existingConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal config.toml: %w", err)
	}

	if err := writeFileAsTargetUser(codexConfigPath, configTOML, 0644); err != nil {
		return fmt.Errorf("failed to write config.toml: %w", err)
	}

	log.Info("✅ Successfully configured %d proxied MCP server(s) for Codex", len(servers))
	return nil
}

// NoOpMCPProcessor is a no-op implementation for agents that don't support MCP configs
type NoOpMCPProcessor struct{}

// NewNoOpMCPProcessor creates a new no-op MCP processor
func NewNoOpMCPProcessor() *NoOpMCPProcessor {
	return &NoOpMCPProcessor{}
}

// ProcessMCPConfigs implements MCPProcessor with no operation
func (p *NoOpMCPProcessor) ProcessMCPConfigs(targetHomeDir string) error {
	log.Info("🔌 MCP config processing not supported for this agent type")
	return nil
}

// MCPProxyServerInfo represents a server exposed by the MCP proxy
type MCPProxyServerInfo struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// FetchMCPProxyServers fetches the list of available MCP servers from the proxy's /servers endpoint
func FetchMCPProxyServers(mcpProxyURL string) ([]MCPProxyServerInfo, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Get(mcpProxyURL + "/servers")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch MCP proxy servers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("MCP proxy returned status %d: %s", resp.StatusCode, string(body))
	}

	var servers []MCPProxyServerInfo
	if err := json.NewDecoder(resp.Body).Decode(&servers); err != nil {
		return nil, fmt.Errorf("failed to decode MCP proxy servers response: %w", err)
	}

	return servers, nil
}

// ClaudeCodeProxiedMCPProcessor handles MCP config processing for Claude Code
// when using an MCP proxy. Instead of writing local stdio configs, it writes
// URL-based configs pointing to the MCP proxy.
type ClaudeCodeProxiedMCPProcessor struct {
	mcpProxyURL string
}

// NewClaudeCodeProxiedMCPProcessor creates a new proxied Claude Code MCP processor
func NewClaudeCodeProxiedMCPProcessor(mcpProxyURL string) *ClaudeCodeProxiedMCPProcessor {
	return &ClaudeCodeProxiedMCPProcessor{mcpProxyURL: mcpProxyURL}
}

// ProcessMCPConfigs fetches server list from MCP proxy and writes URL-based configs to .claude.json
func (p *ClaudeCodeProxiedMCPProcessor) ProcessMCPConfigs(targetHomeDir string) error {
	log.Info("🔌 Processing MCP configs via MCP proxy for Claude Code agent")

	servers, err := FetchMCPProxyServers(p.mcpProxyURL)
	if err != nil {
		return fmt.Errorf("failed to fetch MCP proxy servers: %w", err)
	}

	if len(servers) == 0 {
		log.Info("🔌 No MCP servers available from proxy")
		return nil
	}

	log.Info("🔌 Found %d MCP server(s) from proxy", len(servers))

	mcpServers := make(map[string]interface{})
	for _, server := range servers {
		mcpServers[server.Name] = map[string]interface{}{
			"type": "http",
			"url":  p.mcpProxyURL + server.URL,
		}
	}

	homeDir := targetHomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
	}

	claudeConfigPath := filepath.Join(homeDir, ".claude.json")

	var existingConfig map[string]interface{}
	if content, err := readFileAsTargetUser(claudeConfigPath); err == nil {
		if err := json.Unmarshal(content, &existingConfig); err != nil {
			log.Info("⚠️  Failed to parse existing .claude.json, creating new config: %v", err)
			existingConfig = make(map[string]interface{})
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to read existing .claude.json: %w", err)
	} else {
		existingConfig = make(map[string]interface{})
	}

	existingConfig["mcpServers"] = mcpServers

	configJSON, err := json.MarshalIndent(existingConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal .claude.json: %w", err)
	}

	if err := writeFileAsTargetUser(claudeConfigPath, configJSON, 0644); err != nil {
		return fmt.Errorf("failed to write .claude.json: %w", err)
	}

	log.Info("✅ Successfully configured %d proxied MCP server(s) for Claude Code", len(mcpServers))
	return nil
}

// OpenCodeProxiedMCPProcessor handles MCP config processing for OpenCode
// when using an MCP proxy. Writes remote-type configs pointing to the MCP proxy.
type OpenCodeProxiedMCPProcessor struct {
	mcpProxyURL string
}

// NewOpenCodeProxiedMCPProcessor creates a new proxied OpenCode MCP processor
func NewOpenCodeProxiedMCPProcessor(mcpProxyURL string) *OpenCodeProxiedMCPProcessor {
	return &OpenCodeProxiedMCPProcessor{mcpProxyURL: mcpProxyURL}
}

// ProcessMCPConfigs fetches server list from MCP proxy and writes remote-type configs to opencode.json
func (p *OpenCodeProxiedMCPProcessor) ProcessMCPConfigs(targetHomeDir string) error {
	log.Info("🔌 Processing MCP configs via MCP proxy for OpenCode agent")

	servers, err := FetchMCPProxyServers(p.mcpProxyURL)
	if err != nil {
		return fmt.Errorf("failed to fetch MCP proxy servers: %w", err)
	}

	if len(servers) == 0 {
		log.Info("🔌 No MCP servers available from proxy")
		return nil
	}

	log.Info("🔌 Found %d MCP server(s) from proxy", len(servers))

	opencodeMcpServers := make(map[string]interface{})
	for _, server := range servers {
		opencodeMcpServers[server.Name] = map[string]interface{}{
			"type":    "remote",
			"url":     p.mcpProxyURL + server.URL,
			"enabled": true,
		}
	}

	homeDir := targetHomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
	}

	opencodeConfigDir := filepath.Join(homeDir, ".config", "opencode")
	opencodeConfigPath := filepath.Join(opencodeConfigDir, "opencode.json")

	if err := mkdirAllAsTargetUser(opencodeConfigDir); err != nil {
		return fmt.Errorf("failed to create OpenCode config directory: %w", err)
	}

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

	existingConfig["mcp"] = opencodeMcpServers

	configJSON, err := json.MarshalIndent(existingConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal opencode.json: %w", err)
	}

	if err := writeFileAsTargetUser(opencodeConfigPath, configJSON, 0644); err != nil {
		return fmt.Errorf("failed to write opencode.json: %w", err)
	}

	log.Info("✅ Successfully configured %d proxied MCP server(s) for OpenCode", len(opencodeMcpServers))
	return nil
}

