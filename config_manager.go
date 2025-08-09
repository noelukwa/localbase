package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/mitchellh/go-homedir"
)

// ConfigManagerImpl implements the ConfigManager interface
type ConfigManagerImpl struct {
	mu     sync.RWMutex
	logger Logger
}

// NewConfigManager creates a new config manager
func NewConfigManager(logger Logger) *ConfigManagerImpl {
	return &ConfigManagerImpl{
		logger: logger,
	}
}

// GetConfigPath returns the configuration directory path
func (c *ConfigManagerImpl) GetConfigPath() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	var configDir string
	switch runtime.GOOS {
	case "windows":
		configDir = filepath.Join(home, "AppData", "Roaming", "localbase")
	case "darwin":
		configDir = filepath.Join(home, "Library", "Application Support", "localbase")
	default: // linux, bsd, etc.
		configDir = filepath.Join(home, ".config", "localbase")
	}

	return configDir, nil
}

// Read reads the configuration from disk
func (c *ConfigManagerImpl) Read() (*Config, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	configDir, err := c.GetConfigPath()
	if err != nil {
		return nil, err
	}

	configFile := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			c.logger.Debug("config file not found, using defaults")
			return c.getDefaultConfig(), nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Apply defaults for missing fields
	if cfg.CaddyAdmin == "" {
		cfg.CaddyAdmin = "http://localhost:2019"
	}
	if cfg.AdminAddress == "" {
		cfg.AdminAddress = "localhost:2025"
	}

	return &cfg, nil
}

// Write saves the configuration to disk
func (c *ConfigManagerImpl) Write(config *Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	configDir, err := c.GetConfigPath()
	if err != nil {
		return err
	}

	// Create config directory if it doesn't exist
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configFile := filepath.Join(configDir, "config.json")

	// Marshal with pretty printing
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write atomically by writing to temp file first
	tempFile := configFile + ".tmp"
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	// Rename temp file to actual config file
	if err := os.Rename(tempFile, configFile); err != nil {
		os.Remove(tempFile) // Clean up temp file
		return fmt.Errorf("failed to save config file: %w", err)
	}

	c.logger.Info("configuration saved", Field{"path", configFile})
	return nil
}

func (c *ConfigManagerImpl) getDefaultConfig() *Config {
	return &Config{
		CaddyAdmin:   "http://localhost:2019",
		AdminAddress: "localhost:2025",
	}
}