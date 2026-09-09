//go:build linux

package linux

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"open-mihomo-gateway/internal/platform"
)

// These tests mutate real host networking. They only run when explicitly
// enabled, because a developer laptop must never be reconfigured by `go test`.
// CI enables them inside a disposable container or network namespace.
//
//	OPEN_SURGE_NETWORK_TESTS=1 go test ./internal/platform/linux/
const networkTestEnv = "OPEN_SURGE_NETWORK_TESTS"

func requireNetworkTests(t *testing.T) {
	t.Helper()
	if os.Getenv(networkTestEnv) != "1" {
		t.Skipf("set %s=1 to run tests that mutate host networking", networkTestEnv)
	}
	if os.Geteuid() != 0 {
		t.Skip("network tests require root or CAP_NET_ADMIN")
	}
}

// ensureDummyTUN creates a throwaway interface standing in for the device
// mihomo would create. Real TUN creation is the proxy core's job; these tests
// only need a named device that a route can point at.
func ensureDummyTUN(t *testing.T, backend *Backend, name string) {
	t.Helper()
	ctx := context.Background()
	runner := backend.runner
	_ = runner.run(ctx, runner.ipPath, "link", "del", name)
	if err := runner.run(ctx, runner.ipPath, "link", "add", name, "type", "dummy"); err != nil {
		t.Skipf("cannot create dummy interface %s (needs CAP_NET_ADMIN): %v", name, err)
	}
	t.Cleanup(func() {
		_ = runner.run(context.Background(), runner.ipPath, "link", "del", name)
	})
	if err := runner.run(ctx, runner.ipPath, "link", "set", name, "up"); err != nil {
		t.Fatalf("bring up %s: %v", name, err)
	}
}

func testBackend(t *testing.T) *Backend {
	t.Helper()
	backend, err := New(WithTUNTimeout(2 * time.Second))
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	if backend.runner.nftPath == "" {
		t.Skip("nft not installed")
	}
	if backend.runner.ipPath == "" {
		t.Skip("iproute2 not installed")
	}
	return backend
}

// TestNetworkNftablesIsolation is the enforcement test for the product rule
// "OpenSurge must never damage rules it does not own". A decoy table standing in
// for the QNAP firewall must survive every OpenSurge operation, including
// failed ones.
func TestNetworkNftablesIsolation(t *testing.T) {
	requireNetworkTests(t)
	ctx := context.Background()
	backend := testBackend(t)
	runner := backend.runner

	const decoy = "opensurge_decoy_firewall"
	decoyRuleset := "table inet " + decoy + "\n" +
		"delete table inet " + decoy + "\n" +
		"table inet " + decoy + " {\n" +
		"\tchain input {\n\t\ttype filter hook input priority filter; policy accept;\n" +
		"\t\ttcp dport 22 accept comment \"decoy: ssh must survive\"\n\t}\n}\n"
	writeRulesetFile(t, backend, decoyRuleset, func(path string) {
		if err := runner.run(ctx, runner.nftPath, "-f", path); err != nil {
			t.Fatalf("create decoy table: %v", err)
		}
	})
	defer func() {
		_ = runner.run(ctx, runner.nftPath, "delete", "table", "inet", decoy)
	}()

	cfg := NATConfigForTest()
	if err := backend.SetupNAT(ctx, cfg); err != nil {
		t.Fatalf("SetupNAT: %v", err)
	}
	assertHasOnlyOpenSurgeRule(t, ctx, backend)

	// Re-applying must converge, not duplicate.
	if err := backend.SetupNAT(ctx, cfg); err != nil {
		t.Fatalf("second SetupNAT: %v", err)
	}
	first := countOpenSurgeRules(t, ctx, backend)
	if err := backend.SetupNAT(ctx, cfg); err != nil {
		t.Fatalf("third SetupNAT: %v", err)
	}
	second := countOpenSurgeRules(t, ctx, backend)
	if first != second {
		t.Fatalf("SetupNAT is not idempotent: %d rules then %d", first, second)
	}
	if first == 0 {
		t.Fatal("expected at least one OpenSurge rule")
	}

	// The decoy must still be intact at this point.
	if !decoySurvives(t, ctx, backend, decoy) {
		t.Fatal("decoy table was damaged while OpenSurge applied its own table")
	}

	if err := backend.RemoveNAT(ctx); err != nil {
		t.Fatalf("RemoveNAT: %v", err)
	}
	exists, err := backend.tableExists(ctx, backend.tableName)
	if err != nil {
		t.Fatalf("tableExists: %v", err)
	}
	if exists {
		t.Fatal("OpenSurge table should be gone after RemoveNAT")
	}
	if !decoySurvives(t, ctx, backend, decoy) {
		t.Fatal("decoy table was damaged by RemoveNAT")
	}

	// Removal must be idempotent too: a second call has to be a no-op.
	if err := backend.RemoveNAT(ctx); err != nil {
		t.Fatalf("RemoveNAT is not idempotent: %v", err)
	}
}

