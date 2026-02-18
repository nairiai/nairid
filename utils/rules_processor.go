package utils

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"eksecd/core/log"
)

// RuleFrontMatter represents the parsed front matter from a rule file
type RuleFrontMatter struct {
	Title       string
	Description string
}

// RulesProcessor defines the interface for processing agent-specific rules
type RulesProcessor interface {
	// ProcessRules processes rules from the eksecd rules directory
	// and copies them to the agent-specific location.
	// targetHomeDir specifies the home directory to deploy rules to.
	// If empty, uses the current user's home directory.
	ProcessRules(targetHomeDir string) error
}

// ParseFrontMatter extracts title and description from markdown front matter
// Expected format:
// ---
// title: Code Style Guidelines
// description: Use this to learn what style guidelines to follow
// ---
func ParseFrontMatter(filePath string) (*RuleFrontMatter, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	frontMatter := &RuleFrontMatter{}
	scanner := bufio.NewScanner(file)

	// Check if file starts with front matter delimiter
	if !scanner.Scan() || scanner.Text() != "---" {
		// No front matter found - return empty front matter
		return frontMatter, nil
	}

	// Parse front matter
	for scanner.Scan() {
		line := scanner.Text()

		// End of front matter
		if line == "---" {
			break
		}

		// Parse key-value pairs
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch strings.ToLower(key) {
		case "title":
			frontMatter.Title = value
		case "description":
			frontMatter.Description = value
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	return frontMatter, nil
}

// ReadRuleBody reads a rule file and returns its title, description, and body
// content with front matter stripped. If the file has front matter with a title,
// that title is returned. Otherwise, the title is derived from the filename
// (e.g. "code-style.md" becomes "code-style"). The description is extracted
// from front matter if present, otherwise it defaults to an empty string.
func ReadRuleBody(filePath string) (title string, description string, body string, err error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read rule file: %w", err)
	}

	text := string(content)

	// Default title from filename without extension
	title = strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

	// Check for front matter
	if strings.HasPrefix(text, "---\n") {
		// Find the closing delimiter
		endIdx := strings.Index(text[4:], "\n---\n")
		if endIdx != -1 {
			frontMatterBlock := text[4 : 4+endIdx]

			// Extract title and description from front matter
			for _, line := range strings.Split(frontMatterBlock, "\n") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					key := strings.ToLower(strings.TrimSpace(parts[0]))
					value := strings.TrimSpace(parts[1])
					switch key {
					case "title":
						if value != "" {
							title = value
						}
					case "description":
						description = value
					}
				}
			}

			// Body is everything after the closing ---
			body = strings.TrimLeft(text[4+endIdx+4:], "\n")
		} else {
			// No closing delimiter found, treat entire content as body
			body = text
		}
	} else {
		body = text
	}

	return title, description, body, nil
}

// GetCcagentRulesDir returns the path to the eksecd rules directory
func GetCcagentRulesDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(homeDir, ".config", "eksecd", "rules"), nil
}

// GetRuleFiles returns a list of markdown files in the eksecd rules directory
func GetRuleFiles() ([]string, error) {
	rulesDir, err := GetCcagentRulesDir()
	if err != nil {
		return nil, err
	}

	// Check if rules directory exists
	if _, err := os.Stat(rulesDir); os.IsNotExist(err) {
		log.Info("📋 Rules directory does not exist: %s", rulesDir)
		return []string{}, nil
	}

	// Read directory
	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read rules directory: %w", err)
	}

	// Filter markdown files
	var ruleFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			ruleFiles = append(ruleFiles, filepath.Join(rulesDir, entry.Name()))
		}
	}

	return ruleFiles, nil
}

// CleanCcagentRulesDir removes all files from the eksecd rules directory
// This should be called before downloading new rules from the server to ensure
// stale rules that were deleted on the server are also removed locally.
func CleanCcagentRulesDir() error {
	rulesDir, err := GetCcagentRulesDir()
	if err != nil {
		return err
	}

	// Check if rules directory exists
	if _, err := os.Stat(rulesDir); os.IsNotExist(err) {
		log.Info("📋 Rules directory does not exist, nothing to clean: %s", rulesDir)
		return nil
	}

	log.Info("📋 Cleaning eksecd rules directory: %s", rulesDir)

	// Remove and recreate the directory to ensure a clean state
	if err := os.RemoveAll(rulesDir); err != nil {
		return fmt.Errorf("failed to remove rules directory: %w", err)
	}

	// Recreate empty directory
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		return fmt.Errorf("failed to recreate rules directory: %w", err)
	}

	log.Info("✅ Successfully cleaned eksecd rules directory")
	return nil
}

