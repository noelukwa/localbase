package main

import (
	"strings"
	"testing"
)

func TestNewValidator(t *testing.T) {
	validator := NewValidator()
	if validator == nil {
		t.Error("NewValidator returned nil")
	}
	
	if validator.domainRegex == nil {
		t.Error("validator domainRegex is nil")
	}
}

func TestValidateDomain(t *testing.T) {
	validator := NewValidator()
	
	// Test valid domains
	validDomains := []string{
		"myapp",
		"test-app",
		"my-service",
		"api",
		"web-server",
		"app123",
		"service-1",
		"a",
		"a1",
		"123",
		"test-123-app",
		"api.sudobox",
		"app.example.com",
		"my-app.dev",
		"api.v1.service",
		"sub.domain.test-app",
		"a.b",
		"1.2.3",
	}
	
	for _, domain := range validDomains {
		t.Run("valid_"+domain, func(t *testing.T) {
			err := validator.ValidateDomain(domain)
			if err != nil {
				t.Errorf("expected domain %s to be valid, got error: %v", domain, err)
			}
		})
	}
	
	// Test invalid domains
	invalidDomains := []struct {
		domain      string
		errorSubstr string
	}{
		{"", "cannot be empty"},
		{" ", "cannot be empty"},
		{"-example", "cannot start or end with a hyphen"},
		{"example-", "cannot start or end with a hyphen"},
		{"-", "cannot start or end with a hyphen"},
		{"example.-bad", "cannot start or end with a hyphen"},
		{"example.bad-", "cannot start or end with a hyphen"},
		{".example.com", "cannot start or end with a dot"},
		{"example.com.", "cannot start or end with a dot"},
		{"example..com", "cannot contain empty labels"},
		{"example_test", "invalid domain format"},
		{"example@test", "invalid domain format"},
		{"example test", "invalid domain format"},
		{"example.test space", "invalid domain format"},
		{strings.Repeat("a", 64) + ".com", "cannot exceed 63 characters"},
		{"example." + strings.Repeat("b", 64), "cannot exceed 63 characters"},
		{strings.Repeat("a."+strings.Repeat("b", 60), 5), "cannot exceed 253 characters"},
		{"localhost", "reserved"},
		{"LOCAL", "reserved"},
		{"example", "reserved"},
		{"test", "reserved"},
		{"invalid", "reserved"},
		{"localhost.something", "reserved"},
	}
	
	for _, testCase := range invalidDomains {
		t.Run("invalid_"+testCase.domain, func(t *testing.T) {
			err := validator.ValidateDomain(testCase.domain)
			if err == nil {
				t.Errorf("expected domain %s to be invalid", testCase.domain)
			} else if !strings.Contains(err.Error(), testCase.errorSubstr) {
				t.Errorf("expected error to contain '%s', got: %v", testCase.errorSubstr, err)
			}
		})
	}
}

func TestValidatePort(t *testing.T) {
	validator := NewValidator()
	
	// Test valid ports
	validPorts := []int{
		1024, 3000, 8080, 8443, 9000, 65535,
	}
	
	for _, port := range validPorts {
		t.Run("valid_port", func(t *testing.T) {
			err := validator.ValidatePort(port)
			if err != nil {
				t.Errorf("expected port %d to be valid, got error: %v", port, err)
			}
		})
	}
	
	// Test invalid ports
	invalidPorts := []struct {
		port        int
		errorSubstr string
	}{
		{0, "must be between 1 and 65535"},
		{-1, "must be between 1 and 65535"},
		{65536, "must be between 1 and 65535"},
		{100000, "must be between 1 and 65535"},
		{1, "well-known port"},
		{22, "well-known port"},
		{80, "well-known port"},
		{443, "well-known port"},
		{1023, "well-known port"},
	}
	
	for _, testCase := range invalidPorts {
		t.Run("invalid_port", func(t *testing.T) {
			err := validator.ValidatePort(testCase.port)
			if err == nil {
				t.Errorf("expected port %d to be invalid", testCase.port)
			} else if !strings.Contains(err.Error(), testCase.errorSubstr) {
				t.Errorf("expected error to contain '%s', got: %v", testCase.errorSubstr, err)
			}
		})
	}
}

func TestValidateDomainTrimming(t *testing.T) {
	validator := NewValidator()
	
	// Test that domain validation trims whitespace
	err := validator.ValidateDomain("  valid-domain  ")
	if err != nil {
		t.Errorf("expected trimmed domain to be valid, got error: %v", err)
	}
}

func TestValidateDomainEdgeCases(t *testing.T) {
	validator := NewValidator()
	
	// Test 63-character domain (should be valid)
	longDomain := strings.Repeat("a", 63)
	err := validator.ValidateDomain(longDomain)
	if err != nil {
		t.Errorf("expected 63-character domain to be valid, got error: %v", err)
	}
	
	// Test single character domain
	err = validator.ValidateDomain("a")
	if err != nil {
		t.Errorf("expected single character domain to be valid, got error: %v", err)
	}
	
	// Test numeric domain
	err = validator.ValidateDomain("123")
	if err != nil {
		t.Errorf("expected numeric domain to be valid, got error: %v", err)
	}
	
	// Test mixed alphanumeric with hyphens
	err = validator.ValidateDomain("a1-b2-c3")
	if err != nil {
		t.Errorf("expected mixed alphanumeric domain to be valid, got error: %v", err)
	}
}