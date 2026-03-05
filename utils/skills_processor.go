package utils

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"nairid/core/log"
)

// SkillsProcessor defines the interface for processing agent-specific skills
type SkillsProcessor interface {
	// ProcessSkills processes skills from the eksecd skills directory
	// and extracts them to the agent-specific location.
	// targetHomeDir specifies the home directory to deploy skills to.
	// If empty, uses the current user's home directory.
	ProcessSkills(targetHomeDir string) error
}

// GetEksecdSkillsDir returns the path to the eksecd skills directory
func GetEksecdSkillsDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(homeDir, ".config", "eksecd", "skills"), nil
}

// GetSkillFiles returns a list of skill files (.zip or .skill) in the eksecd skills directory
func GetSkillFiles() ([]string, error) {
	skillsDir, err := GetEksecdSkillsDir()
	if err != nil {
		return nil, err
	}

	// Check if skills directory exists
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		log.Info("🎯 Skills directory does not exist: %s", skillsDir)
		return []string{}, nil
	}

	// Read directory
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read skills directory: %w", err)
	}

	// Filter skill files (.zip or .skill extensions)
	var skillFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		lowerName := strings.ToLower(entry.Name())
		if strings.HasSuffix(lowerName, ".zip") || strings.HasSuffix(lowerName, ".skill") {
			skillFiles = append(skillFiles, filepath.Join(skillsDir, entry.Name()))
		}
	}

	return skillFiles, nil
}

// CleanEksecdSkillsDir removes all files from the eksecd skills directory
// This should be called before downloading new skills from the server to ensure
// stale skills that were deleted on the server are also removed locally.
func CleanEksecdSkillsDir() error {
	skillsDir, err := GetEksecdSkillsDir()
	if err != nil {
		return err
	}

	// Check if skills directory exists
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		log.Info("🎯 Skills directory does not exist, nothing to clean: %s", skillsDir)
		return nil
	}

	log.Info("🎯 Cleaning eksecd skills directory: %s", skillsDir)

	// Remove and recreate the directory to ensure a clean state
	if err := os.RemoveAll(skillsDir); err != nil {
		return fmt.Errorf("failed to remove skills directory: %w", err)
	}

	// Recreate empty directory
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return fmt.Errorf("failed to recreate skills directory: %w", err)
	}

	log.Info("✅ Successfully cleaned eksecd skills directory")
	return nil
}

// ExtractSkillNameFromFilename extracts the skill name from the filename by removing the attachment ID suffix
// Example: "code-reviewer-a1b2c3.zip" -> "code-reviewer"
// Example: "code-reviewer-a1b2c3.skill" -> "code-reviewer"
func ExtractSkillNameFromFilename(filename string) string {
	// Remove extension (.zip or .skill)
	name := strings.TrimSuffix(filename, ".zip")
	name = strings.TrimSuffix(name, ".skill")

	// Find the last hyphen followed by exactly 6 characters (attachment ID)
	// Walk backwards to find the pattern -{6 chars}
	if len(name) > 7 {
		lastPart := name[len(name)-7:]
		if lastPart[0] == '-' {
			// Verify the last 6 characters look like an attachment ID (alphanumeric)
			isAttachmentID := true
			for _, ch := range lastPart[1:] {
				if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')) {
					isAttachmentID = false
					break
				}
			}
			if isAttachmentID {
				return name[:len(name)-7]
			}
		}
	}

	// Fallback: return the full name without extension
	return name
}

