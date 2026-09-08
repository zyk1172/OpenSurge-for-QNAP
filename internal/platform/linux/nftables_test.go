//go:build linux

package linux

import (
	"strings"
	"testing"

	"open-mihomo-gateway/internal/platform"
)

func baseNATConfig() platform.NATConfig {
	return platform.NATConfig{
		LANInterface: "eth0",
		LANCIDR:      "192.168.2.0/24",
		TUNDevice:    "tun0",
		TableName:    "opensurge",
		FwMark:       0x29,
	}
}

// TestRenderRulesetOwnsOnlyOpenSurgeTable is the single most important
// guarantee in this package: the generated ruleset must never be able to
// damage rules it does not own.
func TestRenderRulesetOwnsOnlyOpenSurgeTable(t *testing.T) {
	ruleset, err := renderRuleset(baseNATConfig())
	if err != nil {
		t.Fatalf("renderRuleset: %v", err)
	}
	if strings.Contains(ruleset, "flush ruleset") {
		t.Fatal("ruleset must never contain 'flush ruleset'")
	}
	for _, forbidden := range []string{"flush table ip", "flush table ip6", "delete table ip filter"} {
		if strings.Contains(ruleset, forbidden) {
			t.Fatalf("ruleset must not touch foreign tables, found %q", forbidden)
		}
	}
	// Every table reference must be our own.
	for _, line := range strings.Split(ruleset, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "table ") || strings.HasPrefix(trimmed, "delete table ") {
			if !strings.Contains(trimmed, "inet opensurge") {
				t.Fatalf("ruleset references a table other than inet opensurge: %q", trimmed)
			}
		}
	}
}

func TestRenderRulesetMarksForwardedTraffic(t *testing.T) {
	ruleset, err := renderRuleset(baseNATConfig())
	if err != nil {
		t.Fatalf("renderRuleset: %v", err)
	}
	for _, want := range []string{
		"type filter hook prerouting",
		`iifname "eth0"`,
		"ip daddr != 192.168.2.0/24",
		"meta mark set 0x00000029",
		`comment "opensurge:`,
	} {
		if !strings.Contains(ruleset, want) {
			t.Fatalf("ruleset missing %q\n---\n%s", want, ruleset)
		}
	}
	// Transparent proxying is entirely the TUN's job; there must be no tproxy
	// or redirect, which would overlap with it.
	for _, forbidden := range []string{"tproxy", "redirect to"} {
		if strings.Contains(ruleset, forbidden) {
			t.Fatalf("ruleset must not use %q: mixing mechanisms is not allowed", forbidden)
		}
	}
}

func TestRenderRulesetMasqueradeOnlyForDirectFallback(t *testing.T) {
	cfg := baseNATConfig()
	without, err := renderRuleset(cfg)
	if err != nil {
		t.Fatalf("renderRuleset: %v", err)
	}
	if strings.Contains(without, "masquerade") {
		t.Fatal("masquerade must be off by default; it is an explicit direct-fallback choice")
	}
	cfg.Masquerade = true
	with, err := renderRuleset(cfg)
	if err != nil {
		t.Fatalf("renderRuleset: %v", err)
	}
	if !strings.Contains(with, "masquerade") {
		t.Fatal("masquerade expected when direct fallback is requested")
	}
	if !strings.Contains(with, `comment "opensurge: source NAT for direct fallback egress"`) {
		t.Fatal("masquerade rule must carry an opensurge comment so rollback can identify it")
	}
}

func TestRenderRulesetIsDeterministic(t *testing.T) {
	first, err := renderRuleset(baseNATConfig())
	if err != nil {
		t.Fatalf("renderRuleset: %v", err)
	}
	second, err := renderRuleset(baseNATConfig())
	if err != nil {
		t.Fatalf("renderRuleset: %v", err)
	}
	if first != second {
		t.Fatal("ruleset rendering must be deterministic so idempotent re-apply is safe")
	}
}

