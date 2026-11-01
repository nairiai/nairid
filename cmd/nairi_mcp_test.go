package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"nairid/clients"
)

const (
	managedAPIKey    = "managed_test"
	selfHostedAPIKey = "nairid_test"
	placeholderKey   = "CCASECRET_NAIRI_MCP_KEY"
	selfHostedKey    = "nairiagentmcp_test"

	stdioServer = `"fs":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/workspace"]}`
	// A remote server the backend leaves in, with a credential in its header.
	urlServer = `"private":{"type":"http","url":"https://mcp.example.com/mcp",` +
		`"headers":{"Authorization":"Bearer test_remote_token"}}`
	composioServer = `"composio":{"command":"npx",` +
		`"args":["mcp-remote","https://composio.example.com/mcp","--header","x-api-key:test_platform_key"]}`
	// What the backend serves a nairid that does not send its version.
	legacyMCPConfig = `{"mcpServers":{` + composioServer + `,` + stdioServer + `,` + urlServer + `}}`
	// What it serves one that does: Composio and gateway-served servers left out.
	strippedMCPConfig = `{"mcpServers":{` + stdioServer + `,` + urlServer + `}}`
)

// reply is one fake backend answer.
type reply struct {
	status int
	body   string
}

var (
	placeholderKeyReply = reply{http.StatusOK, `{"key":"` + placeholderKey + `"}`}
	selfHostedKeyReply  = reply{http.StatusOK, `{"key":"` + selfHostedKey + `"}`}
	notEnabled          = reply{http.StatusNotImplemented, `{"error":"not_enabled"}`}
	pong                = reply{http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":{}}`}
	// The router's 404 on a backend rolled back to before the gateway.
	routeNotFound = reply{http.StatusNotFound, `{"error":"not found"}`}
	edgeNotFound  = reply{http.StatusNotFound, `<html><body>404 Not Found</body></html>`}
	// The gateway's own 404, for an MCP session it does not know.
	sessionNotFound = reply{
		http.StatusNotFound, `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"session not found"}}`,
	}
	serverError404Body = reply{http.StatusInternalServerError, `upstream returned 404 not found`}
	unavailable        = reply{http.StatusServiceUnavailable, `unavailable`}
	unauthorized       = reply{http.StatusUnauthorized, `{"error":"unauthorized"}`}
	agentMismatch      = reply{http.StatusForbidden, `{"error":"agent_mismatch"}`}
	// What claudecontrol main and older stack heads answer a managed agent's MCP key fetch.
	managedKeyRefusal = reply{
		http.StatusBadRequest, `{"error":"managed agents get their MCP key from the secret proxy"}` + "\n",
	}
	unknownAgent = reply{http.StatusBadRequest, `{"error":"agent not found, send X-AGENT-ID"}` + "\n"}
	// No answer: the backend closes the connection.
	dropped = reply{}
)

func (rep reply) write(w http.ResponseWriter) {
	if rep.status == 0 {
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
		return
	}
	w.WriteHeader(rep.status)
	_, _ = w.Write([]byte(rep.body))
}

// fakeBackend answers the agent API calls nairid makes to set up MCP servers.
type fakeBackend struct {
	keyReplies     []reply // one per mcp-key call, the last one repeating
	probeReplies   []reply // one per gateway probe, the last one repeating
	strippedConfig string  // defaults to strippedMCPConfig
	ignoresVersion bool    // a backend from before the gateway sends legacyMCPConfig anyway
	keyCalls       int
	probeCalls     int
	proxiedProbes  int
	sessionProbes  int
	mcpDownloads   int
	stripAsked     bool
}

func (b *fakeBackend) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/agents/mcp-key":
		b.keyReplies[min(b.keyCalls, len(b.keyReplies)-1)].write(w)
		b.keyCalls++
	case clients.NairiAgentMCPGatewayPath:
		reply := b.probeReplies[min(b.probeCalls, len(b.probeReplies)-1)]
		b.probeCalls++
		if r.Header.Get("Mcp-Session-Id") != "" {
			b.sessionProbes++
		}
		reply.write(w)
	case "/api/agents/artifacts":
		_, _ = w.Write([]byte(`[{"title":"MCP servers","type":"mcp_config",` +
			`"files":[{"location":"~/.config/eksecd/mcp/servers.json","attachmentId":"att_mcp"}]}]`))
	default:
		b.mcpDownloads++
		b.stripAsked = r.Header.Get(clients.NairidVersionHeader) != ""
		config := legacyMCPConfig
		if b.stripAsked && !b.ignoresVersion {
			config = strippedMCPConfig
		}
		if b.stripAsked && !b.ignoresVersion && b.strippedConfig != "" {
			config = b.strippedConfig
		}
		data := base64.StdEncoding.EncodeToString([]byte(config))
		_, _ = w.Write([]byte(`{"id":"att_mcp","data":"` + data + `"}`))
	}
}

