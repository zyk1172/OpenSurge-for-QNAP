package gateway

import (
	"context"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/runtime"
)

func recoveryTestState() runtime.State {
	snapshot := platform.NewSnapshot(platform.BackendLinuxNFTables)
	snapshot.NetworkNamespace = "net:[test]"
	snapshot.IPv4Forwarding = "0"
	snapshot.NFTablesTable = "opensurge"
	snapshot.NAT = &platform.NATConfig{
		LANInterface: "eth0",
		LANCIDR:      "192.168.2.0/24",
		TUNDevice:    "tun0",
		FwMark:       0x29,
		TableName:    "opensurge",
	}
	snapshot.Routing = &platform.RoutingConfig{
		LANInterface: "eth0",
		LANCIDR:      "192.168.2.0/24",
		TUNDevice:    "tun0",
		TableID:      20241,
		RulePriority: 20241,
		FwMark:       0x29,
	}
	snapshot.Applied = platform.AppliedSteps{
		IPv4Forwarding: true,
		NAT:            true,
		PolicyRouting:  true,
	}
	return runtime.State{
		BootSessionID:     "boot-a",
		StartedAt:         time.Now(),
		ForwardingApplied: true,
		NATApplied:        true,
		RoutingApplied:    true,
		NetworkSnapshot:   snapshot,
	}
}

func TestStopWithFreshBackendRestoresPersistedNetworkSnapshot(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	paths := runtime.NewPaths(cfg)
	if err := runtime.SaveState(paths.StateFile, recoveryTestState()); err != nil {
		t.Fatal(err)
	}

	freshBackend := &fakeBackend{}
	manager := Manager{cfg: cfg, paths: paths, deps: gatewayDeps{
		geteuid:     func() int { return 0 },
		loadState:   runtime.LoadState,
		saveState:   runtime.SaveState,
		removeState: runtime.RemoveState,
		newBackend:  func() (platform.NetworkBackend, error) { return freshBackend, nil },
		newDHCP:     func(config.Config, runtime.Paths) dhcpService { return &fakeDHCP{} },
		newMihomo:   func(config.Config, runtime.Paths) mihomoService { return &fakeMihomo{} },
		currentBoot: func() (runtime.BootSession, error) { return runtime.BootSession{ID: "boot-a"}, nil },
		stopPrepared: func(config.Config) error { return nil },
	}}

	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if freshBackend.restoreCalls != 1 {
		t.Fatalf("Restore calls = %d, want 1", freshBackend.restoreCalls)
	}
	if _, exists, err := runtime.LoadState(paths.StateFile); err != nil || exists {
		t.Fatalf("state after Stop: exists=%v err=%v", exists, err)
	}
}

func TestInterruptedRuntimeRestoresNetworkWithoutSignalingStalePIDs(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	paths := runtime.NewPaths(cfg)
	state := recoveryTestState()
	state.PIDDNSMasq = 111
	state.PIDMihomo = 222
	if err := runtime.SaveState(paths.StateFile, state); err != nil {
		t.Fatal(err)
	}

	freshBackend := &fakeBackend{}
	dhcpManager := &fakeDHCP{}
	mihomoManager := &fakeMihomo{}
	manager := Manager{cfg: cfg, paths: paths, deps: gatewayDeps{
		geteuid:     func() int { return 0 },
		loadState:   runtime.LoadState,
		saveState:   runtime.SaveState,
		removeState: runtime.RemoveState,
		newBackend:  func() (platform.NetworkBackend, error) { return freshBackend, nil },
		newDHCP:     func(config.Config, runtime.Paths) dhcpService { return dhcpManager },
		newMihomo:   func(config.Config, runtime.Paths) mihomoService { return mihomoManager },
		currentBoot: func() (runtime.BootSession, error) { return runtime.BootSession{ID: "boot-b"}, nil },
		stopPrepared: func(config.Config) error { return nil },
	}}

	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if freshBackend.restoreCalls != 1 {
		t.Fatalf("Restore calls = %d, want 1", freshBackend.restoreCalls)
	}
	if dhcpManager.stopCalled || mihomoManager.stopCalled {
		t.Fatalf("stale process was signaled: dnsmasq=%v mihomo=%v", dhcpManager.stopCalled, mihomoManager.stopCalled)
	}
	if _, exists, err := runtime.LoadState(paths.StateFile); err != nil || exists {
		t.Fatalf("state after interrupted recovery: exists=%v err=%v", exists, err)
	}
}

func TestContainerNamespaceRestartOnSameHostBootNeverSignalsStalePIDs(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	paths := runtime.NewPaths(cfg)
	state := recoveryTestState()
	state.PIDDNSMasq = 111
	state.PIDMihomo = 222
	if err := runtime.SaveState(paths.StateFile, state); err != nil {
		t.Fatal(err)
	}

	freshBackend := &fakeBackend{}
	dhcpManager := &fakeDHCP{running: true}
	mihomoManager := &fakeMihomo{running: true}
	manager := Manager{cfg: cfg, paths: paths, deps: gatewayDeps{
		geteuid:     func() int { return 0 },
		loadState:   runtime.LoadState,
		saveState:   runtime.SaveState,
		removeState: runtime.RemoveState,
		newBackend:  func() (platform.NetworkBackend, error) { return freshBackend, nil },
		newDHCP:     func(config.Config, runtime.Paths) dhcpService { return dhcpManager },
		newMihomo:   func(config.Config, runtime.Paths) mihomoService { return mihomoManager },
		currentBoot: func() (runtime.BootSession, error) {
			return runtime.BootSession{ID: "boot-a", NetworkNamespace: "net:[replacement-container]"}, nil
		},
		stopPrepared: func(config.Config) error { return nil },
	}}

	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if freshBackend.restoreCalls != 1 {
		t.Fatalf("Restore calls = %d, want 1", freshBackend.restoreCalls)
	}
	if dhcpManager.stopCalled || mihomoManager.stopCalled {
		t.Fatalf("container restart signaled stale PIDs: dnsmasq=%v mihomo=%v", dhcpManager.stopCalled, mihomoManager.stopCalled)
	}
	if _, exists, err := runtime.LoadState(paths.StateFile); err != nil || exists {
		t.Fatalf("state after namespace reconciliation: exists=%v err=%v", exists, err)
	}
}
