package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Client sends commands to the daemon
type Client struct {
	config      *Config
	logger      Logger
	tlsManager  *TLSManager
	authManager *AuthManager
}

// NewClient creates a new client
func NewClient(logger Logger) (*Client, error) {
	configManager := NewConfigManager(logger)
	config, err := configManager.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	// Get config path for TLS certificates and auth tokens
	configPath, err := configManager.GetConfigPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get config path: %w", err)
	}
	tlsManager := NewTLSManager(configPath, logger)

	// Create authentication manager
	authManager, err := NewAuthManager(configPath, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create auth manager: %w", err)
	}

	return &Client{
		config:      config,
		logger:      logger,
		tlsManager:  tlsManager,
		authManager: authManager,
	}, nil
}

// SendCommand sends a command to the daemon
func (c *Client) SendCommand(method string, params map[string]any) error {
	// Build command string
	cmdLine := method
	if params != nil {
		// Order matters for some commands
		if domain, ok := params["domain"]; ok {
			cmdLine += fmt.Sprintf(" %v", domain)
		}
		if port, ok := params["port"]; ok {
			cmdLine += fmt.Sprintf(" %v", port)
		}
	}

	// Get TLS configuration
	tlsConfig := c.tlsManager.GetClientTLSConfig()

	// Connect with TLS
	conn, err := tls.Dial("tcp", c.config.AdminAddress, tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// Set timeout
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	// Send command
	if _, err := fmt.Fprintf(conn, "%s\n", cmdLine); err != nil {
		return fmt.Errorf("failed to send command: %w", err)
	}

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	response = strings.TrimSpace(response)

	// Handle response
	if strings.HasPrefix(response, "ERROR:") {
		return fmt.Errorf("%s", strings.TrimPrefix(response, "ERROR: "))
	}

	if strings.HasPrefix(response, "OK:") {
		result := strings.TrimPrefix(response, "OK: ")
		if result != "" && result != " " {
			fmt.Println(result)
		}
		return nil
	}

	// Unexpected response
	return fmt.Errorf("unexpected response: %s", response)
}

// CaddyClientImpl implements the CaddyClient interface
type CaddyClientImpl struct {
	adminURL         string
	httpClient       *http.Client
	logger           Logger
	commandValidator *CommandValidator
	caddyPath        string // Cached secure path to Caddy executable
}

// NewCaddyClient creates a new Caddy client
func NewCaddyClient(adminURL string, logger Logger) *CaddyClientImpl {
	client := &CaddyClientImpl{
		adminURL: adminURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger:           logger,
		commandValidator: NewCommandValidator(logger),
	}

	// Find and validate Caddy executable on initialization
	if path, err := client.commandValidator.ValidateCaddyCommand(); err != nil {
		logger.Error("failed to find secure caddy executable", Field{"error", err})
		// Continue without caching the path - will retry on each use
	} else {
		client.caddyPath = path
		logger.Info("caddy executable validated and cached", Field{"path", path})
	}

	return client
}

// GetConfig retrieves the current Caddy configuration
func (c *CaddyClientImpl) GetConfig(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/config/", c.adminURL), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get Caddy config: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var config map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return config, nil
}

// UpdateConfig updates the Caddy configuration
func (c *CaddyClientImpl) UpdateConfig(ctx context.Context, config map[string]any) error {
	body, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, fmt.Sprintf("%s/config/", c.adminURL), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to update Caddy config: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// IsRunning checks if Caddy is running
func (c *CaddyClientImpl) IsRunning(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/config/", c.adminURL), http.NoBody)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Connection error likely means Caddy is not running
		return false, nil
	}
	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode == http.StatusOK, nil
}

// AddServerBlock adds a new server block for the given domains
func (c *CaddyClientImpl) AddServerBlock(ctx context.Context, domains []string, port int) error {
	// Prepare the server block
	serverBlock := createServerBlock(domains, port)

	// Get current config
	config, err := c.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current config: %w", err)
	}

	// Navigate to or create the necessary structure
	apps, ok := config["apps"].(map[string]any)
	if !ok {
		apps = make(map[string]any)
		config["apps"] = apps
	}

	httpApp, ok := apps["http"].(map[string]any)
	if !ok {
		httpApp = make(map[string]any)
		apps["http"] = httpApp
	}

	servers, ok := httpApp["servers"].(map[string]any)
	if !ok {
		servers = make(map[string]any)
		httpApp["servers"] = servers
	}

	// Add the new server block
	serverID := fmt.Sprintf("srv_%s", domains[0])
	servers[serverID] = serverBlock

	// Update the config
	return c.UpdateConfig(ctx, config)
}

