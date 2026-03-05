package utils

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractSkillNameFromFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{
			name:     "standard format with 6-char ID (.zip)",
			filename: "code-reviewer-a1b2c3.zip",
			want:     "code-reviewer",
		},
		{
			name:     "standard format with 6-char ID (.skill)",
			filename: "code-reviewer-a1b2c3.skill",
			want:     "code-reviewer",
		},
		{
			name:     "multi-part name with 6-char ID (.zip)",
			filename: "pdf-processor-x4y5z6.zip",
			want:     "pdf-processor",
		},
		{
			name:     "multi-part name with 6-char ID (.skill)",
			filename: "pdf-processor-x4y5z6.skill",
			want:     "pdf-processor",
		},
		{
			name:     "single word name (.zip)",
			filename: "formatter-abc123.zip",
			want:     "formatter",
		},
		{
			name:     "single word name (.skill)",
			filename: "formatter-abc123.skill",
			want:     "formatter",
		},
		{
			name:     "name with numbers and 6-char ID",
			filename: "skill-v2-beta-xyz789.zip",
			want:     "skill-v2-beta",
		},
		{
			name:     "no attachment ID suffix (.zip)",
			filename: "my-skill.zip",
			want:     "my-skill",
		},
		{
			name:     "no attachment ID suffix (.skill)",
			filename: "my-skill.skill",
			want:     "my-skill",
		},
		{
			name:     "short name",
			filename: "foo.zip",
			want:     "foo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractSkillNameFromFilename(tt.filename)
			if got != tt.want {
				t.Errorf("ExtractSkillNameFromFilename(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

func TestExtractZipToDirectory(t *testing.T) {
	tests := []struct {
		name           string
		zipStructure   map[string]string // path -> content
		expectedFiles  map[string]string // path -> content after extraction
		shouldFail     bool
		expectedError  string
		singleRootDir  string // if not empty, structure has single root dir
	}{
		{
			name: "ZIP without root directory",
			zipStructure: map[string]string{
				"SKILL.md":            "# Skill Instructions",
				"scripts/deploy.sh":   "#!/bin/bash\necho deploy",
				"references/docs.md":  "# Documentation",
			},
			expectedFiles: map[string]string{
				"SKILL.md":            "# Skill Instructions",
				"scripts/deploy.sh":   "#!/bin/bash\necho deploy",
				"references/docs.md":  "# Documentation",
			},
		},
		{
			name: "ZIP with single root directory",
			zipStructure: map[string]string{
				"code-reviewer/SKILL.md":           "# Code Review Skill",
				"code-reviewer/scripts/review.sh":  "#!/bin/bash\nreview code",
				"code-reviewer/assets/template.md": "Template content",
			},
			singleRootDir: "code-reviewer",
			expectedFiles: map[string]string{
				"SKILL.md":           "# Code Review Skill",
				"scripts/review.sh":  "#!/bin/bash\nreview code",
				"assets/template.md": "Template content",
			},
		},
		{
			name: "ZIP with multiple root directories",
			zipStructure: map[string]string{
				"skill1/SKILL.md": "Skill 1",
				"skill2/SKILL.md": "Skill 2",
			},
			expectedFiles: map[string]string{
				"skill1/SKILL.md": "Skill 1",
				"skill2/SKILL.md": "Skill 2",
			},
		},
		{
			name: "ZIP with zip slip attempt",
			zipStructure: map[string]string{
				"../../../etc/passwd": "malicious content",
			},
			shouldFail:    true,
			expectedError: "zip slip attack detected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for testing
			tmpDir := t.TempDir()
			zipPath := filepath.Join(tmpDir, "test.zip")
			extractDir := filepath.Join(tmpDir, "extract")

			// Create the ZIP file
			err := createTestZip(zipPath, tt.zipStructure)
			if err != nil {
				t.Fatalf("Failed to create test ZIP: %v", err)
			}

			// Extract the ZIP
			err = ExtractZipToDirectory(zipPath, extractDir)

			// Check for expected errors
			if tt.shouldFail {
				if err == nil {
					t.Errorf("Expected error containing %q, but got no error", tt.expectedError)
				} else if tt.expectedError != "" && !contains(err.Error(), tt.expectedError) {
					t.Errorf("Expected error containing %q, got %q", tt.expectedError, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("ExtractZipToDirectory failed: %v", err)
			}

			// Verify extracted files
			for expectedPath, expectedContent := range tt.expectedFiles {
				fullPath := filepath.Join(extractDir, expectedPath)
				content, err := os.ReadFile(fullPath)
				if err != nil {
					t.Errorf("Failed to read extracted file %s: %v", expectedPath, err)
					continue
				}

				if string(content) != expectedContent {
					t.Errorf("File %s content = %q, want %q", expectedPath, string(content), expectedContent)
				}
			}
		})
	}
}

func TestDetectSingleRootDirectory(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		wantRoot string
	}{
		{
			name:     "single root directory",
			files:    []string{"skill/SKILL.md", "skill/scripts/test.sh", "skill/assets/file.txt"},
			wantRoot: "skill",
		},
		{
			name:     "multiple root directories",
			files:    []string{"skill1/file.txt", "skill2/file.txt"},
			wantRoot: "",
		},
		{
			name:     "no root directory",
			files:    []string{"SKILL.md", "scripts/test.sh"},
			wantRoot: "",
		},
		{
			name:     "empty ZIP",
			files:    []string{},
			wantRoot: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock ZIP reader
			buf := new(bytes.Buffer)
			w := zip.NewWriter(buf)

			for _, file := range tt.files {
				f, err := w.Create(file)
				if err != nil {
					t.Fatalf("Failed to create ZIP entry: %v", err)
				}
				_, err = f.Write([]byte("content"))
				if err != nil {
					t.Fatalf("Failed to write ZIP entry: %v", err)
				}
			}

			err := w.Close()
			if err != nil {
				t.Fatalf("Failed to close ZIP writer: %v", err)
			}

			// Create ZIP reader
			r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatalf("Failed to create ZIP reader: %v", err)
			}

			// Test the function
			got := detectSingleRootDirectory(r)
			if got != tt.wantRoot {
				t.Errorf("detectSingleRootDirectory() = %q, want %q", got, tt.wantRoot)
			}
		})
	}
}

// Helper function to create a test ZIP file
func createTestZip(zipPath string, files map[string]string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	w := zip.NewWriter(zipFile)
	defer w.Close()

	for path, content := range files {
		f, err := w.Create(path)
		if err != nil {
			return err
		}
		_, err = f.Write([]byte(content))
		if err != nil {
			return err
		}
	}

	return nil
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGetSkillFiles(t *testing.T) {
	// Create a temporary directory to simulate ~/.config/eksecd/skills/
	tmpDir := t.TempDir()

	// Override the home directory for testing
	originalHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)

	// Create the .config/eksecd/skills structure
	eksecdSkillsDir := filepath.Join(tmpDir, ".config", "eksecd", "skills")
	if err := os.MkdirAll(eksecdSkillsDir, 0755); err != nil {
		t.Fatalf("Failed to create skills directory: %v", err)
	}

	// Restore original HOME after test
	defer os.Setenv("HOME", originalHome)

	tests := []struct {
		name          string
		setupFiles    map[string]string // filename -> content
		expectedFiles []string          // expected base filenames
		shouldFail    bool
	}{
		{
			name: "multiple skill ZIP files",
			setupFiles: map[string]string{
				"code-reviewer-a1b2c3.zip": "zip content 1",
				"pdf-processor-x4y5z6.zip": "zip content 2",
				"formatter-abc123.zip":     "zip content 3",
			},
			expectedFiles: []string{
				"code-reviewer-a1b2c3.zip",
				"pdf-processor-x4y5z6.zip",
				"formatter-abc123.zip",
			},
		},
		{
			name: "multiple skill .skill files",
			setupFiles: map[string]string{
				"code-reviewer-a1b2c3.skill": "skill content 1",
				"pdf-processor-x4y5z6.skill": "skill content 2",
			},
			expectedFiles: []string{
				"code-reviewer-a1b2c3.skill",
				"pdf-processor-x4y5z6.skill",
			},
		},
		{
			name: "mixed .zip and .skill files",
			setupFiles: map[string]string{
				"skill1-abc123.zip":   "zip content",
				"skill2-def456.skill": "skill content",
				"skill3-ghi789.ZIP":   "uppercase zip",
				"skill4-jkl012.SKILL": "uppercase skill",
			},
			expectedFiles: []string{
				"skill1-abc123.zip",
				"skill2-def456.skill",
				"skill3-ghi789.ZIP",
				"skill4-jkl012.SKILL",
			},
		},
		{
			name: "mixed file types - only skill files returned",
			setupFiles: map[string]string{
				"skill1-abc123.zip":   "zip content",
				"skill2-def456.skill": "skill content",
				"readme.md":           "markdown content",
				"config.json":         "json content",
			},
			expectedFiles: []string{
				"skill1-abc123.zip",
				"skill2-def456.skill",
			},
		},
		{
			name:          "empty directory",
			setupFiles:    map[string]string{},
			expectedFiles: []string{},
		},
		{
			name: "directory with subdirectories",
			setupFiles: map[string]string{
				"skill-abc123.zip": "zip content",
			},
			expectedFiles: []string{
				"skill-abc123.zip",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean skills directory before each test
			if err := os.RemoveAll(eksecdSkillsDir); err != nil {
				t.Fatalf("Failed to clean skills directory: %v", err)
			}
			if err := os.MkdirAll(eksecdSkillsDir, 0755); err != nil {
				t.Fatalf("Failed to create skills directory: %v", err)
			}

			// Create test files
			for filename, content := range tt.setupFiles {
				filePath := filepath.Join(eksecdSkillsDir, filename)
				if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
					t.Fatalf("Failed to create test file %s: %v", filename, err)
				}
			}

			// Call GetSkillFiles
			files, err := GetSkillFiles()

			if tt.shouldFail {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("GetSkillFiles failed: %v", err)
			}

			// Extract base filenames for comparison
			var gotFilenames []string
			for _, file := range files {
				gotFilenames = append(gotFilenames, filepath.Base(file))
			}

			// Sort both slices for consistent comparison
			if len(gotFilenames) != len(tt.expectedFiles) {
				t.Errorf("Expected %d files, got %d: %v", len(tt.expectedFiles), len(gotFilenames), gotFilenames)
				return
			}

			// Check each expected file is present
			for _, expected := range tt.expectedFiles {
				found := false
				for _, got := range gotFilenames {
					if got == expected {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected file %s not found in results: %v", expected, gotFilenames)
				}
			}
		})
	}
}

func TestClaudeCodeSkillsProcessor_Integration(t *testing.T) {
	// Create temporary directories
	tmpDir := t.TempDir()
	eksecdSkillsDir := filepath.Join(tmpDir, ".config", "eksecd", "skills")
	claudeSkillsDir := filepath.Join(tmpDir, ".claude", "skills")

	// Override home directory
	originalHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Create eksecd skills directory
	if err := os.MkdirAll(eksecdSkillsDir, 0755); err != nil {
		t.Fatalf("Failed to create eksecd skills directory: %v", err)
	}

	// Create a test skill ZIP file
	skillZipPath := filepath.Join(eksecdSkillsDir, "test-skill-abc123.zip")
	skillContent := map[string]string{
		"SKILL.md":           "# Test Skill\n\nSkill content here",
		"scripts/run.sh":     "#!/bin/bash\necho 'running'",
		"references/doc.md":  "Documentation",
	}

	if err := createTestZip(skillZipPath, skillContent); err != nil {
		t.Fatalf("Failed to create test ZIP: %v", err)
	}

	// Create and run the processor
	processor := NewClaudeCodeSkillsProcessor()
	if err := processor.ProcessSkills(""); err != nil {
		t.Fatalf("ProcessSkills failed: %v", err)
	}

	// Verify the skill was extracted to ~/.claude/skills/test-skill/
	expectedSkillDir := filepath.Join(claudeSkillsDir, "test-skill")

	// Check each expected file
	for filePath, expectedContent := range skillContent {
		fullPath := filepath.Join(expectedSkillDir, filePath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			t.Errorf("Failed to read extracted file %s: %v", filePath, err)
			continue
		}

		if string(content) != expectedContent {
			t.Errorf("File %s content mismatch. Got %q, want %q", filePath, string(content), expectedContent)
		}
	}
}

func TestOpenCodeSkillsProcessor_Integration(t *testing.T) {
	// Create temporary directories
	tmpDir := t.TempDir()
	eksecdSkillsDir := filepath.Join(tmpDir, ".config", "eksecd", "skills")
	opencodeSkillsDir := filepath.Join(tmpDir, ".config", "opencode", "skill")

	// Override home directory
	originalHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Create eksecd skills directory
	if err := os.MkdirAll(eksecdSkillsDir, 0755); err != nil {
		t.Fatalf("Failed to create eksecd skills directory: %v", err)
	}

	// Create a test skill ZIP file with root directory
	skillZipPath := filepath.Join(eksecdSkillsDir, "my-skill-xyz789.zip")
	skillContent := map[string]string{
		"my-skill/SKILL.md":          "# My Skill\n\nContent",
		"my-skill/scripts/script.sh": "#!/bin/bash\necho 'test'",
		"my-skill/assets/file.txt":   "Asset content",
	}

	if err := createTestZip(skillZipPath, skillContent); err != nil {
		t.Fatalf("Failed to create test ZIP: %v", err)
	}

	// Create and run the processor
	processor := NewOpenCodeSkillsProcessor()
	if err := processor.ProcessSkills(""); err != nil {
		t.Fatalf("ProcessSkills failed: %v", err)
	}

	// Verify the skill was extracted to ~/.config/opencode/skill/my-skill/
	// Note: root directory should be stripped
	expectedSkillDir := filepath.Join(opencodeSkillsDir, "my-skill")

	// Expected files after stripping root directory
	expectedFiles := map[string]string{
		"SKILL.md":          "# My Skill\n\nContent",
		"scripts/script.sh": "#!/bin/bash\necho 'test'",
		"assets/file.txt":   "Asset content",
	}

	for filePath, expectedContent := range expectedFiles {
		fullPath := filepath.Join(expectedSkillDir, filePath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			t.Errorf("Failed to read extracted file %s: %v", filePath, err)
			continue
		}

		if string(content) != expectedContent {
			t.Errorf("File %s content mismatch. Got %q, want %q", filePath, string(content), expectedContent)
		}
	}
}

func TestCodexSkillsProcessor_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	eksecdSkillsDir := filepath.Join(tmpDir, ".config", "eksecd", "skills")
	codexSkillsDir := filepath.Join(tmpDir, ".codex", "skills")

	originalHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	if err := os.MkdirAll(eksecdSkillsDir, 0755); err != nil {
		t.Fatalf("Failed to create eksecd skills directory: %v", err)
	}

	skillZipPath := filepath.Join(eksecdSkillsDir, "test-skill-abc123.zip")
	skillContent := map[string]string{
		"SKILL.md":          "# Test Skill\n\nSkill content here",
		"scripts/run.sh":    "#!/bin/bash\necho 'running'",
		"references/doc.md": "Documentation",
	}

	if err := createTestZip(skillZipPath, skillContent); err != nil {
		t.Fatalf("Failed to create test ZIP: %v", err)
	}

	processor := NewCodexSkillsProcessor()
	if err := processor.ProcessSkills(""); err != nil {
		t.Fatalf("ProcessSkills failed: %v", err)
	}

	expectedSkillDir := filepath.Join(codexSkillsDir, "test-skill")

	for filePath, expectedContent := range skillContent {
		fullPath := filepath.Join(expectedSkillDir, filePath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			t.Errorf("Failed to read extracted file %s: %v", filePath, err)
			continue
		}

		if string(content) != expectedContent {
			t.Errorf("File %s content mismatch. Got %q, want %q", filePath, string(content), expectedContent)
		}
	}
}

