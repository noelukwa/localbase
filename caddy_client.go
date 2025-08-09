package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// CaddyClientImpl implements the CaddyClient interface
type CaddyClientImpl struct {
	adminURL   string
	httpClient *http.Client
	logger     Logger
}

// NewCaddyClient creates a new Caddy client
func NewCaddyClient(adminURL string, logger Logger) *CaddyClientImpl {
	return &CaddyClientImpl{
		adminURL: adminURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// GetConfig retrieves the current Caddy configuration
func (c *CaddyClientImpl) GetConfig(ctx context.Context) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/config/", c.adminURL), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get Caddy config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to get Caddy config (status %d)", resp.StatusCode)
		}
		return nil, fmt.Errorf("failed to get Caddy config (status %d): %s", resp.StatusCode, body)
	}

	var config map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to decode Caddy config: %w", err)
	}

	return config, nil
}

// UpdateConfig updates the Caddy configuration
func (c *CaddyClientImpl) UpdateConfig(ctx context.Context, config map[string]interface{}) error {
	jsonData, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, fmt.Sprintf("%s/config/", c.adminURL), bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to update Caddy config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to update Caddy config (status %d)", resp.StatusCode)
		}
		return fmt.Errorf("failed to update Caddy config (status %d): %s", resp.StatusCode, body)
	}

	return nil
}

// AddServerBlock adds a new server block to Caddy configuration
func (c *CaddyClientImpl) AddServerBlock(ctx context.Context, domains []string, port int) error {
	config, err := c.GetConfig(ctx)
	if err != nil {
		return err
	}

	// Ensure the config structure is initialized
	if config == nil {
		config = make(map[string]interface{})
	}

	if _, ok := config["apps"]; !ok {
		config["apps"] = make(map[string]interface{})
	}

	apps := config["apps"].(map[string]interface{})
	if _, ok := apps["http"]; !ok {
		apps["http"] = make(map[string]interface{})
	}

	httpApp := apps["http"].(map[string]interface{})
	if _, ok := httpApp["servers"]; !ok {
		httpApp["servers"] = make(map[string]interface{})
	}

	servers := httpApp["servers"].(map[string]interface{})
	serverName := "default"
	
	// Build new routes
	newRoutes := []interface{}{}
	for _, domain := range domains {
		newRoutes = append(newRoutes, map[string]interface{}{
			"match": []map[string]interface{}{
				{"host": []string{domain}},
			},
			"handle": []map[string]interface{}{
				{
					"handler": "reverse_proxy",
					"upstreams": []map[string]interface{}{
						{"dial": fmt.Sprintf("localhost:%d", port)},
					},
				},
			},
		})
	}

	if existingServer, ok := servers[serverName]; ok {
		server := existingServer.(map[string]interface{})
		if existingRoutes, ok := server["routes"].([]interface{}); ok {
			server["routes"] = append(existingRoutes, newRoutes...)
		} else {
			server["routes"] = newRoutes
		}
		servers[serverName] = server
	} else {
		servers[serverName] = map[string]interface{}{
			"listen": []string{":80", ":443"},
			"routes": newRoutes,
		}
	}

	return c.UpdateConfig(ctx, config)
}

// IsRunning checks if Caddy is running
func (c *CaddyClientImpl) IsRunning(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/config/", c.adminURL), nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Connection error means Caddy is not running
		return false, nil
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}

// EnsureRunning checks if Caddy is running and starts it if not
func (c *CaddyClientImpl) EnsureRunning(ctx context.Context) error {
	running, err := c.IsRunning(ctx)
	if err != nil {
		return fmt.Errorf("failed to check Caddy status: %w", err)
	}
	if !running {
		c.logger.Info("Caddy is not running, starting it now...")
		if err := c.StartCaddy(ctx); err != nil {
			return fmt.Errorf("failed to start Caddy: %w", err)
		}
	}
	return nil
}

// spinnerModel is a bubbletea model for the Caddy startup spinner
type spinnerModel struct {
	spinner   int
	frames    []string
	colors    []lipgloss.Color
	done      chan error
	finished  bool
	err       error
	quitting  bool
}

func newSpinnerModel() spinnerModel {
	return spinnerModel{
		frames: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
		colors: []lipgloss.Color{"#FF6B6B", "#4ECDC4", "#45B7D1", "#96CEB4", "#FFEAA7", "#DDA0DD", "#98D8C8", "#F7DC6F"},
	}
}

func (m spinnerModel) Init() tea.Cmd {
	return tea.Batch(
		tea.Tick(time.Millisecond*80, func(t time.Time) tea.Msg {
			return t
		}),
		func() tea.Msg {
			return <-m.done
		},
	)
}

