package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

const (
	// NairiMCPServerName is the agent CLI's entry for the Nairi agent MCP gateway.
	NairiMCPServerName = "nairi"
	// LocalMCPsHeader lists the remote MCP servers a self-hosted agent kept in its own config,
	// so the gateway does not serve them a second time.
	LocalMCPsHeader = "X-Nairi-Local-MCPs"
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