// TestRenderRulesetRejectsInjection covers the rule that anything reaching an
// exec argv or an nft expression is validated first. Values shown here arrive
// from configuration files and the Web UI.
func TestRenderRulesetRejectsInjection(t *testing.T) {
	cases := []struct {
		name  string
		mutate func(*platform.NATConfig)
	}{
		{"interface with shell metacharacter", func(c *platform.NATConfig) { c.LANInterface = "eth0; rm -rf /" }},
		{"interface with command substitution", func(c *platform.NATConfig) { c.LANInterface = "eth0$(id)" }},
		{"interface with quote escape", func(c *platform.NATConfig) { c.LANInterface = `eth0" accept` }},
		{"interface too long", func(c *platform.NATConfig) { c.LANInterface = "abcdefghijklmnopqrstuvwxyz" }},
		{"empty interface", func(c *platform.NATConfig) { c.LANInterface = "" }},
		{"table name with metacharacter", func(c *platform.NATConfig) { c.TableName = "opensurge; flush ruleset" }},
		{"table name uppercase", func(c *platform.NATConfig) { c.TableName = "OpenSurge" }},
		{"invalid cidr", func(c *platform.NATConfig) { c.LANCIDR = "192.168.2.0/99" }},
		{"cidr as command", func(c *platform.NATConfig) { c.LANCIDR = "$(id)/24" }},
		{"tun device injection", func(c *platform.NATConfig) { c.TUNDevice = "tun0; nft flush ruleset" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseNATConfig()
			tc.mutate(&cfg)
			if _, err := renderRuleset(cfg); err == nil {
				t.Fatalf("expected rejection for %q", tc.name)
			}
		})
	}
}

func TestRenderRulesetDefaultsTableName(t *testing.T) {
	cfg := baseNATConfig()
	cfg.TableName = ""
	ruleset, err := renderRuleset(cfg)
	if err != nil {
		t.Fatalf("renderRuleset: %v", err)
	}
	if !strings.Contains(ruleset, "table inet opensurge {") {
		t.Fatalf("expected default table name, got:\n%s", ruleset)
	}
}

func TestValidateInterfaceName(t *testing.T) {
	valid := []string{"eth0", "br-lan", "vlan.10", "opensurge0", "eth0@if12"}
	for _, name := range valid {
		if err := validateInterfaceName(name); err != nil {
			t.Fatalf("expected %q to be valid: %v", name, err)
		}
	}
	invalid := []string{"", " ", "eth0 ", "eth 0", "eth0;id", "eth0|x", "../../etc", "a-very-long-interface-name"}
	for _, name := range invalid {
		if err := validateInterfaceName(name); err == nil {
			t.Fatalf("expected %q to be rejected", name)
		}
	}
}

func TestValidateTableName(t *testing.T) {
	if err := validateTableName("opensurge"); err != nil {
		t.Fatalf("opensurge should be valid: %v", err)
	}
	for _, name := range []string{"", "OpenSurge", "1table", "table;flush", "ta ble"} {
		if err := validateTableName(name); err == nil {
			t.Fatalf("expected %q to be rejected", name)
		}
	}
}

func TestValidateIPv4AndCIDR(t *testing.T) {
	if _, err := validateIPv4("192.168.2.241"); err != nil {
		t.Fatalf("valid IPv4 rejected: %v", err)
	}
	for _, value := range []string{"", "not-an-ip", "::1", "192.168.2.256"} {
		if _, err := validateIPv4(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
	if _, err := validateCIDR("192.168.2.0/24"); err != nil {
		t.Fatalf("valid CIDR rejected: %v", err)
	}
	for _, value := range []string{"192.168.2.0", "192.168.2.0/33", "junk"} {
		if _, err := validateCIDR(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
	var coded *platform.Error
	_, err := validateCIDR("junk")
	if !asPlatformError(err, &coded) || coded.Code != platform.CodeSubnetInvalid {
		t.Fatalf("expected coded subnet error, got %v", err)
	}
}

func TestRuleSpecIsContentScoped(t *testing.T) {
	cfg := platform.RoutingConfig{FwMark: 0x29, TableID: 20241}
	spec := ruleSpec(cfg)
	if !strings.Contains(spec, "fwmark 0x29") || !strings.Contains(spec, "lookup 20241") {
		t.Fatalf("rule spec must match on content so removal cannot hit foreign rules: %q", spec)
	}
}
