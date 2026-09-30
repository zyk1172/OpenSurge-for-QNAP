package dhcp

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/runtime"
)

func TestDNSMasqArgsUseProductionForegroundMode(t *testing.T) {
	got := dnsmasqArgs("/tmp/dnsmasq.conf", "/tmp/dnsmasq.log")
	want := []string{"--keep-in-foreground", "--log-facility=/tmp/dnsmasq.log", "--conf-file=/tmp/dnsmasq.conf"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dnsmasqArgs() = %#v, want %#v", got, want)
	}
}

func TestManagerStartResolvesRelativeConfigAndLogPaths(t *testing.T) {
	workDir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	argsFile := filepath.Join(workDir, "dnsmasq.args")
	t.Setenv("OPENSURGE_DNSMASQ_ARGS", argsFile)
	fakeDNSMasq := filepath.Join(workDir, "fake-dnsmasq")
	if err := os.WriteFile(fakeDNSMasq, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OPENSURGE_DNSMASQ_ARGS\"\nexec /bin/sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DHCP.Enabled = true
	cfg.DHCP.Binary = fakeDNSMasq
	cfg.Runtime.Dir = filepath.Join(".", "relative-runtime")
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		t.Fatal(err)
	}
	manager := New(cfg, paths)
	pid, err := manager.Start()
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Stop(pid); err != nil {
			t.Errorf("Stop() error = %v", err)
		}
	})

	var data []byte
	deadline := time.Now().Add(2 * time.Second)
	for {
		var err error
		data, err = os.ReadFile(argsFile)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	wantConfig, err := filepath.Abs(paths.DNSMasqConf)
	if err != nil {
		t.Fatal(err)
	}
	wantLog, err := filepath.Abs(paths.DNSMasqLog)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{"--keep-in-foreground", "--log-facility=" + wantLog, "--conf-file=" + wantConfig}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("dnsmasq process args = %#v, want %#v", args, want)
	}
}
