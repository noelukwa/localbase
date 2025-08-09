package main

import (
	"fmt"
	"regexp"
	"strings"
)

// DomainValidator implements domain and port validation
type DomainValidator struct {
	domainRegex *regexp.Regexp
}

// NewValidator creates a new validator instance
func NewValidator() *DomainValidator {
	// Modified regex to support domain names with dots for local development
	// Each label (part separated by dots) follows RFC 1123 rules
	domainRegex := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`)
	return &DomainValidator{
		domainRegex: domainRegex,
	}
}

// ValidateDomain checks if a domain name is valid
func (v *DomainValidator) ValidateDomain(domain string) error {
	domain = strings.TrimSpace(domain)
	
	if domain == "" {
		return fmt.Errorf("domain cannot be empty")
	}
	
	// Check for leading/trailing dots first
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return fmt.Errorf("domain cannot start or end with a dot")
	}
	
	// Check overall domain length (253 chars max for FQDN, but we'll be more restrictive)
	if len(domain) > 253 {
		return fmt.Errorf("domain length cannot exceed 253 characters")
	}
	
	// Split domain into labels and validate each label
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if len(label) == 0 {
			return fmt.Errorf("domain cannot contain empty labels (consecutive dots)")
		}
		if len(label) > 63 {
			return fmt.Errorf("domain label '%s' cannot exceed 63 characters", label)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("domain label '%s' cannot start or end with a hyphen", label)
		}
	}
	
	if !v.domainRegex.MatchString(domain) {
		return fmt.Errorf("invalid domain format: must contain only alphanumeric characters, hyphens, and dots")
	}
	
	// Check for reserved names (check the first label for single-label domains)
	firstLabel := labels[0]
	reserved := []string{"localhost", "local", "example", "test", "invalid"}
	for _, r := range reserved {
		if strings.EqualFold(firstLabel, r) {
			return fmt.Errorf("domain '%s' is reserved", firstLabel)
		}
	}
	
	return nil
}

// ValidatePort checks if a port number is valid
func (v *DomainValidator) ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", port)
	}
	
	// Well-known ports typically require elevated privileges
	if port < 1024 {
		return fmt.Errorf("port %d is a well-known port and may require elevated privileges", port)
	}
	
	return nil
}