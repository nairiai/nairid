package clients

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchMCPKey(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantKey string
		wantErr bool
	}{
		{name: "returns the key", status: http.StatusOK, body: `{"key":"mcpk_test"}`, wantKey: "mcpk_test"},
		{name: "gateway not enabled", status: http.StatusNotImplemented, body: `{"error":"not enabled"}`, wantErr: true},
		{name: "older backend", status: http.StatusNotFound, body: `404 page not found`, wantErr: true},
		{name: "empty key", status: http.StatusOK, body: `{"key":""}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/agents/mcp-key" || r.Header.Get("Authorization") != "Bearer ccagent_test" || r.Header.Get("X-AGENT-ID") != "ag_123" {
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
		})
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
		t.Errorf("expected no %s before UseMCPGateway, got %q", NairidVersionHeader, gotVersion)
	}

	client.UseMCPGateway("1.2.3")
	if _, err := client.FetchAttachment("att_123"); err != nil {
		t.Fatal(err)
	}
	if gotVersion != "1.2.3" {
		t.Errorf("%s = %q, want 1.2.3", NairidVersionHeader, gotVersion)
	}
}
