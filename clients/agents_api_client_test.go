package clients

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchMCPKey(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantKey     string
		wantErr     bool
		wantStatus  int
		wantManaged bool
	}{
		{name: "returns the key", status: http.StatusOK, body: `{"key":"mcpk_test"}`, wantKey: "mcpk_test"},
		{
			name: "secret proxy placeholder", status: http.StatusOK, body: `{"key":"CCASECRET_NAIRI_MCP_KEY"}`,
			wantKey: "CCASECRET_NAIRI_MCP_KEY", wantManaged: true,
		},
		{
			name: "managed agent on an older backend", status: http.StatusBadRequest,
			body:    `{"error":"managed agents get their MCP key from the secret proxy"}` + "\n",
			wantErr: true, wantStatus: http.StatusBadRequest, wantManaged: true,
		},
		{
			name: "unknown agent", status: http.StatusBadRequest, body: `{"error":"agent not found, send X-AGENT-ID"}`,
			wantErr: true, wantStatus: http.StatusBadRequest,
		},
		{
			name: "gateway not enabled", status: http.StatusNotImplemented, body: `{"error":"not enabled"}`,
			wantErr: true, wantStatus: http.StatusNotImplemented,
		},
		{
			name: "older backend", status: http.StatusNotFound, body: `404 page not found`,
			wantErr: true, wantStatus: http.StatusNotFound,
		},
		{name: "empty key", status: http.StatusOK, body: `{"key":""}`, wantErr: true},
		{
			name: "backend error", status: http.StatusBadGateway, body: `bad gateway`,
			wantErr: true, wantStatus: http.StatusBadGateway,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/agents/mcp-key" || r.Header.Get("Authorization") != "Bearer ccagent_test" ||
					r.Header.Get("X-AGENT-ID") != "ag_123" {
					t.Errorf("unexpected request %s %v", r.URL.Path, r.Header)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()

			key, err := NewAgentsApiClient("ccagent_test", ts.URL, "ag_123").FetchMCPKey()
			if (err != nil) != tt.wantErr || key != tt.wantKey {
				t.Errorf("FetchMCPKey() = %q, %v; want %q, error %v", key, err, tt.wantKey, tt.wantErr)
			}
			if got := AgentMCPStatus(err); got != tt.wantStatus {
				t.Errorf("AgentMCPStatus(%v) = %d, want %d", err, got, tt.wantStatus)
			}
			if got := IsManagedAgentMCPKey(key, err); got != tt.wantManaged {
				t.Errorf("IsManagedAgentMCPKey(%q, %v) = %v, want %v", key, err, got, tt.wantManaged)
			}
		})
	}
}

func TestFetchMCPKey_NetworkErrorIsUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	ts.Close()

	_, err := NewAgentsApiClient("ccagent_test", ts.URL, "ag_123").FetchMCPKey()
	if !errors.Is(err, ErrAgentMCPUnreachable) || AgentMCPStatus(err) != 0 {
		t.Errorf("FetchMCPKey() on a closed server = %v, want an unreachable error", err)
	}
}

// The backend strips gateway-served servers from MCP configs only when nairid sends its version.
func TestFetchAttachment_SendsVersionOnlyWithGateway(t *testing.T) {
	var gotVersion string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotVersion = r.Header.Get(NairidVersionHeader)
		_, _ = w.Write([]byte(`{"id":"att_123","data":""}`))
	}))
	defer ts.Close()

	client := NewAgentsApiClient("ccagent_test", ts.URL, "ag_123")
	if _, err := client.FetchAttachment("att_123"); err != nil {
		t.Fatal(err)
	}
	if gotVersion != "" {
		t.Errorf("expected no %s before UseAgentMCPGateway, got %q", NairidVersionHeader, gotVersion)
	}

	client.UseAgentMCPGateway("1.2.3")
	if _, err := client.FetchAttachment("att_123"); err != nil {
		t.Fatal(err)
	}
	if gotVersion != "1.2.3" {
		t.Errorf("%s = %q, want 1.2.3", NairidVersionHeader, gotVersion)
	}
}

func TestProbeAgentMCPGateway(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantErr      bool
		wantNotFound bool
	}{
		{name: "gateway answers", status: http.StatusOK, body: `{"jsonrpc":"2.0","id":1,"result":{}}`},
		{
			name: "backend without the gateway", status: http.StatusNotFound, body: `{"error":"not found"}`,
			wantErr: true, wantNotFound: true,
		},
		{name: "edge 404", status: http.StatusNotFound, body: `<html>404 Not Found</html>`, wantErr: true},
		{
			name:    "gateway session 404",
			status:  http.StatusNotFound,
			body:    `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"session not found"}}`,
			wantErr: true,
		},
		{name: "500 naming a 404", status: http.StatusInternalServerError, body: `upstream 404`, wantErr: true},
		{name: "key rejected", status: http.StatusUnauthorized, body: `{"error":"unauthorized"}`, wantErr: true},
		{name: "another agent's key", status: http.StatusForbidden, body: `{"error":"agent_mismatch"}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assertProbeRequest(t, r)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()

			err := NewAgentsApiClient("nairid_test", ts.URL, "ag_123").ProbeAgentMCPGateway("nairiagentmcp_test", "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("ProbeAgentMCPGateway() = %v, want error %v", err, tt.wantErr)
			}
			if IsAgentMCPRouteNotFound(err) != tt.wantNotFound {
				t.Errorf("IsAgentMCPRouteNotFound(%v) = %v, want %v", err, !tt.wantNotFound, tt.wantNotFound)
			}
			if tt.wantErr && AgentMCPStatus(err) != tt.status {
				t.Errorf("AgentMCPStatus(%v) = %d, want %d", err, AgentMCPStatus(err), tt.status)
			}
		})
	}
}

func TestProbeAgentMCPGateway_ThroughProxy(t *testing.T) {
	var proxied int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied++
		if r.URL.Host != "api.example.test" {
			t.Errorf("proxy got a request for %q, want the backend host", r.URL.Host)
		}
		assertProbeRequest(t, r)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer proxy.Close()

	client := NewAgentsApiClient("nairid_test", "http://api.example.test", "ag_123")
	if err := client.ProbeAgentMCPGateway("nairiagentmcp_test", proxy.URL); err != nil {
		t.Fatal(err)
	}
	if proxied != 1 {
		t.Errorf("proxy saw %d probes, want 1", proxied)
	}
}

func TestProbeAgentMCPGateway_NetworkErrorIsUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	ts.Close()

	err := NewAgentsApiClient("nairid_test", ts.URL, "ag_123").ProbeAgentMCPGateway("nairiagentmcp_test", "")
	if !errors.Is(err, ErrAgentMCPUnreachable) {
		t.Errorf("ProbeAgentMCPGateway() on a closed server = %v, want an unreachable error", err)
	}
}

// assertProbeRequest checks the probe is the ping the agent CLI's first request looks like,
// without an MCP session.
func assertProbeRequest(t *testing.T, r *http.Request) {
	t.Helper()
	body, _ := io.ReadAll(r.Body)
	if r.Method != http.MethodPost || r.URL.Path != NairiAgentMCPGatewayPath ||
		r.Header.Get("Authorization") != "Bearer nairiagentmcp_test" || r.Header.Get("X-AGENT-ID") != "ag_123" ||
		r.Header.Get("Accept") != "application/json, text/event-stream" ||
		r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Mcp-Session-Id") != "" ||
		string(body) != `{"jsonrpc":"2.0","id":1,"method":"ping"}` {
		t.Errorf("unexpected probe %s %s %v %s", r.Method, r.URL.Path, r.Header, body)
	}
}
