package controlapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/mihomo"
	"open-mihomo-gateway/internal/runtime"
)

// ApplyProfile is the boundary that would execute stop/start in production.
// Persist its result here so repeated polling can prove exactly one apply.
type hostImportRunner struct {
	fakeConfigurationRunner
	calls int
	err   error
}

func (r *hostImportRunner) ApplyProfile(_ context.Context, path, _ string, payload []byte, source, overlay string) (ProfileApplyResult, error) {
	r.calls++
	if r.err != nil {
		return ProfileApplyResult{}, r.err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return ProfileApplyResult{}, err
	}
	if err := os.WriteFile(cfg.Mihomo.Profile, payload, 0o600); err != nil {
		return ProfileApplyResult{}, err
	}
	cfg.Mihomo.ProfileSourceDigest, cfg.Mihomo.ProfileOverlayDigest = source, overlay
	if err := writeAtomic(path, []byte(config.Render(cfg)), 0o600); err != nil {
		return ProfileApplyResult{}, err
	}
	return ProfileApplyResult{Revision: fileDigest(path), Reloaded: true}, nil
}

func hostImportFixture(t *testing.T, hosts string, optimizer bool) (*Server, hostHostsSyncSettings, *hostImportRunner) {
	t.Helper()
	path, dir, _ := writeContainerProfileReloadFixture(t)
	store := NewStore(dir)
	file := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(file, []byte(hosts), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := defaultHostHostsSyncSettings()
	settings.Enabled = true
	settings.AutoUpdate = true
	settings.Path = file
	if err := store.SaveHostHostsSyncSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHostHostsManaged([]byte(hosts)); err != nil {
		t.Fatal(err)
	}
	if optimizer {
		state := []byte(`{"schema_version":1,"results":[{"domain":"nas.local","selected":{"ip":"104.18.1.2"}}]}`)
		if err := os.WriteFile(filepath.Join(dir, "cloudflare-optimizer-state.json"), state, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	candidate, err := prepareContainerProfileReconcile(path, dir)
	if err != nil || candidate == nil {
		t.Fatalf("fixture candidate=%+v %v", candidate, err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(cfg.Mihomo.Profile, candidate.Payload, 0o600)
	cfg.Mihomo.ProfileSourceDigest, cfg.Mihomo.ProfileOverlayDigest = candidate.SourceDigest, candidate.OverlayDigest
	if err := writeAtomic(path, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Runtime.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SaveState(runtime.NewPaths(cfg).StateFile, runtime.State{Lifecycle: "active", PIDMihomo: 4242, PIDSmartDNS: 4243, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	runner := &hostImportRunner{}
	return &Server{store: store, configPath: path, configRunner: runner}, settings, runner
}

func TestAutomaticHostImportNoChangesNeverAppliesProfile(t *testing.T) {
	for _, kind := range []string{"unchanged", "mtime", "comments-whitespace-order", "unrelated-rules", "unrelated-dns", "metadata-only"} {
		t.Run(kind, func(t *testing.T) {
			s, settings, runner := hostImportFixture(t, "192.0.2.10 nas.local\n192.0.2.20 second.local\n", false)
			switch kind {
			case "mtime":
				os.Chtimes(settings.Path, time.Now().Add(time.Hour), time.Now().Add(time.Hour))
			case "comments-whitespace-order":
				os.WriteFile(settings.Path, []byte("# updated timestamp\n192.0.2.20\tsecond.local\n192.0.2.10  nas.local # comment\n"), 0o600)
			case "unrelated-rules", "unrelated-dns":
				doc := mihomo.DefaultProfileOverlayDocument()
				doc.Enabled = true
				if kind == "unrelated-rules" {
					doc.Rules.Prepend = []string{"DOMAIN,pending.example,DIRECT"}
				} else {
					doc.DNS.Merge["nameserver"] = []any{"1.1.1.1"}
				}
				data, err := mihomo.RenderProfileOverlay(doc)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.store.SaveProfileOverlay(data); err != nil {
					t.Fatal(err)
				}
			case "metadata-only":
				cfg, _ := config.Load(s.configPath)
				cfg.Mihomo.ProfileOverlayDigest = strings.Repeat("a", 64)
				writeAtomic(s.configPath, []byte(config.Render(cfg)), 0o600)
			}
			if kind == "unrelated-rules" || kind == "unrelated-dns" || kind == "metadata-only" {
				candidate, err := prepareContainerProfileReconcile(s.configPath, s.store.Dir())
				if err != nil || candidate == nil {
					t.Fatal("fixture did not reproduce a broad reconciliation candidate", err)
				}
			}
			cfg, _ := config.Load(s.configPath)
			statePath := runtime.NewPaths(cfg).StateFile
			beforeState, _ := os.ReadFile(statePath)
			beforeConfig, _ := os.ReadFile(s.configPath)
			beforeProfile, _ := os.ReadFile(cfg.Mihomo.Profile)
			for i := 0; i < 4; i++ {
				var err error
				settings, err = s.syncHostHosts(t.Context(), settings, false)
				if err != nil {
					t.Fatal(err)
				}
			}
			if runner.calls != 0 {
				t.Fatalf("unchanged hosts caused %d ApplyProfile calls", runner.calls)
			}
			ops, err := s.store.Operations(20)
			if err != nil || len(ops) != 0 {
				t.Fatalf("no-op created lifecycle operations: %+v %v", ops, err)
			}
			afterState, _ := os.ReadFile(statePath)
			afterConfig, _ := os.ReadFile(s.configPath)
			afterProfile, _ := os.ReadFile(cfg.Mihomo.Profile)
			if !bytes.Equal(beforeState, afterState) || !bytes.Equal(beforeConfig, afterConfig) || !bytes.Equal(beforeProfile, afterProfile) {
				t.Fatal("no-op changed runtime, config or effective profile")
			}
			if settings.LastSyncAt == "" || settings.LastError != "" {
				t.Fatalf("no-op was not recorded as a successful check: %+v", settings)
			}
		})
	}
}

func TestAutomaticHostImportEffectiveChangeAppliesOnce(t *testing.T) {
	for _, next := range []string{"192.0.2.11 nas.local\n", "192.0.2.10 nas.local\n192.0.2.20 added.local\n", "# removed all managed hosts\n"} {
		t.Run(next, func(t *testing.T) {
			s, settings, runner := hostImportFixture(t, "192.0.2.10 nas.local\n", false)
			os.WriteFile(settings.Path, []byte(next), 0o600)
			// Simulate legacy success metadata/snapshot with an unapplied profile.
			s.store.SaveHostHostsManaged([]byte(normalizeManagedHostHosts(next)))
			sum := sha256.Sum256([]byte(normalizeManagedHostHosts(next)))
			settings.LastDigest = hex.EncodeToString(sum[:])
			s.store.SaveHostHostsSyncSettings(settings)
			for i := 0; i < 4; i++ {
				var err error
				settings, err = s.syncHostHosts(t.Context(), settings, false)
				if err != nil {
					t.Fatal(err)
				}
			}
			if runner.calls != 1 {
				t.Fatalf("wanted one apply, got %d", runner.calls)
			}
			assertHostHostsOperation(t, s.store, "succeeded", "")
		})
	}
}

func TestAutomaticHostImportShadowedChangeDoesNotReload(t *testing.T) {
	s, settings, runner := hostImportFixture(t, "192.0.2.10 nas.local\n", true)
	os.WriteFile(settings.Path, []byte("192.0.2.99 nas.local\n"), 0o600)
	if _, err := s.syncHostHosts(t.Context(), settings, false); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 0 {
		t.Fatal("optimizer-shadowed hosts change reloaded gateway")
	}
}

func TestAutomaticHostImportFailedApplyIsRetriedWithoutAdvancingDigest(t *testing.T) {
	s, settings, runner := hostImportFixture(t, "192.0.2.10 nas.local\n", false)
	os.WriteFile(settings.Path, []byte("192.0.2.11 nas.local\n"), 0o600)
	runner.err = errors.New("injected failure")
	updated, err := s.syncHostHosts(t.Context(), settings, false)
	if err == nil || updated.LastDigest != settings.LastDigest {
		t.Fatal("failed apply advanced success metadata")
	}
	runner.err = nil
	if _, err := s.syncHostHosts(t.Context(), updated, false); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 2 {
		t.Fatalf("failed apply was not retried: %d", runner.calls)
	}
}

func TestConcurrentManualAndAutomaticHostImportApplyOnce(t *testing.T) {
	s, settings, runner := hostImportFixture(t, "192.0.2.10 nas.local\n", false)
	if err := os.WriteFile(settings.Path, []byte("192.0.2.11 nas.local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func(force bool) {
			defer group.Done()
			_, err := s.syncHostHosts(t.Context(), settings, force)
			errors <- err
		}(i%2 == 0)
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if runner.calls != 1 {
		t.Fatalf("concurrent polling caused %d profile applies", runner.calls)
	}
	assertHostHostsOperation(t, s.store, "succeeded", "")
}
