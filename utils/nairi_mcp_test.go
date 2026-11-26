package utils

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"

	"nairid/clients"
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

// With the gateway on, Codex waits for the nairi tools before the first turn instead of starting
// without them. A grace the customer set stays.
func TestCodexMCPProcessor_SetsOptionalMCPStartupGrace(t *testing.T) {
	tests := []struct {
		name     string
		existing string // config.toml before the write, none when empty
		nairi    *NairiMCP
		want     interface{} // mcp_optional_startup_grace_ms after the write, nil when absent
	}{
		{name: "fresh config", nairi: testNairiMCP(), want: int64(10000)},
		{
			name:     "customer's value kept",
			existing: "mcp_optional_startup_grace_ms = 2500\n",
			nairi:    testNairiMCP(),
			want:     int64(2500),
		},
		{
			name:     "customer's zero kept",
			existing: "mcp_optional_startup_grace_ms = 0\n",
			nairi:    testNairiMCP(),
			want:     int64(0),
		},
		{name: "gateway off", nairi: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := writeRemoteMCPConfig(t)
			writeCodexConfig(t, home, tt.existing)

			if err := NewCodexMCPProcessor(tt.nairi).ProcessMCPConfigs(""); err != nil {
				t.Fatal(err)
			}

			grace, ok := readCodexConfig(t, home)["mcp_optional_startup_grace_ms"]
			if tt.want == nil && ok {
				t.Fatalf("mcp_optional_startup_grace_ms = %v, want none", grace)
			}
			if tt.want != nil && grace != tt.want {
				t.Errorf("mcp_optional_startup_grace_ms = %v (%T), want %v", grace, grace, tt.want)
			}
		})
	}
}

// The gateway write keeps the customer's Codex settings and gives the same bytes every run.
func TestCodexMCPProcessor_GatewayWriteKeepsSettingsAndIsStable(t *testing.T) {
	home := writeRemoteMCPConfig(t)
	writeCodexConfig(t, home, "model = \"gpt-5-codex\"\n[profiles.fast]\nmodel = \"gpt-5-codex-mini\"\n"+
		"[features]\nweb_search_request = true\n")
	processor := NewCodexMCPProcessor(testNairiMCP())
	configPath := filepath.Join(home, ".codex", "config.toml")

	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.ProcessMCPConfigs(""); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("second write changed config.toml:\n%s\nthen\n%s", first, second)
	}
	config := readCodexConfig(t, home)
	profiles, _ := config["profiles"].(map[string]interface{})
	fast, _ := profiles["fast"].(map[string]interface{})
	if config["model"] != "gpt-5-codex" || fast["model"] != "gpt-5-codex-mini" {
		t.Errorf("customer settings lost: model %v, profiles %v", config["model"], config["profiles"])
	}
	if want := map[string]interface{}{"web_search_request": true}; !reflect.DeepEqual(config["features"], want) {
		t.Errorf("features = %v, want %v", config["features"], want)
	}
	if grace := config["mcp_optional_startup_grace_ms"]; grace != int64(10000) {
		t.Errorf("mcp_optional_startup_grace_ms = %v, want 10000", grace)
	}
}

// Once the gateway is off, every write path removes the grace nairid wrote: on a self-hosted
// agent it would also slow the customer's own Codex.
func TestCodexMCPProcessor_GatewayOffRemovesNairidGrace(t *testing.T) {
	tests := []struct {
		name      string
		on, off   *CodexMCPProcessor
		noServers bool // the off run finds no downloaded MCP configs
	}{
		{
			name: "self-hosted, gateway unreachable",
			on:   &CodexMCPProcessor{nairi: testNairiMCP()},
			off:  &CodexMCPProcessor{},
		},
		{
			name:      "self-hosted, no MCP servers",
			on:        &CodexMCPProcessor{nairi: testNairiMCP()},
			off:       &CodexMCPProcessor{},
			noServers: true,
		},
		{
			name: "managed, failing closed",
			on:   &CodexMCPProcessor{nairi: testNairiMCP(), stdioOnly: true},
			off:  &CodexMCPProcessor{stdioOnly: true},
		},
		{
			name: "self-hosted gateway, then managed failing closed",
			on:   &CodexMCPProcessor{nairi: testNairiMCP()},
			off:  &CodexMCPProcessor{stdioOnly: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGatewayOffRemovesGrace(t, tt.on, tt.off, tt.noServers)
		})
	}
}

