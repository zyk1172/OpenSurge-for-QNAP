package cloudflareopt

import (
	"testing"
	"time"
)

func TestDefaultConfigValid(t *testing.T) {
	cfg := DefaultConfig()
	if err := Validate(cfg); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if !cfg.Enabled || cfg.Schedule.EveryDays != 1 || cfg.Scan.BudgetSeconds != 75 {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if !cfg.Health.Enabled || cfg.Health.CheckIntervalMinutes != 30 || cfg.Health.LatencyThresholdMS != 100 {
		t.Fatalf("unexpected health defaults: %#v", cfg.Health)
	}
	if cfg.Scan.CandidateLimit != 1024 || cfg.Scan.TCPConcurrency != 128 || cfg.Scan.MaxLatencyMS != 100 || cfg.Scan.MaxLossRate != 0 || cfg.Scan.MinDownloadMbps != 0 || cfg.Scan.DownloadMaxBytes != DefaultDownloadRequestBytes {
		t.Fatalf("unexpected scan defaults: %#v", cfg.Scan)
	}
}

func TestNormalizeDeduplicatesDomains(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Targets = []Target{
		{Domain: " API.Example.COM. ", Enabled: true},
		{Domain: "api.example.com", Enabled: true},
		{Domain: "cdn.example.com", Enabled: true, TestPath: "/health"},
	}
	cfg = Normalize(cfg)
	if len(cfg.Targets) != 2 {
		t.Fatalf("targets = %#v", cfg.Targets)
	}
	if cfg.Targets[0].Domain != "api.example.com" || cfg.Targets[0].TestPath != "/" {
		t.Fatalf("normalized first target = %#v", cfg.Targets[0])
	}
}

func TestIntervalScheduleUsesLastRunAnchor(t *testing.T) {
	location := time.FixedZone("test", 8*60*60)
	now := time.Date(2026, 9, 17, 22, 0, 0, 0, location)
	last := time.Date(2026, 9, 14, 4, 1, 0, 0, location)
	next, err := NextRun(Schedule{Mode: ScheduleInterval, EveryDays: 7, At: "04:00"}, now, &last)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 21, 4, 0, 0, 0, location)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestCronSchedule(t *testing.T) {
	location := time.FixedZone("test", 8*60*60)
	now := time.Date(2026, 9, 17, 22, 0, 0, 0, location)
	next, err := NextRun(Schedule{Mode: ScheduleCron, Cron: "0 4 * * 0"}, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 20, 4, 0, 0, 0, location)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestCronRejectsSixFields(t *testing.T) {
	if err := ValidateSchedule(Schedule{Mode: ScheduleCron, Cron: "0 0 4 * * 0"}); err == nil {
		t.Fatal("expected six-field cron to be rejected")
	}
}

func TestBlacklistRealIPSemanticDedup(t *testing.T) {
	plan := PlanRealIP("blacklist", []string{"+.example.com"}, []string{"api.example.com", "other.example.net"})
	if got := plan.CoveredBy["api.example.com"]; got != "+.example.com" {
		t.Fatalf("covered by = %q", got)
	}
	if len(plan.Append) != 1 || plan.Append[0] != "other.example.net" {
		t.Fatalf("append = %#v", plan.Append)
	}
}

func TestRuleModeDetectsEarlierConflict(t *testing.T) {
	plan := PlanRealIP("rule", []string{"DOMAIN-SUFFIX,example.com,fake-ip", "MATCH,fake-ip"}, []string{"api.example.com"})
	if plan.Conflicts["api.example.com"] == "" {
		t.Fatalf("expected conflict: %#v", plan)
	}
}

func TestRuleModePrependsMissingRealIP(t *testing.T) {
	plan := PlanRealIP("rule", []string{"DOMAIN,other.example.com,real-ip"}, []string{"api.example.com"})
	if len(plan.Prepend) != 1 || plan.Prepend[0] != "DOMAIN,api.example.com,real-ip" {
		t.Fatalf("prepend = %#v", plan.Prepend)
	}
}

func TestWhitelistUnmatchedAlreadyUsesRealIP(t *testing.T) {
	plan := PlanRealIP("whitelist", []string{"+.fake.example.com"}, []string{"api.example.com"})
	if plan.CoveredBy["api.example.com"] != "whitelist-unmatched" || len(plan.Append) != 0 {
		t.Fatalf("plan = %#v", plan)
	}
}


func TestNormalizeMigratesLegacyContinuousHealthAndStandardScan(t *testing.T) {
	cfg := Config{
		SchemaVersion: SchemaVersion,
		Enabled:       true,
		Schedule:      Schedule{Mode: ScheduleInterval, EveryDays: 7, At: "04:00"},
		Scan: ScanSettings{
			BudgetSeconds:          60,
			CandidateLimit:         256,
			TCPConcurrency:         64,
			TCPAttempts:            2,
			TCPTimeoutMS:           800,
			HTTPSCandidateCount:    15,
			HTTPTimeoutMS:          2000,
			DownloadCandidateCount: 3,
			DownloadSeconds:        2,
			DownloadMaxBytes:       4 << 20,
		},
	}
	got := Normalize(cfg)
	if !got.Health.Enabled || got.Health.CheckIntervalMinutes != 30 || got.Health.LatencyThresholdMS != 100 {
		t.Fatalf("legacy health migration = %#v", got.Health)
	}
	if got.Scan.CandidateLimit != 1024 || got.Scan.TCPConcurrency != 128 || got.Scan.DownloadCandidateCount != 8 || got.Scan.DownloadMaxBytes != DefaultDownloadRequestBytes {
		t.Fatalf("legacy scan migration = %#v", got.Scan)
	}
	if got.Schedule.EveryDays != 7 {
		t.Fatalf("explicit legacy schedule should be preserved: %#v", got.Schedule)
	}
}

func TestValidateRejectsUnsafeHealthAndScanThresholds(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Health.CheckIntervalMinutes = 1
	if err := Validate(cfg); err == nil {
		t.Fatal("expected too-frequent health checks to be rejected")
	}
	cfg = DefaultConfig()
	cfg.Scan.MaxLossRate = 1.1
	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid scan loss threshold to be rejected")
	}
}

func TestValidateMinimumDownloadThreshold(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Scan.MinDownloadMbps = 20
	if err := Validate(cfg); err != nil {
		t.Fatalf("valid minimum throughput threshold rejected: %v", err)
	}

	cfg.Scan.MinDownloadMbps = -1
	if err := Validate(cfg); err == nil {
		t.Fatal("negative minimum throughput threshold should be rejected")
	}

	cfg = DefaultConfig()
	cfg.Scan.MinDownloadMbps = 20
	cfg.Scan.DownloadCandidateCount = 0
	if err := Validate(cfg); err == nil {
		t.Fatal("minimum throughput threshold requires download candidates")
	}
}

func TestNormalizeMigratesSmallDownloadStreams(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Scan.DownloadMaxBytes = 16 << 20
	got := Normalize(cfg)
	if got.Scan.DownloadMaxBytes != DefaultDownloadRequestBytes {
		t.Fatalf("small legacy download stream was not migrated: got %d want %d", got.Scan.DownloadMaxBytes, DefaultDownloadRequestBytes)
	}
}
