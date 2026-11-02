package utils

import (
	"path/filepath"
	"testing"
)

func TestMCPDeployedKeepsPathsWithSpaces(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", "")
	first := filepath.Join("/Users", "Jane Doe", ".claude.json")
	second := filepath.Join("/Users", "Jane Doe", ".codex", "config.toml")

	for _, p := range []string{first, second} {
		if err := recordMCPDeployed(p); err != nil {
			t.Fatal(err)
		}
	}
	if !mcpWasDeployed(first) || !mcpWasDeployed(second) {
		t.Fatalf("recorded paths not found: %q", readMCPDeployed())
	}
	if err := forgetMCPDeployed(first); err != nil {
		t.Fatal(err)
	}
	if mcpWasDeployed(first) || !mcpWasDeployed(second) {
		t.Errorf("after forgetting %q the record is %q", first, readMCPDeployed())
	}
}
