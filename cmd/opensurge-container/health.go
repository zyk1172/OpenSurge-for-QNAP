package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Query the long-lived Control/Web service instead of starting a new set of
// ip/nft probes in every Docker healthcheck process. The daemon's shared probe
// limit then also bounds children across repeated healthchecks.
func checkWebReadiness(ctx context.Context, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid Web address: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/health/ready", nil)
	if err != nil {
		return err
	}
	client := &http.Client{
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Web readiness returned HTTP %d", response.StatusCode)
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result); err != nil {
		return fmt.Errorf("invalid readiness response: %w", err)
	}
	if result.Status != "ready" {
		return fmt.Errorf("gateway data plane is not ready")
	}
	return nil
}
