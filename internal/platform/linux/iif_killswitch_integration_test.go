//go:build linux

package linux

import (
	"context"
	"testing"
	"time"

	"open-mihomo-gateway/internal/platform"
)

func TestNetworkIngressInterfaceKillSwitchBlocksMainRouteFallback(t *testing.T) {
	requireNetworkTests(t)
	ctx := context.Background()
	backend, err := New(WithTUNTimeout(2 * time.Second))
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	if backend.runner.ipPath == "" {
		t.Skip("iproute2 not installed")
	}
	backend.runner.nftPath = ""

	const lan = "oskillsw0"
	const peer = "oskillswp0"
	const tun = "oskillswt0"
	const table = uint32(20243)
	const priority = uint32(20243)

	_ = backend.runner.run(ctx, backend.runner.ipPath, "link", "del", lan)
	if err := backend.runner.run(ctx, backend.runner.ipPath, "link", "add", lan, "type", "veth", "peer", "name", peer); err != nil {
		t.Skipf("cannot create veth pair (needs CAP_NET_ADMIN): %v", err)
	}
	t.Cleanup(func() {
		_ = backend.runner.run(context.Background(), backend.runner.ipPath, "link", "del", lan)
	})
	for _, name := range []string{lan, peer} {
		if err := backend.runner.run(ctx, backend.runner.ipPath, "link", "set", name, "up"); err != nil {
			t.Fatalf("bring up %s: %v", name, err)
		}
	}
	if err := backend.runner.run(ctx, backend.runner.ipPath, "addr", "add", "192.0.2.1/24", "dev", lan); err != nil {
		t.Fatalf("assign LAN address: %v", err)
	}
	ensureDummyTUN(t, backend, tun)

	cfg := platform.RoutingConfig{
		LANInterface:      lan,
		UpstreamInterface: lan,
		LANCIDR:           "192.0.2.0/24",
		TUNDevice:         tun,
		UpstreamGateway:   "192.0.2.254",
		TableID:           table,
		RulePriority:      priority,
		RuleMode:          platform.RoutingRuleIngressInterface,
	}
	if err := backend.SetupPolicyRouting(ctx, cfg); err != nil {
		t.Fatalf("SetupPolicyRouting(iif): %v", err)
	}
	t.Cleanup(func() { _ = backend.removePolicyRouting(context.Background(), cfg) })

	guard, err := backend.guardRulePresent(ctx, cfg)
	if err != nil {
		t.Fatalf("guardRulePresent: %v", err)
	}
	if !guard {
		t.Fatal("fail-closed iif guard rule was not created")
	}

	// If the primary selector disappears, forwarded traffic must hit the later
	// prohibit rule instead of continuing into the namespace main table.
	if err := backend.runner.run(ctx, backend.runner.ipPath, "rule", "del", "pref", "20243", "iif", lan, "table", "20243"); err != nil {
		t.Fatalf("remove primary iif rule: %v", err)
	}
	if _, err := backend.runner.output(ctx, backend.runner.ipPath,
		"-4", "route", "get", "198.51.100.8", "from", "192.0.2.50", "iif", lan); err == nil {
		t.Fatal("forwarded lookup succeeded after primary rule removal; main-table fallback was not blocked")
	}

	// Re-apply restores the primary rule while retaining the guard.
	if err := backend.SetupPolicyRouting(ctx, cfg); err != nil {
		t.Fatalf("restore policy routing: %v", err)
	}

	// A missing TUN default causes the first lookup rule to miss. The guard must
	// still stop traversal before the main table can route the packet directly.
	if err := backend.runner.run(ctx, backend.runner.ipPath, "route", "del", "default", "dev", tun, "table", "20243"); err != nil {
		t.Fatalf("remove TUN default route: %v", err)
	}
	if _, err := backend.runner.output(ctx, backend.runner.ipPath,
		"-4", "route", "get", "198.51.100.8", "from", "192.0.2.50", "iif", lan); err == nil {
		t.Fatal("forwarded lookup succeeded after TUN default removal; main-table fallback was not blocked")
	}
	if err := backend.runner.run(ctx, backend.runner.ipPath, "route", "replace", "default", "dev", tun, "table", "20243"); err != nil {
		t.Fatalf("restore TUN default: %v", err)
	}

	if err := backend.removePolicyRouting(ctx, cfg); err != nil {
		t.Fatalf("removePolicyRouting: %v", err)
	}
	guard, err = backend.guardRulePresent(ctx, cfg)
	if err != nil {
		t.Fatalf("guardRulePresent after cleanup: %v", err)
	}
	if guard {
		t.Fatal("fail-closed guard survived exact cleanup")
	}
}
