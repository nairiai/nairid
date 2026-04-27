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
	// Same input should always produce the same output
	input := "github.com/owner/repo"
	result1 := SanitizeNamespace(input)
	result2 := SanitizeNamespace(input)
	if result1 != result2 {
		t.Errorf("SanitizeNamespace should be deterministic: got %q and %q for same input", result1, result2)
	}
}

func TestSanitizeNamespace_DifferentInputsDifferentOutputs(t *testing.T) {
	// Different repo identifiers should produce different namespaces
	ns1 := SanitizeNamespace("github.com/owner/repo-a")
	ns2 := SanitizeNamespace("github.com/owner/repo-b")
	if ns1 == ns2 {
		t.Errorf("Different inputs should produce different outputs: both got %q", ns1)
	}
}

func TestResolveInstanceNamespace_ExplicitAgentID(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "namespace-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create a minimal EnvManager
	em := &EnvManager{
		envVars:  map[string]string{"NAIRI_AGENT_ID": "my-agent"},
		envPath:  filepath.Join(tempDir, ".env"),
		stopChan: make(chan struct{}),
	}

	ns, err := ResolveInstanceNamespace(em, "github.com/owner/repo")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if ns != "my-agent" {
		t.Errorf("Expected 'my-agent', got %q", ns)
	}
}

func TestResolveInstanceNamespace_LegacyAgentID(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "namespace-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	em := &EnvManager{
		envVars:  map[string]string{"EKSEC_AGENT_ID": "legacy-agent"},
		envPath:  filepath.Join(tempDir, ".env"),
		stopChan: make(chan struct{}),
	}

	ns, err := ResolveInstanceNamespace(em, "github.com/owner/repo")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if ns != "legacy-agent" {
		t.Errorf("Expected 'legacy-agent', got %q", ns)
	}
}

func TestResolveInstanceNamespace_NairiOverridesLegacy(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "namespace-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	em := &EnvManager{
		envVars: map[string]string{
			"NAIRI_AGENT_ID": "nairi-agent",
			"EKSEC_AGENT_ID": "legacy-agent",
		},
		envPath:  filepath.Join(tempDir, ".env"),
		stopChan: make(chan struct{}),
	}

	ns, err := ResolveInstanceNamespace(em, "github.com/owner/repo")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if ns != "nairi-agent" {
		t.Errorf("Expected 'nairi-agent' (NAIRI takes precedence), got %q", ns)
	}
}

func TestResolveInstanceNamespace_FallbackToRepoIdentifier(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "namespace-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Clear any NAIRI_AGENT_ID / EKSEC_AGENT_ID from the process environment
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

	em := &EnvManager{
		envVars:  make(map[string]string),
		envPath:  filepath.Join(tempDir, ".env"),
		stopChan: make(chan struct{}),
	}

	ns, err := ResolveInstanceNamespace(em, "github.com/owner/repo")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if ns != "github.com__owner__repo" {
		t.Errorf("Expected 'github.com__owner__repo', got %q", ns)
	}
}

func TestResolveInstanceNamespace_NoIdentifier(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "namespace-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Clear any NAIRI_AGENT_ID / EKSEC_AGENT_ID from the process environment
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

	em := &EnvManager{
		envVars:  make(map[string]string),
		envPath:  filepath.Join(tempDir, ".env"),
		stopChan: make(chan struct{}),
	}

	_, err = ResolveInstanceNamespace(em, "")
	if err == nil {
		t.Error("Expected error when no identifier available, got nil")
	}
}

func TestResolveInstanceNamespace_AgentIDWithSpecialChars(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "namespace-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	em := &EnvManager{
		envVars:  map[string]string{"NAIRI_AGENT_ID": "org/agent-name"},
		envPath:  filepath.Join(tempDir, ".env"),
		stopChan: make(chan struct{}),
	}

	ns, err := ResolveInstanceNamespace(em, "")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if ns != "org__agent-name" {
		t.Errorf("Expected 'org__agent-name', got %q", ns)
	}
}