func TestCodexSkillsProcessor_WithRootDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	eksecdSkillsDir := filepath.Join(tmpDir, ".config", "eksecd", "skills")
	codexSkillsDir := filepath.Join(tmpDir, ".codex", "skills")

	originalHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	if err := os.MkdirAll(eksecdSkillsDir, 0755); err != nil {
		t.Fatalf("Failed to create eksecd skills directory: %v", err)
	}

	skillZipPath := filepath.Join(eksecdSkillsDir, "my-skill-xyz789.zip")
	skillContent := map[string]string{
		"my-skill/SKILL.md":          "# My Skill\n\nContent",
		"my-skill/scripts/script.sh": "#!/bin/bash\necho 'test'",
	}

	if err := createTestZip(skillZipPath, skillContent); err != nil {
		t.Fatalf("Failed to create test ZIP: %v", err)
	}

	processor := NewCodexSkillsProcessor()
	if err := processor.ProcessSkills(""); err != nil {
		t.Fatalf("ProcessSkills failed: %v", err)
	}

	expectedSkillDir := filepath.Join(codexSkillsDir, "my-skill")

	expectedFiles := map[string]string{
		"SKILL.md":          "# My Skill\n\nContent",
		"scripts/script.sh": "#!/bin/bash\necho 'test'",
	}

	for filePath, expectedContent := range expectedFiles {
		fullPath := filepath.Join(expectedSkillDir, filePath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			t.Errorf("Failed to read extracted file %s: %v", filePath, err)
			continue
		}

		if string(content) != expectedContent {
			t.Errorf("File %s content mismatch. Got %q, want %q", filePath, string(content), expectedContent)
		}
	}
}