// ExtractZipToDirectory extracts a ZIP file to the target directory
// Handles ZIP files with or without a single root directory, stripping the root if present
// Prevents zip slip attacks by validating all paths stay within target directory
// Processes in memory to avoid leaving temporary files
func ExtractZipToDirectory(zipPath, targetDir string) error {
	// Read the entire ZIP file into memory
	zipData, err := os.ReadFile(zipPath)
	if err != nil {
		return fmt.Errorf("failed to read zip file: %w", err)
	}

	// Create a bytes reader for in-memory processing
	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("failed to open zip reader: %w", err)
	}

	// Detect if ZIP has a single root directory that should be stripped
	rootDir := detectSingleRootDirectory(zipReader)

	// Extract each file
	for _, file := range zipReader.File {
		// Skip directories
		if file.FileInfo().IsDir() {
			continue
		}

		// Determine the target path
		targetPath := file.Name
		if rootDir != "" {
			// Strip the root directory
			if !strings.HasPrefix(targetPath, rootDir+"/") {
				continue // Skip files not in root directory
			}
			targetPath = strings.TrimPrefix(targetPath, rootDir+"/")
		}

		// Skip if path is now empty
		if targetPath == "" {
			continue
		}

		// Prevent zip slip attack - validate path stays within target directory
		fullPath := filepath.Join(targetDir, targetPath)
		if !strings.HasPrefix(filepath.Clean(fullPath), filepath.Clean(targetDir)) {
			return fmt.Errorf("zip slip attack detected: path %s escapes target directory", targetPath)
		}

		// Create parent directories
		if err := mkdirAllAsTargetUser(filepath.Dir(fullPath)); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", targetPath, err)
		}

		// Extract file content
		if err := extractZipFile(file, fullPath); err != nil {
			return fmt.Errorf("failed to extract %s: %w", targetPath, err)
		}
	}

	return nil
}

// detectSingleRootDirectory checks if all files in the ZIP are under a single root directory
// Returns the root directory name if found, empty string otherwise
func detectSingleRootDirectory(zipReader *zip.Reader) string {
	if len(zipReader.File) == 0 {
		return ""
	}

	var rootDir string
	for _, file := range zipReader.File {
		// Get the first component of the path
		parts := strings.Split(file.Name, "/")
		if len(parts) == 0 {
			return ""
		}

		firstComponent := parts[0]

		// Initialize root directory from first file
		if rootDir == "" {
			rootDir = firstComponent
			continue
		}

		// Check if this file is under the same root
		if firstComponent != rootDir {
			return "" // Multiple roots found
		}
	}

	return rootDir
}

// extractZipFile extracts a single file from a ZIP archive
func extractZipFile(file *zip.File, targetPath string) error {
	// Open the file from ZIP
	rc, err := file.Open()
	if err != nil {
		return fmt.Errorf("failed to open file in zip: %w", err)
	}
	defer rc.Close()

	// Read entire content into memory
	content, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("failed to read file content: %w", err)
	}

	// Write to target path
	if err := writeFileAsTargetUser(targetPath, content, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// ClaudeCodeSkillsProcessor handles skills processing for Claude Code
type ClaudeCodeSkillsProcessor struct{}

// NewClaudeCodeSkillsProcessor creates a new Claude Code skills processor
func NewClaudeCodeSkillsProcessor() *ClaudeCodeSkillsProcessor {
	return &ClaudeCodeSkillsProcessor{}
}

// ProcessSkills implements SkillsProcessor for Claude Code
// targetHomeDir specifies the home directory to deploy skills to.
// If empty, uses the current user's home directory.
func (p *ClaudeCodeSkillsProcessor) ProcessSkills(targetHomeDir string) error {
	log.Info("🎯 Processing skills for Claude Code agent")

	// Get skill files from nairid directory
	skillFiles, err := GetSkillFiles()
	if err != nil {
		return fmt.Errorf("failed to get skill files: %w", err)
	}

	if len(skillFiles) == 0 {
		log.Info("🎯 No skills found in eksecd skills directory")
		return nil
	}

	log.Info("🎯 Found %d skill file(s) to process", len(skillFiles))

	// Determine home directory for Claude Code skills
	homeDir := targetHomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
	}

	log.Info("🎯 Deploying skills to home directory: %s", homeDir)

	// Target directory: ~/.claude/skills/
	claudeSkillsDir := filepath.Join(homeDir, ".claude", "skills")

	// Clean up existing skills directory to avoid stale skills
	log.Info("🎯 Cleaning Claude Code skills directory: %s", claudeSkillsDir)
	if err := removeAllAsTargetUser(claudeSkillsDir); err != nil && !os.IsNotExist(err) {
		log.Info("⚠️  Failed to remove existing skills directory: %v", err)
	}

	// Create fresh skills directory
	if err := mkdirAllAsTargetUser(claudeSkillsDir); err != nil {
		return fmt.Errorf("failed to create Claude skills directory: %w", err)
	}

	// Extract each skill ZIP to its own directory
	for _, skillFile := range skillFiles {
		fileName := filepath.Base(skillFile)
		skillName := ExtractSkillNameFromFilename(fileName)
		targetSkillDir := filepath.Join(claudeSkillsDir, skillName)

		log.Info("🎯 Extracting skill: %s -> %s", fileName, targetSkillDir)

		// Create skill directory
		if err := mkdirAllAsTargetUser(targetSkillDir); err != nil {
			log.Info("⚠️  Failed to create skill directory %s: %v", targetSkillDir, err)
			continue
		}

		// Extract ZIP to skill directory
		if err := ExtractZipToDirectory(skillFile, targetSkillDir); err != nil {
			log.Info("⚠️  Failed to extract skill %s: %v", skillName, err)
			continue
		}

		// Verify SKILL.md exists
		skillMdPath := filepath.Join(targetSkillDir, "SKILL.md")
		if _, err := os.Stat(skillMdPath); os.IsNotExist(err) {
			log.Info("⚠️  Missing SKILL.md in skill %s", skillName)
		}
	}

	log.Info("✅ Successfully processed %d skill(s) for Claude Code", len(skillFiles))
	return nil
}

