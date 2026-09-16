package controlapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/mihomo"
)

func TestProfileOverlayHostsViewRoundTripsNativeWildcards(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store, configPath: filepath.Join(t.TempDir(), "missing-config.yaml")}

	initial := getProfileOverlayHostsForTest(t, server)
	if !initial.UseHosts || !initial.UseSystemHosts {
		t.Fatalf("expected Mihomo default hosts switches to be true: %+v", initial)
	}

	body := `{"enabled":true,"use_hosts":true,"standard_hosts":"0.0.0.0 ads.example.test\n1.1.1.1 exact.example.test","native_hosts_yaml":"'*.example.com': 192.0.2.10\n'+.example.net': target.example\nexact.example.test: 9.9.9.9\n"}`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/profile-overlay?view=hosts", strings.NewReader(body))
	request.Header.Set("If-Match", `"`+initial.Revision+`"`)
	recorder := httptest.NewRecorder()
	server.handleProfileOverlay(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("PUT hosts view status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var updated profileOverlayHostsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if !updated.Enabled || !updated.UseHosts {
		t.Fatalf("updated hosts view lost switches: %+v", updated)
	}
	if !strings.Contains(updated.StandardHosts, "ads.example.test") || !strings.Contains(updated.NativeHostsYAML, "*.example.com") {
		t.Fatalf("focused hosts response lost content: %+v", updated)
	}

	_, document, _, err := server.loadProfileOverlay()
	if err != nil {
		t.Fatal(err)
	}
	composition, err := mihomo.ComposeProfileOverlay([]byte("proxies: []\nrules:\n  - MATCH,DIRECT\n"), document)
	if err != nil {
		t.Fatalf("ComposeProfileOverlay() error = %v", err)
	}
	for _, want := range []string{"hosts:", "*.example.com", "+.example.net", "192.0.2.10", "target.example", "9.9.9.9", "ads.example.test"} {
		if !strings.Contains(composition.ProfileYAML, want) {
			t.Fatalf("composed profile missing %q:\n%s", want, composition.ProfileYAML)
		}
	}
	if strings.Contains(composition.ProfileYAML, "1.1.1.1") {
		t.Fatalf("native exact hosts key did not override conventional entry:\n%s", composition.ProfileYAML)
	}
	if strings.Contains(composition.ProfileYAML, "OPENSURGE NATIVE MIHOMO HOSTS") {
		t.Fatalf("persistence marker leaked into composed profile:\n%s", composition.ProfileYAML)
	}
}

func TestProfileOverlayHostsViewSupportsPartialAgentUpdates(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store, configPath: filepath.Join(t.TempDir(), "missing-config.yaml")}
	initial := getProfileOverlayHostsForTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/profile-overlay?view=hosts", strings.NewReader(`{"native_hosts_yaml":"'*.agent.example': 192.0.2.20"}`))
	request.Header.Set("If-Match", initial.Revision)
	recorder := httptest.NewRecorder()
	server.handleProfileOverlay(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("partial PUT status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var updated profileOverlayHostsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if !updated.UseHosts || !strings.Contains(updated.NativeHostsYAML, "*.agent.example") {
		t.Fatalf("partial update did not enable/persist configured hosts: %+v", updated)
	}

	bad := httptest.NewRequest(http.MethodPut, "/api/v1/profile-overlay?view=hosts", strings.NewReader(`{"native_hosts_yaml":"- not-a-mapping"}`))
	bad.Header.Set("If-Match", updated.Revision)
	badRecorder := httptest.NewRecorder()
	server.handleProfileOverlay(badRecorder, bad)
	if badRecorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid native hosts status = %d body=%s", badRecorder.Code, badRecorder.Body.String())
	}
}

func getProfileOverlayHostsForTest(t *testing.T, server *Server) profileOverlayHostsResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/profile-overlay?view=hosts", nil)
	recorder := httptest.NewRecorder()
	server.handleProfileOverlay(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET hosts view status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response profileOverlayHostsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}
