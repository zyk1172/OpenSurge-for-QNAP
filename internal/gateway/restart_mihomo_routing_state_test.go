package gateway

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/runtime"
)

func restartRoutingTestState(t *testing.T, cfg config.Config) (runtime.Paths, runtime.State) {
	t.Helper()
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		t.Fatal(err)
	}
	profileDigest, err := config.MihomoProfileDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := platform.NewSnapshot(platform.BackendLinuxNFTables)
	snapshot.Routing = &platform.RoutingConfig{
		LANInterface: "eth0", LANCIDR: "192.168.2.0/24", TUNDevice: "tun0",
		TableID: 20241, RulePriority: 20241, RuleMode: platform.RoutingRuleIngressInterface,
	}
	snapshot.Applied.PolicyRouting = true
	state := runtime.State{
		BootSessionID:            "boot-a",
		PIDMihomo:                12,
		MihomoProcessFingerprint: "pid-12-start",
		PIDDNSMasq:               11,
		TUNDevice:                "tun0",
		ProfileDigest:            profileDigest,
		StartedAt:                time.Now(),
		RoutingApplied:           true,
		NetworkSnapshot:          snapshot,
	}
	if err := runtime.SaveState(paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	return paths, state
}

func TestRestartMihomoFinalStateSaveFailureKeepsTrackedReplacementRecoverable(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	paths, _ := restartRoutingTestState(t, cfg)

	backend := &fakeBackend{}
	mihomoManager := &fakeMihomo{startPID: 34}
	saveCalls := 0
	manager := Manager{cfg: cfg, paths: paths, deps: gatewayDeps{
		geteuid: func() int { return 0 }, loadState: runtime.LoadState,
		saveState: func(path string, state runtime.State) error {
			saveCalls++
			if saveCalls == 3 {
				return errors.New("simulated durable state write failure")
			}
			return runtime.SaveState(path, state)
		},
		newBackend: func() (platform.NetworkBackend, error) { return backend, nil },
		newMihomo:  func(config.Config, runtime.Paths) mihomoService { return mihomoManager },
		currentBoot: func() (runtime.BootSession, error) { return runtime.BootSession{ID: "boot-a"}, nil },
		processFingerprint: fakeProcessFingerprint,
		processMatches:     fakeProcessMatches,
		now:                time.Now,
	}}

	err := manager.RestartMihomo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "save repaired routing state") {
		t.Fatalf("RestartMihomo() error=%v", err)
	}
	if mihomoManager.stoppedPID != 12 {
		t.Fatalf("replacement mihomo was stopped after final state write failure; stopped pid=%d", mihomoManager.stoppedPID)
	}
	persisted, exists, loadErr := runtime.LoadState(paths.StateFile)
	if loadErr != nil || !exists {
		t.Fatalf("persisted state: exists=%v err=%v", exists, loadErr)
	}
	if persisted.PIDMihomo != 34 || persisted.MihomoProcessFingerprint != "pid-34-start" {
		t.Fatalf("replacement process is not durably tracked: %#v", persisted)
	}
	if persisted.RoutingApplied {
		t.Fatalf("failed final state write left routing marked healthy: %#v", persisted)
	}
}

func TestRestartMihomoRoutingRepairFailurePersistsDegradedState(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	paths, _ := restartRoutingTestState(t, cfg)

	backend := &fakeBackend{routingErr: errors.New("simulated routing failure")}
	mihomoManager := &fakeMihomo{startPID: 34}
	manager := Manager{cfg: cfg, paths: paths, deps: gatewayDeps{
		geteuid: func() int { return 0 }, loadState: runtime.LoadState, saveState: runtime.SaveState,
		newBackend: func() (platform.NetworkBackend, error) { return backend, nil },
		newMihomo:  func(config.Config, runtime.Paths) mihomoService { return mihomoManager },
		currentBoot: func() (runtime.BootSession, error) { return runtime.BootSession{ID: "boot-a"}, nil },
		processFingerprint: fakeProcessFingerprint,
		processMatches:     fakeProcessMatches,
		now:                time.Now,
	}}

	err := manager.RestartMihomo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "repair policy routing") {
		t.Fatalf("RestartMihomo() error=%v", err)
	}
	if mihomoManager.stoppedPID != 34 {
		t.Fatalf("replacement mihomo was not stopped after routing repair failure; stopped pid=%d", mihomoManager.stoppedPID)
	}
	persisted, exists, loadErr := runtime.LoadState(paths.StateFile)
	if loadErr != nil || !exists {
		t.Fatalf("persisted state: exists=%v err=%v", exists, loadErr)
	}
	if persisted.PIDMihomo != 0 || persisted.MihomoProcessFingerprint != "" || persisted.RoutingApplied {
		t.Fatalf("routing repair failure was not persisted as degraded: %#v", persisted)
	}
}
