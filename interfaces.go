package main

import (
	"context"
	"net"
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
	Value interface{}
}

// DomainService manages domain registrations
type DomainService interface {
	Add(ctx context.Context, domain string, port int) error
	Remove(ctx context.Context, domain string) error
	List(ctx context.Context) ([]string, error)
	Shutdown(ctx context.Context) error
}

// MDNSService handles mDNS broadcasting
type MDNSService interface {
	Register(ctx context.Context, domain, service, host string, port int, ip net.IP) (MDNSServer, error)
	StartBroadcast(ctx context.Context) error
}

// MDNSServer represents a registered mDNS service
type MDNSServer interface {
	Shutdown() error
}

// CaddyClient manages Caddy configurations
type CaddyClient interface {
	GetConfig(ctx context.Context) (map[string]interface{}, error)
	UpdateConfig(ctx context.Context, config map[string]interface{}) error
	AddServerBlock(ctx context.Context, domains []string, port int) error
	RemoveServerBlock(ctx context.Context, domains []string) error
	ClearAllServerBlocks(ctx context.Context) error
	IsRunning(ctx context.Context) (bool, error)
	StartCaddy(ctx context.Context) error
	EnsureRunning(ctx context.Context) error
}

// ConfigManager handles application configuration
type ConfigManager interface {
	Read() (*Config, error)
	Write(config *Config) error
	GetConfigPath() (string, error)
}

// ConnectionPool manages client connections
type ConnectionPool interface {
	Accept(conn net.Conn) error
	Close() error
}

// Validator provides input validation
type Validator interface {
	ValidateDomain(domain string) error
	ValidatePort(port int) error
}