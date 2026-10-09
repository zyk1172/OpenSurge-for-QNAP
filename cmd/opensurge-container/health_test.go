package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthRequiresDataPlaneReadinessResponse(t *testing.T) {
	for _, test := range []struct {
		name  string
		code  int
		body  string
		ready bool
	}{
		{"running", 200, `{"status":"ready","desired_running":true}`, true},
		{"intentionally stopped", 200, `{"status":"ready","desired_running":false}`, true},
		{"live alone", 200, `{"status":"ok"}`, false},
		{"unavailable", 503, `{"status":"not_ready"}`, false},
		{"invalid body", 200, `<html>login</html>`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/health/ready" {
					t.Errorf("path: %s", r.URL.Path)
				}
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			err := checkWebReadiness(t.Context(), strings.TrimPrefix(server.URL, "http://"))
			if (err == nil) != test.ready {
				t.Fatalf("health error = %v, ready = %t", err, test.ready)
			}
		})
	}
}

func TestHealthReturnsWhenControlServiceStalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := checkWebReadiness(ctx, strings.TrimPrefix(server.URL, "http://")); err == nil {
		t.Fatal("stalled service reported ready")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("health did not respect deadline: %s", elapsed)
	}
}
