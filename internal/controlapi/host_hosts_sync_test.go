package controlapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/mihomo"
)

func TestHostHostsSyncPersistsManagedLayerAndAppliesEffectiveProfile(t *testing.T) {
	configPath, storeDir, _ := writeContainerProfileReloadFixture(t)
	store := NewStore(storeDir)
	hostFile := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostFile, []byte("192.0.2.10 nas.local\n0.0.0.0 ads.local\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	document := mihomo.DefaultProfileOverlayDocument()
	document.Enabled = false
	document.DNS.Merge["hosts-file"] = "1.1.1.1 manual.local\n\n" + hostHostsBegin + "\n192.0.2.99 legacy.local\n" + hostHostsEnd
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
	if err := store.SaveHostHostsSyncSettings(settings); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingConfigurationRunner{}
	server := &Server{store: store, configPath: configPath, configRunner: recorder}

	updated, err := server.syncHostHosts(context.Background(), settings, true)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastEntries != 2 || updated.LastDigest == "" || updated.LastSyncAt == "" {
		t.Fatalf("sync metadata=%+v", updated)
	}
	managed, err := store.HostHostsManaged()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(managed), "nas.local") || !strings.Contains(string(managed), "ads.local") {
		t.Fatalf("managed snapshot=%q", managed)
	}
	payload := string(recorder.profilePayload)
	for _, want := range []string{"nas.local", "192.0.2.10", "ads.local", "0.0.0.0", "use-hosts: true"} {
		if !strings.Contains(payload, want) {
			t.Fatalf("applied profile missing %q:\n%s", want, payload)
		}
	}
	if strings.Contains(payload, "manual.local") {
		t.Fatalf("disabled manual overlay leaked into effective profile:\n%s", payload)
	}

	raw, _, _, err := server.loadProfileOverlay()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "manual.local") || strings.Contains(string(raw), hostHostsBegin) || strings.Contains(string(raw), "legacy.local") || strings.Contains(string(raw), "nas.local") {
		t.Fatalf("managed QNAP hosts must stay independent from the manual overlay and migrate legacy blocks:\n%s", raw)
	}
}

func TestHostHostsSyncDoesNotAdvanceSuccessMetadataWhenApplyFails(t *testing.T) {
	configPath, storeDir, _ := writeContainerProfileReloadFixture(t)
	store := NewStore(storeDir)
	hostFile := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostFile, []byte("192.0.2.20 new.local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHostHostsManaged([]byte("192.0.2.10 old.local")); err != nil {
		t.Fatal(err)
	}
	settings := defaultHostHostsSyncSettings()
	settings.Enabled = true
	settings.Path = hostFile
	settings.LastDigest = strings.Repeat("a", 64)
	settings.LastSyncAt = "2026-09-20T01:02:03Z"
	settings.LastEntries = 1
	if err := store.SaveHostHostsSyncSettings(settings); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: store, configPath: configPath, configRunner: fakeConfigurationRunner{profileErr: errors.New("reload failed")}}
	updated, err := server.syncHostHosts(context.Background(), settings, false)
	if err == nil {
		t.Fatal("expected failed profile apply")
	}
	if updated.LastDigest != settings.LastDigest || updated.LastSyncAt != settings.LastSyncAt || updated.LastEntries != settings.LastEntries {
		t.Fatalf("failed apply advanced success metadata: before=%+v after=%+v", settings, updated)
	}
	if updated.LastError == "" {
		t.Fatal("failed apply must record last_error")
	}
	managed, err := store.HostHostsManaged()
	if err != nil {
		t.Fatal(err)
	}
	if string(managed) != "192.0.2.10 old.local" {
		t.Fatalf("failed apply did not restore prior managed snapshot: %q", managed)
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


func TestHostHostsSyncAPIUpdatesPartialSettings(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store}

	request := httptest.NewRequest(http.MethodPut, "/api/v1/host-hosts-sync", strings.NewReader(`{"auto_update":true,"interval_minutes":60}`))
	response := httptest.NewRecorder()
	server.handleHostHostsSync(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	settings, err := store.HostHostsSyncSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.AutoUpdate || settings.IntervalMinutes != 60 {
		t.Fatalf("settings=%+v", settings)
	}
}


func TestNormalizeManagedHostHosts(t *testing.T) {
	input := "162.159.4.181\t\twiki.m-team.cc\n172.64.155.48        tracker.m-team.cc\n172.64.155.48\ttracker.m-team.io   # tracker\n\n# kept comment\n"
	got := normalizeManagedHostHosts(input)
	want := "162.159.4.181 wiki.m-team.cc\n172.64.155.48 tracker.m-team.cc\n172.64.155.48 tracker.m-team.io # tracker\n# kept comment"
	if got != want {
		t.Fatalf("normalized hosts mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}
