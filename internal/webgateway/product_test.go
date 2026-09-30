package webgateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNASPlatformIdentityAndHostRoutingBoundary(t *testing.T) {
	for _, platform := range []string{"", "qnap", "synology", "fnos", "generic"} {
		t.Run(platform, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(http.StatusNoContent)
			}))
			defer upstream.Close()
			s, err := New(Options{Upstream: upstream.URL, ControlToken: "internal", AuthDir: t.TempDir(), NASPlatform: platform, QNAPOnly: true, AllowedHosts: []string{"example.com"}})
			if err != nil {
				t.Fatal(err)
			}
			h := s.Handler()
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/product", nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("product status = %d: %s", recorder.Code, recorder.Body.String())
			}
			var metadata struct {
				Platform     string `json:"platform"`
				Name         string `json:"name"`
				Network      string `json:"network_driver"`
				HostTakeover bool   `json:"host_takeover"`
				Experimental bool   `json:"experimental"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &metadata); err != nil {
				t.Fatal(err)
			}
			qnap := platform == "" || platform == "qnap"
			if metadata.HostTakeover != qnap || metadata.Experimental == qnap || (metadata.Network == "qnet") != qnap {
				t.Fatalf("wrong platform capabilities: %+v", metadata)
			}
			if qnap && metadata.Platform != "qnap" || !qnap && metadata.Platform != platform {
				t.Fatalf("wrong platform identity: %+v", metadata)
			}
			recorder = httptest.NewRecorder()
			h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/auth/", nil))
			if !strings.Contains(recorder.Body.String(), metadata.Name) {
				t.Fatal("login page does not use deployment identity")
			}
			cookieRecorder := httptest.NewRecorder()
			s.issueSession(cookieRecorder)
			cookie := cookieRecorder.Result().Cookies()[0]
			remoteToken, _, err := s.remoteTokens.Rotate()
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/api/v1/qnap-host-routing", remoteAPIPrefix + "/qnap-host-routing"} {
				for _, method := range []string{http.MethodGet, http.MethodPut} {
					request := httptest.NewRequest(method, path, nil)
					request.AddCookie(cookie)
					request.Header.Set("Authorization", "Bearer "+remoteToken)
					request.Header.Set("Origin", "http://example.com")
					recorder = httptest.NewRecorder()
					h.ServeHTTP(recorder, request)
					want := http.StatusNotFound
					if qnap {
						want = http.StatusNoContent
					}
					if recorder.Code != want {
						t.Fatalf("%s %s: got %d want %d", method, path, recorder.Code, want)
					}
				}
			}
			if !qnap && calls != 0 {
				t.Fatal("unsupported host routing reached privileged upstream")
			}
		})
	}
}

func TestRejectUnknownNASPlatform(t *testing.T) {
	if _, err := New(Options{NASPlatform: "sy nology"}); err == nil {
		t.Fatal("unknown platform accepted")
	}
}