// assertGatewayOffRemovesGrace writes with the gateway on, then off, and checks the grace is gone.
func assertGatewayOffRemovesGrace(t *testing.T, on, off *CodexMCPProcessor, noServers bool) {
	t.Helper()
	home := writeRemoteMCPConfig(t)
	if err := on.ProcessMCPConfigs(""); err != nil {
		t.Fatal(err)
	}
	if grace := readCodexConfig(t, home)["mcp_optional_startup_grace_ms"]; grace != int64(10000) {
		t.Fatalf("gateway on: mcp_optional_startup_grace_ms = %v, want 10000", grace)
	}
	if noServers {
		if err := os.Remove(filepath.Join(home, ".config", "eksecd", "mcp", "private.json")); err != nil {
			t.Fatal(err)
		}
	}

	if err := off.ProcessMCPConfigs(""); err != nil {
		t.Fatal(err)
	}

	if grace, ok := readCodexConfig(t, home)["mcp_optional_startup_grace_ms"]; ok {
		t.Errorf("gateway off: mcp_optional_startup_grace_ms = %v, want none", grace)
	}
}

// A grace the customer set stays through gateway on and off runs.
func TestCodexMCPProcessor_CustomerGraceSurvivesGatewayOnAndOff(t *testing.T) {
	home := writeRemoteMCPConfig(t)
	writeCodexConfig(t, home, "mcp_optional_startup_grace_ms = 15000\n")
	runs := []*CodexMCPProcessor{
		{},
		{nairi: testNairiMCP()},
		{},
		{nairi: testNairiMCP(), stdioOnly: true},
		{stdioOnly: true},
	}
	for i, p := range runs {
		if err := p.ProcessMCPConfigs(""); err != nil {
			t.Fatal(err)
		}
		if grace := readCodexConfig(t, home)["mcp_optional_startup_grace_ms"]; grace != int64(15000) {
			t.Errorf("run %d, gateway on %t: grace = %v, want 15000", i, p.nairi != nil, grace)
		}
	}
}

// Turning the gateway on and off again gives back the config from before, and turning it on
// again sets the grace again.
func TestCodexMCPProcessor_GatewayOffOnOffIsIdempotent(t *testing.T) {
	home := writeRemoteMCPConfig(t)
	writeCodexConfig(t, home, "model = \"gpt-5-codex\"\n")
	configPath := filepath.Join(home, ".codex", "config.toml")
	var written [][]byte
	for _, nairi := range []*NairiMCP{nil, testNairiMCP(), nil, testNairiMCP()} {
		if err := NewCodexMCPProcessor(nairi).ProcessMCPConfigs(""); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		written = append(written, content)
	}

	if !bytes.Equal(written[0], written[2]) {
		t.Errorf("off, on, off changed config.toml:\n%s\nthen\n%s", written[0], written[2])
	}
	if !bytes.Equal(written[1], written[3]) {
		t.Errorf("on, off, on changed config.toml:\n%s\nthen\n%s", written[1], written[3])
	}
	if grace := readCodexConfig(t, home)["mcp_optional_startup_grace_ms"]; grace != int64(10000) {
		t.Errorf("gateway on again: mcp_optional_startup_grace_ms = %v, want 10000", grace)
	}
}

// With the gateway off, only a value equal to nairid's is removed, whatever numeric type it has.
func TestWithOptionalMCPStartupGrace_GatewayOffMatchesNairidValueOnly(t *testing.T) {
	tests := []struct {
		grace interface{}
		kept  bool
	}{
		{grace: 10000},
		{grace: int64(10000)},
		{grace: float64(10000)},
		{grace: int64(15000), kept: true},
		{grace: int64(0), kept: true},
		{grace: 10000.5, kept: true},
		{grace: "10000", kept: true},
	}
	for _, tt := range tests {
		config := map[string]interface{}{codexOptionalMCPStartupGraceKey: tt.grace}
		withOptionalMCPStartupGrace(config, false)
		if _, ok := config[codexOptionalMCPStartupGraceKey]; ok != tt.kept {
			t.Errorf("%v (%T): kept = %t, want %t", tt.grace, tt.grace, ok, tt.kept)
		}
	}
}

func writeCodexConfig(t *testing.T, home, content string) {
	t.Helper()
	if content == "" {
		return
	}
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readCodexConfig(t *testing.T, home string) map[string]interface{} {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]interface{}
	if err := toml.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	return config
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
	if oauth, ok := entry["oauth"]; !ok || oauth != false {
		t.Errorf("nairi oauth = %v (set %v), want false so a gateway 401 does not disable it", oauth, ok)
	}
	assertGatewayHeaders(t, entry["headers"].(map[string]interface{}))
	if _, ok := config.MCP["private"]["oauth"]; ok {
		t.Error("only the nairi entry turns off OpenCode's OAuth")
	}
}

