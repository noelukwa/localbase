package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// Server represents the localbase daemon server
type Server struct {
	config          *Config
	logger          Logger
	localbase       DomainService
	pool            *ConnectionPoolImpl
	protocolHandler *ProtocolHandler
	listener        net.Listener
	shutdownChan    chan struct{}
	mu              sync.RWMutex
}

// NewServer creates a new server instance
func NewServer(config *Config, logger Logger) (*Server, error) {
	// Create dependencies
	configManager := NewConfigManager(logger)
	caddyClient := NewCaddyClient(config.CaddyAdmin, logger)
	validator := NewValidator()
	
	// Ensure Caddy is running
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := caddyClient.EnsureRunning(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure Caddy is running: %w", err)
	}
	
	// Create localbase service
	lb, err := NewLocalBase(logger, configManager, caddyClient, validator)
	if err != nil {
		return nil, fmt.Errorf("failed to create localbase: %w", err)
	}
	
	server := &Server{
		config:       config,
		logger:       logger,
		localbase:    lb,
		shutdownChan: make(chan struct{}),
	}
	
	// Create protocol handler with server reference for shutdown
	server.protocolHandler = NewProtocolHandlerWithShutdown(lb, validator, logger, server.triggerShutdown)
	
	return server, nil
}

// GetListenerAddr safely returns the listener address
func (s *Server) GetListenerAddr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return ""
}

// triggerShutdown is called when a shutdown request is received
func (s *Server) triggerShutdown() {
	select {
	case s.shutdownChan <- struct{}{}:
		s.logger.Info("shutdown signal sent")
	default:
		s.logger.Debug("shutdown already in progress")
	}
}

// Start starts the server
func (s *Server) Start(ctx context.Context) error {
	// Start listening
	listener, err := net.Listen("tcp", s.config.AdminAddress)
	if err != nil {
		return fmt.Errorf("failed to start localbase server: %w", err)
	}
	
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()
	
	s.logger.Info("localbase server started", Field{"address", s.config.AdminAddress})
	
	// Create connection pool
	s.pool = NewConnectionPool(ctx, 100, s.protocolHandler.HandleConnection, s.logger)
	
	// Start broadcast
	if lb, ok := s.localbase.(*LocalBase); ok {
		go lb.startBroadcast(ctx)
	}
	
	// Accept connections
	go s.acceptConnections(ctx)
	
	// Wait for shutdown signal from either context or shutdown command
	select {
	case <-ctx.Done():
		s.logger.Info("context cancelled, shutting down")
	case <-s.shutdownChan:
		s.logger.Info("shutdown command received, shutting down")
	}
	
	// Graceful shutdown
	return s.shutdown()
}

func (s *Server) acceptConnections(ctx context.Context) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				s.logger.Error("error accepting connection", Field{"error", err})
				continue
			}
		}
		
		if err := s.pool.Accept(conn); err != nil {
			s.logger.Error("failed to handle connection", Field{"error", err})
		}
	}
}

func (s *Server) shutdown() error {
	s.logger.Info("shutting down localbase server")
	
	// Stop accepting new connections
	s.mu.Lock()
	if s.listener != nil {
		s.listener.Close()
	}
	s.mu.Unlock()
	
	// Close connection pool
	if s.pool != nil {
		if err := s.pool.Close(); err != nil {
			s.logger.Error("error closing connection pool", Field{"error", err})
		}
	}
	
	// Shutdown localbase
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	if err := s.localbase.Shutdown(ctx); err != nil {
		s.logger.Error("error shutting down localbase", Field{"error", err})
		return err
	}
	
	return nil
}

// Client sends commands to the daemon
type Client struct {
	config *Config
	logger Logger
}

// NewClient creates a new client
func NewClient(logger Logger) (*Client, error) {
	configManager := NewConfigManager(logger)
	config, err := configManager.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	
	return &Client{
		config: config,
		logger: logger,
	}, nil
}