func (b *fakeBackend) serve(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(b.handle))
	t.Cleanup(ts.Close)
	return ts
}

// secretProxy stands in for a managed pod's secret proxy in front of the backend and sets
// AGENT_HTTP_PROXY to it.
func (b *fakeBackend) secretProxy(t *testing.T) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == clients.NairiAgentMCPGatewayPath {
			b.proxiedProbes++
		}
		b.handle(w, r)
	}))
	t.Cleanup(ts.Close)
	t.Setenv("AGENT_HTTP_PROXY", ts.URL)
}

// recordRetrySleeps makes retries instant and returns the waits they asked for.
func recordRetrySleeps(t *testing.T) *[]time.Duration {
	t.Helper()
	slept := []time.Duration{}
	agentMCPRetrySleep = func(d time.Duration) { slept = append(slept, d) }
	t.Cleanup(func() { agentMCPRetrySleep = time.Sleep })
	return &slept
}

type setupAgentMCPCase struct {
	name           string
	apiKey         string
	managed        bool
	keyReplies     []reply
	probeReplies   []reply
	wantKey        string // set when the gateway is used
	wantStdioOnly  bool
	wantKeyCalls   int
	wantProbeCalls int
	wantSleeps     []time.Duration
}

var allRetries = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

func TestSetupAgentMCP_SelfHosted(t *testing.T) {
	keyOK := []reply{selfHostedKeyReply}
	tests := []setupAgentMCPCase{
		{
			name:         "gateway turned off keeps remote servers local",
			keyReplies:   []reply{notEnabled},
			wantKeyCalls: 1,
		},
		{
			name:         "a failing key fetch is not retried and keeps remote servers local",
			keyReplies:   []reply{unavailable},
			wantKeyCalls: 1,
		},
		{
			name:       "backend rolled back to before the gateway keeps remote servers local",
			keyReplies: keyOK, probeReplies: []reply{routeNotFound},
			wantKeyCalls: 1, wantProbeCalls: 1,
		},
		{
			name:       "an edge 404 keeps remote servers local",
			keyReplies: keyOK, probeReplies: []reply{edgeNotFound},
			wantKeyCalls: 1, wantProbeCalls: 1,
		},
		{
			name:       "the gateway's session 404 keeps remote servers local",
			keyReplies: keyOK, probeReplies: []reply{sessionNotFound},
			wantKeyCalls: 1, wantProbeCalls: 1,
		},
		{
			name:       "a 500 naming a 404 is retried, then keeps remote servers local",
			keyReplies: keyOK, probeReplies: []reply{serverError404Body},
			wantKeyCalls: 1, wantProbeCalls: 4, wantSleeps: allRetries,
		},
		{
			name:       "a rejected probe keeps remote servers local",
			keyReplies: keyOK, probeReplies: []reply{unauthorized},
			wantKeyCalls: 1, wantProbeCalls: 1,
		},
		{
			name:       "a blip, then the gateway",
			keyReplies: keyOK, probeReplies: []reply{unavailable, pong},
			wantKey: selfHostedKey, wantKeyCalls: 1, wantProbeCalls: 2, wantSleeps: allRetries[:1],
		},
		{
			name:       "the gateway answers",
			keyReplies: keyOK, probeReplies: []reply{pong},
			wantKey: selfHostedKey, wantKeyCalls: 1, wantProbeCalls: 1,
		},
		{
			name:         "an unknown agent's 400 keeps remote servers local",
			keyReplies:   []reply{unknownAgent},
			wantKeyCalls: 1,
		},
		{
			name:          "a managed agent without a secret proxy on an older backend keeps only stdio servers",
			keyReplies:    []reply{managedKeyRefusal},
			wantStdioOnly: true, wantKeyCalls: 1,
		},
		{
			name:       "a managed agent without a secret proxy keeps only stdio servers, unprobed",
			keyReplies: []reply{placeholderKeyReply}, probeReplies: []reply{pong},
			wantStdioOnly: true, wantKeyCalls: 1,
		},
	}
	for _, tt := range tests {
		tt.apiKey = selfHostedAPIKey
		t.Run(tt.name, func(t *testing.T) { runSetupAgentMCPCase(t, tt) })
	}
}

