package webgateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPasswordFreeWebAccess(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "existing"}[existing], func(t *testing.T) {
			dir := t.TempDir()
			if existing {
				// Password-free access must not read even an unreadable/corrupt legacy account.
				if err := os.WriteFile(filepath.Join(dir, "admin.json"), []byte("invalid legacy account"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer internal-secret" {
					t.Error("missing internal Control token")
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()
			gateway, err := New(Options{
				Upstream: upstream.URL, ControlToken: "internal-secret", AuthDir: dir,
				DisableAuthentication: true, RequireBootstrapToken: true, QNAPOnly: true, NASPlatform: "synology",
			})
			if err != nil {
				t.Fatal(err)
			}
			handler := gateway.Handler()
			request := func(method, path, origin string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, "http://192.168.2.241:8080"+path, nil)
				if origin != "" {
					r.Header.Set("Origin", origin)
				}
				r.Header.Set("Authorization", "Bearer browser-supplied-token")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Header().Get("Set-Cookie") != "" {
					t.Error("password-free access created a session cookie")
				}
				return w
			}
			for _, path := range []string{"/", "/api/v1/overview"} {
				if w := request(http.MethodGet, path, ""); w.Code != http.StatusOK {
					t.Fatalf("GET %s = %d", path, w.Code)
				}
			}
			if calls != 2 {
				t.Fatalf("proxied requests = %d", calls)
			}
			if w := request(http.MethodGet, "/auth/", ""); w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
				t.Fatalf("legacy login page: %d %s", w.Code, w.Body.String())
			}
			state := request(http.MethodGet, "/api/auth/state", "")
			var flags map[string]bool
			if err := json.Unmarshal(state.Body.Bytes(), &flags); err != nil || state.Code != http.StatusOK {
				t.Fatalf("auth state: %d %s", state.Code, state.Body.String())
			}
			for _, key := range []string{"authentication_required", "setup_required", "bootstrap_required"} {
				if value, present := flags[key]; !present || value {
					t.Errorf("%s must explicitly be false", key)
				}
			}
			for _, path := range []string{"/api/auth/setup", "/api/auth/login", "/api/auth/logout", "/bootstrap", "/api/v1/session/bootstrap", "/api/v1/menubar", "/api/v1/qnap-host-routing"} {
				if w := request(http.MethodPost, path, "http://192.168.2.241:8080"); w.Code != http.StatusNotFound {
					t.Errorf("blocked %s = %d", path, w.Code)
				}
			}
			for _, origin := range []string{"", "http://evil.example"} {
				for _, path := range []string{"/api/v1/gateway/start", "/api/auth/remote-token"} {
					if w := request(http.MethodPost, path, origin); w.Code != http.StatusForbidden {
						t.Errorf("mutation %s with origin %q = %d", path, origin, w.Code)
					}
				}
			}
			if w := request(http.MethodPost, "/api/v1/gateway/start", "http://192.168.2.241:8080"); w.Code != http.StatusOK {
				t.Errorf("same-origin local operation = %d", w.Code)
			}
			if w := request(http.MethodGet, "/api/auth/remote-token", ""); w.Code != http.StatusOK {
				t.Errorf("local token status = %d", w.Code)
			}
			rotation := request(http.MethodPost, "/api/auth/remote-token", "http://192.168.2.241:8080")
			var token struct {
				Token string `json:"token"`
			}
			if err := json.Unmarshal(rotation.Body.Bytes(), &token); err != nil || rotation.Code != http.StatusCreated || token.Token == "" {
				t.Fatalf("local token rotation: %d %s", rotation.Code, rotation.Body.String())
			}
			for _, bearer := range []string{"", "wrong", token.Token} {
				r := httptest.NewRequest(http.MethodGet, "http://192.168.2.241:8080/api/remote/v1/overview", nil)
				if bearer != "" {
					r.Header.Set("Authorization", "Bearer "+bearer)
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				want := http.StatusUnauthorized
				if bearer == token.Token {
					want = http.StatusOK
				}
				if w.Code != want {
					t.Errorf("valid remote token=%t: status %d, want %d", bearer == token.Token, w.Code, want)
				}
			}
			if w := request(http.MethodDelete, "/api/auth/remote-token", "http://192.168.2.241:8080"); w.Code != http.StatusOK || gateway.remoteTokens.Verify(token.Token) {
				t.Error("local token revocation failed")
			}
			badHost := httptest.NewRequest(http.MethodGet, "http://evil.example/", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, badHost)
			if w.Code != http.StatusForbidden {
				t.Error("Host validation was bypassed")
			}
			if _, err := os.Stat(filepath.Join(dir, bootstrapTokenFile)); !os.IsNotExist(err) {
				t.Error("password-free mode generated a bootstrap token")
			}
			if !existing {
				if _, err := os.Stat(filepath.Join(dir, "admin.json")); !os.IsNotExist(err) {
					t.Error("password-free mode created an account")
				}
			}
		})
	}
}
