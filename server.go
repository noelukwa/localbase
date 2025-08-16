package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Server represents the localbase daemon server
type Server struct {
	config          *Config
	logger          Logger
	localbase       DomainService
	pool            *ConnectionHandler
	protocolHandler *ProtocolHandler
	tlsManager      *TLSManager
	authManager     *AuthManager
	listener        net.Listener
	shutdownChan    chan struct{}
	mu              sync.RWMutex
}

// NewServer creates a new server instance
func NewServer(config *Config, logger Logger) (*Server, error) {
	// Create dependencies
	configManager := NewConfigManager(logger)
	caddyClient := NewCaddyClient(config.CaddyAdmin, logger)
	validator := NewCommandValidator(logger)

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

	// Ensure Caddy is running
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := caddyClient.EnsureRunning(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure Caddy is running: %w", err)
	}

	// Create LocalBase service
	lb, err := NewLocalBase(logger, configManager, caddyClient, validator)
	if err != nil {
		return nil, fmt.Errorf("failed to create localbase: %w", err)
	}

	server := &Server{
		config:       config,
		logger:       logger,
		localbase:    lb,
		tlsManager:   tlsManager,
		authManager:  authManager,
		shutdownChan: make(chan struct{}),
	}

	// Create protocol handler with server reference for shutdown
	server.protocolHandler = NewProtocolHandler(lb, authManager, logger, server.triggerShutdown)

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

// Start starts the server
func (s *Server) Start(ctx context.Context) error {
	// Create PID file
	if err := s.authManager.CreatePIDFile(); err != nil {
		return fmt.Errorf("failed to create PID file: %w", err)
	}
	defer func() { _ = s.authManager.RemovePIDFile() }()

	// Get TLS configuration
	tlsConfig, err := s.tlsManager.GetTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to get TLS config: %w", err)
	}

	// Start listening with TLS
	listener, err := tls.Listen("tcp", s.config.AdminAddress, tlsConfig)
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
		s.logger.Info("context canceled, shutting down")
	case <-s.shutdownChan:
		s.logger.Info("shutdown command received")
	}

	return s.stop()
}

// acceptConnections accepts and handles incoming connections
func (s *Server) acceptConnections(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Check if listener is nil (server is shutting down)
			s.mu.RLock()
			listener := s.listener
			s.mu.RUnlock()

			if listener == nil {
				return
			}

			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					s.logger.Error("failed to accept connection", Field{"error", err})
					continue
				}
			}

			go func() {
				if err := s.pool.Accept(conn); err != nil {
					s.logger.Error("connection handling error", Field{"error", err})
				}
			}()
		}
	}
}

// triggerShutdown triggers a graceful shutdown
func (s *Server) triggerShutdown() {
	select {
	case s.shutdownChan <- struct{}{}:
	default:
	}
}

// stop gracefully stops the server
func (s *Server) stop() error {
	s.logger.Info("stopping localbase server")

	// Close the listener
	s.mu.Lock()
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}
	s.mu.Unlock()

	// Close connection pool
	if s.pool != nil {
		_ = s.pool.Close()
	}

	// Shutdown LocalBase
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.localbase.Shutdown(ctx); err != nil {
		s.logger.Error("error shutting down localbase", Field{"error", err})
		return err
	}

	return nil
}

// ProtocolHandler handles protocol communication
type ProtocolHandler struct {
	localbase DomainService
	auth      *AuthManager
	logger    Logger
	shutdown  func()
}

// NewProtocolHandler creates a protocol handler
func NewProtocolHandler(localbase DomainService, auth *AuthManager, logger Logger, shutdown func()) *ProtocolHandler {
	return &ProtocolHandler{
		localbase: localbase,
		auth:      auth,
		logger:    logger,
		shutdown:  shutdown,
	}
}

// HandleConnection handles text-based protocol communication
func (h *ProtocolHandler) HandleConnection(ctx context.Context, conn net.Conn) error {
	scanner := bufio.NewScanner(conn)
	writer := bufio.NewWriter(conn)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		response := h.processCommand(line)

		// Send response
		if _, err := writer.WriteString(response + "\n"); err != nil {
			return fmt.Errorf("failed to write response: %w", err)
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("failed to flush response: %w", err)
		}
	}

	return scanner.Err()
}

// processCommand processes a command
func (h *ProtocolHandler) processCommand(command string) string {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "ERROR: empty command"
	}

	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "add":
		if len(args) < 2 {
			return "ERROR: add requires domain and port"
		}
		domain := args[0]
		port := args[1]

		// Convert port to int
		var portInt int
		if _, err := fmt.Sscanf(port, "%d", &portInt); err != nil {
			return "ERROR: invalid port number"
		}

		ctx := context.Background()
		if err := h.localbase.Add(ctx, domain, portInt); err != nil {
			return fmt.Sprintf("ERROR: %v", err)
		}
		return fmt.Sprintf("OK: added %s:%s", domain, port)

	case "remove":
		if len(args) < 1 {
			return "ERROR: remove requires domain"
		}
		domain := args[0]

		ctx := context.Background()
		if err := h.localbase.Remove(ctx, domain); err != nil {
			return fmt.Sprintf("ERROR: %v", err)
		}
		return fmt.Sprintf("OK: removed %s", domain)

	case "list":
		ctx := context.Background()
		domains, err := h.localbase.List(ctx)
		if err != nil {
			return fmt.Sprintf("ERROR: %v", err)
		}

		if len(domains) == 0 {
			return "OK: no domains configured"
		}

		// Format domains with their actual ports
		var domainList []string
		for _, d := range domains {
			domainList = append(domainList, fmt.Sprintf("%s -> localhost:%d", d.Domain, d.Port))
		}
		return fmt.Sprintf("OK: %s", strings.Join(domainList, ", "))

	case "ping":
		return "OK: pong"

	case "shutdown":
		go h.shutdown() // Shutdown in goroutine to allow response
		return "OK: shutting down"

	default:
		return fmt.Sprintf("ERROR: unknown command %s", cmd)
	}
}

