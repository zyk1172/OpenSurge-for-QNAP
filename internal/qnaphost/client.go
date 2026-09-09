package qnaphost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

type Client struct {
	http *http.Client
}

func NewClient(socketPath string) *Client {
	if socketPath == "" {
		socketPath = "/run/opensurge-host/agent.sock"
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &Client{http: &http.Client{Transport: transport, Timeout: 15 * time.Second}}
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var status Status
	if err := c.do(ctx, http.MethodGet, "/v1/status", nil, &status); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (c *Client) Configure(ctx context.Context, cfg NetworkConfig) (Status, error) {
	var status Status
	if err := c.do(ctx, http.MethodPut, "/v1/network", cfg, &status); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://opensurge-host"+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
		return fmt.Errorf("QNAP host agent: %s: %s", resp.Status, string(payload))
	}
	return json.NewDecoder(resp.Body).Decode(output)
}
