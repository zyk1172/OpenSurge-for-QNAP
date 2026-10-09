package gateway

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/runtime"
)

func TestStopRetainsRuntimeStateWhenTrackedProcessIdentityChanges(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		t.Fatal(err)
	}
	state := runtime.State{
		PIDDNSMasq:                os.Getpid(),
		DNSMasqProcessFingerprint: "recorded-process-identity",
		BootSessionID:             "test-boot",
		StartedAt:                 time.Now(),
	}
	if err := runtime.SaveState(paths.StateFile, state); err != nil {
		t.Fatal(err)
	}

	dhcpManager := &fakeDHCP{}
	manager := Manager{cfg: cfg, paths: paths, deps: gatewayDeps{
		geteuid:     func() int { return 0 },
		loadState:   runtime.LoadState,
		saveState:   runtime.SaveState,
		removeState: runtime.RemoveState,
		newDHCP:     func(config.Config, runtime.Paths) dhcpService { return dhcpManager },
		newMihomo:   func(config.Config, runtime.Paths) mihomoService { return &fakeMihomo{} },
		newBackend:  func() (platform.NetworkBackend, error) { return &fakeBackend{}, nil },
		currentBoot: func() (runtime.BootSession, error) { return runtime.BootSession{ID: "test-boot"}, nil },
		processMatches: func(int, string) (bool, error) {
			return false, nil
		},
		stopPrepared: func(config.Config) error { return nil },
	}}

	err := manager.Stop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "process identity changed") {
		t.Fatalf("Stop() error = %v, want identity mismatch", err)
	}
	if dhcpManager.stopCalled {
		t.Fatal("Stop() signaled a PID whose process identity did not match")
	}
	retained, exists, err := runtime.LoadState(paths.StateFile)
	if err != nil || !exists {
		t.Fatalf("runtime state after identity mismatch: exists=%v err=%v", exists, err)
	}
	if retained.PIDDNSMasq != state.PIDDNSMasq || retained.DNSMasqProcessFingerprint != state.DNSMasqProcessFingerprint {
		t.Fatalf("runtime state changed after identity mismatch: got=%#v want=%#v", retained, state)
	}
}

func TestStopTrackedProcessAcceptsAnExitedPID(t *testing.T) {
	const missingPID = 2_000_000_000
	stopCalled := false
	err := stopTrackedProcess(gatewayDeps{
		processMatches: func(int, string) (bool, error) { return false, nil },
	}, "dnsmasq", missingPID, "recorded-process-identity", func(int) error {
		stopCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("stopTrackedProcess() for an exited PID = %v", err)
	}
	if stopCalled {
		t.Fatal("stopTrackedProcess() called the stop function for an exited PID")
	}
}
