package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestBasicIntegration tests the basic flow without requiring Caddy
func TestBasicIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create mock Caddy server
	caddyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config/":
			if r.Method == http.MethodGet {
				// Return empty config
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(`{"apps":{"http":{"servers":{}}}}}`)); err != nil {
					http.Error(w, "failed to write response", http.StatusInternalServerError)
				}
			} else if r.Method == http.MethodPatch {
				// Accept config updates
				w.WriteHeader(http.StatusOK)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer caddyServer.Close()

	// Create config with mock Caddy server
	config := &Config{
		AdminAddress: "localhost:0", // Use random port
		CaddyAdmin:   caddyServer.URL,
	}

	logger := NewLogger(InfoLevel)

	// Create and start server
	server, err := NewServer(config, logger)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Start server in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErrChan := make(chan error, 1)
	go func() {
		err := server.Start(ctx)
		serverErrChan <- err
	}()

	// Wait for server to actually start listening
	var actualAddr string
	for i := 0; i < 50; i++ { // Try for up to 5 seconds
		time.Sleep(100 * time.Millisecond)
		// Use a safe method to get the address without direct field access
		if addr := server.GetListenerAddr(); addr != "" {
			actualAddr = addr
			break
		}
	}

	if actualAddr == "" {
		t.Fatal("Server failed to start listening")
	}

	// Create new config with actual address for client
	clientConfig := &Config{
		AdminAddress: actualAddr,
		CaddyAdmin:   config.CaddyAdmin,
	}
	configManager := NewConfigManager(logger)
	if err := configManager.Write(clientConfig); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Create client
	client, err := NewClient(logger)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test ping
	err = client.SendCommand("ping", nil)
	if err != nil {
		t.Errorf("Ping failed: %v", err)
	}

	// Test add domain
	err = client.SendCommand("add", map[string]any{
		"domain": "testapp",
		"port":   3000,
	})
	if err != nil {
		t.Errorf("Add domain failed: %v", err)
	}

	// Test list domains
	err = client.SendCommand("list", nil)
	if err != nil {
		t.Errorf("List domains failed: %v", err)
	}

	// Test remove domain
	err = client.SendCommand("remove", map[string]any{
		"domain": "testapp.local",
	})
	if err != nil {
		t.Errorf("Remove domain failed: %v", err)
	}

	// Test shutdown
	err = client.SendCommand("shutdown", nil)
	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}

	// Wait for server to shut down
	select {
	case err := <-serverErrChan:
		if err != nil {
			t.Errorf("Server shutdown with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Server did not shut down within timeout")
		cancel() // Force shutdown
	}
}

// TestConfigManagerIntegration tests configuration management
func TestConfigManagerIntegration(t *testing.T) {
	logger := NewLogger(InfoLevel)
	manager := NewConfigManager(logger)

	// Test reading default config
	config, err := manager.Read()
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	// Should have defaults
	if config.CaddyAdmin == "" {
		t.Error("Expected default CaddyAdmin")
	}
	if config.AdminAddress == "" {
		t.Error("Expected default AdminAddress")
	}

	// Test writing custom config with valid localhost addresses
	customConfig := &Config{
		CaddyAdmin:   "http://localhost:2020",
		AdminAddress: "localhost:2026",
	}

	err = manager.Write(customConfig)
	if err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Test reading custom config back
	readConfig, err := manager.Read()
	if err != nil {
		t.Fatalf("Failed to read custom config: %v", err)
	}

	if readConfig.CaddyAdmin != customConfig.CaddyAdmin {
		t.Errorf("CaddyAdmin mismatch: expected %s, got %s", customConfig.CaddyAdmin, readConfig.CaddyAdmin)
	}
	if readConfig.AdminAddress != customConfig.AdminAddress {
		t.Errorf("AdminAddress mismatch: expected %s, got %s", customConfig.AdminAddress, readConfig.AdminAddress)
	}
}

// TestValidatorIntegration tests input validation
func TestValidatorIntegration(t *testing.T) {
	validator := NewValidator()

	// Test valid inputs
	validCases := []struct {
		domain string
		port   int
	}{
		{"myapp", 3000},
		{"test-service", 8080},
		{"api-v2", 9000},
	}

	for _, tc := range validCases {
		t.Run(fmt.Sprintf("valid_%s_%d", tc.domain, tc.port), func(t *testing.T) {
			if err := validator.ValidateDomain(tc.domain); err != nil {
				t.Errorf("Domain %s should be valid: %v", tc.domain, err)
			}
			if err := validator.ValidatePort(tc.port); err != nil {
				t.Errorf("Port %d should be valid: %v", tc.port, err)
			}
		})
	}

	// Test invalid inputs
	invalidCases := []struct {
		domain    string
		port      int
		expectErr bool
	}{
		{"", 3000, true},          // empty domain
		{"myapp", 0, true},        // invalid port
		{"myapp", 70000, true},    // port too high
		{"localhost", 3000, true}, // reserved domain
	}

	for _, tc := range invalidCases {
		t.Run(fmt.Sprintf("invalid_%s_%d", tc.domain, tc.port), func(t *testing.T) {
			domainErr := validator.ValidateDomain(tc.domain)
			portErr := validator.ValidatePort(tc.port)

			if tc.expectErr && domainErr == nil && portErr == nil {
				t.Errorf("Expected validation error for domain=%s port=%d", tc.domain, tc.port)
			}
		})
	}
}

// TestLoggerIntegration tests logging functionality
func TestLoggerIntegration(t *testing.T) {
	// Test different log levels
	levels := []LogLevel{DebugLevel, InfoLevel, ErrorLevel}

	for _, level := range levels {
		t.Run(fmt.Sprintf("level_%d", level), func(t *testing.T) {
			logger := NewLogger(level)

			// These should not panic
			logger.Debug("debug message", Field{"key", "value"})
			logger.Info("info message", Field{"key", "value"})
			logger.Error("error message", Field{"key", "value"})

			// Test ParseLogLevel
			parsedLevel := ParseLogLevel("info")
			if parsedLevel != InfoLevel {
				t.Errorf("Expected InfoLevel, got %d", parsedLevel)
			}
		})
	}
}
