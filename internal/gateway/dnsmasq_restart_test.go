package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/runtime"
)

func newDNSMasqRestartTestManager(t *testing.T, state runtime.State, service *fakeDHCP) (Manager, runtime.Paths) {
	t.Helper()
	cfg := config.Default()
	cfg.Gateway.Mode = config.GatewayModeSameLAN
	cfg.DHCP.Enabled = false
	cfg.Runtime.Dir = t.TempDir()
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		t.Fatal(err)
	}
	if state.BootSessionID == "" {
		state.BootSessionID = "boot-a"
	}
	if state.StartedAt.IsZero() {
		state.StartedAt = time.Now().Add(-time.Minute)
	}
	if err := runtime.SaveState(paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	return Manager{cfg: cfg, paths: paths, deps: gatewayDeps{
		geteuid:   func() int { return 0 },
		loadState: runtime.LoadState,
		saveState: runtime.SaveState,
		newDHCP: func(config.Config, runtime.Paths) dhcpService {
			return service
		},
		currentBoot: func() (runtime.BootSession, error) {
			return runtime.BootSession{ID: "boot-a", StartedAt: time.Now().Add(-time.Hour)}, nil
		},
		processFingerprint: func(pid int) (string, error) {
			if pid <= 0 {
				return "", errors.New("invalid pid")
			}
			return "fingerprint-new", nil
		},
		processMatches: func(pid int, fingerprint string) (bool, error) {
			return pid > 0 && fingerprint == "fingerprint-old", nil
		},
		now: time.Now,
	}}, paths
}

func TestRestartDNSMasqRecoversMissingProcessWithoutSignallingStalePID(t *testing.T) {
	service := &fakeDHCP{startPID: 22, running: false}
	manager, paths := newDNSMasqRestartTestManager(t, runtime.State{
		PIDDNSMasq: 11, DNSMasqProcessFingerprint: "fingerprint-old",
	}, service)

	if err := manager.RestartDNSMasq(context.Background()); err != nil {
		t.Fatalf("RestartDNSMasq() error = %v", err)
	}
	if service.stopCalled {
		t.Fatal("missing dnsmasq caused a signal attempt against the stale pid")
	}
	if !service.startCalled {
		t.Fatal("replacement dnsmasq was not started")
	}
	state, exists, err := runtime.LoadState(paths.StateFile)
	if err != nil || !exists {
		t.Fatalf("LoadState() exists=%v err=%v", exists, err)
	}
	if state.PIDDNSMasq != 22 || state.DNSMasqProcessFingerprint != "fingerprint-new" {
		t.Fatalf("replacement state = %#v", state)
	}
}

func TestRestartDNSMasqDoesNotSignalReusedPID(t *testing.T) {
	service := &fakeDHCP{startPID: 33, running: true}
	manager, paths := newDNSMasqRestartTestManager(t, runtime.State{
		PIDDNSMasq: 77, DNSMasqProcessFingerprint: "fingerprint-old",
	}, service)
	manager.deps.processMatches = func(pid int, fingerprint string) (bool, error) {
		if pid == 77 {
			return false, nil // PID exists, but identity no longer belongs to OpenSurge.
		}
		return true, nil
	}

	if err := manager.RestartDNSMasq(context.Background()); err != nil {
		t.Fatalf("RestartDNSMasq() error = %v", err)
	}
	if service.stopCalled {
		t.Fatal("PID reuse protection failed: unrelated process would have been signalled")
	}
	state, _, err := runtime.LoadState(paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if state.PIDDNSMasq != 33 {
		t.Fatalf("replacement pid = %d", state.PIDDNSMasq)
	}
}

func TestRestartDNSMasqStopFailureRestoresOwnedPID(t *testing.T) {
	service := &fakeDHCP{startPID: 33, running: true, stopErr: errors.New("process busy")}
	manager, paths := newDNSMasqRestartTestManager(t, runtime.State{
		PIDDNSMasq: 11, DNSMasqProcessFingerprint: "fingerprint-old",
	}, service)

	err := manager.RestartDNSMasq(context.Background())
	if err == nil || !strings.Contains(err.Error(), "process busy") {
		t.Fatalf("RestartDNSMasq() error = %v", err)
	}
	if service.startCalled {
		t.Fatal("replacement started while the owned old process was still alive")
	}
	state, _, loadErr := runtime.LoadState(paths.StateFile)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if state.PIDDNSMasq != 11 || state.DNSMasqProcessFingerprint != "fingerprint-old" {
		t.Fatalf("runtime ownership was not restored: %#v", state)
	}
}

func TestRestartDNSMasqRejectsPreviousBootBeforeTouchingProcess(t *testing.T) {
	service := &fakeDHCP{startPID: 22, running: true}
	manager, _ := newDNSMasqRestartTestManager(t, runtime.State{
		PIDDNSMasq: 11, DNSMasqProcessFingerprint: "fingerprint-old", BootSessionID: "previous-boot",
	}, service)

	err := manager.RestartDNSMasq(context.Background())
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("RestartDNSMasq() error = %v", err)
	}
	if service.stopCalled || service.startCalled {
		t.Fatal("previous-boot runtime touched dnsmasq")
	}
}

func TestRestartDNSMasqArchivesPreviousLog(t *testing.T) {
	service := &fakeDHCP{startPID: 22, running: false}
	manager, paths := newDNSMasqRestartTestManager(t, runtime.State{
		PIDDNSMasq: 11, DNSMasqProcessFingerprint: "fingerprint-old",
	}, service)
	if err := os.WriteFile(paths.DNSMasqLog, []byte("crash evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := manager.RestartDNSMasq(context.Background()); err != nil {
		t.Fatalf("RestartDNSMasq() error = %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(paths.DNSMasqLog), "dnsmasq-before-restart-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("archived dnsmasq logs = %v", matches)
	}
}
