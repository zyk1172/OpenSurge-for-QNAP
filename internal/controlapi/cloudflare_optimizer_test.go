package controlapi

import (
	"testing"

	"open-mihomo-gateway/internal/cloudflareopt"
)

func TestMergeBoundedScanResultsPreservesUnfinishedEnabledDomain(t *testing.T) {
	previous := []cloudflareopt.TargetResult{
		{Domain: "a.example.com", Selected: cloudflareopt.CandidateResult{IP: "104.16.0.1"}},
		{Domain: "b.example.com", Selected: cloudflareopt.CandidateResult{IP: "104.16.0.2"}},
	}
	fresh := []cloudflareopt.TargetResult{
		{Domain: "a.example.com", Selected: cloudflareopt.CandidateResult{IP: "104.16.0.9"}},
	}
	targets := []cloudflareopt.Target{
		{Domain: "a.example.com", Enabled: true},
		{Domain: "b.example.com", Enabled: true},
	}

	got := mergeBoundedScanResults(previous, fresh, targets)
	if len(got) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(got))
	}
	if got[0].Domain != "a.example.com" || got[0].Selected.IP != "104.16.0.9" {
		t.Fatalf("fresh result was not preferred: %#v", got[0])
	}
	if got[1].Domain != "b.example.com" || got[1].Selected.IP != "104.16.0.2" {
		t.Fatalf("unfinished target lost previous verified result: %#v", got[1])
	}
}

func TestRetainEnabledOptimizerResultsDropsRemovedAndDisabledTargets(t *testing.T) {
	results := []cloudflareopt.TargetResult{
		{Domain: "a.example.com", Selected: cloudflareopt.CandidateResult{IP: "104.16.0.1"}},
		{Domain: "b.example.com", Selected: cloudflareopt.CandidateResult{IP: "104.16.0.2"}},
		{Domain: "c.example.com", Selected: cloudflareopt.CandidateResult{IP: "104.16.0.3"}},
	}
	targets := []cloudflareopt.Target{
		{Domain: "a.example.com", Enabled: true},
		{Domain: "b.example.com", Enabled: false},
	}

	got := retainEnabledOptimizerResults(results, targets)
	if len(got) != 1 || got[0].Domain != "a.example.com" {
		t.Fatalf("results = %#v, want only enabled target", got)
	}
}
