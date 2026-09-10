package webgateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteTokenStorePersistsOnlyHashAndRevokes(t *testing.T) {
	dir := t.TempDir()
	store := NewRemoteTokenStore(dir)
	token, status, err := store.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !status.Enabled || !strings.HasPrefix(token, remoteTokenPrefix) {
		t.Fatalf("unexpected token status: %#v token=%q", status, token)
	}
	if !store.Verify(token) {
		t.Fatal("new token did not verify")
	}
	if store.Verify(token + "wrong") {
		t.Fatal("wrong token verified")
	}

	path := filepath.Join(dir, remoteTokenFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token record: %v", err)
	}
	if bytes.Contains(data, []byte(token)) {
		t.Fatal("remote token was persisted in plaintext")
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatalf("stat token record: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("token record mode = %o, want 600", info.Mode().Perm())
	}

	if err := store.Revoke(); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if store.Verify(token) {
		t.Fatal("revoked token still verifies")
	}
	status, err = store.Status()
	if err != nil {
		t.Fatalf("Status after revoke: %v", err)
	}
	if status.Enabled {
		t.Fatalf("status still enabled after revoke: %#v", status)
	}
}

func TestRemoteManagementTokenProxiesQNAPAPIWithoutBrowserOrigin(t *testing.T) {
	const controlToken = "loopback-control-secret"
	var gotPath, gotAuthorization, gotRemoteMarker string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		gotRemoteMarker = r.Header.Get("X-OpenSurge-Remote-Management")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	gateway, err := New(Options{
		Upstream:     upstream.URL,
		ControlToken: controlToken,
		AuthDir:      t.TempDir(),
		QNAPOnly:     true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.auth.Setup("admin", "correct horse battery staple"); err != nil {
		t.Fatalf("setup admin: %v", err)
	}

	sessionRecorder := httptest.NewRecorder()
	gateway.issueSession(sessionRecorder)
	cookies := sessionRecorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("admin session cookie not issued")
	}

	rotate := httptest.NewRequest(http.MethodPost, "http://192.168.2.241:8080/api/auth/remote-token", nil)
	rotate.Host = "192.168.2.241:8080"
	rotate.Header.Set("Origin", "http://192.168.2.241:8080")
	rotate.AddCookie(cookies[0])
	rotateRecorder := httptest.NewRecorder()
	gateway.Handler().ServeHTTP(rotateRecorder, rotate)
	if rotateRecorder.Code != http.StatusCreated {
		t.Fatalf("rotate status = %d body=%s", rotateRecorder.Code, rotateRecorder.Body.String())
	}
	var rotated struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rotateRecorder.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("decode rotate response: %v", err)
	}
	if !strings.HasPrefix(rotated.Token, remoteTokenPrefix) {
		t.Fatalf("unexpected remote token %q", rotated.Token)
	}

	request := httptest.NewRequest(http.MethodPost, "http://192.168.2.241:8080/api/remote/v1/gateway/start", strings.NewReader(`{}`))
	request.Host = "192.168.2.241:8080"
	request.Header.Set("Authorization", "Bearer "+rotated.Token)
	request.Header.Set("Content-Type", "application/json")
	// Machine clients do not need a browser Origin header.
	recorder := httptest.NewRecorder()
	gateway.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("remote mutation status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if gotPath != "/api/v1/gateway/start" {
		t.Fatalf("upstream path = %q, want /api/v1/gateway/start", gotPath)
	}
	if gotAuthorization != "Bearer "+controlToken {
		t.Fatalf("upstream authorization = %q", gotAuthorization)
	}
	if gotRemoteMarker != "token" {
		t.Fatalf("remote marker = %q", gotRemoteMarker)
	}

	invalid := httptest.NewRequest(http.MethodGet, "http://192.168.2.241:8080/api/remote/v1/overview", nil)
	invalid.Host = "192.168.2.241:8080"
	invalid.Header.Set("Authorization", "Bearer osr_wrong")
	invalidRecorder := httptest.NewRecorder()
	gateway.Handler().ServeHTTP(invalidRecorder, invalid)
	if invalidRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want 401", invalidRecorder.Code)
	}

	blocked := httptest.NewRequest(http.MethodPost, "http://192.168.2.241:8080/api/remote/v1/network/apply-static", nil)
	blocked.Host = "192.168.2.241:8080"
	blocked.Header.Set("Authorization", "Bearer "+rotated.Token)
	blockedRecorder := httptest.NewRecorder()
	gateway.Handler().ServeHTTP(blockedRecorder, blocked)
	if blockedRecorder.Code != http.StatusNotFound {
		t.Fatalf("QNAP-blocked remote path status = %d, want 404", blockedRecorder.Code)
	}

	selfRotate := httptest.NewRequest(http.MethodPost, "http://192.168.2.241:8080/api/auth/remote-token", nil)
	selfRotate.Host = "192.168.2.241:8080"
	selfRotate.Header.Set("Authorization", "Bearer "+rotated.Token)
	selfRotate.Header.Set("Origin", "http://192.168.2.241:8080")
	selfRotateRecorder := httptest.NewRecorder()
	gateway.Handler().ServeHTTP(selfRotateRecorder, selfRotate)
	if selfRotateRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("remote token rotated itself: status=%d", selfRotateRecorder.Code)
	}
}

func TestRemoteCapabilitiesRequireTokenAndDescribeManagementSurface(t *testing.T) {
	gateway, err := New(Options{ControlToken: "control", AuthDir: t.TempDir(), QNAPOnly: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	token, _, err := gateway.remoteTokens.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	unauthorized := httptest.NewRequest(http.MethodGet, "http://192.168.2.241/api/remote/v1/capabilities", nil)
	unauthorized.Host = "192.168.2.241"
	unauthorizedRecorder := httptest.NewRecorder()
	gateway.Handler().ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("capabilities without token status = %d, want 401", unauthorizedRecorder.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "http://192.168.2.241/api/remote/v1/capabilities", nil)
	request.Host = "192.168.2.241"
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	gateway.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{"/gateway/start", "/config", "/sources", "/devices", "/policies", "/doctor"} {
		if !strings.Contains(body, want) {
			t.Fatalf("capabilities missing %q: %s", want, body)
		}
	}
}
