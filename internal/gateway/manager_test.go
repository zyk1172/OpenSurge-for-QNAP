package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/device"
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/runtime"
)

func TestWarmManagedTailscaleOnlyWhenEnabled(t *testing.T) {
	calls := 0
	deps := gatewayDeps{warmTailscale: func(context.Context, config.Config) error {
		calls++
		return nil
	}}
	manager := Manager{cfg: config.Default()}
	manager.warmManagedTailscale(context.Background(), deps)
	if calls != 0 {
		t.Fatalf("disabled Tailscale warm-up calls = %d", calls)
	}
	manager.cfg.Tailscale.Enabled = true
	manager.warmManagedTailscale(context.Background(), deps)
	if calls != 1 {
		t.Fatalf("enabled Tailscale warm-up calls = %d", calls)
	}
}

func TestManagedTailscaleWarmupReportsDispatchNotReachability(t *testing.T) {
	for _, dispatchErr := range []error{nil, errors.New("controller unavailable")} {
		cfg := config.Default()
		cfg.Tailscale.Enabled = true
		cfg.Tailscale.ExitNode = "100.90.3.4"
		var progress []Progress
		ctx := WithProgress(context.Background(), func(p Progress) { progress = append(progress, p) })
		Manager{cfg: cfg}.warmManagedTailscale(ctx, gatewayDeps{warmTailscale: func(context.Context, config.Config) error { return dispatchErr }})
		wantNotice := "tailscale_warmup_started"
		if dispatchErr != nil {
			wantNotice = "tailscale_warmup_unavailable"
		}
		if !slices.Equal(progress, []Progress{{Phase: "initiating_tailscale"}, {Notice: wantNotice}}) {
			t.Fatalf("warm-up progress = %+v", progress)
		}
	}
}

func TestPreflightRejectsSameGatewayAndUpstreamInterface(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Interface = "en0"
	cfg.Gateway.UpstreamInterface = " en0 "
	manager := Manager{cfg: cfg, paths: runtime.NewPaths(cfg), deps: defaultGatewayDeps()}

	err := manager.preflight(context.Background(), &fakeBackend{}, &fakeDHCP{}, &fakeMihomo{}, manager.deps)
	if err == nil {
		t.Fatalf("preflight() succeeded")
	}
	if !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("preflight() error = %q", err)
	}
}

func TestPreflightAcceptsSameInterfaceInSameLANMode(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Mode = config.GatewayModeSameLAN
	cfg.Gateway.Interface = "en0"
	cfg.Gateway.UpstreamInterface = " en0 "
	cfg.Gateway.LANIP = "192.168.1.20"
	cfg.DHCP.Enabled = false
	cfg.Transparent.Mode = config.TransparentModeTUN
	manager := Manager{
		cfg:   cfg,
		paths: runtime.NewPaths(cfg),
		deps: gatewayDeps{
			interfaceByName: func(name string) (*net.Interface, error) {
				return &net.Interface{Name: strings.TrimSpace(name)}, nil
			},
			interfaces: func() ([]net.Interface, error) {
				return []net.Interface{{Name: "en0"}}, nil
			},
			interfaceAddrs: func(iface *net.Interface) ([]net.Addr, error) {
				return []net.Addr{&net.IPNet{
					IP:   net.ParseIP(cfg.Gateway.LANIP),
					Mask: net.CIDRMask(24, 32),
				}}, nil
			},
		},
	}

	err := manager.preflight(context.Background(), &fakeBackend{}, &fakeDHCP{}, &fakeMihomo{}, manager.deps)
	if err != nil {
		t.Fatalf("preflight() error = %v", err)
	}
}

