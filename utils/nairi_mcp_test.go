package utils

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func testNairiMCP() *NairiMCP {
	return &NairiMCP{
		URL:     "https://api.example.com/api/agents/mcp",
		Headers: map[string]string{"Authorization": "Bearer mcpk_test", "X-AGENT-ID": "ag_123"},
	}
}

// Same vector as the backend's agentmcpkeys test, so both sides name a server the same way.
func TestLocalServerID_MatchesBackendVector(t *testing.T) {
	if got := LocalServerID("https://mcp.example.com/mcp"); got != "7a930d27a3f5c464" {
		t.Errorf("LocalServerID = %q, want 7a930d27a3f5c464", got)
	}
}

func TestWithNairiMCP_NilKeepsServers(t *testing.T) {
	servers := map[string]interface{}{"fs": map[string]interface{}{"command": "npx"}}
	got := withNairiMCP(servers, nil)
	if len(got) != 1 || got["fs"] == nil {
		t.Errorf("expected servers unchanged, got %v", got)
	}
}

func TestWithNairiMCP_AddsGatewayEntry(t *testing.T) {
	servers := map[string]interface{}{
		"private": map[string]interface{}{"type": "http", "url": "https://mcp.example.com/mcp"},
		"fs":      map[string]interface{}{"command": "npx", "args": []interface{}{"server-fs"}},
	}
	nairi := testNairiMCP()

	got := withNairiMCP(servers, nairi)

	entry, ok := got[NairiMCPServerName].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a %q entry, got %v", NairiMCPServerName, got)
	}
	if entry["type"] != "http" || entry["url"] != nairi.URL {
		t.Errorf("unexpected entry %v", entry)
	}
	headers := entry["headers"].(map[string]interface{})
	if headers["Authorization"] != "Bearer mcpk_test" || headers["X-AGENT-ID"] != "ag_123" {
		t.Errorf("missing auth headers: %v", headers)
	}
	if headers[LocalMCPsHeader] != "7a930d27a3f5c464" {
		t.Errorf("%s = %v, want the private server's ID only", LocalMCPsHeader, headers[LocalMCPsHeader])
	}
	if _, leaked := nairi.Headers[LocalMCPsHeader]; leaked {
		t.Error("withNairiMCP must not modify the caller's headers")
	}
}

func TestWithNairiMCP_NoRemoteServersOmitsLocalHeader(t *testing.T) {
	got := withNairiMCP(map[string]interface{}{}, testNairiMCP())
	headers := got[NairiMCPServerName].(map[string]interface{})["headers"].(map[string]interface{})
	if _, ok := headers[LocalMCPsHeader]; ok {
		t.Errorf("expected no %s header, got %v", LocalMCPsHeader, headers)
	}
}

func TestWithNairiMCP_LocalIDsSorted(t *testing.T) {
	servers := map[string]interface{}{
		"a": map[string]interface{}{"url": "https://b.example.com/mcp"},
		"b": map[string]interface{}{"url": "https://a.example.com/mcp"},
	}
	got := withNairiMCP(servers, testNairiMCP())
	headers := got[NairiMCPServerName].(map[string]interface{})["headers"].(map[string]interface{})

	ids := []string{LocalServerID("https://b.example.com/mcp"), LocalServerID("https://a.example.com/mcp")}
	if ids[0] > ids[1] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	if headers[LocalMCPsHeader] != ids[0]+","+ids[1] {
		t.Errorf("%s = %v, want %s,%s", LocalMCPsHeader, headers[LocalMCPsHeader], ids[0], ids[1])
	}
}

func TestWithNairiMCP_CustomerServerNamedNairiKeepsWorking(t *testing.T) {
	customer := map[string]interface{}{"command": "nairi-tools"}
	servers := map[string]interface{}{NairiMCPServerName: customer}

	got := withNairiMCP(servers, testNairiMCP())

	moved, ok := got[NairiMCPServerName+"-2"].(map[string]interface{})
	if !ok || moved["command"] != "nairi-tools" {
		t.Fatalf("expected the customer's server under nairi-2, got %v", got)
	}
	if got[NairiMCPServerName].(map[string]interface{})["url"] != testNairiMCP().URL {
		t.Errorf("expected the gateway entry under %q", NairiMCPServerName)
	}
}