// OpenCodeSkillsProcessor handles skills processing for OpenCode
type OpenCodeSkillsProcessor struct{}

// NewOpenCodeSkillsProcessor creates a new OpenCode skills processor
func NewOpenCodeSkillsProcessor() *OpenCodeSkillsProcessor {
	return &OpenCodeSkillsProcessor{}
}

// ProcessSkills implements SkillsProcessor for OpenCode
// targetHomeDir specifies the home directory to deploy skills to.
// If empty, uses the current user's home directory.
func (p *OpenCodeSkillsProcessor) ProcessSkills(targetHomeDir string) error {
	log.Info("🎯 Processing skills for OpenCode agent")

	// Get skill files from nairid directory
	skillFiles, err := GetSkillFiles()
	if err != nil {
		return fmt.Errorf("failed to get skill files: %w", err)
	}

	if len(skillFiles) == 0 {
		log.Info("🎯 No skills found in eksecd skills directory")
		return nil
	}

	log.Info("🎯 Found %d skill file(s) to process", len(skillFiles))

	// Determine home directory for OpenCode skills
	homeDir := targetHomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
	}

	log.Info("🎯 Deploying skills to home directory: %s", homeDir)

	// Target directory: ~/.config/opencode/skill/
	opencodeSkillsDir := filepath.Join(homeDir, ".config", "opencode", "skill")

	// Clean up existing skills directory to avoid stale skills
	log.Info("🎯 Cleaning OpenCode skills directory: %s", opencodeSkillsDir)
	if err := removeAllAsTargetUser(opencodeSkillsDir); err != nil && !os.IsNotExist(err) {
		log.Info("⚠️  Failed to remove existing skills directory: %v", err)
	}

	// Create fresh skills directory with correct ownership
	if err := mkdirAllAsTargetUser(opencodeSkillsDir); err != nil {
		return fmt.Errorf("failed to create OpenCode skills directory: %w", err)
	}

	// Extract each skill ZIP to its own directory
	for _, skillFile := range skillFiles {
		fileName := filepath.Base(skillFile)
		skillName := ExtractSkillNameFromFilename(fileName)
		targetSkillDir := filepath.Join(opencodeSkillsDir, skillName)

		log.Info("🎯 Extracting skill: %s -> %s", fileName, targetSkillDir)

		// Create skill directory with correct ownership
		if err := mkdirAllAsTargetUser(targetSkillDir); err != nil {
			log.Info("⚠️  Failed to create skill directory %s: %v", targetSkillDir, err)
			continue
		}

		// Extract ZIP to skill directory
		if err := ExtractZipToDirectory(skillFile, targetSkillDir); err != nil {
			log.Info("⚠️  Failed to extract skill %s: %v", skillName, err)
			continue
		}

		// Verify SKILL.md exists
		skillMdPath := filepath.Join(targetSkillDir, "SKILL.md")
		if _, err := os.Stat(skillMdPath); os.IsNotExist(err) {
			log.Info("⚠️  Missing SKILL.md in skill %s", skillName)
		}
	}

	log.Info("✅ Successfully processed %d skill(s) for OpenCode", len(skillFiles))
	return nil
}

