package env

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeNamespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple agent ID",
			input:    "my-agent",
			expected: "my-agent",
		},
		{
			name:     "github repo identifier",
			input:    "github.com/owner/repo",
			expected: "github.com__owner__repo",
		},
		{
			name:     "github repo with .git suffix",
			input:    "github.com/owner/repo.git",
			expected: "github.com__owner__repo.git",
		},
		{
			name:     "agent ID with spaces",
			input:    "agent with spaces",
			expected: "agent-with-spaces",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "default",
		},
		{
			name:     "only dots",
			input:    "...",
			expected: "default",
		},
		{
			name:     "only hyphens",
			input:    "---",
			expected: "default",
		},
		{
			name:     "backslashes",
			input:    "path\\to\\repo",
			expected: "path__to__repo",
		},
		{
			name:     "colons",
			input:    "host:port",
			expected: "host__port",
		},
		{
			name:     "special characters",
			input:    "agent@org!#$",
			expected: "agent-org",
		},
		{
			name:     "alphanumeric with underscores",
			input:    "eksecbackend",
			expected: "eksecbackend",
		},
		{
			name:     "docker-style agent ID",
			input:    "claude-rc-router",
			expected: "claude-rc-router",
		},
		{
			name:     "nested github path",
			input:    "github.com/nairiai/nairid",
			expected: "github.com__nairiai__nairid",
		},
		{
			name:     "leading dot removed",
			input:    ".hidden",
			expected: "hidden",
		},
		{
			name:     "trailing dot removed",
			input:    "name.",
			expected: "name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeNamespace(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizeNamespace(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSanitizeNamespace_Deterministic(t *testing.T) {
	input := "github.com/owner/repo"
	result1 := SanitizeNamespace(input)
	result2 := SanitizeNamespace(input)
	if result1 != result2 {
		t.Errorf("SanitizeNamespace should be deterministic: got %q and %q for same input", result1, result2)
	}
}

func TestSanitizeNamespace_DifferentInputsDifferentOutputs(t *testing.T) {
	ns1 := SanitizeNamespace("github.com/owner/repo-a")
	ns2 := SanitizeNamespace("github.com/owner/repo-b")
	if ns1 == ns2 {
		t.Errorf("Different inputs should produce different outputs: both got %q", ns1)
	}
}

// --- ResolveInstanceNamespace tests ---

func newTestEnvManager(t *testing.T, vars map[string]string) *EnvManager {
	t.Helper()
	tempDir := t.TempDir()
	return &EnvManager{
		envVars:  vars,
		envPath:  filepath.Join(tempDir, ".env"),
		stopChan: make(chan struct{}),
	}
}

func TestResolveInstanceNamespace_ExplicitAgentID(t *testing.T) {
	em := newTestEnvManager(t, map[string]string{"NAIRI_AGENT_ID": "my-agent"})
	ns, err := ResolveInstanceNamespace(em, "github.com/owner/repo")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if ns != "my-agent" {
		t.Errorf("Expected 'my-agent', got %q", ns)
	}
}

func TestResolveInstanceNamespace_LegacyAgentID(t *testing.T) {
	em := newTestEnvManager(t, map[string]string{"EKSEC_AGENT_ID": "legacy-agent"})
	ns, err := ResolveInstanceNamespace(em, "github.com/owner/repo")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if ns != "legacy-agent" {
		t.Errorf("Expected 'legacy-agent', got %q", ns)
	}
}

func TestResolveInstanceNamespace_NairiOverridesLegacy(t *testing.T) {
	em := newTestEnvManager(t, map[string]string{
		"NAIRI_AGENT_ID": "nairi-agent",
		"EKSEC_AGENT_ID": "legacy-agent",
	})
	ns, err := ResolveInstanceNamespace(em, "github.com/owner/repo")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if ns != "nairi-agent" {
		t.Errorf("Expected 'nairi-agent' (NAIRI takes precedence), got %q", ns)
	}
}

func TestResolveInstanceNamespace_FallbackToRepoIdentifier(t *testing.T) {
	originalNairi := os.Getenv("NAIRI_AGENT_ID")
	originalEksec := os.Getenv("EKSEC_AGENT_ID")
	_ = os.Unsetenv("NAIRI_AGENT_ID")
	_ = os.Unsetenv("EKSEC_AGENT_ID")
	defer func() {
		if originalNairi != "" {
			_ = os.Setenv("NAIRI_AGENT_ID", originalNairi)
		}
		if originalEksec != "" {
			_ = os.Setenv("EKSEC_AGENT_ID", originalEksec)
		}
	}()

	em := newTestEnvManager(t, map[string]string{})
	ns, err := ResolveInstanceNamespace(em, "github.com/owner/repo")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if ns != "github.com__owner__repo" {
		t.Errorf("Expected 'github.com__owner__repo', got %q", ns)
	}
}

func TestResolveInstanceNamespace_NoIdentifier(t *testing.T) {
	originalNairi := os.Getenv("NAIRI_AGENT_ID")
	originalEksec := os.Getenv("EKSEC_AGENT_ID")
	_ = os.Unsetenv("NAIRI_AGENT_ID")
	_ = os.Unsetenv("EKSEC_AGENT_ID")
	defer func() {
		if originalNairi != "" {
			_ = os.Setenv("NAIRI_AGENT_ID", originalNairi)
		}
		if originalEksec != "" {
			_ = os.Setenv("EKSEC_AGENT_ID", originalEksec)
		}
	}()

	em := newTestEnvManager(t, map[string]string{})
	_, err := ResolveInstanceNamespace(em, "")
	if err == nil {
		t.Error("Expected error when no identifier available, got nil")
	}
}

