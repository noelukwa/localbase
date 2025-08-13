package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
)

// Logger interface for structured logging
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	Fatal(msg string, fields ...Field)
}

// Field represents a key-value pair for structured logging
type Field struct {
	Key   string
	Value any
}

// LogLevel represents the logging level
type LogLevel int

const (
	DebugLevel LogLevel = iota
	InfoLevel
	ErrorLevel
	FatalLevel
)

// DefaultLogger is the standard implementation of the Logger interface
type DefaultLogger struct {
	level  LogLevel
	mu     sync.Mutex
	logger *log.Logger
}

// NewLogger creates a new logger instance
func NewLogger(level LogLevel) *DefaultLogger {
	return &DefaultLogger{
		level:  level,
		logger: log.New(os.Stdout, "", log.LstdFlags),
	}
}

func (l *DefaultLogger) shouldLog(level LogLevel) bool {
	return level >= l.level
}

func (l *DefaultLogger) formatMessage(level, msg string, fields []Field) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("[%s] %s", level, msg))

	for _, field := range fields {
		parts = append(parts, fmt.Sprintf("%s=%v", field.Key, field.Value))
	}

	return strings.Join(parts, " ")
}

// Debug logs a debug message
func (l *DefaultLogger) Debug(msg string, fields ...Field) {
	if !l.shouldLog(DebugLevel) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.Println(l.formatMessage("DEBUG", msg, fields))
}

// Info logs an info message
func (l *DefaultLogger) Info(msg string, fields ...Field) {
	if !l.shouldLog(InfoLevel) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.Println(l.formatMessage("INFO", msg, fields))
}

func (l *DefaultLogger) Error(msg string, fields ...Field) {
	if !l.shouldLog(ErrorLevel) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.Println(l.formatMessage("ERROR", msg, fields))
}

// Fatal logs a fatal error message and exits
func (l *DefaultLogger) Fatal(msg string, fields ...Field) {
	l.mu.Lock()
	l.logger.Println(l.formatMessage("FATAL", msg, fields))
	l.mu.Unlock()
	os.Exit(1)
}

// ParseLogLevel parses a string log level
func ParseLogLevel(level string) LogLevel {
	switch strings.ToLower(level) {
	case "debug":
		return DebugLevel
	case "error":
		return ErrorLevel
	case "fatal":
		return FatalLevel
	default:
		return InfoLevel
	}
}

// Interfaces

// DomainService manages domain registrations
type DomainService interface {
	Add(ctx context.Context, domain string, port int) error
	Remove(ctx context.Context, domain string) error
	List(ctx context.Context) ([]string, error)
	Shutdown(ctx context.Context) error
}

// CaddyClient manages Caddy configurations
type CaddyClient interface {
	GetConfig(ctx context.Context) (map[string]any, error)
	UpdateConfig(ctx context.Context, config map[string]any) error
	AddServerBlock(ctx context.Context, domains []string, port int) error
	RemoveServerBlock(ctx context.Context, domains []string) error
	ClearAllServerBlocks(ctx context.Context) error
	IsRunning(ctx context.Context) (bool, error)
	StartCaddy(ctx context.Context) error
	EnsureRunning(ctx context.Context) error
}

// Config represents the application configuration
type Config struct {
	CaddyAdmin   string `json:"caddy_admin"`
	AdminAddress string `json:"admin_address"`
}

// ConfigManagerInterface handles application configuration
type ConfigManagerInterface interface {
	Read() (*Config, error)
	Write(config *Config) error
	GetConfigPath() (string, error)
}

// Validator provides input validation
type Validator interface {
	ValidateDomain(domain string) error
	ValidatePort(port int) error
}

// Utility functions

// ParseAddress ensures the address includes localhost binding
func ParseAddress(addr string) (string, error) {
	// If no host is specified, default to localhost
	if !strings.Contains(addr, ":") {
		return "", fmt.Errorf("invalid address format: missing port")
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid address format: %w", err)
	}

	// If no host specified, use localhost
	if host == "" {
		host = "localhost"
	}

	// Validate host is localhost or loopback
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return "", fmt.Errorf("admin interface must bind to localhost only")
	}

	// Validate port
	var portNum int
	if _, err := fmt.Sscanf(port, "%d", &portNum); err != nil {
		return "", fmt.Errorf("invalid port: %w", err)
	}

	if portNum < 1 || portNum > 65535 {
		return "", fmt.Errorf("port must be between 1 and 65535")
	}

	return net.JoinHostPort(host, port), nil
}

// getHomeDir returns the user's home directory
func getHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	// Fallback to environment variables
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		return home
	}
	return ""
}