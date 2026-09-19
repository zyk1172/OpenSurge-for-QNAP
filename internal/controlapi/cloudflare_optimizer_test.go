package controlapi

import (
	"testing"
	"time"

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

	got := mergeBoundedScanResults(previous, fresh, targets, nil)
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


func TestRetainEnabledOptimizerHealthDropsRemovedTargets(t *testing.T) {
	health := []cloudflareopt.HealthResult{
		{Domain: "a.example.com", IP: "104.16.0.1", Healthy: true},
		{Domain: "b.example.com", IP: "104.16.0.2", Healthy: false},
	}
	targets := []cloudflareopt.Target{{Domain: "a.example.com", Enabled: true}}

	got := retainEnabledOptimizerHealth(health, targets)
	if len(got) != 1 || got[0].Domain != "a.example.com" {
		t.Fatalf("health = %#v, want only enabled target", got)
	}
}

func TestPopulateNextHealthCheckUsesLastCheckAnchor(t *testing.T) {
	api := &cloudflareOptimizerAPI{}
	cfg := cloudflareopt.DefaultConfig()
	cfg.Targets = []cloudflareopt.Target{{Domain: "a.example.com", Enabled: true}}
	last := time.Date(2026, 9, 19, 2, 0, 0, 0, time.UTC)
	state := cloudflareopt.State{
		LastHealthCheckAt: &last,
		Results: []cloudflareopt.TargetResult{{
			Domain: "a.example.com",
			Selected: cloudflareopt.CandidateResult{IP: "104.16.0.1"},
		}},
	}

	api.populateNextHealthCheck(&state, cfg)
	want := last.Add(30 * time.Minute)
	if state.NextHealthCheckAt == nil || !state.NextHealthCheckAt.Equal(want) {
		t.Fatalf("next health check = %v, want %v", state.NextHealthCheckAt, want)
	}
}


func TestCloudflareOptimizerScanTimeoutReservesDownloadPhase(t *testing.T) {
	cfg := cloudflareopt.DefaultConfig()
	cfg.Scan.BudgetSeconds = 75
	cfg.Scan.HTTPSCandidateCount = 30
	cfg.Scan.DownloadCandidateCount = 8
	cfg.Scan.DownloadSeconds = 4

	got := cloudflareOptimizerScanTimeout(cfg)
	want := (75 + 30*4 + 20) * time.Second
	if got != want {
		t.Fatalf("scan timeout = %v, want %v", got, want)
	}
}

func TestMergeBoundedScanResultsDropsExplicitlyRejectedDomain(t *testing.T) {
	previous := []cloudflareopt.TargetResult{
		{Domain: "a.example.com", Selected: cloudflareopt.CandidateResult{IP: "104.16.0.1", DownloadMbps: 5}},
		{Domain: "b.example.com", Selected: cloudflareopt.CandidateResult{IP: "104.16.0.2", DownloadMbps: 50}},
	}
	targets := []cloudflareopt.Target{
		{Domain: "a.example.com", Enabled: true},
		{Domain: "b.example.com", Enabled: true},
	}

	got := mergeBoundedScanResults(previous, nil, targets, []string{"a.example.com"})
	if len(got) != 1 || got[0].Domain != "b.example.com" {
		t.Fatalf("rejected domain kept stale result: %#v", got)
	}
}