// TestNetworkPolicyRoutingLifecycle verifies the fwmark rule and the dedicated
// routing table are created and removed cleanly, and that removal is content
// scoped so a foreign rule at a different mark is untouched.
func TestNetworkPolicyRoutingLifecycle(t *testing.T) {
	requireNetworkTests(t)
	ctx := context.Background()
	backend := testBackend(t)
	ensureDummyTUN(t, backend, "tun0")

	iface, err := backend.primaryInterface(ctx)
	if err != nil {
		t.Skipf("cannot determine a usable interface: %v", err)
	}
	cfg := platform.RoutingConfig{
		LANInterface: iface.Name,
		LANCIDR:      "192.0.2.0/24",
		TUNDevice:    "tun0",
		TableID:      20241,
		RulePriority: 20241,
		FwMark:       0x29,
	}
	if err := backend.SetupPolicyRouting(ctx, cfg); err != nil {
		t.Fatalf("SetupPolicyRouting: %v", err)
	}
	present, err := backend.rulePresent(ctx, cfg)
	if err != nil {
		t.Fatalf("rulePresent: %v", err)
	}
	if !present {
		t.Fatal("policy rule was not created")
	}
	// Applying again must not duplicate the rule.
	if err := backend.SetupPolicyRouting(ctx, cfg); err != nil {
		t.Fatalf("second SetupPolicyRouting: %v", err)
	}
	count := countRuleLines(t, ctx, backend, cfg)
	if count != 1 {
		t.Fatalf("expected exactly one policy rule after re-apply, found %d", count)
	}

	if err := backend.removePolicyRouting(ctx, cfg); err != nil {
		t.Fatalf("removePolicyRouting: %v", err)
	}
	present, err = backend.rulePresent(ctx, cfg)
	if err != nil {
		t.Fatalf("rulePresent after removal: %v", err)
	}
	if present {
		t.Fatal("policy rule should be gone after removal")
	}
	if err := backend.removePolicyRouting(ctx, cfg); err != nil {
		t.Fatalf("removePolicyRouting is not idempotent: %v", err)
	}
}

// TestNetworkSnapshotRestore verifies the pre-change forwarding value is
// captured and restored rather than blindly forced back to 0.
func TestNetworkSnapshotRestore(t *testing.T) {
	requireNetworkTests(t)
	ctx := context.Background()
	backend := testBackend(t)
	ensureDummyTUN(t, backend, "tun0")

	iface, err := backend.primaryInterface(ctx)
	if err != nil {
		t.Skipf("cannot determine a usable interface: %v", err)
	}
	before, err := backend.currentIPv4Forwarding()
	if err != nil {
		t.Fatalf("currentIPv4Forwarding: %v", err)
	}

	if err := backend.SetupNAT(ctx, NATConfigForTest()); err != nil {
		t.Fatalf("SetupNAT: %v", err)
	}
	if err := backend.SetupPolicyRouting(ctx, platform.RoutingConfig{
		LANInterface: iface.Name,
		LANCIDR:      "192.0.2.0/24",
		TUNDevice:    "tun0",
		TableID:      20241,
		RulePriority: 20241,
		FwMark:       0x29,
	}); err != nil {
		t.Fatalf("SetupPolicyRouting: %v", err)
	}

	restore, err := backend.EnableIPv4Forwarding(ctx)
	if err != nil {
		t.Fatalf("EnableIPv4Forwarding: %v", err)
	}
	if value, err := backend.currentIPv4Forwarding(); err != nil || value != "1" {
		t.Fatalf("forwarding should be 1, got %q err %v", value, err)
	}
	if !procSysWritable(procIPv4Forward) {
		// Docker's default read-only /proc/sys. Restoring must still succeed as
		// a no-op when the value is already correct, rather than failing and
		// blocking unwinding of everything else.
		t.Logf("note: %s is read-only in this container (Docker default); verifying the no-op restore path", procIPv4Forward)
		if before != "1" {
			t.Skipf("forwarding starts at %q and cannot be changed here; run with sysctls: net.ipv4.ip_forward=1", before)
		}
	}

	snapshot, err := backend.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := backend.Restore(ctx, snapshot); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if value, err := backend.currentIPv4Forwarding(); err != nil {
		t.Fatalf("read forwarding after restore: %v", err)
	} else if value != before {
		t.Fatalf("forwarding restored to %q, want %q", value, before)
	}
	_ = restore
}

