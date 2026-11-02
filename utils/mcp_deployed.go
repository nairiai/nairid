package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"nairid/core/log"
)

// The agent config files nairid has written MCP servers into. When the backend drops the last
// server, only these get their MCP section cleared, so MCP entries a user wrote stay untouched.
func mcpDeployedPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".config", "eksecd", "mcp-deployed"), nil
}

func readMCPDeployed() []string {
	path, err := mcpDeployedPath()
	if err != nil {
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	// One path per line: a home directory can contain spaces.
	var paths []string
	for _, line := range strings.Split(string(content), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths
}

func writeMCPDeployed(configPaths []string) error {
	path, err := mcpDeployedPath()
	if err != nil {
		return err
	}
	if len(configPaths) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove %s: %w", path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, []byte(strings.Join(configPaths, "\n")+"\n"), 0644)
}

func recordMCPDeployed(configPath string) error {
	deployed := readMCPDeployed()
	if slices.Contains(deployed, configPath) {
		return nil
	}
	return writeMCPDeployed(append(deployed, configPath))
}

func mcpWasDeployed(configPath string) bool {
	return slices.Contains(readMCPDeployed(), configPath)
}

func forgetMCPDeployed(configPath string) error {
	deployed := readMCPDeployed()
	return writeMCPDeployed(slices.DeleteFunc(deployed, func(p string) bool { return p == configPath }))
}

// clearDeployedJSONMCP removes the MCP section key from a JSON agent config nairid wrote it into.
func clearDeployedJSONMCP(configPath, key string) error {
	if !mcpWasDeployed(configPath) {
		return nil
	}
	content, err := readFileAsTargetUser(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return forgetMCPDeployed(configPath)
	}
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", configPath, err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		log.Info("⚠️  Not clearing MCP servers from unparseable %s: %v", configPath, err)
		return nil
	}
	delete(config, key)
	configJSON, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal %s: %w", configPath, err)
	}
	log.Info("🔌 Removing the MCP servers nairid deployed to %s", configPath)
	if err := replaceFileAsTargetUser(configPath, configJSON, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", configPath, err)
	}
	return forgetMCPDeployed(configPath)
}
