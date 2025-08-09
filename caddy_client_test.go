package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewCaddyClient(t *testing.T) {
	logger := NewLogger(InfoLevel)
	client := NewCaddyClient("http://localhost:2019", logger)
	
	if client == nil {
		t.Error("NewCaddyClient returned nil")
	}
	
	if client.adminURL != "http://localhost:2019" {
		t.Errorf("Expected adminURL http://localhost:2019, got %s", client.adminURL)
	}
	
	if client.logger != logger {
		t.Error("Logger not set correctly")
	}
	
	if client.httpClient == nil {
		t.Error("HTTP client not initialized")
	}
	
	if client.httpClient.Timeout != 10*time.Second {
		t.Errorf("Expected timeout 10s, got %v", client.httpClient.Timeout)
	}
}

func TestCaddyClientGetConfig(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config/" {
			t.Errorf("Expected path /config/, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET method, got %s", r.Method)
		}
		
		config := map[string]interface{}{
			"apps": map[string]interface{}{
				"http": map[string]interface{}{
					"servers": map[string]interface{}{},
				},
			},
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(config)
	}))
	defer server.Close()
	
	logger := NewLogger(InfoLevel)
	client := NewCaddyClient(server.URL, logger)
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	config, err := client.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	
	if config == nil {
		t.Error("GetConfig returned nil config")
	}
	
	apps, ok := config["apps"].(map[string]interface{})
	if !ok {
		t.Error("Expected apps in config")
	}
	
	_, ok = apps["http"].(map[string]interface{})
	if !ok {
		t.Error("Expected http app in config")
	}
}

func TestCaddyClientGetConfigError(t *testing.T) {
	// Create mock server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()
	
	logger := NewLogger(InfoLevel)
	client := NewCaddyClient(server.URL, logger)
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	_, err := client.GetConfig(ctx)
	if err == nil {
		t.Error("Expected error for server error response")
	}
	
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("Expected error to contain status code, got: %v", err)
	}
}

func TestCaddyClientUpdateConfig(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config/" {
			t.Errorf("Expected path /config/, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPatch {
			t.Errorf("Expected PATCH method, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		
		// Decode and verify the config
		var config map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			t.Errorf("Failed to decode request body: %v", err)
		}
		
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	
	logger := NewLogger(InfoLevel)
	client := NewCaddyClient(server.URL, logger)
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	testConfig := map[string]interface{}{
		"test": "value",
	}
	
	err := client.UpdateConfig(ctx, testConfig)
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}
}

func TestCaddyClientAddServerBlock(t *testing.T) {
	// Track requests
	requestCount := 0
	
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		
		if r.Method == http.MethodGet {
			// Return empty config for GET request
			config := map[string]interface{}{
				"apps": map[string]interface{}{
					"http": map[string]interface{}{
						"servers": map[string]interface{}{},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(config)
		} else if r.Method == http.MethodPatch {
			// Verify PATCH request
			var config map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
				t.Errorf("Failed to decode PATCH body: %v", err)
			}
			
			// Verify structure
			apps, ok := config["apps"].(map[string]interface{})
			if !ok {
				t.Error("Expected apps in config")
			}
			
			httpApp, ok := apps["http"].(map[string]interface{})
			if !ok {
				t.Error("Expected http app in config")
			}
			
			servers, ok := httpApp["servers"].(map[string]interface{})
			if !ok {
				t.Error("Expected servers in http app")
			}
			
			defaultServer, ok := servers["default"].(map[string]interface{})
			if !ok {
				t.Error("Expected default server")
			}
			
			routes, ok := defaultServer["routes"].([]interface{})
			if !ok {
				t.Error("Expected routes in default server")
			}
			
			if len(routes) != 1 {
				t.Errorf("Expected 1 route, got %d", len(routes))
			}
			
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()
	
	logger := NewLogger(InfoLevel)
	client := NewCaddyClient(server.URL, logger)
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	err := client.AddServerBlock(ctx, []string{"test.local"}, 3000)
	if err != nil {
		t.Fatalf("AddServerBlock failed: %v", err)
	}
	
	if requestCount != 2 {
		t.Errorf("Expected 2 requests (GET + PATCH), got %d", requestCount)
	}
}

func TestCaddyClientIsRunning(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{})
	}))
	defer server.Close()
	
	logger := NewLogger(InfoLevel)
	client := NewCaddyClient(server.URL, logger)
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	running, err := client.IsRunning(ctx)
	if err != nil {
		t.Fatalf("IsRunning failed: %v", err)
	}
	
	if !running {
		t.Error("Expected Caddy to be running")
	}
}

func TestCaddyClientIsRunningFalse(t *testing.T) {
	// Use non-existent server
	logger := NewLogger(InfoLevel)
	client := NewCaddyClient("http://localhost:99999", logger)
	
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	
	running, err := client.IsRunning(ctx)
	if err != nil {
		t.Fatalf("IsRunning should not fail for connection error: %v", err)
	}
	
	if running {
		t.Error("Expected Caddy to not be running")
	}
}

func TestCaddyClientEnsureRunning(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{})
	}))
	defer server.Close()
	
	logger := NewLogger(InfoLevel)
	client := NewCaddyClient(server.URL, logger)
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	err := client.EnsureRunning(ctx)
	if err != nil {
		t.Fatalf("EnsureRunning failed: %v", err)
	}
}

func TestCaddyClientEnsureRunningError(t *testing.T) {
	// Use non-existent server to test failure to start Caddy
	logger := NewLogger(InfoLevel)
	client := NewCaddyClient("http://localhost:99999", logger)
	
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	
	err := client.EnsureRunning(ctx)
	if err == nil {
		t.Error("Expected error when Caddy fails to start")
		return
	}
	
	// With the new auto-start behavior, we expect an error about failing to start Caddy
	// This could be either "failed to start Caddy" or "context deadline exceeded"
	if !strings.Contains(err.Error(), "failed to start Caddy") && !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("Expected error message about failing to start Caddy or timeout, got: %v", err)
	}
}