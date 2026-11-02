package main

import (
	"reflect"
	"testing"

	"nairid/clients"
)

func TestAgentMCPConfigRefresh(t *testing.T) {
	keyOK := []reply{selfHostedKeyReply}
	changedConfig := `{"mcpServers":{` + stdioServer + `,"linear":{"type":"http","url":"https://mcp.linear.app/mcp"}}}`
	tests := []struct {
		name         string
		keyReplies   []reply
		changeConfig bool
		wantKeyCalls int
		wantGateway  bool
		wantServers  []string
	}{
		{
			name:       "first boot got the unknown agent 400, refresh moves to the gateway",
			keyReplies: []reply{unknownAgent, selfHostedKeyReply}, wantKeyCalls: 2,
			wantGateway: true, wantServers: []string{"fs", "nairi", "private"},
		},
		{
			name: "still unknown, refresh keeps remote servers local", keyReplies: []reply{unknownAgent},
			wantKeyCalls: 2, wantServers: []string{"composio", "fs", "private"},
		},
		{
			name: "on the gateway, refresh picks up a changed MCP server", keyReplies: keyOK, changeConfig: true,
			wantKeyCalls: 1, wantGateway: true, wantServers: []string{"fs", "linear", "nairi"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &fakeBackend{}
			cfg := agentRun{agentType: "claude", keyReplies: tt.keyReplies, backend: backend}.start(t)
			if tt.changeConfig {
				backend.strippedConfig = changedConfig
			}

			if err := cfg.refresh(); err != nil {
				t.Fatal(err)
			}

			if got := cfg.current().nairi != nil; got != tt.wantGateway {
				t.Errorf("on the gateway after refresh = %v, want %v", got, tt.wantGateway)
			}
			if backend.keyCalls != tt.wantKeyCalls || backend.mcpDownloads != 2 || backend.stripAsked != tt.wantGateway {
				t.Errorf("key calls = %d, MCP config downloads = %d, last with %s: %v; want %d, 2, %v",
					backend.keyCalls, backend.mcpDownloads, clients.NairidVersionHeader, backend.stripAsked,
					tt.wantKeyCalls, tt.wantGateway)
			}
			_, servers := readAgentMCPServers(t, "claude", cfg.targetHomeDir)
			if got := serverNames(servers); !reflect.DeepEqual(got, tt.wantServers) {
				t.Errorf("MCP servers = %v, want %v", got, tt.wantServers)
			}
		})
	}
}

// A managed agent off the gateway re-probes on its own timer; a refresh only rewrites its config.
func TestAgentMCPConfigRefresh_ManagedKeepsItsSetup(t *testing.T) {
	backend := &fakeBackend{}
	cfg := agentRun{agentType: "claude", managed: true, keyReplies: []reply{notEnabled}, backend: backend}.start(t)
	before, keyCalls := cfg.current(), backend.keyCalls

	if err := cfg.refresh(); err != nil {
		t.Fatal(err)
	}

	if backend.keyCalls != keyCalls || !reflect.DeepEqual(cfg.current(), before) {
		t.Errorf("refresh set the agent up again: key calls %d -> %d, setup %+v -> %+v",
			keyCalls, backend.keyCalls, before, cfg.current())
	}
	assertStdioOnlyConfig(t, "claude", cfg.targetHomeDir, []string{"fs"})
}