// ClaudeCodeRulesProcessor handles rules processing for Claude Code
type ClaudeCodeRulesProcessor struct{}

// NewClaudeCodeRulesProcessor creates a new Claude Code rules processor
func NewClaudeCodeRulesProcessor(workDir string) *ClaudeCodeRulesProcessor {
	return &ClaudeCodeRulesProcessor{}
}

// ProcessRules implements RulesProcessor for Claude Code
// targetHomeDir specifies the home directory to deploy rules to.
// If empty, uses the current user's home directory.
func (p *ClaudeCodeRulesProcessor) ProcessRules(targetHomeDir string) error {
	log.Info("📋 Processing rules for Claude Code agent")

	// Get rule files from eksecd directory
	ruleFiles, err := GetRuleFiles()
	if err != nil {
		return fmt.Errorf("failed to get rule files: %w", err)
	}

	if len(ruleFiles) == 0 {
		log.Info("📋 No rules found in eksecd rules directory")
		return nil
	}

	log.Info("📋 Found %d rule file(s) to process", len(ruleFiles))

	// Determine home directory for Claude Code rules
	homeDir := targetHomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
	}

	log.Info("📋 Deploying rules to home directory: %s", homeDir)

	// Create .claude/rules directory in home directory
	claudeRulesDir := filepath.Join(homeDir, ".claude", "rules")

	// Clean up existing rules directory to avoid stale rules
	log.Info("📋 Cleaning Claude Code rules directory: %s", claudeRulesDir)
	if err := removeAllAsTargetUser(claudeRulesDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing rules directory: %w", err)
	}

	// Create fresh rules directory
	if err := mkdirAllAsTargetUser(claudeRulesDir); err != nil {
		return fmt.Errorf("failed to create Claude rules directory: %w", err)
	}

	// Copy each rule file
	for _, ruleFile := range ruleFiles {
		fileName := filepath.Base(ruleFile)
		destPath := filepath.Join(claudeRulesDir, fileName)

		log.Info("📋 Copying rule: %s -> %s", fileName, destPath)

		// Read source file
		content, err := os.ReadFile(ruleFile)
		if err != nil {
			return fmt.Errorf("failed to read rule file %s: %w", ruleFile, err)
		}

		// Write to destination
		if err := writeFileAsTargetUser(destPath, content, 0644); err != nil {
			return fmt.Errorf("failed to write rule file %s: %w", destPath, err)
		}
	}

	log.Info("✅ Successfully processed %d rule(s) for Claude Code", len(ruleFiles))
	return nil
}

// OpenCodeRulesProcessor handles rules processing for OpenCode
type OpenCodeRulesProcessor struct {
	workDir string
}

// NewOpenCodeRulesProcessor creates a new OpenCode rules processor
func NewOpenCodeRulesProcessor(workDir string) *OpenCodeRulesProcessor {
	return &OpenCodeRulesProcessor{
		workDir: workDir,
	}
}

// ProcessRules implements RulesProcessor for OpenCode
// It builds a single AGENTS.md file at ~/.config/opencode/AGENTS.md containing
// all rules inlined under a "Rules" section with title, description, and body
// for each rule. OpenCode natively reads AGENTS.md from the global config directory.
// targetHomeDir specifies the home directory to deploy config to.
// If empty, uses the current user's home directory.
func (p *OpenCodeRulesProcessor) ProcessRules(targetHomeDir string) error {
	log.Info("📋 Processing rules for OpenCode agent")

	// Get rule files from eksecd directory
	ruleFiles, err := GetRuleFiles()
	if err != nil {
		return fmt.Errorf("failed to get rule files: %w", err)
	}

	if len(ruleFiles) == 0 {
		log.Info("📋 No rules found in eksecd rules directory")
		return nil
	}

	log.Info("📋 Found %d rule file(s) to process", len(ruleFiles))

	// Determine home directory for OpenCode config
	homeDir := targetHomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
	}

	log.Info("📋 Deploying OpenCode AGENTS.md to home directory: %s", homeDir)

	opencodeConfigDir := filepath.Join(homeDir, ".config", "opencode")

	// Ensure OpenCode config directory exists with correct ownership
	if err := mkdirAllAsTargetUser(opencodeConfigDir); err != nil {
		return fmt.Errorf("failed to create OpenCode config directory: %w", err)
	}

	// Build AGENTS.md content from all rule files
	var sb strings.Builder
	sb.WriteString("# Rules\n\n")
	sb.WriteString("The following is a list of rules that you need to strictly follow when they are relevant to your task.\n")

	for _, ruleFile := range ruleFiles {
		title, description, body, err := ReadRuleBody(ruleFile)
		if err != nil {
			return fmt.Errorf("failed to read rule file %s: %w", ruleFile, err)
		}

		sb.WriteString("\n## ")
		sb.WriteString(title)
		sb.WriteString("\n\n")
		if description != "" {
			sb.WriteString(description)
			sb.WriteString("\n\n")
		}
		sb.WriteString(body)
		sb.WriteString("\n")
	}

	agentsMdPath := filepath.Join(opencodeConfigDir, "AGENTS.md")
	log.Info("📋 Creating AGENTS.md at: %s", agentsMdPath)

	if err := writeFileAsTargetUser(agentsMdPath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write AGENTS.md: %w", err)
	}

	// Clean up old OpenCode rules directory if it exists (from previous approach)
	oldOpencodeRulesDir := filepath.Join(opencodeConfigDir, "rules")
	if err := removeAllAsTargetUser(oldOpencodeRulesDir); err != nil && !os.IsNotExist(err) {
		log.Info("⚠️  Failed to remove old OpenCode rules directory: %v", err)
	}

	// Clean up old opencode.json if it exists (from previous approach)
	oldOpencodeJsonPath := filepath.Join(opencodeConfigDir, "opencode.json")
	if err := removeAllAsTargetUser(oldOpencodeJsonPath); err != nil && !os.IsNotExist(err) {
		log.Info("⚠️  Failed to remove old opencode.json: %v", err)
	}

	log.Info("✅ Successfully processed %d rule(s) for OpenCode", len(ruleFiles))
	return nil
}

