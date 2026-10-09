package gateway

import (
	"strings"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/runtime"
)

// This fixture deliberately has no lifecycle field: it is the partial runtime
// persisted by the image observed on the NAS, before this fix.
func TestLegacyPartialRuntimeCannotRestartOnlyMihomo(t *testing.T) {
	cfg := qnapRecoveryTestConfig(t)
	backend := &fakeBackend{}
	engine := &fakeMihomo{startPID: 2001}
	m := qnapRecoveryTestManager(t, cfg, backend, &fakeDHCP{}, engine)
	m.deps.currentBoot = func() (runtime.BootSession, error) { return runtime.BootSession{ID: "host-boot"}, nil }
	if err := runtime.Ensure(m.paths); err != nil {
		t.Fatal(err)
	}
	digest, err := config.MihomoProfileDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := platform.NewSnapshot(platform.BackendLinuxNFTables)
	snapshot.Routing = &platform.RoutingConfig{LANInterface: "eth0", UpstreamInterface: "eth0", LANCIDR: cfg.Gateway.LANCIDR, TUNDevice: "tun0", UpstreamGateway: cfg.Gateway.UpstreamGateway, TableID: cfg.Transparent.RouteTableID, RulePriority: cfg.Transparent.RouteRulePriority, RuleMode: platform.RoutingRuleIngressInterface}
	if err := runtime.SaveState(m.paths.StateFile, runtime.State{BootSessionID: "host-boot", DNSFrontend: "smartdns", ProfileDigest: digest, StartedAt: time.Now(), NetworkSnapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	if err := m.RestartMihomo(t.Context()); err == nil || !strings.Contains(err.Error(), "full gateway recovery") {
		t.Fatalf("legacy partial runtime accepted engine-only restart: %v", err)
	}
	if engine.startCalled {
		t.Fatal("engine started without DNS dependencies")
	}
}
