package env

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEnvManager_Basic(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "nairid-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create a test .env file
	envPath := filepath.Join(tempDir, ".env")
	envContent := "TEST_VAR=test_value\nANOTHER_VAR=another_value\n"
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		t.Fatalf("Failed to write test .env file: %v", err)
	}

	// Create EnvManager with custom path
	em := &EnvManager{
		envVars:  make(map[string]string),
		envPath:  envPath,
		stopChan: make(chan struct{}),
	}

	// Test Load
	if err := em.Load(); err != nil {
		t.Fatalf("Failed to load env vars: %v", err)
	}

	// Test Get for loaded var
	if got := em.Get("TEST_VAR"); got != "test_value" {
		t.Errorf("Expected 'test_value', got '%s'", got)
	}

	if got := em.Get("ANOTHER_VAR"); got != "another_value" {
		t.Errorf("Expected 'another_value', got '%s'", got)
	}

	// Test Get for non-existent var (should fall back to os.Getenv)
	_ = os.Setenv("OS_VAR", "os_value")
	defer func() { _ = os.Unsetenv("OS_VAR") }()

	if got := em.Get("OS_VAR"); got != "os_value" {
		t.Errorf("Expected 'os_value', got '%s'", got)
	}
}

func TestEnvManager_Reload(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "nairid-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create initial test .env file
	envPath := filepath.Join(tempDir, ".env")
	envContent := "TEST_VAR=initial_value\n"
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		t.Fatalf("Failed to write test .env file: %v", err)
	}

	// Create EnvManager with custom path
	em := &EnvManager{
		envVars:  make(map[string]string),
		envPath:  envPath,
		stopChan: make(chan struct{}),
	}

	// Load initial values
	if err := em.Load(); err != nil {
		t.Fatalf("Failed to load env vars: %v", err)
	}

	if got := em.Get("TEST_VAR"); got != "initial_value" {
		t.Errorf("Expected 'initial_value', got '%s'", got)
	}

	// Update .env file
	updatedContent := "TEST_VAR=updated_value\nNEW_VAR=new_value\n"
	if err := os.WriteFile(envPath, []byte(updatedContent), 0644); err != nil {
		t.Fatalf("Failed to update test .env file: %v", err)
	}

	// Reload
	if err := em.Reload(); err != nil {
		t.Fatalf("Failed to reload env vars: %v", err)
	}

	// Test updated value
	if got := em.Get("TEST_VAR"); got != "updated_value" {
		t.Errorf("Expected 'updated_value', got '%s'", got)
	}

	// Test new value
	if got := em.Get("NEW_VAR"); got != "new_value" {
		t.Errorf("Expected 'new_value', got '%s'", got)
	}
}

func TestEnvManager_ThreadSafety(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "nairid-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create test .env file
	envPath := filepath.Join(tempDir, ".env")
	envContent := "TEST_VAR=test_value\n"
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		t.Fatalf("Failed to write test .env file: %v", err)
	}

	// Create EnvManager with custom path
	em := &EnvManager{
		envVars:  make(map[string]string),
		envPath:  envPath,
		stopChan: make(chan struct{}),
	}

	if err := em.Load(); err != nil {
		t.Fatalf("Failed to load env vars: %v", err)
	}

	// Test concurrent reads and writes
	var wg sync.WaitGroup
	const numRoutines = 10
	const numOperations = 100

	// Start goroutines that read
	for i := 0; i < numRoutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				_ = em.Get("TEST_VAR")
			}
		}()
	}

	// Start goroutines that reload
	for i := 0; i < numRoutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				_ = em.Reload()
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()
}

func TestEnvManager_MissingFile(t *testing.T) {
	// Create EnvManager with non-existent file path
	em := &EnvManager{
		envVars:  make(map[string]string),
		envPath:  "/non/existent/path/.env",
		stopChan: make(chan struct{}),
	}

	// Load should not fail, just log a debug message
	if err := em.Load(); err != nil {
		t.Errorf("Load should not fail with missing file: %v", err)
	}

	// Reload should not fail either
	if err := em.Reload(); err != nil {
		t.Errorf("Reload should not fail with missing file: %v", err)
	}

	// Should fall back to system env vars
	_ = os.Setenv("FALLBACK_VAR", "fallback_value")
	defer func() { _ = os.Unsetenv("FALLBACK_VAR") }()

	if got := em.Get("FALLBACK_VAR"); got != "fallback_value" {
		t.Errorf("Expected 'fallback_value', got '%s'", got)
	}
}

