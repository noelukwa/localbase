package main

import (
	"strings"
	"testing"
)

func TestNewCommandValidator(t *testing.T) {
	logger := NewLogger(InfoLevel)

	cv := NewCommandValidator(logger)
	if cv == nil {
		t.Fatal("NewCommandValidator returned nil")
	}
	if cv.logger != logger {
		t.Error("logger not set correctly")
	}
}

func TestCommandValidatorValidateDomain(t *testing.T) {
	logger := NewLogger(InfoLevel)
	cv := NewCommandValidator(logger)

	// Test valid domains
	validDomains := []string{"api", "test-app", "localhost", "myapp.local"}
	for _, domain := range validDomains {
		err := cv.ValidateDomain(domain)
		if err != nil {
			t.Errorf("expected domain %s to be valid, got error: %v", domain, err)
		}
	}

	// Test invalid domain with dangerous characters
	err := cv.ValidateDomain("domain;with;semicolons")
	if err == nil {
		t.Error("ValidateDomain should return error for domain with dangerous characters")
	}
}

func TestCommandValidatorValidatePort(t *testing.T) {
	logger := NewLogger(InfoLevel)
	cv := NewCommandValidator(logger)

	// Test valid ports
	validPorts := []int{1024, 3000, 8080, 8443, 9000, 65535}
	for _, port := range validPorts {
		err := cv.ValidatePort(port)
		if err != nil {
			t.Errorf("expected port %d to be valid, got error: %v", port, err)
		}
	}

	// Test invalid ports
	invalidPorts := []int{0, -1, 65536, 100000}
	for _, port := range invalidPorts {
		err := cv.ValidatePort(port)
		if err == nil {
			t.Errorf("expected port %d to be invalid", port)
		}
	}
}

// Test DomainValidator functionality
func TestNewDomainValidator(t *testing.T) {
	validator := NewValidator()
	if validator == nil {
		t.Fatal("NewValidator returned nil")
	}

	if validator.domainRegex == nil {
		t.Error("validator domainRegex is nil")
	}
}

func TestDomainValidatorDomain(t *testing.T) {
	validator := NewValidator()

	// Test valid domains (for local development)
	validDomains := []string{
		"myapp",
		"test-app",
		"api",
		"web-server",
		"app123",
		"api.suboxo",
		"app.example",
		"my-app.dev",
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
		domain string
	}{
		{""},
		{strings.Repeat("a", 254)},
	}

	for _, testCase := range invalidDomains {
		t.Run("invalid_"+testCase.domain, func(t *testing.T) {
			err := validator.ValidateDomain(testCase.domain)
			if err == nil {
				t.Errorf("expected domain %s to be invalid", testCase.domain)
			}
		})
	}
}

func TestDomainValidatorPort(t *testing.T) {
	validator := NewValidator()

	// Test valid ports
	validPorts := []int{1, 1024, 3000, 8080, 8443, 9000, 65535}

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
		port int
	}{
		{0},
		{-1},
		{65536},
	}

	for _, testCase := range invalidPorts {
		t.Run("invalid_port", func(t *testing.T) {
			err := validator.ValidatePort(testCase.port)
			if err == nil {
				t.Errorf("expected port %d to be invalid", testCase.port)
			}
		})
	}
}