package smartdns

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/runtime"
)

func TestRewriteValidationBindsPreservesViewOptions(t *testing.T) {
	input := "bind 192.168.2.240:53 -group opensurge-resolver -force-aaaa-soa\n" +
		"bind-tcp 192.168.2.240:53 -group opensurge-resolver -force-aaaa-soa\n" +
		"server 127.0.0.1:1053 -group opensurge-gateway -exclude-default-group\n"
	got := rewriteValidationBinds(input, 53001)
	for _, want := range []string{
		"bind 127.0.0.1:53001 -group opensurge-resolver -force-aaaa-soa",
		"bind-tcp 127.0.0.1:53001 -group opensurge-resolver -force-aaaa-soa",
		"server 127.0.0.1:1053 -group opensurge-gateway -exclude-default-group",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rewritten validation config missing %q:\n%s", want, got)
		}
	}
}

func TestRenderedConfigLoadsInPinnedSmartDNS(t *testing.T) {
	if os.Getenv("OPEN_SURGE_SMARTDNS_INTEGRATION_TESTS") != "1" {
		t.Skip("set OPEN_SURGE_SMARTDNS_INTEGRATION_TESTS=1 in the Linux namespace lab")
	}
	if _, err := exec.LookPath("smartdns"); err != nil {
		t.Fatalf("smartdns integration test requires the pinned smartdns binary: %v", err)
	}

	cfg := config.Default()
	cfg.Gateway.Mode = config.GatewayModeSameLAN
	cfg.Gateway.Interface = "eth0"
	cfg.Gateway.UpstreamInterface = "eth0"
	cfg.Gateway.LANIP = "192.0.2.240"
	cfg.Gateway.UpstreamGateway = "192.0.2.1"
	cfg.DNS.Listen = cfg.Gateway.LANIP
	cfg.DHCP.Enabled = false
	cfg.Runtime.Dir = t.TempDir()
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		t.Fatal(err)
	}

	manager := New(cfg, paths)
	if err := manager.WriteConfig(); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if err := manager.ValidateWrittenConfig(); err != nil {
		t.Fatalf("generated SmartDNS config was rejected by pinned runtime: %v", err)
	}
}
