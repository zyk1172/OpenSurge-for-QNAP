package webgateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestGatewayRequiresLoginAndInjectsInternalBearer(t *testing.T) {
	const token = "internal-control-token"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"proxied":true}`)
	}))
	defer upstream.Close()

	gateway, err := New(Options{
		Upstream:     upstream.URL,
		ControlToken: token,
		AuthDir:      filepath.Join(t.TempDir(), "auth"),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	client := server.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != "/auth/" {
		t.Fatalf("unauthenticated GET = %d location %q", response.StatusCode, response.Header.Get("Location"))
	}

	setupPayload, _ := json.Marshal(map[string]string{"username": "admin", "password": "correct horse battery staple"})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/auth/setup", bytes.NewReader(setupPayload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", server.URL)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("setup status = %d", response.StatusCode)
	}
	cookies := response.Cookies()
	if len(cookies) == 0 {
		t.Fatal("setup did not issue session cookie")
	}

	request, _ = http.NewRequest(http.MethodGet, server.URL+"/api/v1/test", nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("proxied GET status = %d", response.StatusCode)
	}
}

func TestGatewayRejectsMutationWithoutSameOrigin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	gateway, err := New(Options{Upstream: upstream.URL, ControlToken: "token", AuthDir: filepath.Join(t.TempDir(), "auth")})
	if err != nil {
		t.Fatal(err)
	}
	if err := gateway.auth.Setup("admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	recorder := httptest.NewRecorder()
	gateway.issueSession(recorder)
	cookie := recorder.Result().Cookies()[0]
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/gateway/start", bytes.NewReader([]byte(`{}`)))
	request.AddCookie(cookie)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("mutation without Origin status = %d, want 403", response.StatusCode)
	}
}

func TestGatewayRejectsPublicUnlistedHost(t *testing.T) {
	gateway, err := New(Options{Upstream: "http://127.0.0.1:61767", ControlToken: "token", AuthDir: filepath.Join(t.TempDir(), "auth")})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://example.com/health/live", nil)
	request.Host = "example.com"
	recorder := httptest.NewRecorder()
	gateway.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("public unlisted host status = %d, want 403", recorder.Code)
	}
}

func TestGatewayReadinessRequiresDesiredAndObservedDataPlaneToMatch(t *testing.T) {
	tests := []struct {
		name           string
		desiredRunning bool
		gateway        string
		runtimeState   string
		wantStatus     int
	}{
		{name: "running and active", desiredRunning: true, gateway: "running", runtimeState: "active", wantStatus: http.StatusOK},
		{name: "running intent but interrupted", desiredRunning: true, gateway: "degraded", runtimeState: "interrupted", wantStatus: http.StatusServiceUnavailable},
		{name: "running intent but runtime absent", desiredRunning: true, gateway: "stopped", runtimeState: "none", wantStatus: http.StatusServiceUnavailable},
		{name: "explicitly stopped and clean", desiredRunning: false, gateway: "stopped", runtimeState: "none", wantStatus: http.StatusOK},
		{name: "stop intent but stale runtime remains", desiredRunning: false, gateway: "degraded", runtimeState: "active", wantStatus: http.StatusServiceUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const token = "internal-control-token"
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/overview" {
					t.Fatalf("readiness upstream path = %q", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer "+token {
					t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
				}
				writeJSON(w, http.StatusOK, map[string]any{"status": map[string]any{
					"gateway":         test.gateway,
					"runtime_state":   test.runtimeState,
					"desired_running": test.desiredRunning,
				}})
			}))
			defer upstream.Close()

			gateway, err := New(Options{Upstream: upstream.URL, ControlToken: token, AuthDir: filepath.Join(t.TempDir(), "auth")})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/health/ready", nil)
			recorder := httptest.NewRecorder()
			gateway.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("readiness status = %d body=%s, want %d", recorder.Code, recorder.Body.String(), test.wantStatus)
			}
		})
	}
}