func TestSetupAgentMCP_Managed(t *testing.T) {
	keyOK := []reply{placeholderKeyReply}
	tests := []setupAgentMCPCase{
		{
			name:       "the gateway answers through the secret proxy",
			keyReplies: keyOK, probeReplies: []reply{pong},
			wantKey: placeholderKey, wantKeyCalls: 1, wantProbeCalls: 1,
		},
		{
			// Managed is decided by the secret proxy, not the key prefix.
			name:   "a managed row with a self-hosted API key uses the gateway",
			apiKey: selfHostedAPIKey, keyReplies: keyOK, probeReplies: []reply{pong},
			wantKey: placeholderKey, wantKeyCalls: 1, wantProbeCalls: 1,
		},
		{
			name: "retries a failing key fetch, then uses the gateway",
			keyReplies: []reply{
				{http.StatusBadGateway, `bad gateway`}, unavailable, placeholderKeyReply,
			},
			probeReplies: []reply{pong},
			wantKey:      placeholderKey, wantKeyCalls: 3, wantProbeCalls: 1, wantSleeps: allRetries[:2],
		},
		{
			name:         "retries an MCP key not ready yet, then uses the gateway",
			keyReplies:   []reply{{http.StatusConflict, `{"error":"not ready"}`}, placeholderKeyReply},
			probeReplies: []reply{pong},
			wantKey:      placeholderKey, wantKeyCalls: 2, wantProbeCalls: 1, wantSleeps: allRetries[:1],
		},
		{
			name:          "an older backend's 404 is not retried and keeps only stdio servers",
			keyReplies:    []reply{{http.StatusNotFound, `404 page not found`}},
			wantStdioOnly: true, wantKeyCalls: 1,
		},
		{
			name:          "a fleet member the backend does not know keeps only stdio servers",
			keyReplies:    []reply{{http.StatusBadRequest, `{"error":"agent not found"}`}},
			wantStdioOnly: true, wantKeyCalls: 1,
		},
		{
			name:          "gateway turned off keeps only stdio servers",
			keyReplies:    []reply{notEnabled},
			wantStdioOnly: true, wantKeyCalls: 1,
		},
		{
			name:          "a failing backend keeps only stdio servers after retrying",
			keyReplies:    []reply{{http.StatusInternalServerError, `internal error`}},
			wantStdioOnly: true, wantKeyCalls: 4, wantSleeps: allRetries,
		},
		{
			name:       "a secret proxy filling another agent's key keeps only stdio servers",
			keyReplies: keyOK, probeReplies: []reply{agentMismatch},
			wantStdioOnly: true, wantKeyCalls: 1, wantProbeCalls: 1,
		},
		{
			name:       "retries a probe 401 while the secret proxy gets the key, then uses the gateway",
			keyReplies: keyOK, probeReplies: []reply{unauthorized, pong},
			wantKey: placeholderKey, wantKeyCalls: 1, wantProbeCalls: 2, wantSleeps: allRetries[:1],
		},
		{
			name:       "a probe 401 that does not clear keeps only stdio servers",
			keyReplies: keyOK, probeReplies: []reply{unauthorized},
			wantStdioOnly: true, wantKeyCalls: 1, wantProbeCalls: 4, wantSleeps: allRetries,
		},
		{
			name:       "a rolled back backend keeps only stdio servers, not the local ones",
			keyReplies: keyOK, probeReplies: []reply{routeNotFound},
			wantStdioOnly: true, wantKeyCalls: 1, wantProbeCalls: 1,
		},
		{
			name:       "a 500 naming a 404 is retried, then keeps only stdio servers",
			keyReplies: keyOK, probeReplies: []reply{serverError404Body},
			wantStdioOnly: true, wantKeyCalls: 1, wantProbeCalls: 4, wantSleeps: allRetries,
		},
	}
	for _, tt := range tests {
		tt.managed = true
		if tt.apiKey == "" {
			tt.apiKey = managedAPIKey
		}
		t.Run(tt.name, func(t *testing.T) { runSetupAgentMCPCase(t, tt) })
	}
}

