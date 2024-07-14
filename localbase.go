package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/oleksandr/bonjour"
)

type Record struct {
	service string
	host    string
	server  *bonjour.Server
}

type LocalBase struct {
	records map[string]*Record
	mu      sync.Mutex
}

func NewLocalBase() *LocalBase {
	return &LocalBase{
		records: make(map[string]*Record),
	}
}

func (lb *LocalBase) List() []string {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	domains := make([]string, 0, len(lb.records))
	for domain := range lb.records {
		domains = append(domains, domain)
	}
	return domains
}

func (lb *LocalBase) Add(domain string, port int) error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	config, err := readConfig()
	if err != nil {
		return err
	}

	localIP, err := getLocalIP()
	if err != nil {
		log.Fatalln("Error getting local IP:", err.Error())
	}
	log.Println("Local IP:", localIP)

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
		localIP,
		[]string{},
		nil)

	if err != nil {
		log.Fatalln("Error registering frontend service:", err.Error())
	}

	lb.records[fullDomain] = &Record{
		service: service,
		host:    fullHost,
		server:  s1,
	}

	if err := addCaddyServerBlock([]string{fullDomain}, port, config.CaddyAdmin); err != nil {
		s1.Shutdown()
		delete(lb.records, domain)
		return fmt.Errorf("failed to add Caddy server block: %v", err)
	}
	return nil
}

func (lb *LocalBase) Remove(domain string) error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	record, exists := lb.records[domain]
	if !exists {
		return fmt.Errorf("domain %s not registered", domain)
	}

	record.server.Shutdown()
	delete(lb.records, domain)
	log.Printf("Removed domain: %s", domain)
	return nil
}

func (lb *LocalBase) Shutdown() {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	for domain, rec := range lb.records {
		rec.server.Shutdown()
		log.Printf("Shutting down domain: %s", domain)
	}
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

	localIP, err := getLocalIP()
	if err != nil {
		log.Fatalln("Error getting local IP:", err.Error())
	}

	for domain, info := range lb.records {
		info.server.Shutdown()

		server, err := bonjour.RegisterProxy(
			"localbase",
			info.service,
			"",
			80,
			info.host,
			localIP,
			[]string{},
			nil)

		if err != nil {
			log.Fatalln("Error registering frontend service:", err.Error())
		}

		if err != nil {
			log.Printf("Error re-registering service for %s: %v", domain, err)
			continue
		}

		info.server = server
	}
}
