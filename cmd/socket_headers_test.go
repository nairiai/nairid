package main

import (
	"testing"

	"nairid/clients"
	"nairid/core"
)

func TestSocketHandshakeHeadersSendNairidVersion(t *testing.T) {
	headers := socketHandshakeHeaders("key", "ccagent-id", "repo", "agent")

	got := headers[clients.NairidVersionHeader]
	if len(got) != 1 || got[0] == "" || got[0] != core.GetVersion() {
		t.Fatalf("%s = %v, want [%s]", clients.NairidVersionHeader, got, core.GetVersion())
	}
	if headers["X-AGENT-ID"][0] != "agent" || headers["X-CCAGENT-ID"][0] != "ccagent-id" {
		t.Fatalf("auth headers changed: %v", headers)
	}
}
