package env

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nairid/core/log"
	"github.com/joho/godotenv"
)

type EnvManager struct {
	mu       sync.RWMutex
	envVars  map[string]string
	envPath  string
	ticker   *time.Ticker
	stopChan chan struct{}
	hooks    []func()
}

func NewEnvManager() (*EnvManager, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get config directory: %w", err)
	}

	envPath := filepath.Join(configDir, ".env")

	em := &EnvManager{
		envVars:  make(map[string]string),
		envPath:  envPath,
		stopChan: make(chan struct{}),
		hooks:    []func(){},
	}

	if err := em.Load(); err != nil {
		log.Error("Failed to load initial environment variables: %v", err)
	}

	return em, nil
}

// GetConfigDir returns the config directory path, either from NAIRI_CONFIG_DIR
// environment variable (or legacy EKSEC_CONFIG_DIR) or the default ~/.config/eksecd
func GetConfigDir() (string, error) {
	// Check if NAIRI_CONFIG_DIR is set, fall back to legacy EKSEC_CONFIG_DIR
	configDir := os.Getenv("NAIRI_CONFIG_DIR")
	if configDir == "" {
		configDir = os.Getenv("EKSEC_CONFIG_DIR") // Legacy env var
	}
	if configDir != "" {
		// Expand ~ if present
		if len(configDir) >= 2 && configDir[:2] == "~/" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get home directory: %w", err)
			}
			configDir = filepath.Join(homeDir, configDir[2:])
		}

		if err := os.MkdirAll(configDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create config directory: %w", err)
		}

		return configDir, nil
	}

	// Default to ~/.config/eksecd
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	configDir = filepath.Join(homeDir, ".config", "eksecd")

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %w", err)
	}

	return configDir, nil
}

// GetOutboundAttachmentsDir returns the directory path for storing outbound attachments for a given job
// and creates the directory if it doesn't exist. Per-job subdirectories prevent file leakage between concurrent jobs.
func GetOutboundAttachmentsDir(jobID string) (string, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config directory: %w", err)
	}

	dir := filepath.Join(configDir, "attachments", jobID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create outbound attachments directory: %w", err)
	}

	return dir, nil
}

func (em *EnvManager) Load() error {
	em.mu.Lock()
	defer em.mu.Unlock()

	if _, err := os.Stat(em.envPath); os.IsNotExist(err) {
		log.Debug("No .env file found at %s, using system environment variables only", em.envPath)
		return nil
	}

	envMap, err := godotenv.Read(em.envPath)
	if err != nil {
		return fmt.Errorf("failed to read .env file: %w", err)
	}

	for key, value := range envMap {
		em.envVars[key] = value
		os.Setenv(key, value)
	}

	log.Debug("Loaded %d environment variables from %s", len(envMap), em.envPath)
	return nil
}

func (em *EnvManager) Get(key string) string {
	em.mu.RLock()
	defer em.mu.RUnlock()

	if value, exists := em.envVars[key]; exists {
		return value
	}

	return os.Getenv(key)
}

func (em *EnvManager) Set(key, value string) error {
	em.mu.Lock()
	defer em.mu.Unlock()

	// Update in-memory map
	em.envVars[key] = value

	// Update process environment
	if err := os.Setenv(key, value); err != nil {
		return fmt.Errorf("failed to set environment variable: %w", err)
	}

	return nil
}

func (em *EnvManager) Reload() error {
	em.mu.Lock()
	defer em.mu.Unlock()

	if _, err := os.Stat(em.envPath); os.IsNotExist(err) {
		log.Debug("No .env file found at %s during reload", em.envPath)
		return nil
	}

	envMap, err := godotenv.Read(em.envPath)
	if err != nil {
		return fmt.Errorf("failed to reload .env file: %w", err)
	}

	// Update/add keys from the .env file
	for key, value := range envMap {
		em.envVars[key] = value
		os.Setenv(key, value)
	}

	log.Info("Reloaded %d environment variables from %s", len(envMap), em.envPath)

	// Call all registered reload hooks
	em.callHooks()

	return nil
}

func (em *EnvManager) StartPeriodicRefresh(interval time.Duration) {
	em.ticker = time.NewTicker(interval)

	go func() {
		log.Info("Started periodic environment variable refresh every %s", interval)

		for {
			select {
			case <-em.ticker.C:
				if err := em.Reload(); err != nil {
					log.Error("Failed to reload environment variables: %v", err)
				}
			case <-em.stopChan:
				log.Info("Stopping periodic environment variable refresh")
				return
			}
		}
	}()
}

func (em *EnvManager) Stop() {
	if em.ticker != nil {
		em.ticker.Stop()
	}

	close(em.stopChan)
}

func (em *EnvManager) RegisterReloadHook(hook func()) {
	em.mu.Lock()
	defer em.mu.Unlock()

	em.hooks = append(em.hooks, hook)
	log.Debug("Registered reload hook, total hooks: %d", len(em.hooks))
}

func (em *EnvManager) callHooks() {
	for i, hook := range em.hooks {
		func(idx int, h func()) {
			defer func() {
				if r := recover(); r != nil {
					log.Error("Reload hook %d panicked: %v", idx, r)
				}
			}()

			log.Debug("Executing reload hook %d", idx)
			h()
		}(i, hook)
	}
}
