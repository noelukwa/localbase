package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/oleksandr/bonjour"
)

// ConfigManager handles configuration persistence
type ConfigManager struct {
	logger Logger
}

// NewConfigManager creates a new config manager
func NewConfigManager(logger Logger) *ConfigManager {
	return &ConfigManager{logger: logger}
}

// GetConfigPath returns the OS-specific config directory path
func (c *ConfigManager) GetConfigPath() (string, error) {
	var configDir string

	switch runtime.GOOS {
	case "darwin":
		// macOS: ~/Library/Application Support/localbase
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		configDir = filepath.Join(home, "Library", "Application Support", "localbase")
	case "linux":
		// Linux: ~/.config/localbase or $XDG_CONFIG_HOME/localbase
		if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
			configDir = filepath.Join(xdgConfig, "localbase")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get home directory: %w", err)
			}
			configDir = filepath.Join(home, ".config", "localbase")
		}
	case "windows":
		// Windows: %APPDATA%\localbase
		if appData := os.Getenv("APPDATA"); appData != "" {
			configDir = filepath.Join(appData, "localbase")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get home directory: %w", err)
			}
			configDir = filepath.Join(home, "AppData", "Roaming", "localbase")
		}
	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return "", fmt.Errorf("failed to create config directory: %w", err)
	}

	return configDir, nil
}

// GetConfigFile returns the path to the config file
func (c *ConfigManager) GetConfigFile() (string, error) {
	configPath, err := c.GetConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(configPath, "config.json"), nil
}

