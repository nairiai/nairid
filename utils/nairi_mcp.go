package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"nairid/core/log"
)

const (
	// NairiMCPServerName is the agent CLI's entry for the Nairi agent MCP gateway.
	NairiMCPServerName = "nairi"
	// LocalMCPsHeader lists the remote MCP servers a self-hosted agent kept in its own config,
	// so the gateway does not serve them a second time.
	LocalMCPsHeader = "X-Nairi-Local-MCPs"
	// codexNairiStartupTimeoutSec replaces Codex's 10s default, which also covers the first
	// tools/list: the gateway may take up to 10s to initialize upstreams and 10s to list them.
	codexNairiStartupTimeoutSec = 30
)

// NairiMCP is the Nairi agent MCP gateway entry nairid adds to the agent CLI's MCP config. The
// gateway serves the agent's remote and Composio MCP servers.
type NairiMCP struct {
	URL     string
	Headers map[string]string
}

// LocalServerID names a remote MCP server by its URL without sending the URL, which can hold
// a token. Must match the backend's agentmcpkeys.LocalServerID.
func LocalServerID(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:8])
}

// withNairiMCP adds the gateway entry to the merged servers. A customer server already named
// "nairi" keeps working under a suffixed name, like any other duplicate.
func withNairiMCP(servers map[string]interface{}, nairi *NairiMCP) map[string]interface{} {
	if nairi == nil {
		return servers
	}

	headers := make(map[string]interface{}, len(nairi.Headers)+1)
	for k, v := range nairi.Headers {
		headers[k] = v
	}
	if ids := localServerIDs(servers); ids != "" {
		headers[LocalMCPsHeader] = ids
	}

	if existing, ok := servers[NairiMCPServerName]; ok {
		servers[uniqueServerName(servers, NairiMCPServerName)] = existing
	}
	servers[NairiMCPServerName] = map[string]interface{}{
		"type":    "http",
		"url":     nairi.URL,
		"headers": headers,
	}
	return servers
}

// agentMCPConfigFiles is each agent CLI's config under its home and the key of its MCP servers.
var agentMCPConfigFiles = map[string]struct{ path, serversKey string }{
	"claude":   {".claude.json", "mcpServers"},
	"opencode": {filepath.Join(".config", "opencode", "opencode.json"), "mcp"},
	"codex":    {filepath.Join(".codex", "config.toml"), "mcp_servers"},
}

// HasNairiMCPEntry reports whether the agent CLI's config under homeDir holds the gateway entry.
// An agent CLI without an MCP config reports true.
func HasNairiMCPEntry(agentType, homeDir string) (bool, error) {
	file, ok := agentMCPConfigFiles[agentType]
	if !ok {
		return true, nil
	}
	if homeDir == "" {
		var err error
		if homeDir, err = os.UserHomeDir(); err != nil {
			return false, fmt.Errorf("failed to get home directory: %w", err)
		}
	}
	content, err := readFileAsTargetUser(filepath.Join(homeDir, file.path))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var config map[string]interface{}
	unmarshal := json.Unmarshal
	if agentType == "codex" {
		unmarshal = toml.Unmarshal
	}
	if err := unmarshal(content, &config); err != nil {
		return false, fmt.Errorf("failed to parse %s: %w", file.path, err)
	}
	servers, _ := config[file.serversKey].(map[string]interface{})
	_, ok = servers[NairiMCPServerName]
	return ok, nil
}

// agentMCPServers merges the downloaded MCP configs, drops the remote servers with stdioOnly,
// then adds the gateway entry if there is one.
func agentMCPServers(nairi *NairiMCP, stdioOnly bool) (map[string]interface{}, error) {
	servers, err := MergeMCPConfigs()
	if err != nil {
		return nil, fmt.Errorf("failed to merge MCP configs: %w", err)
	}
	if stdioOnly {
		servers = withoutRemoteServers(servers)
	}
	return withNairiMCP(servers, nairi), nil
}

// withoutRemoteServers drops the remote servers. A backend from before the gateway ignores
// nairid's version and still sends Composio's mcp-remote wrapper with the platform key.
func withoutRemoteServers(servers map[string]interface{}) map[string]interface{} {
	for name, serverConfig := range servers {
		configMap, _ := serverConfig.(map[string]interface{})
		if !isRemoteServer(configMap) {
			continue
		}
		log.Warn("🔌 Dropping remote MCP server %q: managed agents reach remote servers only through the Nairi agent MCP gateway", name)
		delete(servers, name)
	}
	return servers
}

// isMCPConfigPath reports whether path is in the directory MergeMCPConfigs reads.
func isMCPConfigPath(path string) bool {
	mcpDir, err := GetEksecdMCPDir()
	return err == nil && filepath.Dir(filepath.Clean(path)) == mcpDir
}

// stdioOnlyMCPConfig drops the remote servers from one downloaded MCP config file.
func stdioOnlyMCPConfig(content []byte) ([]byte, error) {
	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		return nil, err
	}
	if servers, ok := config["mcpServers"].(map[string]interface{}); ok {
		config["mcpServers"] = withoutRemoteServers(servers)
	}
	return json.Marshal(config)
}

// isRemoteServer reports whether a server reaches a remote one a managed agent must not run: a
// url entry, a non-stdio type, or Composio's mcp-remote wrapper. A customer's own wrapper stays.
func isRemoteServer(configMap map[string]interface{}) bool {
	if _, hasURL := configMap["url"]; hasURL {
		return true
	}
	if serverType, _ := configMap["type"].(string); serverType != "" && serverType != "stdio" {
		return true
	}
	return isComposioWrapper(configMap)
}

// isComposioWrapper reports whether a server runs mcp-remote against composio.dev or with a
// COMPOSIO_API_KEY. nairid can't see the backend's Composio flag, so doubt counts as Composio.
func isComposioWrapper(configMap map[string]interface{}) bool {
	command, _ := configMap["command"].(string)
	words := []string{command}
	args, _ := configMap["args"].([]interface{})
	for _, arg := range args {
		s, _ := arg.(string)
		words = append(words, s)
	}
	at := slices.IndexFunc(words, isMCPRemote)
	if at < 0 {
		return false
	}
	// mcp-remote connects to its first argument.
	if at+1 == len(words) || mayBeComposioURL(words[at+1]) {
		return true
	}
	env, _ := configMap["env"].(map[string]interface{})
	for name, value := range env {
		s, _ := value.(string)
		words = append(words, name, s)
	}
	return slices.ContainsFunc(words, func(word string) bool {
		return strings.Contains(strings.ToUpper(word), "COMPOSIO_API_KEY")
	})
}

// mayBeComposioURL reports whether raw is on composio.dev, a subdomain or a lookalike such as
// composio.dev.evil, or too broken to tell.
func mayBeComposioURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return true
	}
	return strings.Contains("."+strings.ToLower(parsed.Hostname())+".", ".composio.dev.")
}

// isMCPRemote matches mcp-remote run directly, by path or as an npx package with a version.
func isMCPRemote(s string) bool {
	name := path.Base(s)
	return name == "mcp-remote" || strings.HasPrefix(name, "mcp-remote@")
}

// localServerIDs lists the remote servers the backend left in the downloaded config: the ones
// the gateway can't reach, such as a self-hosted agent's private-network servers.
func localServerIDs(servers map[string]interface{}) string {
	var ids []string
	for _, serverConfig := range servers {
		configMap, ok := serverConfig.(map[string]interface{})
		if !ok {
			continue
		}
		if url, ok := configMap["url"].(string); ok && url != "" {
			ids = append(ids, LocalServerID(url))
		}
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}