func TestCodexSkillsProcessor_NoSkills(t *testing.T) {
	tmpDir := t.TempDir()

	originalHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	processor := NewCodexSkillsProcessor()
	if err := processor.ProcessSkills(""); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	codexSkillsDir := filepath.Join(tmpDir, ".codex", "skills")
	if _, err := os.Stat(codexSkillsDir); !os.IsNotExist(err) {
		t.Errorf("Expected skills directory not to exist when no skills")
	}
}

func TestNoOpSkillsProcessor(t *testing.T) {
	processor := NewNoOpSkillsProcessor()
	err := processor.ProcessSkills("")

	if err != nil {
		t.Errorf("NoOpSkillsProcessor should not return an error, got: %v", err)
	}
}

func TestCleanEksecdSkillsDir(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()

	// Override home directory
	originalHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Create skills directory with some files
	skillsDir := filepath.Join(tmpDir, ".config", "eksecd", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("Failed to create skills directory: %v", err)
	}

	// Add some test files
	testFiles := []string{"skill1.zip", "skill2.zip", "readme.txt"}
	for _, filename := range testFiles {
		filePath := filepath.Join(skillsDir, filename)
		if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Clean the directory
	if err := CleanEksecdSkillsDir(); err != nil {
		t.Fatalf("CleanEksecdSkillsDir failed: %v", err)
	}

	// Verify directory exists but is empty
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		t.Fatalf("Failed to read skills directory: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("Expected empty directory, but found %d entries", len(entries))
	}
}

func TestCleanEksecdSkillsDir_NonExistent(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()

	// Override home directory
	originalHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Don't create the skills directory - test cleaning non-existent directory
	err := CleanEksecdSkillsDir()

	// Should not fail when directory doesn't exist
	if err != nil {
		t.Errorf("CleanEksecdSkillsDir should not fail for non-existent directory, got: %v", err)
	}

	// Verify directory was NOT created (function returns early if dir doesn't exist)
	skillsDir := filepath.Join(tmpDir, ".config", "eksecd", "skills")
	if _, err := os.Stat(skillsDir); !os.IsNotExist(err) {
		t.Error("Skills directory should not be created if it didn't exist (early return expected)")
	}
}
