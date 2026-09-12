//go:build linux

package linux

import (
	"encoding/json"
	"testing"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/platform"
)

func raw(value string) json.RawMessage { return json.RawMessage(value) }

func TestIPv6FakeRuleMatchesOnlySyntheticPrefix(t *testing.T) {
	cfg := platform.RoutingConfig{TableID: 20241, RulePriority: 20241}
	matching := ipRule{
		"priority": raw("20241"),
		"table":    raw("20241"),
		"dst":      raw(`"` + config.MihomoFakeIPv6Range + `"`),
	}
	if !ipv6FakeRuleMatches(matching, cfg) {
		t.Fatalf("expected fake-IP selector to match: %#v", matching)
	}

	broad := ipRule{
		"priority": raw("20241"),
		"table":    raw("20241"),
		"iifname":  raw(`"eth0"`),
		"dst":      raw(`"default"`),
	}
	if ipv6FakeRuleMatches(broad, cfg) {
		t.Fatalf("broad ingress/default IPv6 rule must not match fake-IP ownership: %#v", broad)
	}
}

func TestIPv6FakeGuardMatchesSyntheticPrefixOnly(t *testing.T) {
	cfg := platform.RoutingConfig{TableID: 20241, RulePriority: 20241}
	guard := ipRule{
		"priority": raw("20242"),
		"dst":      raw(`"` + config.MihomoFakeIPv6Range + `"`),
		"action":   raw(`"prohibit"`),
	}
	if !ipv6FakeGuardMatches(guard, cfg) {
		t.Fatalf("expected fake-IP guard to match: %#v", guard)
	}
	guard["dst"] = raw(`"::/0"`)
	if ipv6FakeGuardMatches(guard, cfg) {
		t.Fatal("default-route prohibit must not be treated as the OpenSurge fake-IP guard")
	}
}