func runSetupAgentMCPCase(t *testing.T, tt setupAgentMCPCase) {
	t.Setenv("AGENT_HTTP_PROXY", "")
	// Older hosts still set it; nairid no longer falls back to mcproxy.
	t.Setenv("AGENT_MCP_PROXY", "http://mcp-proxy.internal:8082")
	slept := recordRetrySleeps(t)
	backend := &fakeBackend{keyReplies: tt.keyReplies, probeReplies: tt.probeReplies}
	ts := backend.serve(t)
	if tt.managed {
		backend.secretProxy(t)
	}
	client := clients.NewAgentsApiClient(tt.apiKey, ts.URL, "ag_123")

	got := setupAgentMCP(client, ts.URL, "ag_123")

	if backend.keyCalls != tt.wantKeyCalls || backend.probeCalls != tt.wantProbeCalls {
		t.Errorf("key calls = %d, probes = %d; want %d, %d",
			backend.keyCalls, backend.probeCalls, tt.wantKeyCalls, tt.wantProbeCalls)
	}
	wantProxied := 0
	if tt.managed {
		wantProxied = backend.probeCalls
	}
	if backend.proxiedProbes != wantProxied || backend.sessionProbes != 0 {
		t.Errorf("probes through the secret proxy = %d, with an MCP session = %d; want %d, 0",
			backend.proxiedProbes, backend.sessionProbes, wantProxied)
	}
	if !reflect.DeepEqual(*slept, append([]time.Duration{}, tt.wantSleeps...)) {
		t.Errorf("retry waits = %v, want %v", *slept, tt.wantSleeps)
	}
	if got.stdioOnly != tt.wantStdioOnly {
		t.Errorf("stdio only = %v, want %v", got.stdioOnly, tt.wantStdioOnly)
	}
	if _, err := client.FetchAttachment("att_mcp"); err != nil {
		t.Fatal(err)
	}
	wantStrip := tt.wantKey != "" || tt.wantStdioOnly
	if backend.stripAsked != wantStrip {
		t.Errorf("MCP configs asked without remote servers = %v, want %v", backend.stripAsked, wantStrip)
	}
	if tt.wantKey == "" {
		if got.nairi != nil {
			t.Errorf("expected no gateway entry, got %v", got.nairi)
		}
		return
	}
	if got.nairi == nil {
		t.Fatal("expected a gateway entry")
	}
	if got.nairi.URL != ts.URL+clients.NairiAgentMCPGatewayPath ||
		got.nairi.Headers["Authorization"] != "Bearer "+tt.wantKey || got.nairi.Headers["X-AGENT-ID"] != "ag_123" {
		t.Errorf("unexpected gateway entry %v", got.nairi)
	}
}

