package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/runtime"
)

type failingCleanupBackend struct {
	*fakeBackend
	restoreErr error
}

func (b *failingCleanupBackend) Restore(ctx context.Context, snapshot *platform.NetworkSnapshot) error {
	if b.restoreErr != nil {
		return b.restoreErr
	}
	return b.fakeBackend.Restore(ctx, snapshot)
}

type recoverySmartDNS struct{ fakeDHCP }

func (*recoverySmartDNS) ValidateWrittenConfig() error                       { return nil }
func (*recoverySmartDNS) ValidateWrittenConfigContext(context.Context) error { return nil }

func TestFailedStartAndRollbackRequireFullRecoveryIncludingDNS(t *testing.T) {
	cfg := qnapRecoveryTestConfig(t)
	base := &fakeBackend{}
	backend := &failingCleanupBackend{fakeBackend: base, restoreErr: errors.New("check rule: context deadline exceeded")}
	engine := &fakeMihomo{startErr: errors.New("mihomo API not ready after 2s")}
	dnsmasq := &fakeDHCP{startPID: 2002}
	dns := &recoverySmartDNS{fakeDHCP: fakeDHCP{startPID: 2003}}
	m := qnapRecoveryTestManager(t, cfg, base, dnsmasq, engine)
	m.deps.currentBoot = func() (runtime.BootSession, error) { return runtime.BootSession{ID: "host-boot"}, nil }
	m.deps.newBackend = func() (platform.NetworkBackend, error) { return backend, nil }
	m.deps.newSmartDNS = func(config.Config, runtime.Paths) smartDNSService { return dns }
	if err := runtime.Ensure(m.paths); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SaveGatewayDesiredState(runtime.GatewayDesiredStatePath(m.paths.Dir), true); err != nil {
		t.Fatal(err)
	}

	if err := m.Start(t.Context()); err == nil || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("fault injection did not retain failed rollback: %v", err)
	}
	state, exists, err := runtime.LoadState(m.paths.StateFile)
	if err != nil || !exists || state.Lifecycle != "cleanup" || state.PIDSmartDNS != 0 || state.PIDDNSMasq != 0 {
		t.Fatalf("failed-start journal=%+v exists=%v err=%v", state, exists, err)
	}
	engine.startCalled = false
	if err := m.RestartMihomo(t.Context()); err == nil || !strings.Contains(err.Error(), "full gateway recovery") {
		t.Fatalf("partial runtime accepted narrow engine restart: %v", err)
	}
	if engine.startCalled {
		t.Fatal("narrow recovery started an engine with no DNS")
	}

	// A cleanup failure must remain fail-closed and preserve its recipe.
	if recovered, err := m.RecoverAfterContainerRestart(t.Context()); err == nil || recovered {
		t.Fatalf("failed cleanup permitted recovery: recovered=%v err=%v", recovered, err)
	}
	if _, exists, err := runtime.LoadState(m.paths.StateFile); err != nil || !exists {
		t.Fatal("cleanup journal was lost")
	}
	backend.restoreErr = nil
	engine.startErr = nil
	engine.startPID = 2001
	if recovered, err := m.RecoverAfterContainerRestart(t.Context()); err != nil || !recovered {
		t.Fatalf("full recovery: recovered=%v err=%v", recovered, err)
	}
	state, exists, err = runtime.LoadState(m.paths.StateFile)
	if err != nil || !exists || state.Lifecycle != "running" || state.PIDMihomo != 2001 || state.PIDDNSMasq != 2002 || state.PIDSmartDNS != 2003 || !state.RoutingApplied {
		t.Fatalf("full recovered gateway=%+v exists=%v err=%v", state, exists, err)
	}
	if !dns.startCalled || !dnsmasq.startCalled || base.routingCalls != 1 {
		t.Fatal("full recovery skipped DNS or routing")
	}
}
