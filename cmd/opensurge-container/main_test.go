package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewWebAuthenticationModes(t *testing.T) {
	t.Setenv("OPENSURGE_NAS_PLATFORM", "qnap")
	for _, enabled := range []bool{false, true} {
		server, err := newWeb("", "127.0.0.1:61767", "internal-token", t.TempDir(), "", enabled, true, false, true)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://192.168.2.241:8080/auth/", nil))
		want := http.StatusFound
		if enabled {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Errorf("web-auth=%t: /auth/ status %d, want %d", enabled, w.Code, want)
		}
	}
}