// agentRun is one nairid start against a fake backend, from the key fetch to the agent CLI's
// config.
type agentRun struct {
	agentType    string
	managed      bool
	withoutProxy bool // a managed pod whose env lacks AGENT_HTTP_PROXY
	keyReplies   []reply
	probeReplies []reply // defaults to pong
	backend      *fakeBackend
}

func (r agentRun) run(t *testing.T) string {
	t.Helper()
	return r.start(t).targetHomeDir
}

// start writes the agent CLI's MCP config the way NewCmdRunner does and returns what wrote it.
func (r agentRun) start(t *testing.T) *agentMCPConfig {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENT_EXEC_USER", "")
	t.Setenv("AGENT_HTTP_PROXY", "")
	recordRetrySleeps(t)
	r.backend.keyReplies = r.keyReplies
	r.backend.probeReplies = r.probeReplies
	if len(r.probeReplies) == 0 {
		r.backend.probeReplies = []reply{pong}
	}
	ts := r.backend.serve(t)
	apiKey := selfHostedAPIKey
	if r.managed {
		apiKey = managedAPIKey
	}
	if r.managed && !r.withoutProxy {
		r.backend.secretProxy(t)
	}
	client := clients.NewAgentsApiClient(apiKey, ts.URL, "ag_123")
	agentHome := t.TempDir()
	writeStaleAgentConfig(t, r.agentType, agentHome)

	cfg := &agentMCPConfig{
		setup: setupAgentMCP(client, ts.URL, "ag_123"), client: client, apiBaseURL: ts.URL, agentID: "ag_123",
		agentType: r.agentType, workDir: agentHome, targetHomeDir: agentHome,
	}
	if err := cfg.fetchArtifacts(); err != nil {
		t.Fatal(err)
	}
	if err := cfg.writeMCPConfigs(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

var agentConfigPaths = map[string]string{
	"claude":   ".claude.json",
	"opencode": filepath.Join(".config", "opencode", "opencode.json"),
	"codex":    filepath.Join(".codex", "config.toml"),
}

// writeStaleAgentConfig leaves a remote server from an earlier run next to a setting of the user's.
func writeStaleAgentConfig(t *testing.T, agentType, agentHome string) {
	t.Helper()
	stale := map[string]string{
		"claude":   `{"theme":"dark","mcpServers":{"stale":{"type":"http","url":"https://stale.example.com/mcp"}}}`,
		"opencode": `{"theme":"dark","mcp":{"stale":{"type":"remote","url":"https://stale.example.com/mcp"}}}`,
		"codex":    "theme = \"dark\"\n\n[mcp_servers.stale]\nurl = \"https://stale.example.com/mcp\"\n",
	}[agentType]
	path := filepath.Join(agentHome, agentConfigPaths[agentType])
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(stale), 0644); err != nil {
		t.Fatal(err)
	}
}

// readAgentMCPServers returns the agent CLI's config file and its MCP servers.
func readAgentMCPServers(t *testing.T, agentType, agentHome string) (string, map[string]map[string]any) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(agentHome, agentConfigPaths[agentType]))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Theme       string                    `json:"theme" toml:"theme"`
		ClaudeCode  map[string]map[string]any `json:"mcpServers"`
		OpenCode    map[string]map[string]any `json:"mcp"`
		Codex       map[string]map[string]any `toml:"mcp_servers"`
		ShellPolicy map[string]any            `toml:"shell_environment_policy"`
	}
	unmarshal := json.Unmarshal
	if agentType == "codex" {
		unmarshal = toml.Unmarshal
	}
	if err := unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	if config.Theme != "dark" {
		t.Errorf("expected the rest of the %s config kept, got %s", agentType, content)
	}
	if agentType == "codex" && config.ShellPolicy == nil {
		t.Errorf("expected shell_environment_policy in config.toml, got %s", content)
	}
	servers := map[string]map[string]map[string]any{
		"claude": config.ClaudeCode, "opencode": config.OpenCode, "codex": config.Codex,
	}[agentType]
	return string(content), servers
}