func TestPreflightAcceptsSameInterfaceInSameWiFiDHCPMode(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Mode = config.GatewayModeSameWiFiDHCP
	cfg.Gateway.Interface = "en0"
	cfg.Gateway.UpstreamInterface = " en0 "
	cfg.Gateway.LANIP = "192.168.1.20"
	cfg.DHCP.Enabled = true
	cfg.DHCP.RangeStart = "192.168.1.120"
	cfg.DHCP.RangeEnd = "192.168.1.199"
	cfg.Transparent.Mode = config.TransparentModeTUN
	manager := Manager{
		cfg:   cfg,
		paths: runtime.NewPaths(cfg),
		deps: gatewayDeps{
			interfaceByName: func(name string) (*net.Interface, error) {
				return &net.Interface{Name: strings.TrimSpace(name)}, nil
			},
			interfaces: func() ([]net.Interface, error) {
				return []net.Interface{{Name: "en0"}}, nil
			},
			interfaceAddrs: func(iface *net.Interface) ([]net.Addr, error) {
				return []net.Addr{&net.IPNet{
					IP:   net.ParseIP(cfg.Gateway.LANIP),
					Mask: net.CIDRMask(24, 32),
				}}, nil
			},
		},
	}

	err := manager.preflight(context.Background(), &fakeBackend{}, &fakeDHCP{}, &fakeMihomo{}, manager.deps)
	if err != nil {
		t.Fatalf("preflight() error = %v", err)
	}
}

func TestPreflightRejectsDifferentInterfacesInSameLANMode(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Mode = config.GatewayModeSameLAN
	cfg.Gateway.Interface = "en0"
	cfg.Gateway.UpstreamInterface = "en7"
	cfg.DHCP.Enabled = false
	cfg.Transparent.Mode = config.TransparentModeTUN
	manager := Manager{cfg: cfg, paths: runtime.NewPaths(cfg), deps: defaultGatewayDeps()}

	err := manager.preflight(context.Background(), &fakeBackend{}, &fakeDHCP{}, &fakeMihomo{}, manager.deps)
	if err == nil {
		t.Fatalf("preflight() succeeded")
	}
	if !strings.Contains(err.Error(), "same_lan requires gateway and upstream interfaces to match") {
		t.Fatalf("preflight() error = %q", err)
	}
}

