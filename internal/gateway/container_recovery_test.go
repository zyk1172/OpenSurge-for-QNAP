package gateway

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/runtime"
)

func qnapRecoveryTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Gateway.Mode = config.GatewayModeSameLAN
	cfg.Gateway.Interface = "eth0"
	cfg.Gateway.UpstreamInterface = "eth0"
	cfg.Gateway.LANIP = "192.168.50.2"
	cfg.Gateway.LANPrefixLen = 24
	cfg.Gateway.LANCIDR = "192.168.50.0/24"
	cfg.Gateway.UpstreamGateway = "192.168.50.1"
	cfg.DHCP.Enabled = false
	cfg.DNS.Listen = cfg.Gateway.LANIP
	cfg.Transparent.Mode = config.TransparentModeTUN
	cfg.Runtime.Dir = t.TempDir()
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	return cfg
}

func qnapRecoveryTestManager(t *testing.T, cfg config.Config, backend *fakeBackend, dhcpManager *fakeDHCP, mihomoManager *fakeMihomo) Manager {
	t.Helper()
	paths := runtime.NewPaths(cfg)
	return Manager{cfg: cfg, paths: paths, deps: gatewayDeps{
		geteuid:     func() int { return 0 },
		loadState:   runtime.LoadState,
		saveState:   runtime.SaveState,
		removeState: runtime.RemoveState,
		ensure:      runtime.Ensure,
		newDHCP: func(config.Config, runtime.Paths) dhcpService {
			return dhcpManager
		},
		newMihomo: func(config.Config, runtime.Paths) mihomoService {
			return mihomoManager
		},
		newBackend: func() (platform.NetworkBackend, error) { return backend, nil },
		interfaceByName: func(name string) (*net.Interface, error) {
			return &net.Interface{Name: name}, nil
		},
		currentBoot: func() (runtime.BootSession, error) {
			return runtime.BootSession{ID: "host-boot", NetworkNamespace: "net:[new]"}, nil
		},
		processFingerprint: fakeProcessFingerprint,
		processMatches:     fakeProcessMatches,
		stopPrepared:       func(config.Config) error { return nil },
		now:                time.Now,
	}}
}

