package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/oleksandr/bonjour"
)

const (
	daemonAddr = "localhost:8080"
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

func (lb *LocalBase) Add(domain string) error {
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
	lb := NewLocalBase()

	listener, err := net.Listen("tcp", daemonAddr)
	if err != nil {
		log.Fatalf("Failed to start daemon: %v", err)
	}
	defer listener.Close()

	log.Println("LocalBase daemon started. Listening on", daemonAddr)

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		lb.Shutdown()
		os.Exit(0)
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			continue
		}
		go handleConnection(conn, lb)
	}
}

func handleConnection(conn net.Conn, lb *LocalBase) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	if scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		cmd := parts[0]
		switch cmd {
		case "add", "remove":
			if len(parts) != 2 {
				fmt.Fprintln(conn, "Invalid command. Usage: add/remove <domain>")
				return
			}
			domain := parts[1]
			if cmd == "add" {
				err := lb.Add(domain)
				if err != nil {
					fmt.Fprintf(conn, "Error: %v\n", err)
				} else {
					fmt.Fprintf(conn, "Added domain: %s\n", domain)
				}
			} else {
				err := lb.Remove(domain)
				if err != nil {
					fmt.Fprintf(conn, "Error: %v\n", err)
				} else {
					fmt.Fprintf(conn, "Removed domain: %s\n", domain)
				}
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
	case "add", "remove":
		if len(os.Args) != 3 {
			fmt.Printf("Usage: localbase %s <domain>\n", os.Args[1])
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
