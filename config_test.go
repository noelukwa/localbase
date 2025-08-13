package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewConfigManager(t *testing.T) {
	logger := NewLogger(InfoLevel)

	cm := NewConfigManager(logger)
	if cm == nil {
		t.Fatal("NewConfigManager returned nil")
	}
	if cm.logger != logger {
		t.Error("logger not set correctly")
	}
}

func TestGetConfigPath(t *testing.T) {
	logger := NewLogger(InfoLevel)
	cm := NewConfigManager(logger)

	path, err := cm.GetConfigPath()
	if err != nil {
		t.Fatalf("GetConfigPath failed: %v", err)
	}

	if path == "" {
		t.Error("GetConfigPath returned empty path")
	}

	// Verify the path contains the expected directory name
	if !strings.Contains(path, "localbase") {
		t.Errorf("config path should contain 'localbase', got: %s", path)
	}

	// Verify directory is created
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("config directory should be created: %s", path)
	}
}

func TestConfigManagerReadWrite(t *testing.T) {
	logger := NewLogger(InfoLevel)
	cm := NewConfigManager(logger)

	// Create a test config
	testConfig := &Config{
		CaddyAdmin:   "http://localhost:2019",
		AdminAddress: "localhost:2025",
	}

	// Write the config
	err := cm.Write(testConfig)
	if err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Read the config back
	readConfig, err := cm.Read()
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	// Verify config values
	if readConfig.CaddyAdmin != testConfig.CaddyAdmin {
		t.Errorf("CaddyAdmin mismatch: expected %s, got %s", testConfig.CaddyAdmin, readConfig.CaddyAdmin)
	}

	if readConfig.AdminAddress != testConfig.AdminAddress {
		t.Errorf("AdminAddress mismatch: expected %s, got %s", testConfig.AdminAddress, readConfig.AdminAddress)
	}
}

func TestConfigManagerDefaultConfig(t *testing.T) {
	logger := NewLogger(InfoLevel)
	cm := NewConfigManager(logger)

	// Get config path and remove config file if it exists
	configPath, err := cm.GetConfigPath()
	if err != nil {
		t.Fatalf("GetConfigPath failed: %v", err)
	}

	configFile := filepath.Join(configPath, "config.json")
	_ = os.Remove(configFile) // Ignore error if file doesn't exist

	// Read config (should return default)
	config, err := cm.Read()
	if err != nil {
		t.Fatalf("Failed to read default config: %v", err)
	}

	// Verify default values
	if config.CaddyAdmin != "http://localhost:2019" {
		t.Errorf("Default CaddyAdmin mismatch: expected 'http://localhost:2019', got '%s'", config.CaddyAdmin)
	}

	if config.AdminAddress != "localhost:2025" {
		t.Errorf("Default AdminAddress mismatch: expected 'localhost:2025', got '%s'", config.AdminAddress)
	}
}

func TestConfigManagerInvalidJSON(t *testing.T) {
	logger := NewLogger(InfoLevel)
	cm := NewConfigManager(logger)

	// Get config path
	configPath, err := cm.GetConfigPath()
	if err != nil {
		t.Fatalf("GetConfigPath failed: %v", err)
	}

	// Write invalid JSON
	configFile := filepath.Join(configPath, "config.json")
	err = os.WriteFile(configFile, []byte("invalid json content"), 0o600)
	if err != nil {
		t.Fatalf("Failed to write invalid JSON: %v", err)
	}

	// Try to read config (should fail)
	_, err = cm.Read()
	if err == nil {
		t.Error("Expected error when reading invalid JSON config")
	}

	// Clean up
	_ = os.Remove(configFile)
}

func TestConfigManagerConfigValidation(t *testing.T) {
	logger := NewLogger(InfoLevel)
	cm := NewConfigManager(logger)

	// Test config with empty required fields
	testConfig := &Config{
		CaddyAdmin:   "",
		AdminAddress: "",
	}

	// Write and read back
	err := cm.Write(testConfig)
	if err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	readConfig, err := cm.Read()
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	// Should have default values filled in
	if readConfig.CaddyAdmin == "" {
		t.Error("Empty CaddyAdmin should be filled with default")
	}

	if readConfig.AdminAddress == "" {
		t.Error("Empty AdminAddress should be filled with default")
	}
}