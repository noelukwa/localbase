package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
	builtBy = "unknown"
)

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
			cmd := exec.Command(os.Args[0], "start", "--caddy", caddyAdmin, "--addr", adminAddr, "--log-level", logLevel) // #nosec G204 -- using own binary path with validated flags
			cmd.Stdout = nil
			cmd.Stderr = nil
			cmd.Stdin = nil
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("failed to start in detached mode: %w", err)
			}
			logger.Info("localbase started in background", Field{"pid", cmd.Process.Pid})
			return nil
		}

		// Create server
		server, err := NewServer(cfg, logger)
		if err != nil {
			return fmt.Errorf("failed to create server: %w", err)
		}

		// Setup context with signal handling
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

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

		return client.SendCommand("add", map[string]any{
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

		return client.SendCommand("remove", map[string]any{
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

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of localbase",
	Run: func(cmd *cobra.Command, args []string) {
		// If version is still "dev", try to get it from build info
		if version == "dev" {
			if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
				version = info.Main.Version
			}
		}

		fmt.Printf("LocalBase %s\n", version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built: %s\n", date)
		fmt.Printf("  built by: %s\n", builtBy)
	},
}

var pingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Ping the localbase daemon",
	Long:  `Check if the localbase daemon is running and responsive.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := NewLogger(ErrorLevel) // Quiet for ping
		client, err := NewClient(logger)
		if err != nil {
			return fmt.Errorf("failed to connect to localbase daemon: %w", err)
		}

		err = client.SendCommand("ping", nil)
		if err != nil {
			return fmt.Errorf("ping failed: %w", err)
		}

		fmt.Println("pong")
		return nil
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
	if err := addCmd.MarkFlagRequired("port"); err != nil {
		panic(fmt.Errorf("failed to mark port flag as required: %w", err))
	}

	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(pingCmd)
	rootCmd.AddCommand(versionCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
