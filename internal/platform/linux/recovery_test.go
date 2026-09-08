//go:build linux

package linux

import (
	"context"
	"encoding/json"
	"testing"

	"open-mihomo-gateway/internal/platform"
)

func TestRestoreSkipsSnapshotFromDifferentNetworkNamespace(t *testing.T) {
	backend, err := New()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := platform.NewSnapshot(platform.BackendLinuxNFTables)
	snapshot.NetworkNamespace = "net:[definitely-not-current]"
	snapshot.IPv4Forwarding = "0"
	snapshot.NFTablesTable = "opensurge"
	snapshot.NAT = &platform.NATConfig{TableName: "opensurge"}
	snapshot.Routing = &platform.RoutingConfig{TableID: 20241, RulePriority: 20241, FwMark: 0x29}
	snapshot.Applied = platform.AppliedSteps{IPv4Forwarding: true, NAT: true, PolicyRouting: true}

	// Namespace mismatch must return before nft/ip are consulted, so this remains
	// safe and testable even on an unprivileged CI runner without those tools.
	if err := backend.Restore(context.Background(), snapshot); err != nil {
		t.Fatalf("Restore() on foreign namespace = %v, want safe no-op", err)
	}
}

func TestRuleMatchesConfigUsesExactStructuredFields(t *testing.T) {
	raw := []byte(`{"priority":20241,"fwmark":"0x29","table":20241}`)
	var rule ipRule
	if err := json.Unmarshal(raw, &rule); err != nil {
		t.Fatal(err)
	}
	cfg := platform.RoutingConfig{TableID: 20241, RulePriority: 20241, FwMark: 0x29}
	if !ruleMatchesConfig(rule, cfg) {
		t.Fatal("exact structured rule did not match")
	}
	cfg.FwMark = 0x290
	if ruleMatchesConfig(rule, cfg) {
		t.Fatal("0x29 incorrectly matched 0x290")
	}
}

func TestParseRawUint32AcceptsHexStringAndJSONNumber(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want uint32
	}{
		{`"0x29"`, 0x29},
		{`20241`, 20241},
	} {
		value, ok := parseRawUint32(json.RawMessage(test.raw))
		if !ok || value != test.want {
			t.Fatalf("parseRawUint32(%s) = %d,%v want %d,true", test.raw, value, ok, test.want)
		}
	}
}
