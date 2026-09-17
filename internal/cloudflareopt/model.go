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
	ScheduleInterval = "interval"
	ScheduleCron     = "cron"
)

type Config struct {
	SchemaVersion int          `json:"schema_version"`
	Enabled       bool         `json:"enabled"`
	Schedule      Schedule     `json:"schedule"`
	Scan          ScanSettings `json:"scan"`
	Targets       []Target     `json:"targets"`
}

type Schedule struct {
	Mode      string `json:"mode"`
	EveryDays int    `json:"every_days,omitempty"`
	At        string `json:"at,omitempty"`
	Cron      string `json:"cron,omitempty"`
}

type ScanSettings struct {
	BudgetSeconds          int   `json:"budget_seconds"`
	CandidateLimit         int   `json:"candidate_limit"`
	TCPConcurrency         int   `json:"tcp_concurrency"`
	TCPAttempts            int   `json:"tcp_attempts"`
	TCPTimeoutMS           int   `json:"tcp_timeout_ms"`
	HTTPSCandidateCount    int   `json:"https_candidate_count"`
	HTTPTimeoutMS          int   `json:"http_timeout_ms"`
	DownloadCandidateCount int   `json:"download_candidate_count"`
	DownloadSeconds        int   `json:"download_seconds"`
	DownloadMaxBytes       int64 `json:"download_max_bytes"`
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

type State struct {
	SchemaVersion   int            `json:"schema_version"`
	Running         bool           `json:"running"`
	StartedAt       *time.Time     `json:"started_at,omitempty"`
	LastRunAt       *time.Time     `json:"last_run_at,omitempty"`
	NextRunAt       *time.Time     `json:"next_run_at,omitempty"`
	LastError       string         `json:"last_error,omitempty"`
	OverlayRevision string         `json:"overlay_revision,omitempty"`
	Results         []TargetResult `json:"results"`
}

type Response struct {
	Config Config `json:"config"`
	State  State  `json:"state"`
}

func DefaultConfig() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Enabled:       false,
		Schedule: Schedule{
			Mode:      ScheduleInterval,
			EveryDays: 7,
			At:        "04:00",
		},
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

func validateScan(scan ScanSettings) error {
	if scan.BudgetSeconds < 15 || scan.BudgetSeconds > 180 {
		return fmt.Errorf("scan budget_seconds must be between 15 and 180")
	}
	if scan.CandidateLimit < 32 || scan.CandidateLimit > 2048 {
		return fmt.Errorf("scan candidate_limit must be between 32 and 2048")
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
	if scan.HTTPSCandidateCount < 1 || scan.HTTPSCandidateCount > 64 {
		return fmt.Errorf("scan https_candidate_count must be between 1 and 64")
	}
	if scan.HTTPTimeoutMS < 500 || scan.HTTPTimeoutMS > 10000 {
		return fmt.Errorf("scan http_timeout_ms must be between 500 and 10000")
	}
	if scan.DownloadCandidateCount < 0 || scan.DownloadCandidateCount > 10 {
		return fmt.Errorf("scan download_candidate_count must be between 0 and 10")
	}
	if scan.DownloadSeconds < 1 || scan.DownloadSeconds > 10 {
		return fmt.Errorf("scan download_seconds must be between 1 and 10")
	}
	if scan.DownloadMaxBytes < 256<<10 || scan.DownloadMaxBytes > 64<<20 {
		return fmt.Errorf("scan download_max_bytes must be between 256 KiB and 64 MiB")
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
