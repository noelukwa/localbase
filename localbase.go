package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/oleksandr/bonjour"
)

type Record struct {
	service string
	host    string
	port    int
	server  *bonjour.Server
	mu      sync.Mutex
}

type LocalBase struct {
	records       map[string]*Record
	mu            sync.RWMutex
	logger        Logger
	configManager ConfigManager
	caddyClient   CaddyClient
	validator     Validator
	localIP       net.IP
	ipMu          sync.RWMutex
}

func NewLocalBase(logger Logger, configManager ConfigManager, caddyClient CaddyClient, validator Validator) (*LocalBase, error) {
	localIP, err := getLocalIP()
	if err != nil {
		return nil, fmt.Errorf("failed to get local IP: %w", err)
	}
	
	return &LocalBase{
		records:       make(map[string]*Record),
		logger:        logger,
		configManager: configManager,
		caddyClient:   caddyClient,
		validator:     validator,
		localIP:       localIP,
	}, nil
}

func (lb *LocalBase) List(ctx context.Context) ([]string, error) {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	domains := make([]string, 0, len(lb.records))
	for domain := range lb.records {
		domains = append(domains, domain)
	}
	return domains, nil
}

func (lb *LocalBase) Add(ctx context.Context, domain string, port int) error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	// Validate inputs first
	if err := lb.validator.ValidateDomain(domain); err != nil {
		return fmt.Errorf("domain validation failed: %w", err)
	}
	
	if err := lb.validator.ValidatePort(port); err != nil {
		return fmt.Errorf("port validation failed: %w", err)
	}

	// Get current IP
	lb.ipMu.RLock()
	localIP := lb.localIP
	lb.ipMu.RUnlock()
	
	lb.logger.Debug("using local IP", Field{"ip", localIP.String()})

	clean := strings.TrimSpace(domain)
	fullDomain := fmt.Sprintf("%s.local", clean)
	if _, exists := lb.records[fullDomain]; exists {
		return fmt.Errorf("domain %s already registered", fullDomain)
	}
	fullHost := fmt.Sprintf("%s.", fullDomain)

	service := fmt.Sprintf("_%s._tcp", clean)
	// Register nodecrane service
	s1, err := bonjour.RegisterProxy(
		"localbase",
		service,
		"",
		80,
		fullHost,
		localIP.String(),
		[]string{},
		nil)

	if err != nil {
		return fmt.Errorf("failed to register mDNS service: %w", err)
	}

	lb.records[fullDomain] = &Record{
		service: service,
		host:    fullHost,
		port:    port,
		server:  s1,
	}

	if err := lb.caddyClient.AddServerBlock(ctx, []string{fullDomain}, port); err != nil {
		s1.Shutdown()
		delete(lb.records, fullDomain)
		return fmt.Errorf("failed to add Caddy server block: %w", err)
	}
	return nil
}

func (lb *LocalBase) Remove(ctx context.Context, domain string) error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	record, exists := lb.records[domain]
	if !exists {
		return fmt.Errorf("domain %s not registered", domain)
	}

	record.mu.Lock()
	if record.server != nil {
		record.server.Shutdown()
	}
	record.mu.Unlock()

	// Remove Caddy server block
	if err := lb.caddyClient.RemoveServerBlock(ctx, []string{domain}); err != nil {
		lb.logger.Error("failed to remove Caddy server block", Field{"domain", domain}, Field{"error", err.Error()})
		// Continue with cleanup even if Caddy removal fails
	}
	
	delete(lb.records, domain)
	lb.logger.Info("removed domain", Field{"domain", domain})
	return nil
}

func (lb *LocalBase) Shutdown(ctx context.Context) error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	var errors []error
	
	// Shutdown all mDNS services
	for domain, rec := range lb.records {
		rec.mu.Lock()
		if rec.server != nil {
			rec.server.Shutdown()
		}
		rec.mu.Unlock()
		lb.logger.Info("shutting down domain", Field{"domain", domain})
	}

	// Clear all Caddy server blocks
	if err := lb.caddyClient.ClearAllServerBlocks(ctx); err != nil {
		lb.logger.Error("failed to clear Caddy server blocks during shutdown", Field{"error", err.Error()})
		errors = append(errors, fmt.Errorf("failed to clear Caddy server blocks: %w", err))
	} else {
		lb.logger.Info("cleared all Caddy server blocks during shutdown")
	}
	
	if len(errors) > 0 {
		return fmt.Errorf("shutdown errors: %v", errors)
	}
	return nil
}

func (lb *LocalBase) startBroadcast(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			lb.broadcastAll()
		case <-ctx.Done():
			return
		}
	}
}

func (lb *LocalBase) broadcastAll() {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	// Update local IP if changed
	newIP, err := getLocalIP()
	if err != nil {
		lb.logger.Error("failed to get local IP during broadcast", Field{"error", err})
		return
	}
	
	lb.ipMu.Lock()
	lb.localIP = newIP
	lb.ipMu.Unlock()

	for domain, info := range lb.records {
		// Create new record to avoid race condition
		newRecord := &Record{
			service: info.service,
			host:    info.host,
			port:    info.port,
		}
		
		// Shutdown old server
		info.mu.Lock()
		if info.server != nil {
			info.server.Shutdown()
		}
		info.mu.Unlock()

		// Register new server
		server, err := bonjour.RegisterProxy(
			"localbase",
			newRecord.service,
			"",
			80,
			newRecord.host,
			newIP.String(),
			[]string{},
			nil)

		if err != nil {
			lb.logger.Error("failed to re-register service",
				Field{"domain", domain},
				Field{"error", err})
			continue
		}

		// Update record with new server
		newRecord.server = server
		lb.records[domain] = newRecord
	}
}