func TestResolveInstanceNamespace_AgentIDWithSpecialChars(t *testing.T) {
	em := newTestEnvManager(t, map[string]string{"NAIRI_AGENT_ID": "org/agent-name"})
	ns, err := ResolveInstanceNamespace(em, "")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if ns != "org__agent-name" {
		t.Errorf("Expected 'org__agent-name', got %q", ns)
	}
}

// --- NamespacedInstanceDir tests ---

func TestNamespacedInstanceDir_CreatesDirectory(t *testing.T) {
	configDir := t.TempDir()

	dir, err := NamespacedInstanceDir(configDir, "my-agent")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	expected := filepath.Join(configDir, "instances", "my-agent")
	if dir != expected {
		t.Errorf("Expected %q, got %q", expected, dir)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Expected directory to exist: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("Expected %s to be a directory", dir)
	}
}

func TestNamespacedInstanceDir_DifferentNamespaces(t *testing.T) {
	configDir := t.TempDir()

	dir1, err := NamespacedInstanceDir(configDir, "agent-a")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	dir2, err := NamespacedInstanceDir(configDir, "agent-b")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if dir1 == dir2 {
		t.Errorf("Different namespaces should produce different directories, both got %q", dir1)
	}
}

func TestNamespacedInstanceDir_Idempotent(t *testing.T) {
	configDir := t.TempDir()

	dir1, _ := NamespacedInstanceDir(configDir, "agent")
	dir2, _ := NamespacedInstanceDir(configDir, "agent")
	if dir1 != dir2 {
		t.Errorf("Expected same result on repeated calls: %q vs %q", dir1, dir2)
	}
}

// --- NamespacedStatePath tests ---

func TestNamespacedStatePath(t *testing.T) {
	instanceDir := "/home/user/.config/eksecd/instances/my-agent"
	result := NamespacedStatePath(instanceDir)
	expected := filepath.Join(instanceDir, "state.json")
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

// --- NamespacedLogsDir tests ---

func TestNamespacedLogsDir(t *testing.T) {
	instanceDir := "/home/user/.config/eksecd/instances/my-agent"
	result := NamespacedLogsDir(instanceDir)
	expected := filepath.Join(instanceDir, "logs")
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

// --- MigrateLegacyState tests ---

func TestMigrateLegacyState_CopiesWhenNamespacedMissing(t *testing.T) {
	configDir := t.TempDir()

	// Create legacy state.json
	legacyContent := []byte(`{"agent_id":"test-123","jobs":{}}`)
	if err := os.WriteFile(filepath.Join(configDir, "state.json"), legacyContent, 0644); err != nil {
		t.Fatalf("Failed to write legacy state: %v", err)
	}

	// Create namespaced dir but NOT state.json
	namespacedDir := filepath.Join(configDir, "instances", "test-agent")
	if err := os.MkdirAll(namespacedDir, 0755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}
	namespacedPath := filepath.Join(namespacedDir, "state.json")

	MigrateLegacyState(configDir, namespacedPath)

	// Verify migration
	data, err := os.ReadFile(namespacedPath)
	if err != nil {
		t.Fatalf("Expected namespaced state to exist after migration: %v", err)
	}
	if string(data) != string(legacyContent) {
		t.Errorf("Expected migrated content %q, got %q", string(legacyContent), string(data))
	}
}

func TestMigrateLegacyState_SkipsWhenNamespacedExists(t *testing.T) {
	configDir := t.TempDir()

	// Create legacy state.json with OLD content
	if err := os.WriteFile(filepath.Join(configDir, "state.json"), []byte("old"), 0644); err != nil {
		t.Fatalf("Failed to write legacy state: %v", err)
	}

	// Create namespaced state.json with CURRENT content
	namespacedDir := filepath.Join(configDir, "instances", "test-agent")
	if err := os.MkdirAll(namespacedDir, 0755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}
	namespacedPath := filepath.Join(namespacedDir, "state.json")
	if err := os.WriteFile(namespacedPath, []byte("current"), 0644); err != nil {
		t.Fatalf("Failed to write namespaced state: %v", err)
	}

	MigrateLegacyState(configDir, namespacedPath)

	// Verify namespaced state was NOT overwritten
	data, _ := os.ReadFile(namespacedPath)
	if string(data) != "current" {
		t.Errorf("Expected namespaced state to remain 'current', got %q", string(data))
	}
}

func TestMigrateLegacyState_NoopWhenNoLegacy(t *testing.T) {
	configDir := t.TempDir()

	namespacedDir := filepath.Join(configDir, "instances", "test-agent")
	if err := os.MkdirAll(namespacedDir, 0755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}
	namespacedPath := filepath.Join(namespacedDir, "state.json")

	// Should not panic or error — just silently skip
	MigrateLegacyState(configDir, namespacedPath)

	// Verify no file was created
	if _, err := os.Stat(namespacedPath); !os.IsNotExist(err) {
		t.Errorf("Expected no file to be created when no legacy state exists")
	}
}
