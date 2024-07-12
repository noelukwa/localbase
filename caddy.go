package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func getCaddyConfig(caddyAdmin string) (map[string]interface{}, error) {
	resp, err := http.Get(fmt.Sprintf("%s/config/", caddyAdmin))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get Caddy config: %s", body)
	}

	var config map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, err
	}

	return config, nil
}

func addCaddyServerBlock(domains []string, port int, caddyAdmin string) error {
	config, err := getCaddyConfig(caddyAdmin)
	if err != nil {
		return err
	}

	// Ensure the config structure is initialized
	if config == nil {
		config = make(map[string]interface{})
	}

	if _, ok := config["apps"]; !ok {
		config["apps"] = make(map[string]interface{})
	}

	apps := config["apps"].(map[string]interface{})
	if _, ok := apps["http"]; !ok {
		apps["http"] = make(map[string]interface{})
	}

	httpApp := apps["http"].(map[string]interface{})
	if _, ok := httpApp["servers"]; !ok {
		httpApp["servers"] = make(map[string]interface{})
	}

	servers := httpApp["servers"].(map[string]interface{})
	serverName := "default"
	if existingServer, ok := servers[serverName]; ok {
		server := existingServer.(map[string]interface{})
		routes := server["routes"].([]interface{})

		for _, domain := range domains {
			routes = append(routes, map[string]interface{}{
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
			})
		}

		server["routes"] = routes
		servers[serverName] = server
	} else {
		newRoutes := []interface{}{}
		for _, domain := range domains {
			newRoutes = append(newRoutes, map[string]interface{}{
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
			})
		}

		servers[serverName] = map[string]interface{}{
			"listen": []string{":80", ":443"},
			"routes": newRoutes,
		}
	}

	jsonData, err := json.Marshal(config)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/config/", caddyAdmin)
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

func isCaddyRunning(caddyAdmin string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/config/", caddyAdmin), nil)
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

func ensureCaddyRunning(caddyAdmin string) error {
	running, err := isCaddyRunning(caddyAdmin)
	if err == nil && running {
		return nil
	}
	return fmt.Errorf("ensure caddy is installed and running")
}