// A managed agent on the gateway drops a remote server that reached the disk anyway: the gateway
// serves it.
func TestStdioOnlyMCPProcessor_GatewayDropsRemoteServers(t *testing.T) {
	configPaths := map[string]string{
		"claude":   ".claude.json",
		"opencode": filepath.Join(".config", "opencode", "opencode.json"),
		"codex":    filepath.Join(".codex", "config.toml"),
	}
	for agentType, configPath := range configPaths {
		t.Run(agentType, func(t *testing.T) {
			home := writeRemoteMCPConfig(t)
			processor, err := NewStdioOnlyMCPProcessor(agentType, testNairiMCP())
			if err != nil {
				t.Fatal(err)
			}

			if err := processor.ProcessMCPConfigs(home); err != nil {
				t.Fatal(err)
			}

			content, err := os.ReadFile(filepath.Join(home, configPath))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(content), "mcp.example.com") || strings.Contains(string(content), LocalMCPsHeader) {
				t.Errorf("expected the remote server dropped, got %s", content)
			}
			if !strings.Contains(string(content), testNairiMCP().URL) {
				t.Errorf("expected the gateway entry, got %s", content)
			}
		})
	}
}

// A managed agent, on the gateway or failing closed, keeps a customer's own mcp-remote wrapper,
// which the gateway doesn't serve, and drops anything that may be Composio's wrapper.
func TestAgentMCPServers_StdioOnlyKeepsCustomerWrappers(t *testing.T) {
	wrapper := func(url string, extra ...string) string {
		args, _ := json.Marshal(append([]string{"-y", "mcp-remote@0.1.29", url}, extra...))
		return `{"command":"npx","args":` + string(args) + `}`
	}
	tests := []struct {
		name   string
		server string
		kept   bool
	}{
		{name: "customer wrapper", server: wrapper("https://mcp.example.com/sse", "--header", "Authorization:Bearer ${TOKEN}"), kept: true},
		{name: "customer wrapper run directly", server: `{"command":"/usr/local/bin/mcp-remote","args":["https://mcp.example.com/sse"]}`, kept: true},
		{name: "plain stdio server", server: `{"command":"npx","args":["-y","server-fs"],"env":{"ROOT":"/tmp"}}`, kept: true},
		{name: "url server", server: `{"type":"http","url":"https://mcp.example.com/mcp"}`},
		{name: "legacy composio wrapper with the platform key", server: `{"command":"npx","args":["-y","mcp-remote",` +
			`"https://backend.composio.dev/v3/mcp/srv_1/mcp?user_id=u_789","--header","x-api-key:test_platform_key"]}`},
		{name: "composio subdomain", server: wrapper("https://mcp.apollo.composio.dev/v3/mcp/srv_1")},
		{name: "composio upper case with userinfo", server: wrapper("https://mcp.example.com@Backend.Composio.DEV/x")},
		{name: "lookalike host", server: wrapper("https://composio.dev.evil/mcp")},
		{name: "url that won't parse", server: wrapper("https://backend.composio%2Edev/mcp")},
		{name: "no url", server: `{"command":"npx","args":["-y","mcp-remote"]}`},
		{name: "key in env only", server: `{"command":"npx","args":["mcp-remote","https://mcp.example.com/sse"],"env":{"COMPOSIO_API_KEY":"test_platform_key"}}`},
		{name: "key in a header arg only", server: wrapper("https://mcp.example.com/sse", "--header", "x-api-key:${COMPOSIO_API_KEY}")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			onGateway := stdioOnlyServers(t, tt.server, testNairiMCP())
			failingClosed := stdioOnlyServers(t, tt.server, nil)

			_, keptOnGateway := onGateway["server"]
			_, keptFailingClosed := failingClosed["server"]
			if keptOnGateway != tt.kept || keptFailingClosed != tt.kept {
				t.Errorf("server kept on the gateway = %v, failing closed = %v; want %v", keptOnGateway, keptFailingClosed, tt.kept)
			}
			if _, ok := onGateway[NairiMCPServerName]; !ok {
				t.Error("the gateway entry is missing")
			}
		})
	}
}