// ConnectionHandler handles connections directly without pooling
type ConnectionHandler struct {
	handler func(context.Context, net.Conn) error
	logger  Logger
	mu      sync.RWMutex
	active  map[net.Conn]struct{}
}

// NewConnectionPool creates a connection handler
func NewConnectionPool(_ context.Context, _ int, handler func(context.Context, net.Conn) error, logger Logger) *ConnectionHandler {
	return &ConnectionHandler{
		handler: handler,
		logger:  logger,
		active:  make(map[net.Conn]struct{}),
	}
}

// Accept handles a single connection
func (h *ConnectionHandler) Accept(conn net.Conn) error {
	// Track active connection
	h.mu.Lock()
	h.active[conn] = struct{}{}
	h.mu.Unlock()

	// Clean up when done
	defer func() {
		h.mu.Lock()
		delete(h.active, conn)
		h.mu.Unlock()
		_ = conn.Close()
	}()

	// Handle the connection
	ctx := context.Background()
	if err := h.handler(ctx, conn); err != nil {
		h.logger.Error("connection handler error", Field{"error", err})
		return err
	}
	return nil
}

// ActiveConnections returns the number of active connections
func (h *ConnectionHandler) ActiveConnections() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.active)
}

// Close closes all active connections
func (h *ConnectionHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	for conn := range h.active {
		_ = conn.Close()
	}
	h.active = make(map[net.Conn]struct{})
	return nil
}

// AuthManager provides basic file-based authentication for local use
type AuthManager struct {
	configPath string
	logger     Logger
	pidFile    string
}

// NewAuthManager creates an auth manager
func NewAuthManager(configPath string, logger Logger) (*AuthManager, error) {
	auth := &AuthManager{
		configPath: configPath,
		logger:     logger,
		pidFile:    filepath.Join(configPath, ".localbase.pid"),
	}

	// Ensure config directory exists with proper permissions
	if err := os.MkdirAll(configPath, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	return auth, nil
}

// ValidateToken validates a token (for local use)
func (a *AuthManager) ValidateToken(_ string) bool {
	// For local development, just check if daemon is running by same user
	_, err := os.Stat(a.pidFile)
	return err == nil
}

// ValidateRequest validates a request
func (a *AuthManager) ValidateRequest(token string) bool {
	return a.ValidateToken(token)
}

// CreatePIDFile creates a PID file when daemon starts
func (a *AuthManager) CreatePIDFile() error {
	pid := fmt.Sprintf("%d", os.Getpid())
	return os.WriteFile(a.pidFile, []byte(pid), 0o600)
}

// RemovePIDFile removes the PID file when daemon stops
func (a *AuthManager) RemovePIDFile() error {
	return os.Remove(a.pidFile)
}

// GetToken returns a token (PID for local use)
func (a *AuthManager) GetToken() (string, error) {
	pidBytes, err := os.ReadFile(a.pidFile)
	if err != nil {
		return "", fmt.Errorf("daemon not running or permission denied")
	}
	return string(pidBytes), nil
}

// GetClientToken returns a client token
func (a *AuthManager) GetClientToken() (string, error) {
	return a.GetToken()
}

// RotateToken is a no-op for the auth system
func (a *AuthManager) RotateToken() error {
	// For local development, token rotation is not needed
	return nil
}

// TLSManager provides basic TLS for localhost
type TLSManager struct {
	configPath string
	logger     Logger
}

// NewTLSManager creates a TLS manager
func NewTLSManager(configPath string, logger Logger) *TLSManager {
	return &TLSManager{
		configPath: configPath,
		logger:     logger,
	}
}

// GetTLSConfig returns TLS config for localhost
func (t *TLSManager) GetTLSConfig() (*tls.Config, error) {
	certFile := filepath.Join(t.configPath, "cert.pem")
	keyFile := filepath.Join(t.configPath, "key.pem")

	// Generate cert if it doesn't exist
	if !t.certificateExists(certFile, keyFile) {
		if err := t.generateCertificate(certFile, keyFile); err != nil {
			return nil, fmt.Errorf("failed to generate certificate: %w", err)
		}
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load certificate: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ServerName:   "localhost",
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// GetClientTLSConfig returns client TLS config
func (t *TLSManager) GetClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true, // #nosec G402 - localhost self-signed cert
		ServerName:         "localhost",
		MinVersion:         tls.VersionTLS12,
	}
}

// certificateExists checks if certificate files exist
func (t *TLSManager) certificateExists(certFile, keyFile string) bool {
	_, certErr := os.Stat(certFile)
	_, keyErr := os.Stat(keyFile)
	return certErr == nil && keyErr == nil
}

// generateCertificate creates a self-signed certificate
func (t *TLSManager) generateCertificate(certFile, keyFile string) error {
	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	// Certificate template for localhost
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"LocalBase"},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:    []string{"localhost"},
	}

	// Create certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	// Write certificate file
	certOut, err := os.OpenFile(certFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304
	if err != nil {
		return fmt.Errorf("failed to create cert file: %w", err)
	}
	defer func() { _ = certOut.Close() }()

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	// Write private key file
	keyOut, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304
	if err != nil {
		return fmt.Errorf("failed to create key file: %w", err)
	}
	defer func() { _ = keyOut.Close() }()

	privKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)

	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: privKeyBytes}); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	t.logger.Info("generated self-signed certificate for localhost")
	return nil
}
