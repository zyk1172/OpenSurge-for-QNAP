package cloudflareopt

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1

const (
	ScheduleInterval            = "interval"
	ScheduleCron                = "cron"
	DefaultDownloadRequestBytes = int64(200_000_000)
)

type Config struct {
	SchemaVersion int            `json:"schema_version"`
	Enabled       bool           `json:"enabled"`
	Schedule      Schedule       `json:"schedule"`
	Health        HealthSettings `json:"health"`
	Scan          ScanSettings   `json:"scan"`
	Targets       []Target       `json:"targets"`
}

type Schedule struct {
	Mode      string `json:"mode"`
	EveryDays int    `json:"every_days,omitempty"`
	At        string `json:"at,omitempty"`
	Cron      string `json:"cron,omitempty"`
}

type HealthSettings struct {
	Enabled              bool    `json:"enabled"`
	CheckIntervalMinutes int     `json:"check_interval_minutes"`
	LatencyThresholdMS   int     `json:"latency_threshold_ms"`
	LossRateThreshold    float64 `json:"loss_rate_threshold"`
}

type ScanSettings struct {
	BudgetSeconds          int     `json:"budget_seconds"`
	CandidateLimit         int     `json:"candidate_limit"`
	TCPConcurrency         int     `json:"tcp_concurrency"`
	TCPAttempts            int     `json:"tcp_attempts"`
	TCPTimeoutMS           int     `json:"tcp_timeout_ms"`
	MaxLatencyMS           int     `json:"max_latency_ms"`
	MaxLossRate            float64 `json:"max_loss_rate"`
	HTTPSCandidateCount    int     `json:"https_candidate_count"`
	HTTPTimeoutMS          int     `json:"http_timeout_ms"`
	DownloadCandidateCount int     `json:"download_candidate_count"`
	DownloadSeconds        int     `json:"download_seconds"`
	DownloadMaxBytes       int64   `json:"download_max_bytes"`
	MinDownloadMbps        float64 `json:"min_download_mbps"`
}

type Target struct {
	Domain   string `json:"domain"`
	Enabled  bool   `json:"enabled"`
	TestPath string `json:"test_path,omitempty"`
}

type CandidateResult struct {
	IP           string  `json:"ip"`
	LatencyMS    int64   `json:"latency_ms"`
	LossRate     float64 `json:"loss_rate"`
	TTFBMS       int64   `json:"ttfb_ms,omitempty"`
	DownloadMbps float64 `json:"download_mbps,omitempty"`
	Colo         string  `json:"colo,omitempty"`
}

type TargetResult struct {
	Domain       string            `json:"domain"`
	Selected     CandidateResult   `json:"selected"`
	Alternatives []CandidateResult `json:"alternatives"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type HealthResult struct {
	Domain    string    `json:"domain"`
	IP        string    `json:"ip"`
	Healthy   bool      `json:"healthy"`
	LatencyMS int64     `json:"latency_ms,omitempty"`
	LossRate  float64   `json:"loss_rate,omitempty"`
	TTFBMS    int64     `json:"ttfb_ms,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	Error     string    `json:"error,omitempty"`
}

type State struct {
	SchemaVersion      int            `json:"schema_version"`
	Running            bool           `json:"running"`
	Checking           bool           `json:"checking"`
	StartedAt          *time.Time     `json:"started_at,omitempty"`
	LastRunAt          *time.Time     `json:"last_run_at,omitempty"`
	NextRunAt          *time.Time     `json:"next_run_at,omitempty"`
	LastHealthCheckAt  *time.Time     `json:"last_health_check_at,omitempty"`
	NextHealthCheckAt  *time.Time     `json:"next_health_check_at,omitempty"`
	LastError          string         `json:"last_error,omitempty"`
	OverlayRevision    string         `json:"overlay_revision,omitempty"`
	Health             []HealthResult `json:"health"`
	Results            []TargetResult `json:"results"`
}

type Response struct {
	Config Config `json:"config"`
	State  State  `json:"state"`
}

func DefaultScanSettings() ScanSettings {
	// Mirror XIU2/CloudflareSpeedTest's default selection pipeline: sample one
	// address from every /24, TCP/443 with 200 workers and four attempts, do not
	// hard-filter reachable candidates by latency/loss, then download-test the
	// first ten latency-ranked candidates for up to ten seconds each. OpenSurge
	// keeps its target-domain HTTPS/SNI validation and official Cloudflare
	// download stream around that CFST-compatible core.
	return ScanSettings{
		BudgetSeconds:          180,
		CandidateLimit:         0, // 0 = every sampled /24
		TCPConcurrency:         200,
		TCPAttempts:            4,
		TCPTimeoutMS:           1000,
		MaxLatencyMS:           9999,
		MaxLossRate:            1,
		HTTPSCandidateCount:    10,
		HTTPTimeoutMS:          2500,
		DownloadCandidateCount: 10,
		DownloadSeconds:        10,
		DownloadMaxBytes:       DefaultDownloadRequestBytes,
		MinDownloadMbps:        0,
	}
}