// Read reads the configuration from disk
func (c *ConfigManager) Read() (*Config, error) {
	configFile, err := c.GetConfigFile()
	if err != nil {
		return nil, err
	}

	// Default config
	config := &Config{
		CaddyAdmin:   "http://localhost:2019",
		AdminAddress: "localhost:2025",
	}

	// Read config file if it exists
	data, err := os.ReadFile(configFile) // #nosec G304 - config file path is controlled
	if err != nil {
		if os.IsNotExist(err) {
			// Return default config if file doesn't exist
			return config, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse JSON
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate required fields
	if config.CaddyAdmin == "" {
		config.CaddyAdmin = "http://localhost:2019"
	}
	if config.AdminAddress == "" {
		config.AdminAddress = "localhost:2025"
	}

	return config, nil
}

// Write writes the configuration to disk
func (c *ConfigManager) Write(config *Config) error {
	configFile, err := c.GetConfigFile()
	if err != nil {
		return err
	}

	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write atomically by writing to temp file first
	tempFile := configFile + ".tmp"
	if err := os.WriteFile(tempFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write temp config file: %w", err)
	}

	// Rename temp file to actual config file
	if err := os.Rename(tempFile, configFile); err != nil {
		// Clean up temp file
		_ = os.Remove(tempFile)
		return fmt.Errorf("failed to save config file: %w", err)
	}

	c.logger.Info("configuration saved", Field{"path", configFile})
	return nil
}

// LocalBase implements the core domain management functionality
type LocalBase struct {
	logger      Logger
	caddyClient CaddyClient
	validator   Validator
	domainsmu   sync.RWMutex
	domains     map[string]*domainEntry
	mdnsServers map[string]*bonjour.Server
	mdnsMu      sync.RWMutex
	localIP     net.IP
	ipMu        sync.RWMutex
}

type domainEntry struct {
	port int
}

// NewLocalBase creates a new LocalBase instance
func NewLocalBase(logger Logger, _ *ConfigManager, caddyClient CaddyClient, validator Validator) (*LocalBase, error) {
	localIP, err := getLocalIP()
	if err != nil {
		return nil, fmt.Errorf("failed to get local IP: %w", err)
	}

	return &LocalBase{
		logger:      logger,
		caddyClient: caddyClient,
		validator:   validator,
		domains:     make(map[string]*domainEntry),
		mdnsServers: make(map[string]*bonjour.Server),
		localIP:     localIP,
	}, nil
}

// Add registers a new domain
func (l *LocalBase) Add(ctx context.Context, domain string, port int) error {
	// Validate inputs
	if err := l.validator.ValidateDomain(domain); err != nil {
		return fmt.Errorf("invalid domain: %w", err)
	}
	if err := l.validator.ValidatePort(port); err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}

	// Ensure domain ends with .local
	if !strings.HasSuffix(domain, ".local") {
		domain += ".local"
	}

	// Check if already registered
	l.domainsmu.RLock()
	if _, exists := l.domains[domain]; exists {
		l.domainsmu.RUnlock()
		return fmt.Errorf("domain %s is already registered", domain)
	}
	l.domainsmu.RUnlock()

	// Register with Caddy
	if err := l.caddyClient.AddServerBlock(ctx, []string{domain}, port); err != nil {
		return fmt.Errorf("failed to register with Caddy: %w", err)
	}

	// Register mDNS
	if err := l.registerMDNS(ctx, domain, port); err != nil {
		// Rollback Caddy registration
		_ = l.caddyClient.RemoveServerBlock(ctx, []string{domain})
		return fmt.Errorf("failed to register mDNS: %w", err)
	}

	// Store domain entry
	l.domainsmu.Lock()
	l.domains[domain] = &domainEntry{port: port}
	l.domainsmu.Unlock()

	l.logger.Info("domain registered", Field{"domain", domain}, Field{"port", port})
	return nil
}

// Remove unregisters a domain
func (l *LocalBase) Remove(ctx context.Context, domain string) error {
	// Ensure domain ends with .local
	if !strings.HasSuffix(domain, ".local") {
		domain += ".local"
	}

	// Check if registered
	l.domainsmu.RLock()
	entry, exists := l.domains[domain]
	if !exists {
		l.domainsmu.RUnlock()
		return fmt.Errorf("domain %s is not registered", domain)
	}
	l.domainsmu.RUnlock()

	// Unregister from Caddy
	if err := l.caddyClient.RemoveServerBlock(ctx, []string{domain}); err != nil {
		l.logger.Error("failed to remove from Caddy", Field{"domain", domain}, Field{"error", err})
		// Continue with cleanup
	}

	// Unregister mDNS
	l.unregisterMDNS(domain)

	// Remove domain entry
	l.domainsmu.Lock()
	delete(l.domains, domain)
	l.domainsmu.Unlock()

	l.logger.Info("domain unregistered", Field{"domain", domain}, Field{"port", entry.port})
	return nil
}

// List returns all registered domains with their ports
func (l *LocalBase) List(ctx context.Context) ([]DomainInfo, error) {
	l.domainsmu.RLock()
	defer l.domainsmu.RUnlock()

	domains := make([]DomainInfo, 0, len(l.domains))
	for domain, entry := range l.domains {
		domains = append(domains, DomainInfo{
			Domain: domain,
			Port:   entry.port,
		})
	}

	return domains, nil
}

// Shutdown gracefully shuts down the LocalBase service
func (l *LocalBase) Shutdown(ctx context.Context) error {
	l.logger.Info("shutting down LocalBase")

	var errors []string

	// Unregister all mDNS services
	l.mdnsMu.Lock()
	for domain, server := range l.mdnsServers {
		server.Shutdown()
		l.logger.Info("mDNS server shutdown", Field{"domain", domain})
	}
	l.mdnsServers = make(map[string]*bonjour.Server)
	l.mdnsMu.Unlock()

	// Clear all Caddy server blocks
	if err := l.caddyClient.ClearAllServerBlocks(ctx); err != nil {
		errors = append(errors, fmt.Sprintf("failed to clear Caddy server blocks: %v", err))
	}

	// Clear domains
	l.domainsmu.Lock()
	l.domains = make(map[string]*domainEntry)
	l.domainsmu.Unlock()

	if len(errors) > 0 {
		return fmt.Errorf("shutdown errors: %v", errors)
	}

	return nil
}

// registerMDNS registers the domain with mDNS using Bonjour
func (l *LocalBase) registerMDNS(_ context.Context, domain string, port int) error {
	// Get current IP address
	l.ipMu.RLock()
	ip := l.localIP
	l.ipMu.RUnlock()

	// Remove .local suffix for mDNS
	hostname := strings.TrimSuffix(domain, ".local")
	fullHost := fmt.Sprintf("%s.local.", hostname)
	service := fmt.Sprintf("_%s._tcp", hostname)

	server, err := bonjour.RegisterProxy(
		"localbase",
		service,
		"",
		80,
		fullHost,
		ip.String(),
		[]string{fmt.Sprintf("LocalBase managed domain port=%d", port)},
		nil)
	if err != nil {
		return fmt.Errorf("failed to register mDNS service: %w", err)
	}

	// Store server reference
	l.mdnsMu.Lock()
	l.mdnsServers[domain] = server
	l.mdnsMu.Unlock()

	l.logger.Info("mDNS service registered", Field{"domain", domain}, Field{"ip", ip.String()})
	return nil
}

// unregisterMDNS unregisters the domain from mDNS
func (l *LocalBase) unregisterMDNS(domain string) {
	l.mdnsMu.Lock()
	defer l.mdnsMu.Unlock()

	if server, exists := l.mdnsServers[domain]; exists {
		server.Shutdown()
		delete(l.mdnsServers, domain)
		l.logger.Info("mDNS service unregistered", Field{"domain", domain})
	}
}

// startBroadcast periodically refreshes mDNS services to keep them alive
func (l *LocalBase) startBroadcast(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.refreshAllMDNS(ctx)
			newIP, err := getLocalIP()
			if err != nil {
				l.logger.Error("failed to get local IP", Field{"error", err})
				continue
			}

			l.ipMu.Lock()
			oldIP := l.localIP
			if !newIP.Equal(oldIP) {
				l.localIP = newIP
				l.ipMu.Unlock()
				l.logger.Info("IP address changed", Field{"old", oldIP.String()}, Field{"new", newIP.String()})
			} else {
				l.ipMu.Unlock()
			}
		}
	}
}

