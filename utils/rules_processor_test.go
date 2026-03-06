package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test front matter parsing

func TestParseFrontMatter_Valid(t *testing.T) {
	// Create temporary file with front matter
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test-rule.md")

	content := `---
title: Code Style Guidelines
description: Use this to learn what style guidelines to follow in this codebase
---

# Code Style

Some content here.`

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Parse front matter
	frontMatter, err := ParseFrontMatter(testFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if frontMatter.Title != "Code Style Guidelines" {
		t.Errorf("Expected title 'Code Style Guidelines', got: %s", frontMatter.Title)
	}

	if frontMatter.Description != "Use this to learn what style guidelines to follow in this codebase" {
		t.Errorf("Expected description, got: %s", frontMatter.Description)
	}
}

func TestParseFrontMatter_NoFrontMatter(t *testing.T) {
	// Create temporary file without front matter
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test-rule.md")

	content := `# Code Style

Some content without front matter.`

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Parse front matter
	frontMatter, err := ParseFrontMatter(testFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should return empty front matter
	if frontMatter.Title != "" {
		t.Errorf("Expected empty title, got: %s", frontMatter.Title)
	}

	if frontMatter.Description != "" {
		t.Errorf("Expected empty description, got: %s", frontMatter.Description)
	}
}

func TestParseFrontMatter_PartialFrontMatter(t *testing.T) {
	// Create temporary file with partial front matter
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test-rule.md")

	content := `---
title: Code Style Guidelines
---

# Code Style`

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Parse front matter
	frontMatter, err := ParseFrontMatter(testFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if frontMatter.Title != "Code Style Guidelines" {
		t.Errorf("Expected title 'Code Style Guidelines', got: %s", frontMatter.Title)
	}

	if frontMatter.Description != "" {
		t.Errorf("Expected empty description, got: %s", frontMatter.Description)
	}
}

func TestParseFrontMatter_CaseInsensitive(t *testing.T) {
	// Create temporary file with mixed case keys
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test-rule.md")

	content := `---
Title: Code Style Guidelines
Description: Use this guide
---

# Code Style`

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Parse front matter
	frontMatter, err := ParseFrontMatter(testFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if frontMatter.Title != "Code Style Guidelines" {
		t.Errorf("Expected title, got: %s", frontMatter.Title)
	}

	if frontMatter.Description != "Use this guide" {
		t.Errorf("Expected description, got: %s", frontMatter.Description)
	}
}

// Test ReadRuleBody

func TestReadRuleBody_WithFrontMatter(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "code-style.md")

	content := `---
title: Code Style Guidelines
description: Use this to learn coding standards
---

# Code Style
Follow these guidelines.`

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	title, description, body, err := ReadRuleBody(testFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if title != "Code Style Guidelines" {
		t.Errorf("Expected title 'Code Style Guidelines', got: %s", title)
	}

	if description != "Use this to learn coding standards" {
		t.Errorf("Expected description 'Use this to learn coding standards', got: %s", description)
	}

	expectedBody := "# Code Style\nFollow these guidelines."
	if body != expectedBody {
		t.Errorf("Expected body %q, got: %q", expectedBody, body)
	}
}

func TestReadRuleBody_WithoutFrontMatter(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "my-rules.md")

	content := `# Some Rules
Do these things.`

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	title, description, body, err := ReadRuleBody(testFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if title != "my-rules" {
		t.Errorf("Expected title 'my-rules', got: %s", title)
	}

	if description != "" {
		t.Errorf("Expected empty description, got: %s", description)
	}

	if body != content {
		t.Errorf("Expected body %q, got: %q", content, body)
	}
}

func TestReadRuleBody_FrontMatterWithoutTitle(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "testing.md")

	content := `---
description: Some description
---

Write tests.`

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	title, description, body, err := ReadRuleBody(testFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if title != "testing" {
		t.Errorf("Expected title 'testing', got: %s", title)
	}

	if description != "Some description" {
		t.Errorf("Expected description 'Some description', got: %s", description)
	}

	if body != "Write tests." {
		t.Errorf("Expected body 'Write tests.', got: %q", body)
	}
}

// Test GetRuleFiles

func TestGetRuleFiles_EmptyDirectory(t *testing.T) {
	// Create temporary nairid rules directory
	tempDir := t.TempDir()
	rulesDir := filepath.Join(tempDir, ".config", "eksecd", "rules")

	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("Failed to create rules directory: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Get rule files
	files, err := GetRuleFiles()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("Expected 0 files, got: %d", len(files))
	}
}

func TestGetRuleFiles_WithMarkdownFiles(t *testing.T) {
	// Create temporary nairid rules directory
	tempDir := t.TempDir()
	rulesDir := filepath.Join(tempDir, ".config", "eksecd", "rules")

	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("Failed to create rules directory: %v", err)
	}

	// Create test markdown files
	testFiles := []string{"rule1.md", "rule2.md", "rule3.MD"}
	for _, file := range testFiles {
		filePath := filepath.Join(rulesDir, file)
		if err := os.WriteFile(filePath, []byte("# Test"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Create non-markdown file (should be ignored)
	if err := os.WriteFile(filepath.Join(rulesDir, "readme.txt"), []byte("text"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Get rule files
	files, err := GetRuleFiles()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(files) != 3 {
		t.Errorf("Expected 3 files, got: %d", len(files))
	}
}

func TestGetRuleFiles_NonexistentDirectory(t *testing.T) {
	// Use temporary directory that doesn't have rules directory
	tempDir := t.TempDir()

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Get rule files (should return empty list, not error)
	files, err := GetRuleFiles()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("Expected 0 files, got: %d", len(files))
	}
}

// Test CleanEksecdRulesDir

func TestCleanEksecdRulesDir_WithExistingRules(t *testing.T) {
	// Create temporary nairid rules directory with files
	tempDir := t.TempDir()
	rulesDir := filepath.Join(tempDir, ".config", "eksecd", "rules")

	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("Failed to create rules directory: %v", err)
	}

	// Create test rule files
	testFiles := []string{"rule1.md", "rule2.md", "stale-rule.md"}
	for _, file := range testFiles {
		filePath := filepath.Join(rulesDir, file)
		if err := os.WriteFile(filePath, []byte("# Test Rule"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Clean the rules directory
	if err := CleanEksecdRulesDir(); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify directory exists but is empty
	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		t.Fatalf("Failed to read rules directory: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("Expected empty directory, found %d files", len(entries))
	}
}

func TestCleanEksecdRulesDir_NonexistentDirectory(t *testing.T) {
	// Use temporary directory that doesn't have rules directory
	tempDir := t.TempDir()

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Clean should succeed even if directory doesn't exist
	if err := CleanEksecdRulesDir(); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

func TestCleanEksecdRulesDir_RecreatesDirectory(t *testing.T) {
	// Create temporary nairid rules directory
	tempDir := t.TempDir()
	rulesDir := filepath.Join(tempDir, ".config", "eksecd", "rules")

	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("Failed to create rules directory: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Clean the rules directory
	if err := CleanEksecdRulesDir(); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify directory still exists (was recreated)
	if _, err := os.Stat(rulesDir); os.IsNotExist(err) {
		t.Errorf("Expected rules directory to be recreated")
	}
}

// Test ClaudeCodeRulesProcessor

func TestClaudeCodeRulesProcessor_NoRules(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process rules (should succeed with no rules)
	processor := NewClaudeCodeRulesProcessor(workDir)
	if err := processor.ProcessRules(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify no .claude/rules directory was created in home directory
	claudeRulesDir := filepath.Join(tempDir, ".claude", "rules")
	if _, err := os.Stat(claudeRulesDir); !os.IsNotExist(err) {
		// Directory should not exist if no rules
		// Actually, it will be created but empty - let's check if it's empty
		entries, _ := os.ReadDir(claudeRulesDir)
		if len(entries) > 0 {
			t.Errorf("Expected empty rules directory, found %d files", len(entries))
		}
	}
}

func TestClaudeCodeRulesProcessor_WithRules(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	rulesDir := filepath.Join(tempDir, ".config", "eksecd", "rules")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("Failed to create rules directory: %v", err)
	}

	// Create test rule files
	testRules := map[string]string{
		"code-style.md": "# Code Style\nFollow these guidelines.",
		"testing.md":    "# Testing\nWrite tests for everything.",
	}

	for filename, content := range testRules {
		filePath := filepath.Join(rulesDir, filename)
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create test rule: %v", err)
		}
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process rules
	processor := NewClaudeCodeRulesProcessor(workDir)
	if err := processor.ProcessRules(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify .claude/rules directory was created in home directory
	claudeRulesDir := filepath.Join(tempDir, ".claude", "rules")
	if _, err := os.Stat(claudeRulesDir); os.IsNotExist(err) {
		t.Fatalf("Expected .claude/rules directory to exist")
	}

	// Verify rule files were copied
	for filename, expectedContent := range testRules {
		destPath := filepath.Join(claudeRulesDir, filename)

		content, err := os.ReadFile(destPath)
		if err != nil {
			t.Errorf("Expected rule file %s to exist, got error: %v", filename, err)
			continue
		}

		if string(content) != expectedContent {
			t.Errorf("Expected content '%s', got '%s'", expectedContent, string(content))
		}
	}
}

func TestClaudeCodeRulesProcessor_RemovesStaleRules(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	rulesDir := filepath.Join(tempDir, ".config", "eksecd", "rules")
	claudeRulesDir := filepath.Join(tempDir, ".claude", "rules")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("Failed to create rules directory: %v", err)
	}

	if err := os.MkdirAll(claudeRulesDir, 0755); err != nil {
		t.Fatalf("Failed to create claude rules directory: %v", err)
	}

	// Create a stale rule in .claude/rules (in home directory)
	staleRulePath := filepath.Join(claudeRulesDir, "stale-rule.md")
	if err := os.WriteFile(staleRulePath, []byte("# Stale"), 0644); err != nil {
		t.Fatalf("Failed to create stale rule: %v", err)
	}

	// Create a fresh rule in nairid rules directory
	freshRulePath := filepath.Join(rulesDir, "fresh-rule.md")
	if err := os.WriteFile(freshRulePath, []byte("# Fresh"), 0644); err != nil {
		t.Fatalf("Failed to create fresh rule: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process rules
	processor := NewClaudeCodeRulesProcessor(workDir)
	if err := processor.ProcessRules(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify stale rule was removed
	if _, err := os.Stat(staleRulePath); !os.IsNotExist(err) {
		t.Errorf("Expected stale rule to be removed")
	}

	// Verify fresh rule exists
	freshDestPath := filepath.Join(claudeRulesDir, "fresh-rule.md")
	if _, err := os.Stat(freshDestPath); os.IsNotExist(err) {
		t.Errorf("Expected fresh rule to exist")
	}
}

// Test OpenCodeRulesProcessor

func TestOpenCodeRulesProcessor_NoRules(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process rules (should succeed with no rules)
	processor := NewOpenCodeRulesProcessor(workDir)
	if err := processor.ProcessRules(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify AGENTS.md was not created when no rules
	agentsMdPath := filepath.Join(tempDir, ".config", "opencode", "AGENTS.md")
	if _, err := os.Stat(agentsMdPath); !os.IsNotExist(err) {
		t.Errorf("Expected AGENTS.md not to exist when no rules")
	}
}

func TestOpenCodeRulesProcessor_WithRules(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	rulesDir := filepath.Join(tempDir, ".config", "eksecd", "rules")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("Failed to create rules directory: %v", err)
	}

	// Create test rule files with front matter
	rule1 := `---
title: Code Style Guidelines
description: Use this to learn coding standards
---

# Code Style
Follow these guidelines.`

	rule2 := `---
title: Testing Best Practices
description: How to write effective tests
---

# Testing
Write tests for everything.`

	if err := os.WriteFile(filepath.Join(rulesDir, "code-style.md"), []byte(rule1), 0644); err != nil {
		t.Fatalf("Failed to create rule1: %v", err)
	}

	if err := os.WriteFile(filepath.Join(rulesDir, "testing.md"), []byte(rule2), 0644); err != nil {
		t.Fatalf("Failed to create rule2: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process rules
	processor := NewOpenCodeRulesProcessor(workDir)
	if err := processor.ProcessRules(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify AGENTS.md was created
	agentsMdPath := filepath.Join(tempDir, ".config", "opencode", "AGENTS.md")
	content, err := os.ReadFile(agentsMdPath)
	if err != nil {
		t.Fatalf("Expected AGENTS.md to exist, got error: %v", err)
	}

	agentsMd := string(content)

	// Verify header
	if !strings.Contains(agentsMd, "# Rules") {
		t.Errorf("Expected AGENTS.md to contain '# Rules' header")
	}

	if !strings.Contains(agentsMd, "The following is a list of rules") {
		t.Errorf("Expected AGENTS.md to contain preamble text")
	}

	// Verify rule titles are present as ## headings
	if !strings.Contains(agentsMd, "## Code Style Guidelines") {
		t.Errorf("Expected AGENTS.md to contain '## Code Style Guidelines'")
	}

	if !strings.Contains(agentsMd, "## Testing Best Practices") {
		t.Errorf("Expected AGENTS.md to contain '## Testing Best Practices'")
	}

	// Verify descriptions are present
	if !strings.Contains(agentsMd, "Use this to learn coding standards") {
		t.Errorf("Expected AGENTS.md to contain description for code style rule")
	}

	if !strings.Contains(agentsMd, "How to write effective tests") {
		t.Errorf("Expected AGENTS.md to contain description for testing rule")
	}

	// Verify body content is present (without front matter)
	if !strings.Contains(agentsMd, "# Code Style\nFollow these guidelines.") {
		t.Errorf("Expected AGENTS.md to contain body of code style rule")
	}

	if !strings.Contains(agentsMd, "# Testing\nWrite tests for everything.") {
		t.Errorf("Expected AGENTS.md to contain body of testing rule")
	}

	// Verify front matter is NOT present in AGENTS.md
	if strings.Contains(agentsMd, "---") {
		t.Errorf("Expected AGENTS.md to NOT contain front matter delimiters")
	}
}

func TestOpenCodeRulesProcessor_CleansOldArtifacts(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workspace")
	rulesDir := filepath.Join(tempDir, ".config", "eksecd", "rules")
	opencodeRulesDir := filepath.Join(tempDir, ".config", "opencode", "rules")
	opencodeConfigDir := filepath.Join(tempDir, ".config", "opencode")

	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("Failed to create work directory: %v", err)
	}

	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("Failed to create rules directory: %v", err)
	}

	if err := os.MkdirAll(opencodeRulesDir, 0755); err != nil {
		t.Fatalf("Failed to create opencode rules directory: %v", err)
	}

	// Create an old rule in opencode rules directory (from previous approach)
	oldRulePath := filepath.Join(opencodeRulesDir, "old-rule.md")
	if err := os.WriteFile(oldRulePath, []byte("# Old"), 0644); err != nil {
		t.Fatalf("Failed to create old rule: %v", err)
	}

	// Create an old opencode.json (from previous approach)
	oldJsonPath := filepath.Join(opencodeConfigDir, "opencode.json")
	if err := os.WriteFile(oldJsonPath, []byte(`{"instructions":["*.md"]}`), 0644); err != nil {
		t.Fatalf("Failed to create old opencode.json: %v", err)
	}

	// Create a fresh rule in nairid rules directory
	freshRulePath := filepath.Join(rulesDir, "fresh-rule.md")
	if err := os.WriteFile(freshRulePath, []byte("# Fresh"), 0644); err != nil {
		t.Fatalf("Failed to create fresh rule: %v", err)
	}

	// Temporarily override home directory for test
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Process rules
	processor := NewOpenCodeRulesProcessor(workDir)
	if err := processor.ProcessRules(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify old opencode rules directory was removed
	if _, err := os.Stat(opencodeRulesDir); !os.IsNotExist(err) {
		t.Errorf("Expected old opencode rules directory to be removed")
	}

	// Verify old opencode.json was removed
	if _, err := os.Stat(oldJsonPath); !os.IsNotExist(err) {
		t.Errorf("Expected old opencode.json to be removed")
	}

	// Verify AGENTS.md was created
	agentsMdPath := filepath.Join(opencodeConfigDir, "AGENTS.md")
	if _, err := os.Stat(agentsMdPath); os.IsNotExist(err) {
		t.Errorf("Expected AGENTS.md to be created")
	}

	// Verify fresh rule still exists in nairid rules directory
	if _, err := os.Stat(freshRulePath); os.IsNotExist(err) {
		t.Errorf("Expected fresh rule in nairid directory to still exist")
	}
}

// Test CodexRulesProcessor

func TestCodexRulesProcessor_NoRules(t *testing.T) {
	tempDir := t.TempDir()

	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	processor := NewCodexRulesProcessor()
	if err := processor.ProcessRules(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	agentsMdPath := filepath.Join(tempDir, ".codex", "AGENTS.md")
	if _, err := os.Stat(agentsMdPath); !os.IsNotExist(err) {
		t.Errorf("Expected AGENTS.md not to exist when no rules")
	}
}

func TestCodexRulesProcessor_WithRules(t *testing.T) {
	tempDir := t.TempDir()
	rulesDir := filepath.Join(tempDir, ".config", "eksecd", "rules")

	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("Failed to create rules directory: %v", err)
	}

	rule1 := `---
title: Code Style Guidelines
description: Use this to learn coding standards
---

# Code Style
Follow these guidelines.`

	rule2 := `---
title: Testing Best Practices
description: How to write effective tests
---

# Testing
Write tests for everything.`

	if err := os.WriteFile(filepath.Join(rulesDir, "code-style.md"), []byte(rule1), 0644); err != nil {
		t.Fatalf("Failed to create rule1: %v", err)
	}

	if err := os.WriteFile(filepath.Join(rulesDir, "testing.md"), []byte(rule2), 0644); err != nil {
		t.Fatalf("Failed to create rule2: %v", err)
	}

	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	processor := NewCodexRulesProcessor()
	if err := processor.ProcessRules(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	agentsMdPath := filepath.Join(tempDir, ".codex", "AGENTS.md")
	content, err := os.ReadFile(agentsMdPath)
	if err != nil {
		t.Fatalf("Expected AGENTS.md to exist, got error: %v", err)
	}

	agentsMd := string(content)

	if !strings.Contains(agentsMd, "# Rules") {
		t.Errorf("Expected AGENTS.md to contain '# Rules' header")
	}

	if !strings.Contains(agentsMd, "## Code Style Guidelines") {
		t.Errorf("Expected AGENTS.md to contain '## Code Style Guidelines'")
	}

	if !strings.Contains(agentsMd, "## Testing Best Practices") {
		t.Errorf("Expected AGENTS.md to contain '## Testing Best Practices'")
	}

	if !strings.Contains(agentsMd, "# Code Style\nFollow these guidelines.") {
		t.Errorf("Expected AGENTS.md to contain body of code style rule")
	}

	if strings.Contains(agentsMd, "---") {
		t.Errorf("Expected AGENTS.md to NOT contain front matter delimiters")
	}
}

func TestCodexRulesProcessor_WithTargetHomeDir(t *testing.T) {
	tempDir := t.TempDir()
	targetHome := filepath.Join(tempDir, "target-home")
	rulesDir := filepath.Join(tempDir, ".config", "eksecd", "rules")

	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatalf("Failed to create rules directory: %v", err)
	}

	if err := os.WriteFile(filepath.Join(rulesDir, "rule.md"), []byte("# Rule content"), 0644); err != nil {
		t.Fatalf("Failed to create rule: %v", err)
	}

	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir)
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	processor := NewCodexRulesProcessor()
	if err := processor.ProcessRules(targetHome); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	agentsMdPath := filepath.Join(targetHome, ".codex", "AGENTS.md")
	if _, err := os.Stat(agentsMdPath); os.IsNotExist(err) {
		t.Errorf("Expected AGENTS.md to be created at target home dir")
	}
}

// Test NoOpRulesProcessor

func TestNoOpRulesProcessor(t *testing.T) {
	processor := NewNoOpRulesProcessor()
	if err := processor.ProcessRules(""); err != nil {
		t.Fatalf("Expected no error from NoOp processor, got: %v", err)
	}
}