// SendCommand sends a command to the daemon
func (c *Client) SendCommand(method string, params map[string]interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	// Connect to daemon
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
	}
	
	conn, err := dialer.DialContext(ctx, "tcp", c.config.AdminAddress)
	if err != nil {
		return fmt.Errorf("failed to connect to daemon at %s: %w", c.config.AdminAddress, err)
	}
	defer conn.Close()
	
	// Set deadline
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	
	// Create request
	req := Request{
		Version: ProtocolVersion,
		Method:  method,
		Params:  params,
		ID:      fmt.Sprintf("%d", time.Now().UnixNano()),
	}
	
	// Send request
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(&req); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	
	// Read response
	var resp Response
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&resp); err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	
	// Check for error
	if resp.Error != nil {
		return fmt.Errorf("%s", resp.Error.Error())
	}
	
	// Print result
	if resp.Result != nil {
		output, err := json.MarshalIndent(resp.Result, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to format response: %w", err)
		}
		fmt.Println(string(output))
	}
	
	return nil
}

// CLI Commands
var rootCmd = &cobra.Command{
	Use:   "localbase",
	Short: "localbase is a local domain management tool",
	Long: `localbase allows you to manage local domains and their corresponding ports.
It integrates with Caddy server to provide local domain resolution and routing.`,
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the localbase daemon",
	Long:  `Start the localbase daemon, either in the foreground or as a detached process.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		caddyAdmin, _ := cmd.Flags().GetString("caddy")
		adminAddr, _ := cmd.Flags().GetString("addr")
		detached, _ := cmd.Flags().GetBool("detached")
		logLevel, _ := cmd.Flags().GetString("log-level")
		
		// Create logger
		logger := NewLogger(ParseLogLevel(logLevel))
		
		// Create config
		cfg := &Config{
			AdminAddress: adminAddr,
			CaddyAdmin:   caddyAdmin,
		}
		
		// Save config
		configManager := NewConfigManager(logger)
		if err := configManager.Write(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		
		if detached {
			// Start in detached mode
			cmd := exec.Command(os.Args[0], "start", "--caddy", caddyAdmin, "--addr", adminAddr, "--log-level", logLevel)
			cmd.Stdout = nil
			cmd.Stderr = nil
			cmd.Stdin = nil
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("failed to start in detached mode: %w", err)
			}
			fmt.Printf("Started localbase daemon in background (PID: %d)\n", cmd.Process.Pid)
			return nil
		}
		
		// Create server
		server, err := NewServer(cfg, logger)
		if err != nil {
			return err
		}
		
		// Setup signal handling
		ctx, cancel := context.WithCancel(context.Background())
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		
		go func() {
			<-sigChan
			logger.Info("received shutdown signal")
			cancel()
		}()
		
		// Start server
		return server.Start(ctx)
	},
}

var addCmd = &cobra.Command{
	Use:   "add <domain> --port <port>",
	Short: "Add a new domain",
	Long:  `Add a new domain to localbase with the specified port.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		if port == 0 {
			return fmt.Errorf("port is required")
		}
		
		logger := NewLogger(InfoLevel)
		client, err := NewClient(logger)
		if err != nil {
			return err
		}
		
		return client.SendCommand("add", map[string]interface{}{
			"domain": args[0],
			"port":   port,
		})
	},
}

var removeCmd = &cobra.Command{
	Use:   "remove <domain>",
	Short: "Remove a domain",
	Long:  `Remove a domain from localbase.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := NewLogger(InfoLevel)
		client, err := NewClient(logger)
		if err != nil {
			return err
		}
		
		return client.SendCommand("remove", map[string]interface{}{
			"domain": args[0],
		})
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all domains",
	Long:  `List all domains registered in localbase.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := NewLogger(InfoLevel)
		client, err := NewClient(logger)
		if err != nil {
			return err
		}
		
		return client.SendCommand("list", nil)
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop localbase daemon",
	Long:  `Stop the running localbase daemon.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := NewLogger(InfoLevel)
		client, err := NewClient(logger)
		if err != nil {
			return fmt.Errorf("failed to connect to daemon: %w", err)
		}
		
		return client.SendCommand("shutdown", nil)
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
	startCmd.Flags().StringP("addr", "a", "localhost:2025", "localbase daemon address")
	startCmd.Flags().StringP("caddy", "c", "http://localhost:2019", "Caddy admin API address")
	startCmd.Flags().BoolP("detached", "d", false, "Run localbase in background")
	startCmd.Flags().String("log-level", "info", "Log level (debug, info, error)")
	
	rootCmd.AddCommand(addCmd)
	addCmd.Flags().IntP("port", "p", 0, "Port for the local domain")
	addCmd.MarkFlagRequired("port")
	
	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(stopCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "[localbase]: %v\n", err)
		os.Exit(1)
	}
}