func legacyBoundedStandardScanSettings() ScanSettings {
	return ScanSettings{
		BudgetSeconds:          75,
		CandidateLimit:         1024,
		TCPConcurrency:         128,
		TCPAttempts:            3,
		TCPTimeoutMS:           800,
		MaxLatencyMS:           100,
		MaxLossRate:            0,
		HTTPSCandidateCount:    30,
		HTTPTimeoutMS:          2500,
		DownloadCandidateCount: 8,
		DownloadSeconds:        4,
		DownloadMaxBytes:       DefaultDownloadRequestBytes,
		MinDownloadMbps:        0,
	}
}

func legacyInitialStandardScanSettings() ScanSettings {
	return ScanSettings{
		BudgetSeconds:          60,
		CandidateLimit:         256,
		TCPConcurrency:         64,
		TCPAttempts:            2,
		TCPTimeoutMS:           800,
		MaxLatencyMS:           0,
		MaxLossRate:            0,
		HTTPSCandidateCount:    15,
		HTTPTimeoutMS:          2000,
		DownloadCandidateCount: 3,
		DownloadSeconds:        2,
		DownloadMaxBytes:       4 << 20,
		MinDownloadMbps:        0,
	}
}

func DefaultConfig() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Enabled:       true,
		Schedule: Schedule{
			Mode:      ScheduleInterval,
			EveryDays: 1,
			At:        "04:00",
		},
		Health: HealthSettings{
			Enabled:              true,
			CheckIntervalMinutes: 30,
			LatencyThresholdMS:   100,
			LossRateThreshold:    0,
		},
		Scan:    DefaultScanSettings(),
		Targets: []Target{},
	}
}

func Normalize(cfg Config) Config {
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = SchemaVersion
	}
	cfg.Schedule.Mode = strings.ToLower(strings.TrimSpace(cfg.Schedule.Mode))
	cfg.Schedule.At = strings.TrimSpace(cfg.Schedule.At)
	cfg.Schedule.Cron = strings.TrimSpace(cfg.Schedule.Cron)

	// Configs created before continuous monitoring had no health object. Enable
	// the safe default only for that all-zero legacy representation; an explicit
	// user-disabled health config keeps its non-zero thresholds and stays off.
	if !cfg.Health.Enabled && cfg.Health.CheckIntervalMinutes == 0 && cfg.Health.LatencyThresholdMS == 0 && cfg.Health.LossRateThreshold == 0 {
		cfg.Health.Enabled = true
	}
	if cfg.Health.CheckIntervalMinutes == 0 {
		cfg.Health.CheckIntervalMinutes = 30
	}
	if cfg.Health.LatencyThresholdMS == 0 {
		cfg.Health.LatencyThresholdMS = 100
	}
	// Only exact historical built-in presets are migrated. User-customized scan
	// settings remain untouched. This also upgrades installations that persisted
	// the previous OpenSurge standard preset before CFST defaults became the
	// product default.
	if cfg.Scan == legacyInitialStandardScanSettings() || cfg.Scan == legacyBoundedStandardScanSettings() {
		cfg.Scan = DefaultScanSettings()
	}
	if cfg.Scan.MaxLatencyMS == 0 {
		cfg.Scan.MaxLatencyMS = DefaultScanSettings().MaxLatencyMS
	}
	// Older presets requested only 4–16 MiB, which let fast links finish in a
	// fraction of a second and made TLS/TTFB dominate the reported Mbps. The
	// field is not exposed in the UI, so migrate old/small values to a large
	// response stream and let DownloadSeconds define the actual sample window.
	if cfg.Scan.DownloadMaxBytes == 0 || cfg.Scan.DownloadMaxBytes <= 64<<20 {
		cfg.Scan.DownloadMaxBytes = DefaultDownloadRequestBytes
	}

	seen := map[string]bool{}
	targets := make([]Target, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		target.Domain = normalizeDomain(target.Domain)
		target.TestPath = strings.TrimSpace(target.TestPath)
		if target.TestPath == "" {
			target.TestPath = "/"
		}
		if target.Domain == "" || seen[target.Domain] {
			continue
		}
		seen[target.Domain] = true
		targets = append(targets, target)
	}
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].Domain < targets[j].Domain })
	cfg.Targets = targets
	return cfg
}