func TestGetConfigDir_LegacyEksecConfigDir(t *testing.T) {
	// Ensure NAIRI_CONFIG_DIR is not set but EKSEC_CONFIG_DIR is
	originalNairi := os.Getenv("NAIRI_CONFIG_DIR")
	originalEksec := os.Getenv("EKSEC_CONFIG_DIR")
	_ = os.Unsetenv("NAIRI_CONFIG_DIR")
	defer func() {
		if originalNairi != "" {
			_ = os.Setenv("NAIRI_CONFIG_DIR", originalNairi)
		} else {
			_ = os.Unsetenv("NAIRI_CONFIG_DIR")
		}
		if originalEksec != "" {
			_ = os.Setenv("EKSEC_CONFIG_DIR", originalEksec)
		} else {
			_ = os.Unsetenv("EKSEC_CONFIG_DIR")
		}
	}()

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "eksec-config-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	customDir := filepath.Join(tempDir, "legacy-config")
	_ = os.Setenv("EKSEC_CONFIG_DIR", customDir)

	configDir, err := GetConfigDir()
	if err != nil {
		t.Fatalf("GetConfigDir failed: %v", err)
	}

	if configDir != customDir {
		t.Errorf("Expected config dir '%s' from legacy EKSEC_CONFIG_DIR, got '%s'", customDir, configDir)
	}
}

func TestGetConfigDir_NairiOverridesEksec(t *testing.T) {
	// When both are set, NAIRI_CONFIG_DIR should take precedence
	originalNairi := os.Getenv("NAIRI_CONFIG_DIR")
	originalEksec := os.Getenv("EKSEC_CONFIG_DIR")
	defer func() {
		if originalNairi != "" {
			_ = os.Setenv("NAIRI_CONFIG_DIR", originalNairi)
		} else {
			_ = os.Unsetenv("NAIRI_CONFIG_DIR")
		}
		if originalEksec != "" {
			_ = os.Setenv("EKSEC_CONFIG_DIR", originalEksec)
		} else {
			_ = os.Unsetenv("EKSEC_CONFIG_DIR")
		}
	}()

	tempDir, err := os.MkdirTemp("", "config-override-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	nairiDir := filepath.Join(tempDir, "nairi-config")
	eksecDir := filepath.Join(tempDir, "eksec-config")
	_ = os.Setenv("NAIRI_CONFIG_DIR", nairiDir)
	_ = os.Setenv("EKSEC_CONFIG_DIR", eksecDir)

	configDir, err := GetConfigDir()
	if err != nil {
		t.Fatalf("GetConfigDir failed: %v", err)
	}

	if configDir != nairiDir {
		t.Errorf("Expected NAIRI_CONFIG_DIR '%s' to take precedence, got '%s'", nairiDir, configDir)
	}
}

func TestGetConfigDir_Default(t *testing.T) {
	// Ensure NAIRI_CONFIG_DIR and EKSEC_CONFIG_DIR are not set
	originalValue := os.Getenv("NAIRI_CONFIG_DIR")
	originalEksec := os.Getenv("EKSEC_CONFIG_DIR")
	_ = os.Unsetenv("NAIRI_CONFIG_DIR")
	_ = os.Unsetenv("EKSEC_CONFIG_DIR")
	defer func() {
		if originalValue != "" {
			_ = os.Setenv("NAIRI_CONFIG_DIR", originalValue)
		}
		if originalEksec != "" {
			_ = os.Setenv("EKSEC_CONFIG_DIR", originalEksec)
		}
	}()

	configDir, err := GetConfigDir()
	if err != nil {
		t.Fatalf("GetConfigDir failed: %v", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home directory: %v", err)
	}

	expectedDir := filepath.Join(homeDir, ".config", "eksecd")
	if configDir != expectedDir {
		t.Errorf("Expected config dir '%s', got '%s'", expectedDir, configDir)
	}

	// Verify directory was created
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		t.Errorf("Config directory was not created: %s", configDir)
	}
}

func TestGetConfigDir_CustomAbsolute(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "nairid-config-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Set custom config directory
	customDir := filepath.Join(tempDir, "custom-config")
	originalValue := os.Getenv("NAIRI_CONFIG_DIR")
	_ = os.Setenv("NAIRI_CONFIG_DIR", customDir)
	defer func() {
		if originalValue != "" {
			_ = os.Setenv("NAIRI_CONFIG_DIR", originalValue)
		} else {
			_ = os.Unsetenv("NAIRI_CONFIG_DIR")
		}
	}()

	configDir, err := GetConfigDir()
	if err != nil {
		t.Fatalf("GetConfigDir failed: %v", err)
	}

	if configDir != customDir {
		t.Errorf("Expected config dir '%s', got '%s'", customDir, configDir)
	}

	// Verify directory was created
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		t.Errorf("Config directory was not created: %s", configDir)
	}
}

