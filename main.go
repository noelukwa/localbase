package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/oleksandr/bonjour"
)

var (
	daemonAddr    = "localhost:8080"
	caddyAdminAPI = "http://localhost:2019"
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

	if err := addCaddyServerBlock([]string{fullDomain}, port); err != nil {
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

func getLocalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip != nil && !ip.IsLoopback() && ip.To4() != nil {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("no suitable local IP address found")
}

func runDaemon() {

	if err := ensureCaddyRunning(); err != nil {
		log.Fatalf("failed to ensure Caddy is running: %v", err)
	}

	lb := NewLocalBase()

	listener, err := net.Listen("tcp", daemonAddr)
	if err != nil {
		log.Fatalf("Failed to start daemon: %v", err)
	}
	defer listener.Close()

	log.Println("LocalBase daemon started. Listening on", daemonAddr)

	ctx, cancel := context.WithCancel(context.Background())

	go lb.startBroadcast(ctx)

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		cancel()
	}()

	doneChan := make(chan struct{})
	connections := make(chan net.Conn)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					log.Printf("Error accepting connection: %v\n", err)
					continue
				}
			}

			select {
			case connections <- conn:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case conn := <-connections:
			go handleConnection(doneChan, conn, lb)
		case <-doneChan:
			cancel()
		case <-ctx.Done():
			lb.Shutdown()
			log.Println("Shutting down daemon")
			return
		}
	}
}

func handleConnection(ch chan struct{}, conn net.Conn, lb *LocalBase) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	if scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		cmd := parts[0]
		switch cmd {
		case "add":
			if len(parts) != 4 || parts[2] != "--port" {
				fmt.Fprintln(conn, "Invalid command. Usage: add <domain> --port <port>")
				return
			}
			domain := parts[1]
			port, err := strconv.Atoi(parts[3])
			if err != nil {
				fmt.Fprintf(conn, "Invalid port number: %v\n", err)
				return
			}
			err = lb.Add(domain, port)
			if err != nil {
				fmt.Fprintf(conn, "Error: %v\n", err)
			} else {
				fmt.Fprintf(conn, "Added domain: %s with port: %d\n", domain, port)
			}
		case "remove":
			if len(parts) != 2 {
				fmt.Fprintln(conn, "Invalid command. Usage: remove <domain>")
				return
			}
			domain := parts[1]
			err := lb.Remove(domain)
			if err != nil {
				fmt.Fprintf(conn, "Error: %v\n", err)
			} else {
				fmt.Fprintf(conn, "Removed domain: %s\n", domain)
			}

		case "list":
			domains := lb.List()
			if len(domains) == 0 {
				fmt.Fprintln(conn, "No domains registered")
			} else {
				fmt.Fprintln(conn, "Registered domains:")
				for _, domain := range domains {
					fmt.Fprintf(conn, "- %s\n", domain)
				}
			}
		case "stop":
			fmt.Fprintln(conn, "Shutting down localbase")
			close(ch)
		default:
			fmt.Fprintln(conn, "Unknown command")
		}
	}
}

func startDaemon() error {
	cmd := exec.Command(os.Args[0], "daemon")
	cmd.Start()
	return nil
}

func sendCommand(command string) error {
	conn, err := net.Dial("tcp", daemonAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %v", err)
	}
	defer conn.Close()

	_, err = fmt.Fprintln(conn, command)
	if err != nil {
		return fmt.Errorf("failed to send command: %v", err)
	}

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading response: %v", err)
	}

	return nil
}

func startCaddy() error {
	cmd := exec.Command("caddy", "start")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

func isCaddyRunning() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "http://localhost:2019/config", nil)
	if err != nil {
		return false, err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return false, nil
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}

func ensureCaddyRunning() error {
	running, err := isCaddyRunning()
	if err == nil && running {
		log.Println("Caddy is running")
		return nil
	}
	return startCaddy()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: localbase <command> [args...]")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "daemon":
		runDaemon()
	case "start":
		if err := startDaemon(); err != nil {
			log.Fatalf("Failed to start daemon: %v", err)
		}
		fmt.Println("LocalBase daemon started")
	case "stop":
		if err := sendCommand("stop"); err != nil {
			log.Fatalf("Command failed: %v", err)
		}
	case "add":
		if len(os.Args) != 5 || os.Args[3] != "--port" {
			fmt.Println("Usage: localbase add <domain> --port <port>")
			os.Exit(1)
		}
		if err := sendCommand(strings.Join(os.Args[1:], " ")); err != nil {
			log.Fatalf("Command failed: %v", err)
		}
	case "remove":
		if len(os.Args) != 3 {
			fmt.Println("Usage: localbase remove <domain>")
			os.Exit(1)
		}
		if err := sendCommand(strings.Join(os.Args[1:], " ")); err != nil {
			log.Fatalf("Command failed: %v", err)
		}
	case "list":
		if err := sendCommand("list"); err != nil {
			log.Fatalf("Command failed: %v", err)
		}
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