// refreshAllMDNS refreshes all mDNS registrations with the new IP
func (l *LocalBase) refreshAllMDNS(ctx context.Context) {
	l.domainsmu.RLock()
	domains := make(map[string]int)
	for domain, entry := range l.domains {
		domains[domain] = entry.port
	}
	l.domainsmu.RUnlock()

	for domain, port := range domains {
		l.unregisterMDNS(domain)
		if err := l.registerMDNS(ctx, domain, port); err != nil {
			l.logger.Error("failed to refresh mDNS", Field{"domain", domain}, Field{"error", err})
		}
	}
}

// getLocalIP returns the local network IP address, preferring main network interfaces
func getLocalIP() (net.IP, error) {
	// First, try to get IP from network interfaces
	if ip, err := getIPFromInterfaces(); err == nil {
		return ip, nil
	}

	// Fallback: Try to connect to a public DNS server to determine local IP
	return getIPFromConnection()
}

// getIPFromInterfaces tries to get IP from network interfaces
func getIPFromInterfaces() (net.IP, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range interfaces {
		if ip, err := getIPFromInterface(iface); err == nil {
			return ip, nil
		}
	}

	return nil, fmt.Errorf("no suitable interface found")
}

// getIPFromInterface extracts IP from a single interface
func getIPFromInterface(iface net.Interface) (net.IP, error) {
	// Skip loopback and down interfaces
	if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
		return nil, fmt.Errorf("interface %s not suitable", iface.Name)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}

	for _, addr := range addrs {
		if ip := extractPrivateIPv4(addr); ip != nil {
			return ip, nil
		}
	}

	return nil, fmt.Errorf("no suitable IP found on interface %s", iface.Name)
}

// extractPrivateIPv4 extracts private IPv4 addresses from an address
func extractPrivateIPv4(addr net.Addr) net.IP {
	var ip net.IP
	switch v := addr.(type) {
	case *net.IPNet:
		ip = v.IP
	case *net.IPAddr:
		ip = v.IP
	}

	if ip != nil && ip.To4() != nil && !ip.IsLoopback() && ip.IsPrivate() {
		ipv4 := ip.To4()
		if ipv4[0] == 192 || ipv4[0] == 10 {
			return ip
		}
	}

	return nil
}

