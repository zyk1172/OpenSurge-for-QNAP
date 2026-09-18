package controlapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/mihomo"
)

func TestHostHostsSyncPreservesManualHostsAndReplacesManagedBlock(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}

	hostFile := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostFile, []byte("192.0.2.10 nas.local\n0.0.0.0 ads.local\n"), 0600); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: store, configPath: filepath.Join(t.TempDir(), "missing.yaml")}
	_, document, _, err := server.loadProfileOverlay()
	if err != nil {
		t.Fatal(err)
	}
	document.DNS.Merge["hosts-file"] = "1.1.1.1 manual.local"
	data, err := mihomo.RenderProfileOverlay(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProfileOverlay(data); err != nil {
		t.Fatal(err)
	}

	settings := defaultHostHostsSyncSettings()
	settings.Enabled = true
	settings.Path = hostFile
	updated, err := server.syncHostHosts(settings, true)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastEntries != 2 {
		t.Fatalf("entries=%d", updated.LastEntries)
	}

	raw, _, _, err := server.loadProfileOverlay()
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"manual.local", "nas.local", "ads.local", hostHostsBegin, hostHostsEnd} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}

	if err := os.WriteFile(hostFile, []byte("192.0.2.11 nas.local\n"), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err = server.syncHostHosts(updated, true)
	if err != nil {
		t.Fatal(err)
	}

	raw, _, _, err = server.loadProfileOverlay()
	if err != nil {
		t.Fatal(err)
	}
	text = string(raw)
	if strings.Contains(text, "ads.local") {
		t.Fatalf("stale managed host remained: %s", text)
	}
	if !strings.Contains(text, "manual.local") || !strings.Contains(text, "192.0.2.11") {
		t.Fatalf("manual or refreshed host missing: %s", text)
	}
}

func TestHostHostsSyncAPIRejectsTooShortInterval(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store}

	request := httptest.NewRequest(http.MethodPut, "/api/v1/host-hosts-sync", strings.NewReader(`{"interval_minutes":1}`))
	response := httptest.NewRecorder()
	server.handleHostHostsSync(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHostHostsSyncStatusReportsMappedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte("127.0.0.1 localhost\n"), 0600); err != nil {
		t.Fatal(err)
	}

	settings := defaultHostHostsSyncSettings()
	settings.Path = path
	view := hostHostsSyncStatus(settings)
	if !view.Mapped {
		t.Fatalf("expected mapped file, mount_error=%q", view.MountError)
	}

	settings.Path = filepath.Join(t.TempDir(), "missing")
	view = hostHostsSyncStatus(settings)
	if view.Mapped || view.MountError == "" {
		t.Fatalf("expected missing mount status, got mapped=%v error=%q", view.Mapped, view.MountError)
	}
}
