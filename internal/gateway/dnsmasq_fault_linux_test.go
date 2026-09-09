//go:build linux

package gateway

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/dhcp"
	"open-mihomo-gateway/internal/process"
	"open-mihomo-gateway/internal/runtime"
)

func TestDNSMasqCrashRecoveryLinux(t *testing.T) {
	if os.Getenv("OPEN_SURGE_DNSMASQ_FAULT_TESTS") != "1" {
		t.Skip("set OPEN_SURGE_DNSMASQ_FAULT_TESTS=1 inside an isolated Linux network namespace")
	}
	if os.Geteuid() != 0 {
		t.Fatal("fault injection requires root inside the disposable namespace")
	}
	dnsmasqBinary, err := exec.LookPath("dnsmasq")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("dig"); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	cfg := config.Default()
	cfg.Gateway.Mode = config.GatewayModeSameLAN
	cfg.Gateway.Interface = "os-gw-lan"
	cfg.Gateway.UpstreamInterface = "os-gw-lan"
	cfg.Gateway.LANIP = "10.77.1.1"
	cfg.Gateway.LANPrefixLen = 24
	cfg.Gateway.LANCIDR = "10.77.1.0/24"
	cfg.Gateway.UpstreamGateway = "10.77.1.254"
	cfg.DHCP.Binary = dnsmasqBinary
	cfg.DHCP.Enabled = false
	cfg.DNS.Listen = "10.77.1.1"
	cfg.DNS.Port = 53
	cfg.Runtime.Dir = filepath.Join(root, "runtime")
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		t.Fatal(err)
	}

	manager := dhcp.New(cfg, paths)
	if err := manager.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	firstPID, err := manager.Start()
	if err != nil {
		t.Fatalf("start first dnsmasq: %v", err)
	}
	defer func() {
		state, exists, _ := runtime.LoadState(paths.StateFile)
		if exists && state.PIDDNSMasq > 0 {
			_ = manager.Stop(state.PIDDNSMasq)
		} else {
			_ = manager.Stop(firstPID)
		}
	}()
	firstFingerprint, err := process.Fingerprint(firstPID)
	if err != nil || firstFingerprint == "" {
		t.Fatalf("fingerprint first dnsmasq: %q %v", firstFingerprint, err)
	}
	boot, err := runtime.CurrentBootSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.SaveState(paths.StateFile, runtime.State{
		PIDDNSMasq:                firstPID,
		DNSMasqProcessFingerprint: firstFingerprint,
		BootSessionID:             boot.ID,
		StartedAt:                 time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "opensurge.yaml")
	if err := os.WriteFile(configPath, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}

	proc, err := os.FindProcess(firstPID)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("kill first dnsmasq: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for process.IsAlive(firstPID) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if process.IsAlive(firstPID) {
		t.Fatalf("dnsmasq pid %d remained alive after SIGKILL", firstPID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := RestartDNSMasqConfig(ctx, configPath); err != nil {
		t.Fatalf("RestartDNSMasqConfig after crash: %v", err)
	}
	state, exists, err := runtime.LoadState(paths.StateFile)
	if err != nil || !exists {
		t.Fatalf("load replacement state exists=%v err=%v", exists, err)
	}
	if state.PIDDNSMasq <= 0 || state.PIDDNSMasq == firstPID {
		t.Fatalf("replacement pid = %d, first pid = %d", state.PIDDNSMasq, firstPID)
	}
	matches, err := process.MatchesFingerprint(state.PIDDNSMasq, state.DNSMasqProcessFingerprint)
	if err != nil || !matches {
		t.Fatalf("replacement ownership match=%v err=%v", matches, err)
	}

	queryCtx, queryCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer queryCancel()
	out, err := exec.CommandContext(queryCtx, "dig", "+short", "@10.77.1.1", "localhost", "A").CombinedOutput()
	if err != nil {
		t.Fatalf("dns query after recovery failed: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "127.0.0.1") {
		t.Fatalf("unexpected localhost answer after recovery: %q", out)
	}
}
