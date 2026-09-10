package webgateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQNAPFirstAdminRequiresBootstrapToken(t *testing.T) {
	authDir := t.TempDir()
	s, err := New(Options{
		ControlToken:          "control-secret",
		AuthDir:               authDir,
		RequireBootstrapToken: true,
		SecureCookies:         true,
		QNAPOnly:              true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tokenPath := filepath.Join(authDir, bootstrapTokenFile)
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read bootstrap token: %v", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if len(token) != 64 {
		t.Fatalf("expected 32-byte hex bootstrap token, got %d chars", len(token))
	}
	if info, err := os.Stat(tokenPath); err != nil {
		t.Fatalf("stat bootstrap token: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("bootstrap token mode = %o, want 600", info.Mode().Perm())
	}

	stateReq := httptest.NewRequest(http.MethodGet, "http://192.168.2.241:8080/api/auth/state", nil)
	stateReq.Host = "192.168.2.241:8080"
	stateRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(stateRec, stateReq)
	var state map[string]bool
	if err := json.Unmarshal(stateRec.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode auth state: %v", err)
	}
	if !state["setup_required"] || !state["bootstrap_required"] {
		t.Fatalf("unexpected auth state: %#v", state)
	}

	postSetup := func(bootstrap string) *httptest.ResponseRecorder {
		body := []byte(`{"username":"admin","password":"very-long-password","bootstrap_token":"` + bootstrap + `"}`)
		req := httptest.NewRequest(http.MethodPost, "http://192.168.2.241:8080/api/auth/setup", bytes.NewReader(body))
		req.Host = "192.168.2.241:8080"
		req.Header.Set("Origin", "http://192.168.2.241:8080")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}

	if rec := postSetup("wrong-token"); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong bootstrap token status = %d, want 403", rec.Code)
	}
	rec := postSetup(token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid bootstrap token status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("consumed bootstrap token still exists: %v", err)
	}
	if cookie := rec.Header().Get("Set-Cookie"); !strings.Contains(cookie, "Secure") || !strings.Contains(cookie, "HttpOnly") {
		t.Fatalf("secure session cookie missing hardening flags: %q", cookie)
	}
}

func TestQNAPDesktopOnlyPathsAreBlocked(t *testing.T) {
	for _, path := range []string{
		"/api/v1/menubar",
		"/api/v1/sleep-prevention",
		"/api/v1/network/apply-static",
		"/api/v1/network/restore-dhcp",
		"/api/v1/sources/example/reveal",
	} {
		if !qnapPathBlocked(path) {
			t.Fatalf("expected QNAP path to be blocked: %s", path)
		}
	}
	for _, path := range []string{"/api/v1/config", "/api/v1/network/defaults", "/api/v1/sources", "/api/v1/sources/example/apply"} {
		if qnapPathBlocked(path) {
			t.Fatalf("QNAP path unexpectedly blocked: %s", path)
		}
	}
}

func TestReadyRequiresAuthenticatedControlConfig(t *testing.T) {
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/config" {
			t.Fatalf("readiness requested %q, want /api/v1/config", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer control-secret" {
			t.Fatalf("readiness omitted internal control authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":1}`))
	}))
	defer control.Close()

	s, err := New(Options{ControlToken: "control-secret", AuthDir: t.TempDir(), Upstream: control.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/health/ready", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ready status = %d, body=%s", rec.Code, rec.Body.String())
	}
}