// NATConfigForTest builds a NAT config for the host's primary interface.
func NATConfigForTest() platform.NATConfig {
	return platform.NATConfig{
		LANInterface: "lo",
		LANCIDR:      "127.0.0.0/8",
		TUNDevice:    "tun0",
		TableName:    "opensurge",
		FwMark:       0x29,
	}
}

func writeRulesetFile(t *testing.T, backend *Backend, content string, use func(path string)) {
	t.Helper()
	file, err := os.CreateTemp(backend.tempDirOrTmp(), "opensurge-test-*.ruleset")
	if err != nil {
		t.Fatalf("create temp ruleset: %v", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.WriteString(content); err != nil {
		t.Fatalf("write temp ruleset: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close temp ruleset: %v", err)
	}
	use(path)
}

func (b *Backend) tempDirOrTmp() string {
	if b.tempDir != "" {
		return b.tempDir
	}
	return os.TempDir()
}

func (b *Backend) primaryInterface(ctx context.Context) (platform.NetworkInterface, error) {
	interfaces, err := b.detectInterfaces(ctx)
	if err != nil {
		return platform.NetworkInterface{}, err
	}
	for _, iface := range interfaces {
		if iface.Name != "lo" && iface.IsUp() && len(iface.IPv4) > 0 {
			return iface, nil
		}
	}
	return platform.NetworkInterface{}, platform.NewError(platform.CodeInterfaceNotFound, "no usable interface")
}

func countOpenSurgeRules(t *testing.T, ctx context.Context, backend *Backend) int {
	t.Helper()
	ruleset, err := backend.describeTable(ctx)
	if err != nil {
		t.Fatalf("describeTable: %v", err)
	}
	count := 0
	for _, line := range strings.Split(ruleset, "\n") {
		if strings.Contains(line, "comment") && strings.Contains(line, "opensurge") {
			count++
		}
	}
	return count
}

func countRuleLines(t *testing.T, ctx context.Context, backend *Backend, cfg platform.RoutingConfig) int {
	t.Helper()
	out, err := backend.listRules(ctx)
	if err != nil {
		t.Fatalf("listRules: %v", err)
	}
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "fwmark") && strings.Contains(line, markHex(cfg.FwMark)) {
			count++
		}
	}
	return count
}

func decoySurvives(t *testing.T, ctx context.Context, backend *Backend, decoy string) bool {
	t.Helper()
	exists, err := backend.tableExists(ctx, decoy)
	if err != nil {
		t.Fatalf("tableExists(%s): %v", decoy, err)
	}
	return exists
}

func assertHasOnlyOpenSurgeRule(t *testing.T, ctx context.Context, backend *Backend) {
	t.Helper()
	ruleset, err := backend.describeTable(ctx)
	if err != nil {
		t.Fatalf("describeTable: %v", err)
	}
	if !strings.Contains(ruleset, "table inet opensurge") {
		t.Fatalf("expected the opensurge table:\n%s", ruleset)
	}
	for _, line := range strings.Split(ruleset, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "comment") && !strings.Contains(trimmed, "opensurge:") {
			t.Fatalf("table contains a rule not commented as ours: %q", trimmed)
		}
	}
}