func (m spinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case time.Time:
		if m.finished || m.quitting {
			return m, tea.Quit
		}
		m.spinner = (m.spinner + 1) % len(m.frames)
		return m, tea.Tick(time.Millisecond*80, func(t time.Time) tea.Msg {
			return t
		})
	case error:
		m.finished = true
		m.err = msg
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m spinnerModel) View() string {
	if m.quitting {
		return "Cancelled Caddy startup.\n"
	}
	if m.finished {
		if m.err != nil {
			return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Render("✗ Failed to start Caddy: " + m.err.Error() + "\n")
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#96CEB4")).Render("✓ Caddy started successfully!\n")
	}

	frame := m.frames[m.spinner]
	color := m.colors[m.spinner%len(m.colors)]
	
	spinnerStyle := lipgloss.NewStyle().Foreground(color)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	
	return spinnerStyle.Render(frame) + " " + textStyle.Render("Starting Caddy server...")
}

// StartCaddy starts Caddy in the background and shows a fancy spinner
func (c *CaddyClientImpl) StartCaddy(ctx context.Context) error {
	// Create channels for communication
	done := make(chan error, 1)
	
	// Start Caddy process
	go func() {
		cmd := exec.CommandContext(ctx, "caddy", "start")
		cmd.Stdout = nil
		cmd.Stderr = nil
		
		if err := cmd.Run(); err != nil {
			done <- fmt.Errorf("failed to start Caddy: %w", err)
			return
		}

		// Wait for Caddy to be ready
		maxRetries := 30
		for i := 0; i < maxRetries; i++ {
			select {
			case <-ctx.Done():
				done <- ctx.Err()
				return
			default:
			}

			if running, _ := c.IsRunning(ctx); running {
				done <- nil
				return
			}
			time.Sleep(100 * time.Millisecond)
		}

		done <- fmt.Errorf("Caddy did not start within expected time")
	}()

	// Try to run with spinner, fallback to simple wait if no TTY
	model := newSpinnerModel()
	model.done = done
	program := tea.NewProgram(model)
	
	if _, err := program.Run(); err != nil {
		// Fallback: simple waiting without spinner
		c.logger.Info("Starting Caddy server...")
		select {
		case err := <-done:
			if err != nil {
				c.logger.Error("Failed to start Caddy: " + err.Error())
				return fmt.Errorf("failed to start Caddy: %w", err)
			}
			c.logger.Info("✓ Caddy started successfully!")
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// If spinner ran successfully, return its error (if any)
	if model.err != nil {
		return fmt.Errorf("failed to start Caddy: %w", model.err)
	}
	return nil
}

// RemoveServerBlock removes server blocks for the specified domains from Caddy
func (c *CaddyClientImpl) RemoveServerBlock(ctx context.Context, domains []string) error {
	config, err := c.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current config: %w", err)
	}

	apps, ok := config["apps"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid config structure: apps not found")
	}

	http, ok := apps["http"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid config structure: http app not found")
	}

	servers, ok := http["servers"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid config structure: servers not found")
	}

	// Remove routes that match the specified domains from all servers
	for serverName, serverConfig := range servers {
		server, ok := serverConfig.(map[string]interface{})
		if !ok {
			continue
		}

		routes, ok := server["routes"].([]interface{})
		if !ok {
			continue
		}

		// Filter out routes that match the domains to remove
		var filteredRoutes []interface{}
		for _, route := range routes {
			routeMap, ok := route.(map[string]interface{})
			if !ok {
				filteredRoutes = append(filteredRoutes, route)
				continue
			}

			match, ok := routeMap["match"].([]interface{})
			if !ok {
				filteredRoutes = append(filteredRoutes, route)
				continue
			}

			shouldKeep := true
			for _, matchRule := range match {
				matchMap, ok := matchRule.(map[string]interface{})
				if !ok {
					continue
				}

				hosts, ok := matchMap["host"].([]interface{})
				if !ok {
					continue
				}

				// Check if any host in this route matches domains to remove
				for _, host := range hosts {
					hostStr, ok := host.(string)
					if !ok {
						continue
					}

					for _, domain := range domains {
						if hostStr == domain {
							shouldKeep = false
							c.logger.Info("removed Caddy route for domain", Field{"domain", domain})
							break
						}
					}
					if !shouldKeep {
						break
					}
				}
				if !shouldKeep {
					break
				}
			}

			if shouldKeep {
				filteredRoutes = append(filteredRoutes, route)
			}
		}

		// Update the server with filtered routes
		server["routes"] = filteredRoutes
		servers[serverName] = server
	}

	return c.UpdateConfig(ctx, config)
}

// ClearAllServerBlocks removes all server blocks from Caddy configuration
func (c *CaddyClientImpl) ClearAllServerBlocks(ctx context.Context) error {
	config, err := c.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current config: %w", err)
	}

	apps, ok := config["apps"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid config structure: apps not found")
	}

	http, ok := apps["http"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid config structure: http app not found")
	}

	servers, ok := http["servers"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid config structure: servers not found")
	}

	// Clear all server blocks
	serverCount := len(servers)
	for serverName := range servers {
		delete(servers, serverName)
	}

	if serverCount > 0 {
		c.logger.Info("cleared all Caddy server blocks", Field{"count", serverCount})
	}

	return c.UpdateConfig(ctx, config)
}