// CodexSkillsProcessor handles skills processing for Codex
type CodexSkillsProcessor struct{}

// NewCodexSkillsProcessor creates a new Codex skills processor
func NewCodexSkillsProcessor() *CodexSkillsProcessor {
	return &CodexSkillsProcessor{}
}

// ProcessSkills implements SkillsProcessor for Codex
// targetHomeDir specifies the home directory to deploy skills to.
// If empty, uses the current user's home directory.
func (p *CodexSkillsProcessor) ProcessSkills(targetHomeDir string) error {
	log.Info("🎯 Processing skills for Codex agent")

	// Get skill files from nairid directory
	skillFiles, err := GetSkillFiles()
	if err != nil {
		return fmt.Errorf("failed to get skill files: %w", err)
	}

	if len(skillFiles) == 0 {
		log.Info("🎯 No skills found in eksecd skills directory")
		return nil
	}

	log.Info("🎯 Found %d skill file(s) to process", len(skillFiles))

	// Determine home directory for Codex skills
	homeDir := targetHomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
	}

	log.Info("🎯 Deploying skills to home directory: %s", homeDir)

	// Target directory: ~/.codex/skills/
	codexSkillsDir := filepath.Join(homeDir, ".codex", "skills")

	// Clean up existing skills directory to avoid stale skills
	log.Info("🎯 Cleaning Codex skills directory: %s", codexSkillsDir)
	if err := removeAllAsTargetUser(codexSkillsDir); err != nil && !os.IsNotExist(err) {
		log.Info("⚠️  Failed to remove existing skills directory: %v", err)
	}

	// Create fresh skills directory
	if err := mkdirAllAsTargetUser(codexSkillsDir); err != nil {
		return fmt.Errorf("failed to create Codex skills directory: %w", err)
	}

	// Extract each skill ZIP to its own directory
	for _, skillFile := range skillFiles {
		fileName := filepath.Base(skillFile)
		skillName := ExtractSkillNameFromFilename(fileName)
		targetSkillDir := filepath.Join(codexSkillsDir, skillName)

		log.Info("🎯 Extracting skill: %s -> %s", fileName, targetSkillDir)

		// Create skill directory
		if err := mkdirAllAsTargetUser(targetSkillDir); err != nil {
			log.Info("⚠️  Failed to create skill directory %s: %v", targetSkillDir, err)
			continue
		}

		// Extract ZIP to skill directory
		if err := ExtractZipToDirectory(skillFile, targetSkillDir); err != nil {
			log.Info("⚠️  Failed to extract skill %s: %v", skillName, err)
			continue
		}

		// Verify SKILL.md exists
		skillMdPath := filepath.Join(targetSkillDir, "SKILL.md")
		if _, err := os.Stat(skillMdPath); os.IsNotExist(err) {
			log.Info("⚠️  Missing SKILL.md in skill %s", skillName)
		}
	}

	log.Info("✅ Successfully processed %d skill(s) for Codex", len(skillFiles))
	return nil
}

// NoOpSkillsProcessor is a no-op implementation for agents that don't support skills
type NoOpSkillsProcessor struct{}

// NewNoOpSkillsProcessor creates a new no-op skills processor
func NewNoOpSkillsProcessor() *NoOpSkillsProcessor {
	return &NoOpSkillsProcessor{}
}

// ProcessSkills implements SkillsProcessor with no operation
func (p *NoOpSkillsProcessor) ProcessSkills(targetHomeDir string) error {
	log.Info("🎯 Skills processing not supported for this agent type")
	return nil
}
