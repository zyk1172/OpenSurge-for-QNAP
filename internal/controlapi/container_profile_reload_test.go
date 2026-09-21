package controlapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/mihomo"
)

func TestPrepareContainerProfileReconcileRecomposesExternallyChangedOverlay(t *testing.T) {
	configPath, storeDir, sourceDigest := writeContainerProfileReloadFixture(t)

	document := mihomo.DefaultProfileOverlayDocument()
	document.Enabled = true
	document.DNS.Merge["hosts-file"] = "104.18.45.150 ptcafe.club\n104.19.79.151 ptzone.xyz\n"
	overlay, err := mihomo.RenderProfileOverlay(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewStore(storeDir).SaveProfileOverlay(overlay); err != nil {
		t.Fatal(err)
	}

	candidate, err := prepareContainerProfileReconcile(configPath, storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if candidate == nil {
		t.Fatal("changed persisted overlay should produce a reconciliation candidate")
	}
	if candidate.SourceDigest != sourceDigest {
		t.Fatalf("source digest = %q, want %q", candidate.SourceDigest, sourceDigest)
	}
	if candidate.OverlayDigest != mihomo.ProfileOverlayDigest(overlay) {
		t.Fatalf("overlay digest = %q, want %q", candidate.OverlayDigest, mihomo.ProfileOverlayDigest(overlay))
	}
	if candidate.Revision != fileDigest(configPath) {
		t.Fatalf("config revision = %q, want %q", candidate.Revision, fileDigest(configPath))
	}
	text := string(candidate.Payload)
	for _, expected := range []string{"ptcafe.club", "104.18.45.150", "ptzone.xyz", "104.19.79.151"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("composed payload is missing %q:\n%s", expected, text)
		}
	}
}

func TestPrepareContainerProfileReconcileLeavesMatchingEffectiveProfileUnchanged(t *testing.T) {
	configPath, storeDir, sourceDigest := writeContainerProfileReloadFixture(t)
	document := mihomo.DefaultProfileOverlayDocument()
	document.Enabled = true
	document.DNS.Merge["hosts-file"] = "104.18.45.150 ptcafe.club\n"
	overlay, err := mihomo.RenderProfileOverlay(document)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(storeDir)
	if err := store.SaveProfileOverlay(overlay); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	source, actualSourceDigest, err := rawProfileSourceForReconcile(cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	if actualSourceDigest != sourceDigest {
		t.Fatalf("source digest = %q, want %q", actualSourceDigest, sourceDigest)
	}
	composition, err := mihomo.ComposeProfileOverlay(source, document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Mihomo.Profile, []byte(composition.ProfileYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Mihomo.ProfileSourceDigest = sourceDigest
	cfg.Mihomo.ProfileOverlayDigest = mihomo.ProfileOverlayDigest(overlay)
	if err := writeAtomic(configPath, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}

	candidate, err := prepareContainerProfileReconcile(configPath, storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if candidate != nil {
		t.Fatalf("matching effective profile should not be recomposed: %#v", candidate)
	}
}

func TestPrepareContainerProfileReconcileRepairsMatchingMetadataWithStaleProfile(t *testing.T) {
	configPath, storeDir, _ := writeContainerProfileReloadFixture(t)
	document := mihomo.DefaultProfileOverlayDocument()
	document.Enabled = true
	document.DNS.Merge["hosts-file"] = "104.18.45.150 ptcafe.club\n"
	overlay, err := mihomo.RenderProfileOverlay(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewStore(storeDir).SaveProfileOverlay(overlay); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate metadata drift: the config claims the newest overlay revision was
	// applied while the effective profile file still contains the old source.
	cfg.Mihomo.ProfileOverlayDigest = mihomo.ProfileOverlayDigest(overlay)
	if err := writeAtomic(configPath, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}

	candidate, err := prepareContainerProfileReconcile(configPath, storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if candidate == nil {
		t.Fatal("stale effective profile must be reconciled even when metadata matches")
	}
	if !strings.Contains(string(candidate.Payload), "ptcafe.club") {
		t.Fatalf("reconciled payload is missing latest Hosts entry:\n%s", candidate.Payload)
	}
}

func TestPrepareContainerProfileReconcileFailsClosedWithoutRawSource(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config", "opensurge.yaml")
	storeDir := filepath.Join(dir, "control")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(storeDir).Ensure(); err != nil {
		t.Fatal(err)
	}

	effective := []byte("rules:\n  - MATCH,DIRECT\n")
	effectivePath := filepath.Join(dir, "data", "effective.yaml")
	if err := os.MkdirAll(filepath.Dir(effectivePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(effectivePath, effective, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Runtime.Dir = filepath.Join(dir, "runtime")
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = effectivePath
	cfg.Mihomo.ProfileSourceDigest = strings.Repeat("a", 64)
	cfg.Mihomo.ProfileOverlayDigest = strings.Repeat("b", 64)
	if err := writeAtomic(configPath, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}

	document := mihomo.DefaultProfileOverlayDocument()
	document.Enabled = true
	document.DNS.Merge["hosts-file"] = "104.18.238.126 pt.xingyungept.org\n"
	overlay, err := mihomo.RenderProfileOverlay(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewStore(storeDir).SaveProfileOverlay(overlay); err != nil {
		t.Fatal(err)
	}

	candidate, err := prepareContainerProfileReconcile(configPath, storeDir)
	if err == nil || candidate != nil || !strings.Contains(err.Error(), "raw source") {
		t.Fatalf("expected fail-closed missing source error, candidate=%#v err=%v", candidate, err)
	}
}

func TestPrepareContainerProfileReconcileRejectsInvalidExternalOverlay(t *testing.T) {
	configPath, storeDir, _ := writeContainerProfileReloadFixture(t)
	if err := os.WriteFile(filepath.Join(storeDir, "global-profile-overlay.yaml"), []byte("enabled: true\ndns: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate, err := prepareContainerProfileReconcile(configPath, storeDir)
	if err == nil || candidate != nil || !strings.Contains(err.Error(), "stored global profile overlay is invalid") {
		t.Fatalf("invalid external overlay should block lifecycle reconciliation, candidate=%#v err=%v", candidate, err)
	}
}

func writeContainerProfileReloadFixture(t *testing.T) (configPath, storeDir, sourceDigest string) {
	t.Helper()
	dir := t.TempDir()
	configPath = filepath.Join(dir, "config", "opensurge.yaml")
	storeDir = filepath.Join(dir, "control")
	store := NewStore(storeDir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}

	sourceData := []byte("rules:\n  - MATCH,DIRECT\n")
	sourceDigest = mihomo.ProfileOverlayDigest(sourceData)
	sourceID := "0123456789abcdef"
	sourceDir := filepath.Join(storeDir, "sources", sourceID)
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, sourceDigest+".yaml")
	if err := os.WriteFile(sourcePath, sourceData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSources([]Source{{
		SchemaVersion: SchemaVersion,
		ID:            sourceID,
		Name:          "fixture",
		Kind:          "mihomo_profile",
		SnapshotPath:  sourcePath,
		Digest:        sourceDigest,
		Valid:         true,
	}}); err != nil {
		t.Fatal(err)
	}

	effectivePath := filepath.Join(dir, "data", "effective.yaml")
	if err := os.MkdirAll(filepath.Dir(effectivePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(effectivePath, sourceData, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Runtime.Dir = filepath.Join(dir, "runtime")
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = effectivePath
	cfg.Mihomo.ProfileSourceDigest = sourceDigest
	cfg.Mihomo.ProfileOverlayDigest = strings.Repeat("f", 64)
	if err := writeAtomic(configPath, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath, storeDir, sourceDigest
}


func TestPrepareContainerProfileReconcileAppliesHostHostsOutsideDisabledOverlay(t *testing.T) {
	configPath, storeDir, _ := writeContainerProfileReloadFixture(t)
	store := NewStore(storeDir)

	document := mihomo.DefaultProfileOverlayDocument()
	document.Enabled = false
	document.DNS.Merge["hosts-file"] = "1.1.1.1 manual-only.example\n\n" + hostHostsBegin + "\n192.0.2.9 stale-host.example\n" + hostHostsEnd
	overlay, err := mihomo.RenderProfileOverlay(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProfileOverlay(overlay); err != nil {
		t.Fatal(err)
	}

	settings := defaultHostHostsSyncSettings()
	settings.Enabled = true
	settings.Path = filepath.Join(t.TempDir(), "mapped-hosts")
	if err := store.SaveHostHostsSyncSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHostHostsManaged([]byte("192.0.2.20 nas-host.example\n192.0.2.30 cf-host.example")); err != nil {
		t.Fatal(err)
	}
	state := []byte(`{"schema_version":1,"results":[{"domain":"cf-host.example","selected":{"ip":"104.18.1.2"}}]}`)
	if err := os.WriteFile(filepath.Join(storeDir, "cloudflare-optimizer-state.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}

	candidate, err := prepareContainerProfileReconcile(configPath, storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if candidate == nil {
		t.Fatal("enabled QNAP host Hosts must produce a reconciliation candidate even when the manual overlay is disabled")
	}
	text := string(candidate.Payload)
	for _, want := range []string{"nas-host.example", "192.0.2.20", "cf-host.example", "104.18.1.2", "use-hosts: true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("final profile missing %q:\n%s", want, text)
		}
	}
	for _, stale := range []string{"manual-only.example", "stale-host.example", "192.0.2.30"} {
		if strings.Contains(text, stale) {
			t.Fatalf("final profile retained lower-priority/stale host %q:\n%s", stale, text)
		}
	}
}