// stdioOnlyServers merges a downloaded config holding server as a managed agent does.
func stdioOnlyServers(t *testing.T, server string, nairi *NairiMCP) map[string]interface{} {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	mcpDir := filepath.Join(home, ".config", "eksecd", "mcp")
	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"mcpServers":{"server":` + server + `}}`
	if err := os.WriteFile(filepath.Join(mcpDir, "servers.json"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	servers, err := agentMCPServers(nairi, true)
	if err != nil {
		t.Fatal(err)
	}
	return servers
}

func TestHasNairiMCPEntry(t *testing.T) {
	tests := []struct {
		name      string
		agentType string
		config    string // none written when empty
		want      bool
		wantErr   bool
	}{
		{name: "gateway entry", agentType: "claude", config: `{"mcpServers":{"nairi":{"type":"http","url":"https://gw.example.com/mcp"}}}`, want: true},
		{name: "dropped by the CLI", agentType: "claude", config: `{"mcpServers":{"fs":{"command":"npx"}}}`},
		{name: "customer server under the name", agentType: "claude", config: `{"mcpServers":{"nairi":{"command":"npx"}}}`},
		{name: "another url under the name", agentType: "claude", config: `{"mcpServers":{"nairi":{"type":"http","url":"https://mcp.example.com/mcp"}}}`},
		{name: "no config yet", agentType: "claude"},
		{name: "half written", agentType: "claude", config: `{"mcpSer`, wantErr: true},
		{name: "opencode", agentType: "opencode", config: `{"mcp":{"nairi":{"type":"remote","url":"https://gw.example.com/mcp"}}}`, want: true},
		{name: "codex", agentType: "codex", config: "[mcp_servers.nairi]\nurl = \"https://gw.example.com/mcp\"\n", want: true},
		{name: "codex customer server under the name", agentType: "codex", config: "[mcp_servers.nairi]\ncommand = \"npx\"\n"},
		{name: "cursor has no MCP config", agentType: "cursor", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("AGENT_EXEC_USER", "")
			writeAgentMCPConfig(t, filepath.Join(home, agentMCPConfigFiles[tt.agentType].path), tt.config)

			got, err := HasNairiMCPEntry(tt.agentType, home, "https://gw.example.com/mcp")

			if got != tt.want || (err != nil) != tt.wantErr {
				t.Errorf("HasNairiMCPEntry = %v, %v; want %v, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

// writeAgentMCPConfig writes config to path, or nothing when config is empty.
func writeAgentMCPConfig(t *testing.T, path, config string) {
	t.Helper()
	if config == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
}

// Off the gateway, a downloaded MCP config keeps only its stdio servers, so a remote server's
// credential never reaches the disk; files outside the MCP directory are written as sent.
func TestFetchAndStoreArtifact_StdioOnlyFiltersMCPConfigs(t *testing.T) {
	cfg := `{"mcpServers":{"fs":{"command":"npx","args":["server-fs"]},` +
		`"composio":{"command":"npx","args":["-y","mcp-remote@0.1","https://backend.composio.dev/v3/mcp/srv_1/mcp",` +
		`"--header","x-api-key:test_platform_key"]},"private":{"type":"http","url":"https://mcp.example.com/mcp"}}}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "att_1", "data": base64.StdEncoding.EncodeToString([]byte(cfg))})
	}))
	t.Cleanup(ts.Close)
	home := t.TempDir()
	t.Setenv("HOME", home)
	client := clients.NewAgentsApiClient("test-api-key", ts.URL, "ag_123")

	for _, location := range []string{"~/.config/eksecd/mcp/servers.json", "~/notes/servers.json"} {
		if err := FetchAndStoreArtifact(client, "att_1", location, true); err != nil {
			t.Fatal(err)
		}
	}

	mcpContent, err := os.ReadFile(filepath.Join(home, ".config", "eksecd", "mcp", "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(mcpContent) != `{"mcpServers":{"fs":{"args":["server-fs"],"command":"npx"}}}` {
		t.Errorf("MCP config = %s, want the stdio server only", mcpContent)
	}
	other, err := os.ReadFile(filepath.Join(home, "notes", "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(other) != cfg {
		t.Errorf("a file outside the MCP directory changed: %s", other)
	}
}

func TestClaudeMCPAllowRules(t *testing.T) {
	home := writeRemoteMCPConfig(t)
	cfg := `{"mcpServers":{"my.tools-v2":{"command":"tools"},"nairi":{"command":"nairi-tools"}}}`
	mcpDir := filepath.Join(home, ".config", "eksecd", "mcp")
	if err := os.WriteFile(filepath.Join(mcpDir, "local.json"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ClaudeMCPAllowRules(testNairiMCP(), false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"mcp__my_tools-v2", "mcp__nairi", "mcp__nairi-2", "mcp__private"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	got, err = ClaudeMCPAllowRules(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"mcp__my_tools-v2", "mcp__nairi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stdio only: got %v, want %v", got, want)
	}
}

func TestClaudeMCPAllowRules_NoServers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	got, err := ClaudeMCPAllowRules(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no rules, got %v", got)
	}
}