// RemoveServerBlock removes server blocks for the given domains
func (c *CaddyClientImpl) RemoveServerBlock(ctx context.Context, domains []string) error {
	config, err := c.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current config: %w", err)
	}

	servers := c.getServers(config)
	if servers == nil {
		return nil // No servers to remove
	}

	// Create a set of domains for fast lookup
	domainSet := make(map[string]bool)
	for _, d := range domains {
		domainSet[d] = true
	}

	// Find and remove matching server blocks
	for serverID, server := range servers {
		if c.serverContainsDomain(server, domainSet) {
			delete(servers, serverID)
		}
	}

	return c.UpdateConfig(ctx, config)
}

// getServers extracts servers from config
func (c *CaddyClientImpl) getServers(config map[string]any) map[string]any {
	apps, ok := config["apps"].(map[string]any)
	if !ok {
		return nil
	}

	httpApp, ok := apps["http"].(map[string]any)
	if !ok {
		return nil
	}

	servers, ok := httpApp["servers"].(map[string]any)
	if !ok {
		return nil
	}

	return servers
}

// serverContainsDomain checks if server contains any of the domains
func (c *CaddyClientImpl) serverContainsDomain(server any, domainSet map[string]bool) bool {
	serverConfig, ok := server.(map[string]any)
	if !ok {
		return false
	}

	routes, ok := serverConfig["routes"].([]any)
	if !ok || len(routes) == 0 {
		return false
	}

	for _, route := range routes {
		if c.routeContainsDomain(route, domainSet) {
			return true
		}
	}

	return false
}

// routeContainsDomain checks if route contains any of the domains
func (c *CaddyClientImpl) routeContainsDomain(route any, domainSet map[string]bool) bool {
	routeMap, ok := route.(map[string]any)
	if !ok {
		return false
	}

	matchList, ok := routeMap["match"].([]any)
	if !ok || len(matchList) == 0 {
		return false
	}

	for _, match := range matchList {
		matchMap, ok := match.(map[string]any)
		if !ok {
			continue
		}

		hosts, ok := matchMap["host"].([]any)
		if !ok {
			continue
		}

		for _, host := range hosts {
			if hostStr, ok := host.(string); ok && domainSet[hostStr] {
				return true
			}
		}
	}

	return false
}

// ClearAllServerBlocks removes all server blocks
func (c *CaddyClientImpl) ClearAllServerBlocks(ctx context.Context) error {
	config, err := c.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current config: %w", err)
	}

	// Check if there are any apps configured
	apps, ok := config["apps"].(map[string]any)
	if !ok {
		return fmt.Errorf("invalid config structure: apps not found")
	}

	// Clear the http app servers
	if httpApp, ok := apps["http"].(map[string]any); ok {
		httpApp["servers"] = make(map[string]any)
	}

	return c.UpdateConfig(ctx, config)
}

// StartCaddy starts the Caddy server
func (c *CaddyClientImpl) StartCaddy(ctx context.Context) error {
	// Check if already running
	if running, _ := c.IsRunning(ctx); running {
		c.logger.Info("Caddy is already running")
		return nil
	}

	// Use cached path or find Caddy
	caddyPath := c.caddyPath
	if caddyPath == "" {
		var err error
		caddyPath, err = c.commandValidator.ValidateCaddyCommand()
		if err != nil {
			return fmt.Errorf("failed to find Caddy executable: %w", err)
		}
		c.caddyPath = caddyPath
	}

	// Prepare the command with security in mind
	cmd := exec.CommandContext(ctx, caddyPath, "run", "--config", "/dev/null", "--adapter", "json", "--watch") // #nosec G204
	cmd.Env = append(cmd.Env, "HOME="+getHomeDir())

	// Start Caddy in background
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Caddy: %w", err)
	}

	// Don't wait for the process - let it run in background
	go func() {
		_ = cmd.Wait()
	}()

	// Give Caddy time to start with a nice spinner
	return c.waitForCaddyWithSpinner(ctx)
}