func serverNames(servers map[string]map[string]any) []string {
	names := []string{}
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Each managed branch end to end: Composio's credential never reaches the agent, and servers from
// an earlier run do not survive.
func TestManagedAgentMCPConfig(t *testing.T) {
	tests := []struct {
		name         string
		keyReplies   []reply
		probeReplies []reply
		wantServers  []string
	}{
		{name: "gateway", keyReplies: []reply{placeholderKeyReply}, wantServers: []string{"fs", "nairi", "private"}},
		{
			name:        "retry then gateway",
			keyReplies:  []reply{{http.StatusBadGateway, `bad gateway`}, placeholderKeyReply},
			wantServers: []string{"fs", "nairi", "private"},
		},
		{name: "fail closed", keyReplies: []reply{unavailable}, wantServers: []string{"fs"}},
		{
			name:       "fail closed on another agent's key",
			keyReplies: []reply{placeholderKeyReply}, probeReplies: []reply{agentMismatch},
			wantServers: []string{"fs"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &fakeBackend{}
			agentHome := agentRun{
				agentType: "claude", managed: true, keyReplies: tt.keyReplies, probeReplies: tt.probeReplies,
				backend: backend,
			}.run(t)

			if backend.mcpDownloads != 1 || !backend.stripAsked {
				t.Errorf("MCP config downloads = %d, with %s: %v; want 1 with it",
					backend.mcpDownloads, clients.NairidVersionHeader, backend.stripAsked)
			}
			content, servers := readAgentMCPServers(t, "claude", agentHome)
			if got := serverNames(servers); !reflect.DeepEqual(got, tt.wantServers) {
				t.Errorf("MCP servers = %v, want %v", got, tt.wantServers)
			}
			if strings.Contains(content, "test_platform_key") || strings.Contains(content, "mcp-remote") {
				t.Errorf("the agent's config holds Composio's credential: %s", content)
			}
		})
	}
}

// Off the gateway, each agent CLI keeps the stdio servers and nothing that reaches a remote
// server: no url entry, no Composio wrapper or key, no gateway entry.
func TestManagedAgentMCPConfig_FailClosed(t *testing.T) {
	for _, tt := range failClosedCases() {
		t.Run(tt.agentType+"/"+tt.name, func(t *testing.T) {
			backend := &fakeBackend{strippedConfig: tt.strippedConfig, ignoresVersion: tt.ignoresVersion}
			agentHome := agentRun{
				agentType: tt.agentType, managed: true, withoutProxy: tt.withoutProxy,
				keyReplies: []reply{tt.keyReply}, backend: backend,
			}.run(t)

			if backend.mcpDownloads != 1 || !backend.stripAsked {
				t.Errorf("MCP config downloads = %d, with %s: %v; want 1 with it",
					backend.mcpDownloads, clients.NairidVersionHeader, backend.stripAsked)
			}
			assertStdioOnlyConfig(t, tt.agentType, agentHome, tt.wantServers)
		})
	}
}

type failClosedCase struct {
	agentType      string
	name           string
	withoutProxy   bool
	keyReply       reply
	strippedConfig string
	ignoresVersion bool
	wantServers    []string
}

func failClosedCases() []failClosedCase {
	configs := []failClosedCase{
		{name: "stdio server kept", keyReply: notEnabled, strippedConfig: strippedMCPConfig, wantServers: []string{"fs"}},
		{
			name: "only a remote server", keyReply: notEnabled,
			strippedConfig: `{"mcpServers":{` + urlServer + `}}`, wantServers: []string{},
		},
		{name: "backend without the gateway", keyReply: routeNotFound, ignoresVersion: true, wantServers: []string{"fs"}},
		{
			name: "no secret proxy, older backend", withoutProxy: true, keyReply: managedKeyRefusal,
			ignoresVersion: true, wantServers: []string{"fs"},
		},
		{
			name: "no secret proxy", withoutProxy: true, keyReply: placeholderKeyReply,
			strippedConfig: strippedMCPConfig, wantServers: []string{"fs"},
		},
	}
	var cases []failClosedCase
	for _, agentType := range []string{"claude", "codex", "opencode"} {
		for _, cfg := range configs {
			cfg.agentType = agentType
			cases = append(cases, cfg)
		}
	}
	return cases
}

func assertStdioOnlyConfig(t *testing.T, agentType, agentHome string, wantServers []string) {
	t.Helper()
	content, servers := readAgentMCPServers(t, agentType, agentHome)
	if got := serverNames(servers); !reflect.DeepEqual(got, wantServers) {
		t.Errorf("MCP servers = %v, want %v", got, wantServers)
	}
	assertNoRemoteServer(t, agentType+" config", content, "nairi")
	downloaded, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config", "eksecd", "mcp", "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	assertNoRemoteServer(t, "downloaded MCP config", string(downloaded))
}

func assertNoRemoteServer(t *testing.T, what, content string, more ...string) {
	t.Helper()
	leaks := []string{"url", "mcp-remote", "test_platform_key", "test_remote_token", placeholderKey, selfHostedKey}
	for _, leak := range append(leaks, more...) {
		if strings.Contains(content, leak) {
			t.Errorf("the %s contains %q: %s", what, leak, content)
		}
	}
}

// A self-hosted agent on a backend without the gateway keeps every remote server in its own
// config, as before; with the gateway, Composio goes and the gateway entry comes.
func TestSelfHostedAgentMCPConfig(t *testing.T) {
	keyOK := []reply{selfHostedKeyReply}
	tests := []struct {
		name         string
		keyReplies   []reply
		probeReplies []reply
		wantStrip    bool
		wantServers  []string
	}{
		{
			name: "backend without the gateway", keyReplies: keyOK, probeReplies: []reply{routeNotFound},
			wantServers: []string{"composio", "fs", "private"},
		},
		{
			name: "backend without the mcp-key route", keyReplies: []reply{routeNotFound},
			wantServers: []string{"composio", "fs", "private"},
		},
		{
			name: "unreachable backend", keyReplies: []reply{dropped},
			wantServers: []string{"composio", "fs", "private"},
		},
		{
			name: "an unknown agent's 400", keyReplies: []reply{unknownAgent},
			wantServers: []string{"composio", "fs", "private"},
		},
		{
			name: "gateway", keyReplies: keyOK, probeReplies: []reply{pong},
			wantStrip: true, wantServers: []string{"fs", "nairi", "private"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &fakeBackend{}
			agentHome := agentRun{
				agentType: "claude", keyReplies: tt.keyReplies, probeReplies: tt.probeReplies, backend: backend,
			}.run(t)

			if backend.mcpDownloads != 1 || backend.stripAsked != tt.wantStrip {
				t.Errorf("MCP config downloads = %d, with %s: %v; want 1, %v",
					backend.mcpDownloads, clients.NairidVersionHeader, backend.stripAsked, tt.wantStrip)
			}
			if backend.proxiedProbes != 0 {
				t.Errorf("self-hosted probes skip the proxy, %d went through one", backend.proxiedProbes)
			}
			_, servers := readAgentMCPServers(t, "claude", agentHome)
			if got := serverNames(servers); !reflect.DeepEqual(got, tt.wantServers) {
				t.Errorf("MCP servers = %v, want %v", got, tt.wantServers)
			}
		})
	}
}

// fastReprobe makes the gateway re-probe tick every millisecond.
func fastReprobe(t *testing.T) {
	t.Helper()
	interval := agentMCPReprobeInterval
	agentMCPReprobeInterval = time.Millisecond
	t.Cleanup(func() { agentMCPReprobeInterval = interval })
}

// A managed agent that failed closed behind its secret proxy moves to the gateway once it answers.
func TestAgentMCPReprobe_MovesToGateway(t *testing.T) {
	refusedOnce := []reply{notEnabled, placeholderKeyReply}
	tests := []struct {
		agentType    string
		name         string
		keyReplies   []reply
		wantKeyCalls int
	}{
		{agentType: "claude", name: "first tick", keyReplies: refusedOnce, wantKeyCalls: 2},
		{agentType: "codex", name: "first tick", keyReplies: refusedOnce, wantKeyCalls: 2},
		{agentType: "opencode", name: "first tick", keyReplies: refusedOnce, wantKeyCalls: 2},
		{
			agentType: "claude", name: "refused on the first tick too",
			keyReplies: []reply{notEnabled, notEnabled, placeholderKeyReply}, wantKeyCalls: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.agentType+"/"+tt.name, func(t *testing.T) {
			backend := &fakeBackend{}
			cfg := agentRun{agentType: tt.agentType, managed: true, keyReplies: tt.keyReplies, backend: backend}.start(t)
			assertStdioOnlyConfig(t, tt.agentType, cfg.targetHomeDir, []string{"fs"})
			fastReprobe(t)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cfg.reprobeGateway(ctx)

			if setup := cfg.current(); ctx.Err() != nil || setup.nairi == nil || setup.stdioOnly || setup.reprobe {
				t.Fatalf("re-probe ended with %+v, %v; want the gateway", setup, ctx.Err())
			}
			if backend.keyCalls != tt.wantKeyCalls || backend.mcpDownloads != 2 || !backend.stripAsked {
				t.Errorf("key calls = %d, MCP config downloads = %d, last with %s: %v; want %d, 2 with it",
					backend.keyCalls, backend.mcpDownloads, clients.NairidVersionHeader, backend.stripAsked, tt.wantKeyCalls)
			}
			_, servers := readAgentMCPServers(t, tt.agentType, cfg.targetHomeDir)
			if got := serverNames(servers); !reflect.DeepEqual(got, []string{"fs", "nairi", "private"}) {
				t.Errorf("MCP servers = %v, want [fs nairi private]", got)
			}
		})
	}
}

// Agents that did not fail closed behind a secret proxy do not re-probe, even with a gateway that
// would answer.
func TestAgentMCPReprobe_OnlyAfterFailingClosed(t *testing.T) {
	tests := []struct {
		name string
		run  agentRun
	}{
		{name: "gateway at startup", run: agentRun{managed: true, keyReplies: []reply{placeholderKeyReply}}},
		{
			name: "managed without a secret proxy",
			run:  agentRun{managed: true, withoutProxy: true, keyReplies: []reply{managedKeyRefusal, placeholderKeyReply}},
		},
		{name: "self-hosted off the gateway", run: agentRun{keyReplies: []reply{notEnabled, selfHostedKeyReply}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &fakeBackend{}
			tt.run.agentType, tt.run.backend = "claude", backend
			cfg := tt.run.start(t)
			before, keyCalls := cfg.current(), backend.keyCalls
			fastReprobe(t)

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			cfg.reprobeGateway(ctx)

			if ctx.Err() != nil || backend.keyCalls != keyCalls || !reflect.DeepEqual(cfg.current(), before) {
				t.Errorf("re-probe ran: %v, key calls %d -> %d, setup %+v -> %+v",
					ctx.Err(), keyCalls, backend.keyCalls, before, cfg.current())
			}
		})
	}
}
