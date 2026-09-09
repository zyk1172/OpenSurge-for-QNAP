//go:build linux

package linux

import (
	"context"
	"strings"
	"testing"
	"time"

	"open-mihomo-gateway/internal/platform"
)

// TestNetworkIngressInterfaceRoutesTCPAndUDPToTUN exercises the QNAP same-LAN
// data plane without calling nft at all. The test asks the kernel for forwarded
// TCP and UDP route decisions as if packets entered through the QNET-facing LAN
// interface and verifies that both protocols select the dedicated TUN table.
func TestNetworkIngressInterfaceRoutesTCPAndUDPToTUN(t *testing.T) {
	requireNetworkTests(t)
	ctx := context.Background()
	backend, err := New(WithTUNTimeout(2 * time.Second))
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	if backend.runner.ipPath == "" {
		t.Skip("iproute2 not installed")
	}
	// Prove this path is independent of nftables even on CI hosts where nft is
	// installed: any accidental nft call would have no executable to invoke.
	backend.runner.nftPath = ""

	const lan = "osiif0"
	const peer = "osiifp0"
	const tun = "osiiftun0"
	const table = uint32(20242)
	const priority = uint32(20242)

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

	present, err := backend.rulePresent(ctx, cfg)
	if err != nil {
		t.Fatalf("rulePresent: %v", err)
	}
	if !present {
		t.Fatal("iif policy rule was not created")
	}
	ready, err := backend.PolicyRoutingPresent(ctx, cfg)
	if err != nil {
		t.Fatalf("PolicyRoutingPresent: %v", err)
	}
	if !ready {
		t.Fatal("iif policy routing was not fully applied")
	}

	// The dedicated table is OpenSurge-owned. An unexpected more-specific route
	// can bypass the TUN even when the expected LAN and default entries still
	// exist, so health verification must fail closed until it is removed.
	tableText := "20242"
	if err := backend.runner.run(ctx, backend.runner.ipPath, "route", "add", "203.0.113.0/24", "dev", lan, "table", tableText); err != nil {
		t.Fatalf("inject unexpected policy route: %v", err)
	}
	ready, err = backend.PolicyRoutingPresent(ctx, cfg)
	if err != nil {
		t.Fatalf("PolicyRoutingPresent(with foreign route): %v", err)
	}
	if ready {
		t.Fatal("policy routing stayed healthy with an unexpected route in the OpenSurge-owned table")
	}
	if err := backend.runner.run(ctx, backend.runner.ipPath, "route", "del", "203.0.113.0/24", "dev", lan, "table", tableText); err != nil {
		t.Fatalf("remove unexpected policy route: %v", err)
	}
	ready, err = backend.PolicyRoutingPresent(ctx, cfg)
	if err != nil || !ready {
		t.Fatalf("policy routing did not recover after removing unexpected route: ready=%v err=%v", ready, err)
	}

	for _, proto := range []struct {
		name  string
		dport string
	}{
		{name: "tcp", dport: "443"},
		{name: "udp", dport: "443"},
	} {
		out, err := backend.runner.output(ctx, backend.runner.ipPath,
			"-4", "route", "get", "198.51.100.8", "from", "192.0.2.50",
			"iif", lan, "ipproto", proto.name, "sport", "23456", "dport", proto.dport)
		if err != nil {
			t.Fatalf("%s forwarded route lookup: %v", proto.name, err)
		}
		text := string(out)
		if !strings.Contains(text, "dev "+tun) {
			t.Fatalf("%s lookup did not select %s: %s", proto.name, tun, text)
		}
	}

	// Same-LAN direct fallback changes only the dedicated table's default route;
	// it must not require NAT/nftables.
	fallback := cfg
	fallback.DirectFallback = true
	if err := backend.SetupPolicyRouting(ctx, fallback); err != nil {
		t.Fatalf("SetupPolicyRouting(direct fallback): %v", err)
	}
	ready, err = backend.PolicyRoutingPresent(ctx, fallback)
	if err != nil {
		t.Fatalf("PolicyRoutingPresent(direct fallback): %v", err)
	}
	if !ready {
		t.Fatal("direct-fallback policy routing was not fully applied")
	}
	out, err := backend.runner.output(ctx, backend.runner.ipPath,
		"-4", "route", "get", "198.51.100.8", "from", "192.0.2.50", "iif", lan)
	if err != nil {
		t.Fatalf("direct fallback route lookup: %v", err)
	}
	text := string(out)
	if !strings.Contains(text, "via 192.0.2.254") || !strings.Contains(text, "dev "+lan) {
		t.Fatalf("direct fallback did not select upstream gateway: %s", text)
	}

	if err := backend.removePolicyRouting(ctx, fallback); err != nil {
		t.Fatalf("removePolicyRouting: %v", err)
	}
	present, err = backend.rulePresent(ctx, fallback)
	if err != nil {
		t.Fatalf("rulePresent after removal: %v", err)
	}
	if present {
		t.Fatal("iif policy rule survived removal")
	}
}