func saveInterruptedQNAPRuntime(t *testing.T, cfg config.Config) {
	t.Helper()
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		t.Fatal(err)
	}
	snapshot := platform.NewSnapshot(platform.BackendLinuxNFTables)
	snapshot.NetworkNamespace = "net:[old]"
	snapshot.Routing = &platform.RoutingConfig{
		LANInterface: "eth0", UpstreamInterface: "eth0", LANCIDR: cfg.Gateway.LANCIDR,
		TUNDevice: "tun0", UpstreamGateway: cfg.Gateway.UpstreamGateway,
		TableID: cfg.Transparent.RouteTableID, RulePriority: cfg.Transparent.RouteRulePriority,
		RuleMode: platform.RoutingRuleIngressInterface,
	}
	snapshot.Applied.PolicyRouting = true
	if err := runtime.SaveState(paths.StateFile, runtime.State{
		BootSessionID:            "host-boot",
		PIDMihomo:                4242,
		MihomoProcessFingerprint: "stale-mihomo",
		PIDDNSMasq:               4343,
		DNSMasqProcessFingerprint: "stale-dnsmasq",
		TUNDevice:                "tun0",
		StartedAt:                time.Now().Add(-time.Minute),
		ForwardingApplied:        true,
		RoutingApplied:           true,
		NetworkSnapshot:          snapshot,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverAfterContainerRestartMigratesLegacyRunningStateAndStartsFreshDataPlane(t *testing.T) {
	cfg := qnapRecoveryTestConfig(t)
	saveInterruptedQNAPRuntime(t, cfg)
	backend := &fakeBackend{}
	dhcpManager := &fakeDHCP{startPID: 2002}
	mihomoManager := &fakeMihomo{startPID: 2001}
	manager := qnapRecoveryTestManager(t, cfg, backend, dhcpManager, mihomoManager)

	recovered, err := manager.RecoverAfterContainerRestart(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !recovered {
		t.Fatal("restart recovery reported no data-plane restart")
	}
	if !mihomoManager.startCalled || !dhcpManager.startCalled {
		t.Fatalf("fresh services were not started: mihomo=%v dns=%v", mihomoManager.startCalled, dhcpManager.startCalled)
	}
	if mihomoManager.stopCalled || dhcpManager.stopCalled {
		t.Fatal("restart recovery signalled stale process ids from the previous container")
	}
	if backend.restoreCalls != 1 {
		t.Fatalf("interrupted runtime cleanup calls = %d, want 1", backend.restoreCalls)
	}
	if backend.routingCalls != 1 {
		t.Fatalf("fresh policy-routing setup calls = %d, want 1", backend.routingCalls)
	}

	paths := runtime.NewPaths(cfg)
	state, exists, err := runtime.LoadState(paths.StateFile)
	if err != nil || !exists {
		t.Fatalf("fresh runtime state: exists=%v err=%v", exists, err)
	}
	if state.PIDMihomo != 2001 || state.PIDDNSMasq != 2002 || !state.RoutingApplied {
		t.Fatalf("fresh runtime state = %#v", state)
	}
	desired, exists, err := runtime.LoadGatewayDesiredState(runtime.GatewayDesiredStatePath(paths.Dir))
	if err != nil || !exists || !desired.Running {
		t.Fatalf("migrated desired state = %#v exists=%v err=%v", desired, exists, err)
	}
}

func TestRecoverAfterContainerRestartKeepsExplicitlyStoppedGatewayStopped(t *testing.T) {
	cfg := qnapRecoveryTestConfig(t)
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SaveGatewayDesiredState(runtime.GatewayDesiredStatePath(paths.Dir), false); err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{}
	dhcpManager := &fakeDHCP{startPID: 2002}
	mihomoManager := &fakeMihomo{startPID: 2001}
	manager := qnapRecoveryTestManager(t, cfg, backend, dhcpManager, mihomoManager)

	recovered, err := manager.RecoverAfterContainerRestart(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if recovered || mihomoManager.startCalled || dhcpManager.startCalled || backend.routingCalls != 0 {
		t.Fatalf("explicitly stopped gateway was restarted: recovered=%v mihomo=%v dns=%v routes=%d", recovered, mihomoManager.startCalled, dhcpManager.startCalled, backend.routingCalls)
	}
}

func TestRecoverAfterContainerRestartFailureRetainsRunningIntent(t *testing.T) {
	cfg := qnapRecoveryTestConfig(t)
	saveInterruptedQNAPRuntime(t, cfg)
	backend := &fakeBackend{}
	dhcpManager := &fakeDHCP{startPID: 2002}
	mihomoManager := &fakeMihomo{startErr: errors.New("mihomo cannot start")}
	manager := qnapRecoveryTestManager(t, cfg, backend, dhcpManager, mihomoManager)

	recovered, err := manager.RecoverAfterContainerRestart(context.Background())
	if err == nil || !strings.Contains(err.Error(), "mihomo cannot start") {
		t.Fatalf("recovery error = %v", err)
	}
	if recovered {
		t.Fatal("failed recovery reported success")
	}
	paths := runtime.NewPaths(cfg)
	desired, exists, loadErr := runtime.LoadGatewayDesiredState(runtime.GatewayDesiredStatePath(paths.Dir))
	if loadErr != nil || !exists || !desired.Running {
		t.Fatalf("running intent lost after failed recovery: desired=%#v exists=%v err=%v", desired, exists, loadErr)
	}
	if _, exists, loadErr := runtime.LoadState(paths.StateFile); loadErr != nil || exists {
		t.Fatalf("failed fresh start should have rolled runtime back: exists=%v err=%v", exists, loadErr)
	}
}