// CodexRulesProcessor handles rules processing for Codex
type CodexRulesProcessor struct{}

// NewCodexRulesProcessor creates a new Codex rules processor
func NewCodexRulesProcessor() *CodexRulesProcessor {
	return &CodexRulesProcessor{}
}

// ProcessRules implements RulesProcessor for Codex
// It builds a single AGENTS.md file at ~/.codex/AGENTS.md containing
// all rules inlined under a "Rules" section with title, description, and body
// for each rule. Codex natively reads AGENTS.md from the global config directory.
// targetHomeDir specifies the home directory to deploy config to.
// If empty, uses the current user's home directory.
func (p *CodexRulesProcessor) ProcessRules(targetHomeDir string) error {
	log.Info("📋 Processing rules for Codex agent")

	// Get rule files from eksecd directory
	ruleFiles, err := GetRuleFiles()
	if err != nil {
		return fmt.Errorf("failed to get rule files: %w", err)
	}

	if len(ruleFiles) == 0 {
		log.Info("📋 No rules found in eksecd rules directory")
		return nil
	}

	log.Info("📋 Found %d rule file(s) to process", len(ruleFiles))

	// Determine home directory for Codex config
	homeDir := targetHomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
	}

	log.Info("📋 Deploying Codex AGENTS.md to home directory: %s", homeDir)

	codexConfigDir := filepath.Join(homeDir, ".codex")

	// Ensure Codex config directory exists with correct ownership
	if err := mkdirAllAsTargetUser(codexConfigDir); err != nil {
		return fmt.Errorf("failed to create Codex config directory: %w", err)
	}

	// Build AGENTS.md content from all rule files
	var sb strings.Builder
	sb.WriteString("# Rules\n\n")
	sb.WriteString("The following is a list of rules that you need to strictly follow when they are relevant to your task.\n")

	for _, ruleFile := range ruleFiles {
		title, description, body, err := ReadRuleBody(ruleFile)
		if err != nil {
			return fmt.Errorf("failed to read rule file %s: %w", ruleFile, err)
		}

		sb.WriteString("\n## ")
		sb.WriteString(title)
		sb.WriteString("\n\n")
		if description != "" {
			sb.WriteString(description)
			sb.WriteString("\n\n")
		}
		sb.WriteString(body)
		sb.WriteString("\n")
	}

	agentsMdPath := filepath.Join(codexConfigDir, "AGENTS.md")
	log.Info("📋 Creating AGENTS.md at: %s", agentsMdPath)

	if err := writeFileAsTargetUser(agentsMdPath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write AGENTS.md: %w", err)
	}

	log.Info("✅ Successfully processed %d rule(s) for Codex", len(ruleFiles))
	return nil
}

// NoOpRulesProcessor is a no-op implementation for agents that don't support rules
type NoOpRulesProcessor struct{}

// NewNoOpRulesProcessor creates a new no-op rules processor
func NewNoOpRulesProcessor() *NoOpRulesProcessor {
	return &NoOpRulesProcessor{}
}

// ProcessRules implements RulesProcessor with no operation
func (p *NoOpRulesProcessor) ProcessRules(targetHomeDir string) error {
	log.Info("📋 Rules processing not supported for this agent type")
	return nil
}