func TestGetConfigDir_CustomTilde(t *testing.T) {
	// Set custom config directory with tilde
	originalValue := os.Getenv("NAIRI_CONFIG_DIR")
	_ = os.Setenv("NAIRI_CONFIG_DIR", "~/.nairid-custom")
	defer func() {
		if originalValue != "" {
			_ = os.Setenv("NAIRI_CONFIG_DIR", originalValue)
		} else {
			_ = os.Unsetenv("NAIRI_CONFIG_DIR")
		}
	}()

	configDir, err := GetConfigDir()
	if err != nil {
		t.Fatalf("GetConfigDir failed: %v", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home directory: %v", err)
	}

	expectedDir := filepath.Join(homeDir, ".nairid-custom")
	if configDir != expectedDir {
		t.Errorf("Expected config dir '%s', got '%s'", expectedDir, configDir)
	}

	// Clean up created directory
	_ = os.RemoveAll(configDir)
}

func TestGetOutboundAttachmentsDir_CreatesDirectory(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "nairid-outbound-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	originalValue := os.Getenv("NAIRI_CONFIG_DIR")
	_ = os.Setenv("NAIRI_CONFIG_DIR", tempDir)
	defer func() {
		if originalValue != "" {
			_ = os.Setenv("NAIRI_CONFIG_DIR", originalValue)
		} else {
			_ = os.Unsetenv("NAIRI_CONFIG_DIR")
		}
	}()

	dir, err := GetOutboundAttachmentsDir("test-job-123")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	expectedDir := filepath.Join(tempDir, "attachments", "test-job-123")
	if dir != expectedDir {
		t.Errorf("Expected dir '%s', got '%s'", expectedDir, dir)
	}

	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		t.Fatalf("Expected directory to exist at %s", dir)
	}
	if !info.IsDir() {
		t.Errorf("Expected %s to be a directory", dir)
	}

	// Verify directory is group-writable (0775) so agentrunner can write attachments
	perm := info.Mode().Perm()
	if perm&0020 == 0 {
		t.Errorf("Expected directory to be group-writable (0775), got %o", perm)
	}
}

func TestGetOutboundAttachmentsDir_DifferentJobs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "nairid-outbound-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	originalValue := os.Getenv("NAIRI_CONFIG_DIR")
	_ = os.Setenv("NAIRI_CONFIG_DIR", tempDir)
	defer func() {
		if originalValue != "" {
			_ = os.Setenv("NAIRI_CONFIG_DIR", originalValue)
		} else {
			_ = os.Unsetenv("NAIRI_CONFIG_DIR")
		}
	}()

	dir1, err := GetOutboundAttachmentsDir("job-aaa")
	if err != nil {
		t.Fatalf("Expected no error for job-aaa, got: %v", err)
	}

	dir2, err := GetOutboundAttachmentsDir("job-bbb")
	if err != nil {
		t.Fatalf("Expected no error for job-bbb, got: %v", err)
	}

	if dir1 == dir2 {
		t.Errorf("Expected different directories for different jobs, both got: %s", dir1)
	}
}