func TestCheckReservationConflictsRejectsObservedDifferentMACInSameWiFiDHCP(t *testing.T) {
	bundle, err := device.CompilePolicyBundle(device.PolicySet{
		Profiles: []device.Profile{{ID: "home", DefaultPolicies: []string{"DIRECT"}}},
		Devices:  []device.ManagedDevice{{ID: "phone", MAC: "aa:bb:cc:dd:ee:01", IPv4: "192.168.1.101", Profile: "home"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Gateway.Mode = config.GatewayModeSameWiFiDHCP
	cfg.DevicePolicy.Bundle = &bundle
	manager := Manager{cfg: cfg, deps: gatewayDeps{
		probeReservationIP: func(ip, expectedMAC string) error {
			if ip != "192.168.1.101" || expectedMAC != "aa:bb:cc:dd:ee:01" {
				t.Fatalf("probe args = %q/%q", ip, expectedMAC)
			}
			return errors.New("reserved IPv4 already present")
		},
	}}
	if err := manager.checkReservationConflicts(manager.deps); err == nil || !strings.Contains(err.Error(), "already present") {
		t.Fatalf("checkReservationConflicts() error = %v", err)
	}
}

func TestRestartMihomoRejectsImportedProfileDrift(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = filepath.Join(cfg.Runtime.Dir, "imported.yaml")
	if err := os.WriteFile(cfg.Mihomo.Profile, []byte("proxies: []\nproxy-groups: []\nrules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SaveState(paths.StateFile, runtime.State{PIDDNSMasq: 11, PIDMihomo: 12, ProfileDigest: "older-applied-digest", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	mihomoManager := &fakeMihomo{}
	manager := Manager{cfg: cfg, paths: paths, deps: gatewayDeps{
		geteuid: func() int { return 0 }, loadState: runtime.LoadState, saveState: runtime.SaveState,
		newMihomo: func(config.Config, runtime.Paths) mihomoService { return mihomoManager }, now: time.Now,
	}}

	err := manager.RestartMihomo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "differs from the applied runtime") {
		t.Fatalf("RestartMihomo() error=%v", err)
	}
	if mihomoManager.stopCalled || mihomoManager.startCalled {
		t.Fatal("restart touched mihomo while desired imported profile was not applied")
	}
}

func TestRestartMihomoStopFailureRestoresLivePID(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SaveState(paths.StateFile, runtime.State{PIDDNSMasq: 11, PIDMihomo: 12, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	mihomoManager := &fakeMihomo{stopErr: errors.New("old process is busy"), running: true}
	manager := Manager{cfg: cfg, paths: paths, deps: gatewayDeps{
		geteuid: func() int { return 0 }, loadState: runtime.LoadState, saveState: runtime.SaveState,
		newMihomo: func(config.Config, runtime.Paths) mihomoService { return mihomoManager }, now: time.Now,
	}}

	err := manager.RestartMihomo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "old process is busy") {
		t.Fatalf("RestartMihomo() error=%v", err)
	}
	state, exists, loadErr := runtime.LoadState(paths.StateFile)
	if loadErr != nil || !exists || state.PIDMihomo != 12 || state.PIDDNSMasq != 11 {
		t.Fatalf("restored runtime state=%#v exists=%v err=%v", state, exists, loadErr)
	}
	if mihomoManager.startCalled {
		t.Fatal("replacement started while the old process was still alive")
	}
}

func TestRestartMihomoRejectsPreviousBootRuntimeBeforeProcessValidation(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SaveState(paths.StateFile, runtime.State{PIDMihomo: 12, BootSessionID: "previous-boot", StartedAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	mihomoManager := &fakeMihomo{}
	manager := Manager{cfg: cfg, paths: paths, deps: gatewayDeps{
		geteuid: func() int { return 0 }, loadState: runtime.LoadState,
		newMihomo:   func(config.Config, runtime.Paths) mihomoService { return mihomoManager },
		currentBoot: func() (runtime.BootSession, error) { return runtime.BootSession{ID: "current-boot"}, nil },
	}}

	err := manager.RestartMihomo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("RestartMihomo() error = %v", err)
	}
	if mihomoManager.validateCalled || mihomoManager.stopCalled || mihomoManager.startCalled {
		t.Fatal("restart touched mihomo for a previous-boot runtime")
	}
}

type fakeDHCP struct {
	checkErr    error
	writeErr    error
	startPID    int
	startErr    error
	stopErr     error
	startCalled bool
	stopCalled  bool
	running     bool
	events      *[]string
}

func (f *fakeDHCP) Check() error {
	return f.checkErr
}

func (f *fakeDHCP) WriteConfig() error {
	if f.events != nil {
		*f.events = append(*f.events, "dhcp-write")
	}
	return f.writeErr
}

func (f *fakeDHCP) Start() (int, error) {
	f.startCalled = true
	if f.events != nil {
		*f.events = append(*f.events, "dhcp-start")
	}
	return f.startPID, f.startErr
}

func (f *fakeDHCP) Stop(int) error {
	f.stopCalled = true
	if f.events != nil {
		*f.events = append(*f.events, "dhcp-stop")
	}
	return f.stopErr
}

func (f *fakeDHCP) Running(int) bool { return f.running }

type fakeMihomo struct {
	checkErr       error
	writeErr       error
	validateErr    error
	startPID       int
	startErr       error
	stopErr        error
	startCalled    bool
	stopCalled     bool
	validateCalled bool
	stoppedPID     int
	running        bool
	events         *[]string
}

func (f *fakeMihomo) Check() error {
	return f.checkErr
}

func (f *fakeMihomo) WriteConfig() error {
	if f.events != nil {
		*f.events = append(*f.events, "mihomo-write")
	}
	return f.writeErr
}

func (f *fakeMihomo) ValidateWrittenConfig() error {
	f.validateCalled = true
	if f.events != nil {
		*f.events = append(*f.events, "mihomo-validate")
	}
	return f.validateErr
}

func (f *fakeMihomo) ValidateWrittenConfigContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.ValidateWrittenConfig()
}

func (f *fakeMihomo) Start() (int, error) {
	f.startCalled = true
	if f.events != nil {
		*f.events = append(*f.events, "mihomo-start")
	}
	return f.startPID, f.startErr
}

func (f *fakeMihomo) Running(int) bool { return f.running }

func (f *fakeMihomo) Stop(pid int) error {
	f.stopCalled = true
	f.stoppedPID = pid
	if f.events != nil {
		*f.events = append(*f.events, "mihomo-stop")
	}
	return f.stopErr
}

func indexOfEvent(events []string, target string) int {
	for index, event := range events {
		if event == target {
			return index
		}
	}
	return -1
}

func assertEventOrder(t *testing.T, events []string, ordered ...string) {
	t.Helper()
	position := -1
	for _, event := range ordered {
		next := indexOfEvent(events, event)
		if next <= position {
			t.Fatalf("event %q did not occur after offset %d: %v", event, position, events)
		}
		position = next
	}
}

func fakeProcessFingerprint(pid int) (string, error) {
	if pid <= 0 {
		return "", nil
	}
	return fmt.Sprintf("pid-%d-start", pid), nil
}

func fakeProcessMatches(pid int, fingerprint string) (bool, error) {
	expected, _ := fakeProcessFingerprint(pid)
	return expected != "" && fingerprint == expected, nil
}

// fakeBackend is a no-op platform.NetworkBackend for lifecycle tests that only
// care about ordering and error propagation, not about real host state.
type fakeBackend struct {
	ensureTUNErr     error
	validateErr      error
	natErr           error
	routingErr       error
	waitTUNErr       error
	forwardingErr    error
	natCalls         int
	routingCalls     int
	removeNATCalls   int
	removeRoutingCalls int
	restoreCalls     int
	forwardingEnabled   bool
	forwardingRestored  bool
	forwardingAttempted bool
}

func (f *fakeBackend) Name() platform.BackendName { return platform.BackendLinuxNFTables }

func (f *fakeBackend) Capabilities(context.Context) (platform.Capabilities, error) {
	return platform.Capabilities{TUNDeviceNode: true, NFTables: true, IProute2: true}, nil
}

func (f *fakeBackend) DetectInterfaces(context.Context) ([]platform.NetworkInterface, error) {
	return nil, nil
}

func (f *fakeBackend) InterfaceByName(context.Context, string) (platform.NetworkInterface, error) {
	return platform.NetworkInterface{Name: "eth0", Flags: []string{"up"}}, nil
}

func (f *fakeBackend) ValidateTopology(context.Context, platform.NetworkConfig) error {
	return f.validateErr
}

func (f *fakeBackend) EnableIPv4Forwarding(context.Context) (func(context.Context) error, error) {
	if f.forwardingErr != nil {
		return nil, f.forwardingErr
	}
	f.forwardingEnabled = true
	f.forwardingAttempted = true
	return func(context.Context) error { f.forwardingRestored = true; return nil }, nil
}

func (f *fakeBackend) SetupNAT(context.Context, platform.NATConfig) error {
	f.natCalls++
	return f.natErr
}

func (f *fakeBackend) RemoveNAT(context.Context) error {
	f.removeNATCalls++
	return nil
}

func (f *fakeBackend) SetupPolicyRouting(context.Context, platform.RoutingConfig) error {
	f.routingCalls++
	return f.routingErr
}

func (f *fakeBackend) RemovePolicyRouting(context.Context) error {
	f.removeRoutingCalls++
	return nil
}

func (f *fakeBackend) SwitchToDirectFallback(context.Context, platform.RoutingConfig) error {
	return nil
}

func (f *fakeBackend) EnsureTUN(context.Context) error { return f.ensureTUNErr }

func (f *fakeBackend) WaitForTUN(context.Context, string) (platform.NetworkInterface, error) {
	if f.waitTUNErr != nil {
		return platform.NetworkInterface{}, f.waitTUNErr
	}
	return platform.NetworkInterface{Name: "tun0"}, nil
}

func (f *fakeBackend) Snapshot(context.Context) (*platform.NetworkSnapshot, error) {
	return platform.NewSnapshot(platform.BackendLinuxNFTables), nil
}

func (f *fakeBackend) Restore(context.Context, *platform.NetworkSnapshot) error {
	f.restoreCalls++
	f.forwardingRestored = true
	f.forwardingEnabled = false
	return nil
}

func (f *fakeBackend) ObservedState(context.Context) (*platform.ObservedState, error) {
	return &platform.ObservedState{}, nil
}