func Validate(cfg Config) error {
	cfg = Normalize(cfg)
	if cfg.SchemaVersion != SchemaVersion {
		return fmt.Errorf("cloudflare optimizer schema_version must be %d", SchemaVersion)
	}
	if err := ValidateSchedule(cfg.Schedule); err != nil {
		return err
	}
	if err := validateHealth(cfg.Health); err != nil {
		return err
	}
	if err := validateScan(cfg.Scan); err != nil {
		return err
	}
	if len(cfg.Targets) > 64 {
		return fmt.Errorf("cloudflare optimizer supports at most 64 target domains")
	}
	for _, target := range cfg.Targets {
		if err := validateTarget(target); err != nil {
			return err
		}
	}
	return nil
}

func validateHealth(health HealthSettings) error {
	if health.CheckIntervalMinutes < 5 || health.CheckIntervalMinutes > 1440 {
		return fmt.Errorf("health check_interval_minutes must be between 5 and 1440")
	}
	if health.LatencyThresholdMS < 20 || health.LatencyThresholdMS > 5000 {
		return fmt.Errorf("health latency_threshold_ms must be between 20 and 5000")
	}
	if health.LossRateThreshold < 0 || health.LossRateThreshold > 1 {
		return fmt.Errorf("health loss_rate_threshold must be between 0 and 1")
	}
	return nil
}

func validateScan(scan ScanSettings) error {
	if scan.BudgetSeconds < 15 || scan.BudgetSeconds > 180 {
		return fmt.Errorf("scan budget_seconds must be between 15 and 180")
	}
	if scan.CandidateLimit != 0 && (scan.CandidateLimit < 32 || scan.CandidateLimit > 8192) {
		return fmt.Errorf("scan candidate_limit must be 0 (all sampled /24s) or between 32 and 8192")
	}
	if scan.TCPConcurrency < 1 || scan.TCPConcurrency > 256 {
		return fmt.Errorf("scan tcp_concurrency must be between 1 and 256")
	}
	if scan.TCPAttempts < 1 || scan.TCPAttempts > 5 {
		return fmt.Errorf("scan tcp_attempts must be between 1 and 5")
	}
	if scan.TCPTimeoutMS < 200 || scan.TCPTimeoutMS > 5000 {
		return fmt.Errorf("scan tcp_timeout_ms must be between 200 and 5000")
	}
	if scan.MaxLatencyMS < 20 || scan.MaxLatencyMS > 9999 {
		return fmt.Errorf("scan max_latency_ms must be between 20 and 9999")
	}
	if scan.MaxLossRate < 0 || scan.MaxLossRate > 1 {
		return fmt.Errorf("scan max_loss_rate must be between 0 and 1")
	}
	if scan.HTTPSCandidateCount < 1 || scan.HTTPSCandidateCount > 64 {
		return fmt.Errorf("scan https_candidate_count must be between 1 and 64")
	}
	if scan.HTTPTimeoutMS < 500 || scan.HTTPTimeoutMS > 10000 {
		return fmt.Errorf("scan http_timeout_ms must be between 500 and 10000")
	}
	if scan.DownloadCandidateCount < 0 || scan.DownloadCandidateCount > 20 {
		return fmt.Errorf("scan download_candidate_count must be between 0 and 20")
	}
	if scan.DownloadSeconds < 1 || scan.DownloadSeconds > 10 {
		return fmt.Errorf("scan download_seconds must be between 1 and 10")
	}
	if scan.DownloadMaxBytes < 64<<20 || scan.DownloadMaxBytes > 512<<20 {
		return fmt.Errorf("scan download_max_bytes must be between 64 MiB and 512 MiB")
	}
	if scan.MinDownloadMbps < 0 || scan.MinDownloadMbps > 10000 {
		return fmt.Errorf("scan min_download_mbps must be between 0 and 10000")
	}
	if scan.MinDownloadMbps > 0 && scan.DownloadCandidateCount == 0 {
		return fmt.Errorf("scan download_candidate_count must be greater than 0 when min_download_mbps is enabled")
	}
	return nil
}

func validateTarget(target Target) error {
	if target.Domain == "" || len(target.Domain) > 253 {
		return fmt.Errorf("target domain is empty or too long")
	}
	if ip := net.ParseIP(target.Domain); ip != nil {
		return fmt.Errorf("target %q must be a domain, not an IP address", target.Domain)
	}
	labels := strings.Split(target.Domain, ".")
	if len(labels) < 2 {
		return fmt.Errorf("target %q must be a fully qualified domain", target.Domain)
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("target %q is not a valid domain", target.Domain)
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return fmt.Errorf("target %q is not a valid ASCII domain", target.Domain)
			}
		}
	}
	if !strings.HasPrefix(target.TestPath, "/") {
		return fmt.Errorf("target %q test_path must begin with /", target.Domain)
	}
	parsed, err := url.Parse(target.TestPath)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return fmt.Errorf("target %q test_path must be a relative HTTP path", target.Domain)
	}
	return nil
}

func normalizeDomain(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}
