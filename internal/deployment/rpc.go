package deployment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type RPCClient struct {
	client *http.Client
}

func NewRPCClient(socket string) *RPCClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	return &RPCClient{client: &http.Client{Transport: transport, Timeout: 90 * time.Second}}
}

func (c *RPCClient) Status(ctx context.Context) (Status, error) {
	var status Status
	if err := c.do(ctx, http.MethodGet, "/v1/status", nil, &status); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (c *RPCClient) Apply(ctx context.Context, spec Spec) (Status, error) {
	var status Status
	if err := c.do(ctx, http.MethodPost, "/v1/apply", spec, &status); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (c *RPCClient) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://orchestrator"+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("deployment orchestrator unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope struct{ Error string `json:"error"` }
		_ = json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&envelope)
		if envelope.Error == "" {
			envelope.Error = response.Status
		}
		return errors.New(envelope.Error)
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
			return err
		}
	}
	return nil
}

func ServeRPC(ctx context.Context, socket string, orchestrator *Orchestrator) error {
	if orchestrator == nil {
		return fmt.Errorf("orchestrator is required")
	}
	if socket == "" {
		socket = "/run/opensurge/orchestrator.sock"
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = listener.Close()
		return err
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(socket)
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		status, err := orchestrator.Status(r.Context())
		if err != nil {
			writeRPCError(w, http.StatusServiceUnavailable, err)
			return
		}
		writeRPCJSON(w, http.StatusOK, status)
	})
	mux.HandleFunc("POST /v1/apply", func(w http.ResponseWriter, r *http.Request) {
		var spec Spec
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&spec); err != nil {
			writeRPCError(w, http.StatusBadRequest, fmt.Errorf("invalid deployment request: %w", err))
			return
		}
		status, err := orchestrator.Apply(r.Context(), spec)
		if err != nil {
			writeRPCError(w, http.StatusUnprocessableEntity, err)
			return
		}
		writeRPCJSON(w, http.StatusOK, status)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 95 * time.Second, WriteTimeout: 95 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func writeRPCJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeRPCError(w http.ResponseWriter, status int, err error) {
	writeRPCJSON(w, status, map[string]string{"error": err.Error()})
}
