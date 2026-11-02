package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	toml "github.com/pelletier/go-toml/v2"

	"nairid/clients"
	"nairid/core/env"
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

var deployedRules = map[string]string{
	"claude":   filepath.Join(".claude", "rules"),
	"opencode": filepath.Join(".config", "opencode", "AGENTS.md"),
	"codex":    filepath.Join(".codex", "AGENTS.md"),
}

var mcpSectionKeys = map[string]string{"claude": "mcpServers", "opencode": "mcp", "codex": "mcp_servers"}

func readAgentConfig(t *testing.T, agentType, agentHome string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(agentHome, agentConfigPaths[agentType]))
	if err != nil {
		t.Fatal(err)
	}
	unmarshal := json.Unmarshal
	if agentType == "codex" {
		unmarshal = toml.Unmarshal
	}
	config := map[string]any{}
	if err := unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

// assertKeptSettings checks the refresh left the user's settings and OpenCode's permissions alone.
// OpenCode's rules step deletes opencode.json, so its theme goes with any rule, as at startup.
func assertKeptSettings(t *testing.T, agentType string, config map[string]any) {
	t.Helper()
	if agentType != "opencode" && config["theme"] != "dark" {
		t.Errorf("expected the user's theme kept, got %v", config)
	}
	if _, ok := config["permission"]; agentType == "opencode" && !ok {
		t.Errorf("expected OpenCode's permission section kept, got %v", config)
	}
}

func TestAgentMCPConfigRefresh_RemovesTheLastRuleAndMCPServer(t *testing.T) {
	for _, agentType := range []string{"claude", "opencode", "codex"} {
		t.Run(agentType, func(t *testing.T) {
			backend := &fakeBackend{rule: "Use tabs.\n"}
			cfg := agentRun{agentType: agentType, keyReplies: []reply{unknownAgent}, backend: backend}.start(t)
			rules := filepath.Join(cfg.targetHomeDir, deployedRules[agentType])

			if err := cfg.refresh(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(rules); err != nil {
				t.Fatalf("expected the rule deployed at %s: %v", rules, err)
			}
			config := readAgentConfig(t, agentType, cfg.targetHomeDir)
			if config[mcpSectionKeys[agentType]] == nil {
				t.Fatalf("expected MCP servers deployed, got %v", config)
			}
			assertKeptSettings(t, agentType, config)

			backend.rule, backend.noMCP = "", true
			if err := cfg.refresh(); err != nil {
				t.Fatal(err)
			}

			if _, err := os.Stat(rules); !os.IsNotExist(err) {
				t.Errorf("expected the last rule removed from %s, stat err %v", rules, err)
			}
			config = readAgentConfig(t, agentType, cfg.targetHomeDir)
			if servers, ok := config[mcpSectionKeys[agentType]]; ok {
				t.Errorf("expected the last MCP server removed, got %v", servers)
			}
			assertKeptSettings(t, agentType, config)
		})
	}
}

// With nothing deployed by nairid, an empty backend leaves the user's own rules and MCP servers.
func TestAgentMCPConfigRefresh_KeepsWhatTheUserWrote(t *testing.T) {
	userRules := map[string]string{
		"claude":   filepath.Join(".claude", "rules", "mine.md"),
		"opencode": filepath.Join(".config", "opencode", "AGENTS.md"),
		"codex":    filepath.Join(".codex", "AGENTS.md"),
	}
	for _, agentType := range []string{"claude", "opencode", "codex"} {
		t.Run(agentType, func(t *testing.T) {
			backend := &fakeBackend{noMCP: true}
			cfg := agentRun{agentType: agentType, keyReplies: []reply{unknownAgent}, backend: backend}.start(t)
			rule := filepath.Join(cfg.targetHomeDir, userRules[agentType])
			if err := os.MkdirAll(filepath.Dir(rule), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rule, []byte("# My rules\n"), 0644); err != nil {
				t.Fatal(err)
			}

			if err := cfg.refresh(); err != nil {
				t.Fatal(err)
			}

			if _, err := os.Stat(rule); err != nil {
				t.Errorf("expected the user's rules kept at %s: %v", rule, err)
			}
			_, servers := readAgentMCPServers(t, agentType, cfg.targetHomeDir)
			if got := serverNames(servers); !reflect.DeepEqual(got, []string{"stale"}) {
				t.Errorf("MCP servers = %v, want the user's [stale]", got)
			}
		})
	}
}

func TestFetchAndApplyEnvVars_DropsWhatTheBackendRemoved(t *testing.T) {
	t.Setenv("NAIRI_CONFIG_DIR", t.TempDir())
	t.Setenv("REFRESH_TEST_LOCAL", "mine")
	t.Setenv("REFRESH_TEST_ONLY_BACKEND", "")
	_ = os.Unsetenv("REFRESH_TEST_ONLY_BACKEND")
	backend := &fakeBackend{}
	ts := backend.serve(t)
	client := clients.NewAgentsApiClient(selfHostedAPIKey, ts.URL, "ag_123")
	envManager, err := env.NewEnvManager()
	if err != nil {
		t.Fatal(err)
	}

	apply := func(reply string) {
		t.Helper()
		backend.envVars = reply
		if err := fetchAndApplyEnvVars(client, envManager); err != nil {
			t.Fatal(err)
		}
	}
	assertEnv := func(key, want string, wantSet bool) {
		t.Helper()
		got, set := os.LookupEnv(key)
		if got != want || set != wantSet || envManager.Get(key) != want {
			t.Errorf("%s = %q (set %v, manager %q), want %q (set %v)",
				key, got, set, envManager.Get(key), want, wantSet)
		}
	}

	apply(`[{"key":"REFRESH_TEST_LOCAL","value":"backend"},{"key":"REFRESH_TEST_ONLY_BACKEND","value":"b"}]`)
	assertEnv("REFRESH_TEST_LOCAL", "backend", true)
	assertEnv("REFRESH_TEST_ONLY_BACKEND", "b", true)

	apply(`[{"key":"REFRESH_TEST_LOCAL","value":"backend"}]`)
	assertEnv("REFRESH_TEST_ONLY_BACKEND", "", false)
	assertEnv("REFRESH_TEST_LOCAL", "backend", true)

	apply(`[]`)
	assertEnv("REFRESH_TEST_LOCAL", "mine", true)
}