// waitForCaddyWithSpinner waits for Caddy to start with a visual spinner
func (c *CaddyClientImpl) waitForCaddyWithSpinner(ctx context.Context) error {
	// Channel to signal when Caddy is ready or timeout/error occurs
	done := make(chan error, 1)

	// Start checking Caddy status in background
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		timeout := time.After(10 * time.Second)

		for {
			select {
			case <-ctx.Done():
				done <- ctx.Err()
				return
			case <-timeout:
				done <- fmt.Errorf("timeout waiting for Caddy to start")
				return
			case <-ticker.C:
				if running, _ := c.IsRunning(ctx); running {
					done <- nil
					return
				}
			}
		}
	}()

	// Try to run with spinner, fallback to text output if no TTY
	model := newSpinnerModel()
	model.done = done
	program := tea.NewProgram(model)

	if _, err := program.Run(); err != nil {
		// Fallback: text output without spinner
		c.logger.Info("Starting Caddy server...")
		select {
		case err := <-done:
			if err != nil {
				return fmt.Errorf("failed to start Caddy: %w", err)
			}
			c.logger.Info("Caddy started successfully")
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// If we get here, the spinner ran successfully
	// Check if there was an error
	select {
	case err := <-done:
		return err
	default:
		// This shouldn't happen, but handle it gracefully
		return fmt.Errorf("Caddy did not start within expected time")
	}
}

// EnsureRunning ensures Caddy is running
func (c *CaddyClientImpl) EnsureRunning(ctx context.Context) error {
	running, err := c.IsRunning(ctx)
	if err != nil {
		return fmt.Errorf("failed to check Caddy status: %w", err)
	}

	if !running {
		c.logger.Info("Caddy is not running, starting it...")
		if err := c.StartCaddy(ctx); err != nil {
			return fmt.Errorf("failed to start Caddy: %w", err)
		}
	}

	return nil
}

// createServerBlock creates a server block configuration for Caddy
func createServerBlock(domains []string, port int) map[string]any {
	// Convert domains to interface slice
	hostList := make([]any, len(domains))
	for i, domain := range domains {
		hostList[i] = domain
	}

	return map[string]any{
		"listen": []any{":443"},
		"routes": []any{
			map[string]any{
				"match": []any{
					map[string]any{
						"host": hostList,
					},
				},
				"handle": []any{
					map[string]any{
						"handler": "reverse_proxy",
						"upstreams": []any{
							map[string]any{
								"dial": fmt.Sprintf("localhost:%d", port),
							},
						},
					},
				},
			},
		},
		"tls_connection_policies": []any{
			map[string]any{
				"match": map[string]any{
					"sni": hostList,
				},
			},
		},
		"automatic_https": map[string]any{
			"disable_redirects": false,
		},
	}
}

// Spinner model for Caddy startup
type spinnerModel struct {
	spinner int
	frames  []string
	colors  []lipgloss.Color
	done    <-chan error
	err     error
}

func newSpinnerModel() *spinnerModel {
	return &spinnerModel{
		frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		colors: []lipgloss.Color{
			lipgloss.Color("#F8B195"),
			lipgloss.Color("#F67280"),
			lipgloss.Color("#C06C84"),
			lipgloss.Color("#6C5B7B"),
			lipgloss.Color("#355C7D"),
		},
	}
}

func (m *spinnerModel) Init() tea.Cmd {
	return tea.Batch(
		m.tick(),
		m.waitForDone(),
	)
}

func (m *spinnerModel) tick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m *spinnerModel) waitForDone() tea.Cmd {
	return func() tea.Msg {
		err := <-m.done
		return doneMsg{err: err}
	}
}

func (m *spinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.spinner++
		cmd := m.tick()
		return m, cmd
	case doneMsg:
		m.err = msg.err
		return m, tea.Quit
	}
	return m, nil
}

func (m *spinnerModel) View() string {
	if m.err != nil {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Render("✗ Failed to start Caddy: " + m.err.Error() + "\n")
	}

	// Check if we're done
	select {
	case err := <-m.done:
		m.err = err
		if m.err != nil {
			return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Render("✗ Failed to start Caddy: " + m.err.Error() + "\n")
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#96CEB4")).Render("✓ Caddy started successfully!\n")
	default:
		// Still waiting
	}

	frame := m.frames[m.spinner%len(m.frames)]
	color := m.colors[m.spinner%len(m.colors)]

	spinnerStyle := lipgloss.NewStyle().Foreground(color)
	return spinnerStyle.Render(frame) + " Starting Caddy server..."
}

type (
	tickMsg struct{}
	doneMsg struct{ err error }
)
