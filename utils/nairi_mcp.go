package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

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

// agentMCPServers merges the downloaded MCP configs, then adds the gateway entry or, with
// stdioOnly, drops the remote servers.
func agentMCPServers(nairi *NairiMCP, stdioOnly bool) (map[string]interface{}, error) {
	servers, err := MergeMCPConfigs()
	if err != nil {
		return nil, fmt.Errorf("failed to merge MCP configs: %w", err)
	}
	if stdioOnly {
		return withoutRemoteServers(servers), nil
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
		log.Warn("🔌 Dropping remote MCP server %q: no Nairi agent MCP gateway to reach it through", name)
		delete(servers, name)
	}
	return servers
}

// isRemoteServer reports whether a server reaches a remote one: a url entry, a non-stdio type,
// or a stdio bridge through mcp-remote.
func isRemoteServer(configMap map[string]interface{}) bool {
	if _, hasURL := configMap["url"]; hasURL {
		return true
	}
	if serverType, _ := configMap["type"].(string); serverType != "" && serverType != "stdio" {
		return true
	}
	command, _ := configMap["command"].(string)
	if isMCPRemote(command) {
		return true
	}
	args, _ := configMap["args"].([]interface{})
	for _, arg := range args {
		if s, _ := arg.(string); isMCPRemote(s) {
			return true
		}
	}
	return false
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
