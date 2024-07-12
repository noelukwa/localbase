package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mitchellh/go-homedir"
)

type Config struct {
	CaddyAdmin   string `json:"caddy_admin"`
	AdminAddress string `json:"admin_address"`
}

func defaultConfig() *Config {
	return &Config{
		CaddyAdmin:   "http://localhost:2019",
		AdminAddress: "localhost:2025",
	}
}

func getConfigDir() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", err
	}

	var configDir string
	switch runtime.GOOS {
	case "windows":
		configDir = filepath.Join(home, "AppData", "Roaming", "localbase")
	case "darwin":
		configDir = filepath.Join(home, "Library", "Application Support", "localbase")
	default:
		configDir = filepath.Join(home, ".config", "localbase")
	}

	return configDir, nil
}

func saveConfig(cfg *Config) error {
	configDir, err := getConfigDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	configFile := filepath.Join(configDir, "config.json")

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configFile, data, 0644)
}

func readConfig() (*Config, error) {
	configDir, err := getConfigDir()
	if err != nil {
		return &Config{}, err
	}

	configFile := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), nil
		}
		return &Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &Config{}, err
	}

	return &cfg, nil
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