// writeRemoteMCPConfig sets HOME to a temp dir holding one downloaded config with a remote server.
func writeRemoteMCPConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	mcpDir := filepath.Join(home, ".config", "eksecd", "mcp")
	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"mcpServers":{"private":{"type":"http","url":"https://mcp.example.com/mcp"}}}`
	if err := os.WriteFile(filepath.Join(mcpDir, "private.json"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	return home
}

func assertGatewayHeaders(t *testing.T, headers map[string]interface{}) {
	t.Helper()
	if headers["Authorization"] != "Bearer mcpk_test" || headers[LocalMCPsHeader] != "7a930d27a3f5c464" {
		t.Errorf("unexpected gateway headers %v", headers)
	}
}

func TestClaudeCodeMCPProcessor_WritesNairiEntry(t *testing.T) {
	home := writeRemoteMCPConfig(t)

	if err := NewClaudeCodeMCPProcessor(home, testNairiMCP()).ProcessMCPConfigs(""); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]map[string]interface{} `json:"mcpServers"`
	}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	entry := config.MCPServers[NairiMCPServerName]
	if entry["type"] != "http" || entry["url"] != testNairiMCP().URL {
		t.Fatalf("unexpected nairi entry %v", entry)
	}
	assertGatewayHeaders(t, entry["headers"].(map[string]interface{}))
	if config.MCPServers["private"] == nil {
		t.Error("expected the local remote server to stay in the config")
	}
}

func TestCodexMCPProcessor_WritesNairiEntry(t *testing.T) {
	home := writeRemoteMCPConfig(t)

	if err := NewCodexMCPProcessor(testNairiMCP()).ProcessMCPConfigs(""); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]map[string]interface{} `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	entry := config.MCPServers[NairiMCPServerName]
	if entry["url"] != testNairiMCP().URL {
		t.Fatalf("unexpected nairi entry %v", entry)
	}
	assertGatewayHeaders(t, entry["http_headers"].(map[string]interface{}))
	if entry["startup_timeout_sec"] != int64(codexNairiStartupTimeoutSec) {
		t.Errorf("nairi startup_timeout_sec = %v, want %d", entry["startup_timeout_sec"], codexNairiStartupTimeoutSec)
	}
	if _, ok := config.MCPServers["private"]["startup_timeout_sec"]; ok {
		t.Error("only the nairi entry gets the longer startup timeout")
	}
}

func TestCodexMCPProcessor_RenamedCustomerNairiKeepsDefaultTimeout(t *testing.T) {
	home := writeRemoteMCPConfig(t)
	cfg := `{"mcpServers":{"nairi":{"command":"npx","args":["customer-nairi"]}}}`
	if err := os.WriteFile(filepath.Join(home, ".config", "eksecd", "mcp", "nairi.json"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	if err := NewCodexMCPProcessor(testNairiMCP()).ProcessMCPConfigs(""); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]map[string]interface{} `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	if config.MCPServers[NairiMCPServerName]["startup_timeout_sec"] != int64(codexNairiStartupTimeoutSec) {
		t.Errorf("gateway entry lost its startup timeout: %v", config.MCPServers[NairiMCPServerName])
	}
	moved, ok := config.MCPServers[NairiMCPServerName+"-2"]
	if !ok {
		t.Fatalf("expected the customer's server under nairi-2, got %v", config.MCPServers)
	}
	if _, ok := moved["startup_timeout_sec"]; ok {
		t.Error("the customer's renamed server keeps Codex's default startup timeout")
	}
}

func TestOpenCodeMCPProcessor_WritesNairiEntry(t *testing.T) {
	home := writeRemoteMCPConfig(t)

	if err := NewOpenCodeMCPProcessor(home, testNairiMCP()).ProcessMCPConfigs(""); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCP map[string]map[string]interface{} `json:"mcp"`
	}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	entry := config.MCP[NairiMCPServerName]
	if entry["type"] != "remote" || entry["url"] != testNairiMCP().URL || entry["enabled"] != true {
		t.Fatalf("unexpected nairi entry %v", entry)
	}
	assertGatewayHeaders(t, entry["headers"].(map[string]interface{}))
}
