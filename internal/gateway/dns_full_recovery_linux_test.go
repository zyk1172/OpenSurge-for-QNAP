//go:build linux

package gateway

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/dhcp"
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/process"
	"open-mihomo-gateway/internal/runtime"
	"open-mihomo-gateway/internal/smartdns"
)

type dnsRecoveryNamespaceBackend struct {
	*failingCleanupBackend
	namespace string
}

func (b *dnsRecoveryNamespaceBackend) Snapshot(ctx context.Context) (*platform.NetworkSnapshot, error) {
	snapshot, err := b.fakeBackend.Snapshot(ctx)
	if err == nil {
		snapshot.NetworkNamespace = b.namespace
	}
	return snapshot, err
}

// Run only in the existing disposable lab namespace. This proves real :53 ->
// SmartDNS -> local dnsmasq packet handling after the failed-start sequence;
// the engine/network dependencies are fault-injection seams, not TUN proof.
func TestFullGatewayDNSRecoveryLinux(t *testing.T) {
	if os.Getenv("OPEN_SURGE_DNSMASQ_FAULT_TESTS") != "1" {
		t.Skip("requires the isolated Linux DNS fault lab")
	}
	if os.Geteuid() != 0 {
		t.Fatal("requires root inside the disposable namespace")
	}
	cfg := qnapRecoveryTestConfig(t)
	cfg.Gateway.Interface, cfg.Gateway.UpstreamInterface = "os-gw-lan", "os-gw-lan"
	cfg.Gateway.LANIP, cfg.Gateway.LANCIDR = "10.77.1.1", "10.77.1.0/24"
	cfg.Gateway.UpstreamGateway, cfg.DNS.Listen = "10.77.1.254", "10.77.1.1"
	cfg.DHCP.Enabled, cfg.DHCP.Domain = false, "lan"
	boot, err := runtime.CurrentBootSession()
	if err != nil {
		t.Fatal(err)
	}
	base := &fakeBackend{}
	backend := &dnsRecoveryNamespaceBackend{failingCleanupBackend: &failingCleanupBackend{fakeBackend: base, restoreErr: errors.New("injected rollback timeout")}, namespace: boot.NetworkNamespace}
	engine := &fakeMihomo{startErr: errors.New("injected API startup timeout")}
	m := qnapRecoveryTestManager(t, cfg, base, &fakeDHCP{}, engine)
	m.deps.currentBoot = runtime.CurrentBootSession
	m.deps.processFingerprint, m.deps.processMatches = process.Fingerprint, process.MatchesFingerprint
	m.deps.newBackend = func() (platform.NetworkBackend, error) { return backend, nil }
	m.deps.newDHCP = func(cfg config.Config, paths runtime.Paths) dhcpService { return dhcp.New(cfg, paths) }
	m.deps.newSmartDNS = func(cfg config.Config, paths runtime.Paths) smartDNSService { return smartdns.New(cfg, paths) }
	if err := runtime.Ensure(m.paths); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SaveGatewayDesiredState(runtime.GatewayDesiredStatePath(m.paths.Dir), true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		backend.restoreErr = nil
		if err := m.Stop(context.Background()); err != nil {
			t.Errorf("lab cleanup: %v", err)
		}
	})
	if err := m.Start(t.Context()); err == nil || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("missing failure injection: %v", err)
	}
	if err := m.RestartMihomo(t.Context()); err == nil || !strings.Contains(err.Error(), "full gateway recovery") {
		t.Fatalf("partial state allowed engine-only restart: %v", err)
	}
	backend.restoreErr, engine.startErr, engine.startPID = nil, nil, os.Getpid()
	if recovered, err := m.RecoverAfterContainerRestart(t.Context()); err != nil || !recovered {
		t.Fatalf("full recovery=%v: %v", recovered, err)
	}
	state, exists, err := runtime.LoadState(m.paths.StateFile)
	if err != nil || !exists || state.PIDSmartDNS <= 0 || state.PIDDNSMasq <= 0 {
		t.Fatalf("missing DNS services: %+v %v", state, err)
	}
	for _, address := range []string{"127.0.0.1", cfg.Gateway.LANIP} {
		port := "53"
		if address == "127.0.0.1" {
			port = "5353"
		}
		out, err := exec.CommandContext(t.Context(), "dig", "@"+address, "-p", port, "missing-device.lan", "A", "+time=2", "+tries=1").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "status: NXDOMAIN") {
			t.Fatalf("DNS %s:%s did not answer local DNS: %v\n%s", address, port, err, out)
		}
	}
}
