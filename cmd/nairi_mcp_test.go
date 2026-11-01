package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nairid/clients"
)

const (
	managedAPIKey    = "managed_test"
	selfHostedAPIKey = "nairid_test"
)

// mcpKeyBackend answers /api/agents/mcp-key with status and body, and records whether an
// attachment download carried the version header that makes the backend strip remote servers.
func mcpKeyBackend(t *testing.T, status int, body string) (*httptest.Server, *bool, *bool) {
	t.Helper()
	keyAsked, stripAsked := false, false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agents/mcp-key" {
			keyAsked = true
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		stripAsked = r.Header.Get(clients.NairidVersionHeader) != ""
		_, _ = w.Write([]byte(`{"id":"att_123","data":""}`))
	}))
	t.Cleanup(ts.Close)
	return ts, &keyAsked, &stripAsked
}

type setupNairiMCPCase struct {
	name        string
	apiKey      string
	proxy       string
	status      int
	body        string
	wantKey     string
	wantKeyCall bool
}

func TestSetupNairiMCP(t *testing.T) {
	tests := []setupNairiMCPCase{
		{
			name:   "managed without a secret proxy keeps remote servers local",
			apiKey: managedAPIKey,
			status: http.StatusOK, body: `{"key":"CCASECRET_NAIRI_MCP_KEY"}`,
		},
		{
			name:   "managed with the gateway turned off keeps remote servers local",
			apiKey: managedAPIKey, proxy: "http://127.0.0.1:8081",
			status: http.StatusNotImplemented, body: `{"error":"not_enabled"}`,
			wantKeyCall: true,
		},
		{
			name:   "managed on a backend that rejects managed agents keeps remote servers local",
			apiKey: managedAPIKey, proxy: "http://127.0.0.1:8081",
			status: http.StatusBadRequest, body: `{"error":"managed agents get their MCP key from the secret proxy"}`,
			wantKeyCall: true,
		},
		{
			name:   "managed on an older backend keeps remote servers local",
			apiKey: managedAPIKey, proxy: "http://127.0.0.1:8081",
			status: http.StatusNotFound, body: `404 page not found`,
			wantKeyCall: true,
		},
		{
			name:   "managed with the gateway on uses the placeholder",
			apiKey: managedAPIKey, proxy: "http://127.0.0.1:8081",
			status: http.StatusOK, body: `{"key":"CCASECRET_NAIRI_MCP_KEY"}`,
			wantKey: "CCASECRET_NAIRI_MCP_KEY", wantKeyCall: true,
		},
		{
			name:   "self-hosted with the gateway turned off keeps remote servers local",
			apiKey: selfHostedAPIKey,
			status: http.StatusNotImplemented, body: `{"error":"not_enabled"}`,
			wantKeyCall: true,
		},
		{
			name:   "self-hosted with the gateway on uses its key",
			apiKey: selfHostedAPIKey,
			status: http.StatusOK, body: `{"key":"nairiagentmcp_test"}`,
			wantKey: "nairiagentmcp_test", wantKeyCall: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { runSetupNairiMCPCase(t, tt) })
	}
}

func runSetupNairiMCPCase(t *testing.T, tt setupNairiMCPCase) {
	t.Setenv("AGENT_HTTP_PROXY", tt.proxy)
	ts, keyAsked, stripAsked := mcpKeyBackend(t, tt.status, tt.body)
	client := clients.NewAgentsApiClient(tt.apiKey, ts.URL, "ag_123")

	got := setupNairiMCP(client, ts.URL, "ag_123")

	if *keyAsked != tt.wantKeyCall {
		t.Errorf("asked for the MCP key = %v, want %v", *keyAsked, tt.wantKeyCall)
	}
	if _, err := client.FetchAttachment("att_123"); err != nil {
		t.Fatal(err)
	}
	if tt.wantKey == "" {
		if got != nil || *stripAsked {
			t.Errorf("expected no gateway entry and full MCP configs, got entry %v, strip asked %v", got, *stripAsked)
		}
		return
	}
	if got == nil {
		t.Fatal("expected a gateway entry")
	}
	if got.URL != ts.URL+clients.NairiAgentMCPGatewayPath || got.Headers["Authorization"] != "Bearer "+tt.wantKey {
		t.Errorf("unexpected gateway entry %v", got)
	}
	if !*stripAsked {
		t.Error("expected MCP configs to be fetched without gateway-served servers")
	}
}
