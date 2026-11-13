package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/logfile"
	"open-mihomo-gateway/internal/runtime"
)

func TestDiagnosticLogsRedactsBeforeGroupingAndSeparatesServiceFailures(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	cfg.Mihomo.Secret = "private-policy"
	paths := runtime.NewPaths(cfg)
	os.MkdirAll(paths.LogDir, 0o700)
	var lines []string
	for _, port := range []int{1234, 5678} {
		lines = append(lines, fmt.Sprintf(`time="2026-11-10T04:00:00Z" level=warning msg="[TCP] dial private-policy (match Match/) 192.168.2.240:%d --> example.com:443 error: dial tcp: i/o timeout"`, port))
	}
	lines = append(lines, `level=error msg="Start TUN listening error: permission denied"`)
	os.WriteFile(paths.MihomoLog, []byte(strings.Join(lines, "\n")), 0o600)
	os.WriteFile(paths.SmartDNSLog, []byte("[2026-11-10 04:00:00][ERROR] resolve failed SERVFAIL\n"), 0o600)
	logs, info, signals := diagnosticLogs(paths, cfg)
	data, _ := json.Marshal(struct {
		Logs    any
		Signals any
	}{logs, signals})
	if strings.Contains(string(data), cfg.Mihomo.Secret) {
		t.Fatal("secret leaked through structured fields")
	}
	if info["mihomo"].SampledLines != 3 || signals.ConnectionFailures != 2 || signals.OtherWarnings != 2 {
		t.Fatalf("%+v %+v", info, signals)
	}
	var matched bool
	for _, g := range signals.Groups {
		if g.Kind == "connection" {
			matched = true
			if g.Count != 2 || g.SourceIP != "192.168.2.240" || g.Target != "example.com:443" || g.Policy != "[REDACTED]" || g.Reason != "timeout" {
				t.Fatalf("%+v", g)
			}
		}
	}
	if !matched {
		t.Fatal("did not parse connection failures")
	}
}

func TestDiagnosticSampleIsBoundedAndGroupsDeterministic(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	paths := runtime.NewPaths(cfg)
	os.MkdirAll(paths.LogDir, 0o700)
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "level=warning msg=\"[TCP] dial DIRECT (match Match/) 192.168.2.240:5000 --> example%d.com:443 error: connection refused\"\n", i)
	}
	os.WriteFile(paths.MihomoLog, []byte(b.String()), 0o600)
	logs, info, signals := diagnosticLogs(paths, cfg)
	if len(logs["mihomo"]) != 80 || !info["mihomo"].Truncated || signals.SampledLines >= 5000 || len(signals.Groups) != 32 || signals.OmittedGroups <= 0 {
		t.Fatalf("logs=%d info=%+v signals=%+v", len(logs["mihomo"]), info, signals)
	}
	a, _ := json.Marshal(signals)
	_, _, again := diagnosticLogs(paths, cfg)
	c, _ := json.Marshal(again)
	if string(a) != string(c) {
		t.Fatal("nondeterministic groups")
	}
}

func TestLogMaintenanceSkipsLifecycleOperationAndStopsWithContext(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	paths := runtime.NewPaths(cfg)
	os.MkdirAll(paths.LogDir, 0o700)
	configPath := filepath.Join(cfg.Runtime.Dir, "config.yaml")
	os.WriteFile(configPath, []byte(config.Render(cfg)), 0o600)
	f, _ := os.Create(paths.MihomoLog)
	f.Truncate(logfile.MaxSize + 1)
	f.Close()
	s := &Server{configPath: configPath}
	lock, err := runtime.AcquireLifecycleLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.rotateRuntimeLogs(); err != runtime.ErrLifecycleOperationInProgress {
		t.Fatalf("did not skip busy lifecycle: %v", err)
	}
	lock.Release()
	if err := s.rotateRuntimeLogs(); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(paths.MihomoLog)
	if stat.Size() != 0 {
		t.Fatal("did not rotate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.maintainRuntimeLogs(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not stop")
	}
}

func TestServiceWarningsSurviveManyConnectionGroups(t *testing.T) {
	lines := []string{`level=error msg="Start TUN listening error: permission denied"`}
	for i := 0; i < 60; i++ {
		lines = append(lines, fmt.Sprintf(`level=warning msg="[UDP] dial DIRECT (match Match/) [fd00::1]:5000 --> example%d.com:443 error: connection refused"`, i))
	}
	result := summarizeDiagnosticSignals(map[string][]string{"mihomo": lines})
	if len(result.Groups) != 32 || result.OmittedGroups != 29 || result.Groups[0].Kind != "service" || result.ConnectionFailures != 60 {
		t.Fatalf("%+v", result)
	}
	if result.Groups[1].Protocol != "udp" || result.Groups[1].SourceIP != "fd00::1" {
		t.Fatalf("%+v", result.Groups[1])
	}
}

func TestDiagnosticReadErrorIsReportedSeparatelyFromEmptyLog(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	paths := runtime.NewPaths(cfg)
	if err := os.MkdirAll(paths.SmartDNSLog, 0o700); err != nil {
		t.Fatal(err)
	}
	logs, info, _ := diagnosticLogs(paths, cfg)
	if len(logs["smartdns"]) != 0 || info["smartdns"].ReadError == "" {
		t.Fatalf("read error hidden: %+v", info)
	}
}
