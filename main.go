package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

func run(cfg *Config) {

	if err := ensureCaddyRunning(cfg.CaddyAdmin); err != nil {
		log.Fatalf("failed to ensure Caddy is running: %v", err)
	}

	lb := NewLocalBase()

	listener, err := net.Listen("tcp", cfg.AdminAddress)
	if err != nil {
		log.Fatalf("failed to start localbase server: %v", err)
	}
	defer listener.Close()

	log.Println("localBase server started. listening on", cfg.AdminAddress)

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
					log.Printf("error accepting connection: %v\n", err)
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
			log.Println("shutting down localbase")
			lb.Shutdown()
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
			close(ch)
		default:
			fmt.Fprintln(conn, "Unknown command")
		}
	}
}

func sendCommand(command string) error {
	cfg, err := readConfig()
	if err != nil {
		return err
	}

	conn, err := net.Dial("tcp", cfg.AdminAddress)
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

var rootCmd = &cobra.Command{
	Use:   "localbase",
	Short: "localBase is a local domain management tool",
	Long: `localBase allows you to manage local domains and their corresponding ports.
It integrates with Caddy server to provide local domain resolution and routing.`,
}

var addCmd = &cobra.Command{
	Use:   "add <domain> --port <port>",
	Short: "add a new domain",
	Long:  `add a new domain to LocalBase with the specified port.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("usage: localbase add <domain> --port <port>")
		}
		port, _ := cmd.Flags().GetInt("port")
		if port == 0 {
			return fmt.Errorf("port is required")
		}
		return sendCommand(fmt.Sprintf("add %s --port %d", args[0], port))
	},
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "start the localbase",
	Long:  `start the localbase,either in the foreground or as a detached process.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		caddyAdmin, _ := cmd.Flags().GetString("caddy")
		adminAddr, _ := cmd.Flags().GetInt("addr")
		detached, _ := cmd.Flags().GetBool("detached")

		cfg := &Config{
			AdminAddress: fmt.Sprintf(":%d", adminAddr),
			CaddyAdmin:   caddyAdmin,
		}

		if err := saveConfig(cfg); err != nil {
			return fmt.Errorf("failed to save config: %v", err)
		}

		if detached {
			cmd := exec.Command(os.Args[0], "start")
			cmd.Stdout = nil
			cmd.Stderr = nil
			cmd.Stdin = nil
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("failed to start in detached mode: %v", err)
			}

			return nil
		}

		run(cfg)
		return nil
	},
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop localbase daemon",
		Long:  `Stop the running localbase daemon.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return sendCommand("stop")
		},
	}
}

func removeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <domain>",
		Short: "Remove a domain",
		Long:  `Remove a domain from LocalBase.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: localbase remove <domain>")
			}
			return sendCommand(fmt.Sprintf("remove %s", args[0]))
		},
	}
}

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all domains",
		Long:  `List all domains registered in LocalBase.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return sendCommand("list")
		},
	}
}

func init() {
	rootCmd.AddCommand(addCmd)
	addCmd.Flags().IntP("port", "p", 0, "port for the .local domain")
	rootCmd.AddCommand(startCmd)
	startCmd.Flags().IntP("addr", "a", 2025, "localbase process address")
	startCmd.Flags().StringP("caddy", "c", "http://localhost:2019", "local caddy admin address")
	startCmd.Flags().BoolP("detached", "d", false, "run localbase in background")
	rootCmd.AddCommand(stopCmd())
	rootCmd.AddCommand(removeCmd())
	rootCmd.AddCommand(listCmd())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("[localbase]: %v", err)
	}
}
