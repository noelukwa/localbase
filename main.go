package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

const (
	daemonAddr    = "localhost:8080"
	caddyAdminAPI = "http://localhost:2019"
)

type LocalBase struct {
	services map[string]*bonjour.Server
	mu       sync.Mutex
}

func NewLocalBase() *LocalBase {
	return &LocalBase{
		services: make(map[string]*bonjour.Server),
	}
}

func (lb *LocalBase) List() []string {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	domains := make([]string, 0, len(lb.services))
	for domain := range lb.services {
		domains = append(domains, domain)
	}
	return domains
}

func (lb *LocalBase) Add(domain string, port int) error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	if _, exists := lb.services[domain]; exists {
		return fmt.Errorf("domain %s already registered", domain)
	}

	localIP, err := getLocalIP()
	if err != nil {
		return err
	}

	serviceName := strings.TrimSuffix(domain, ".local")
	fullServiceName := fmt.Sprintf("%s._http._tcp.local.", serviceName)

	s, err := bonjour.RegisterProxy(fullServiceName, "_http._tcp", "", 80, serviceName, localIP, []string{"txtv=1", "app=localbase"}, nil)
	if err != nil {
		return err
	}

	lb.services[domain] = s
	log.Printf("Registered domain: %s as %s", domain, fullServiceName)

	// Add Caddy server block
	if err := addCaddyServerBlock(domain, port); err != nil {
		s.Shutdown()
		delete(lb.services, domain)
		return fmt.Errorf("failed to add Caddy server block: %v", err)
	}
	return nil
}

func addCaddyServerBlock(domain string, port int) error {
	config := map[string]interface{}{
		"apps": map[string]interface{}{
			"http": map[string]interface{}{
				"servers": map[string]interface{}{
					domain: map[string]interface{}{
						"listen": []string{":80", ":443"},
						"routes": []map[string]interface{}{
							{
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
							},
						},
					},
				},
			},
		},
	}

	jsonData, err := json.Marshal(config)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/config/", caddyAdminAPI)
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to add Caddy server block: %s", body)
	}

	return nil
}

func (lb *LocalBase) Remove(domain string) error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	s, exists := lb.services[domain]
	if !exists {
		return fmt.Errorf("domain %s not registered", domain)
	}

	s.Shutdown()
	delete(lb.services, domain)
	log.Printf("Removed domain: %s", domain)
	return nil
}

func (lb *LocalBase) Shutdown() {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	for domain, s := range lb.services {
		s.Shutdown()
		log.Printf("Shutting down domain: %s", domain)
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