// getIPFromConnection uses UDP connection to determine local IP
func getIPFromConnection() (net.IP, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return nil, fmt.Errorf("failed to determine local IP: %w", err)
	}
	defer func() { _ = conn.Close() }()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP, nil
}

// DomainValidator validates domain names
type DomainValidator struct {
	domainRegex *regexp.Regexp
}

// NewValidator creates a new validator instance
func NewValidator() *DomainValidator {
	domainRegex := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`)
	return &DomainValidator{
		domainRegex: domainRegex,
	}
}

// ValidateDomain validates a domain name
func (v *DomainValidator) ValidateDomain(domain string) error {
	// Remove .local suffix if present for validation
	domain = strings.TrimSuffix(domain, ".local")

	if domain == "" {
		return fmt.Errorf("domain cannot be empty")
	}

	if len(domain) > 253 {
		return fmt.Errorf("domain name too long (max 253 characters)")
	}

	// Check for reserved domains
	if domain == "localhost" {
		return fmt.Errorf("localhost is a reserved domain")
	}

	// Split domain into labels and validate each
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("domain contains empty label")
		}
		if len(label) > 63 {
			return fmt.Errorf("domain label too long (max 63 characters): %s", label)
		}
		// Check if label matches the pattern
		if !v.domainRegex.MatchString(label) {
			return fmt.Errorf("invalid domain label: %s", label)
		}
	}

	return nil
}

// ValidatePort validates a port number
func (v *DomainValidator) ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

// CommandValidator validates and secures command execution
type CommandValidator struct {
	logger Logger
}

// NewCommandValidator creates a new command validator
func NewCommandValidator(logger Logger) *CommandValidator {
	return &CommandValidator{logger: logger}
}

// ValidateCaddyCommand finds and validates the Caddy executable
func (cv *CommandValidator) ValidateCaddyCommand() (string, error) {
	// Common Caddy installation paths
	commonPaths := []string{
		"/usr/local/bin/caddy",
		"/usr/bin/caddy",
		"/opt/homebrew/bin/caddy",
		"/home/linuxbrew/.linuxbrew/bin/caddy",
		"C:\\Program Files\\Caddy\\caddy.exe",
		"C:\\caddy\\caddy.exe",
	}

	// Also check PATH
	if pathCmd, err := exec.LookPath("caddy"); err == nil {
		commonPaths = append([]string{pathCmd}, commonPaths...)
	}

	for _, path := range commonPaths {
		if cv.isValidExecutable(path) {
			cv.logger.Info("found secure caddy executable", Field{"path", path})
			return path, nil
		}
	}

	return "", fmt.Errorf("caddy executable not found in common locations or PATH")
}

// isValidExecutable checks if a path points to a valid executable
func (cv *CommandValidator) isValidExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	// Check if it's a regular file
	if !info.Mode().IsRegular() {
		return false
	}

	// On Unix-like systems, check if executable
	if runtime.GOOS != "windows" {
		return info.Mode()&0o111 != 0
	}

	// On Windows, check for .exe extension
	return strings.HasSuffix(strings.ToLower(path), ".exe")
}

// ValidateDomain validates a domain name for local use
func (cv *CommandValidator) ValidateDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("domain cannot be empty")
	}

	// Basic domain validation for .local domains
	if len(domain) > 253 {
		return fmt.Errorf("domain too long")
	}

	// Check for dangerous characters
	if strings.ContainsAny(domain, " \t\n\r;|&$`\\\"'<>") {
		return fmt.Errorf("domain contains invalid characters")
	}

	return nil
}

// ValidatePort validates a port number
func (cv *CommandValidator) ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}

	// Reserved ports check (optional for local dev)
	if port < 1024 {
		cv.logger.Debug("using privileged port", Field{"port", port})
	}

	return nil
}
