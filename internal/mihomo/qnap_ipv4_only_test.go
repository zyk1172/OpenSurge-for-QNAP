package mihomo

import (
	"path/filepath"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/runtime"
)

func TestQNAPManagerSuppressesLegacyDNSIPv6WhenTUNIPv6IsOff(t *testing.T) {
	cfg := config.Default()
	cfg.DNS.IPv6 = true
	cfg.Transparent.Mode = config.TransparentModeTUN
	cfg.Transparent.TUNIPv6 = config.TUNIPv6Off
	cfg.Runtime.Dir = t.TempDir()
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")

	manager := New(cfg, runtime.NewPaths(cfg))
	if manager.cfg.DNS.IPv6 {
		t.Fatal("QNAP runtime kept legacy DNS IPv6 enabled")
	}
	rendered, err := RenderConfig(manager.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "ipv6: false") {
		t.Fatalf("QNAP runtime did not render IPv6 disabled:\n%s", rendered)
	}
	if strings.Contains(rendered, "fake-ip-range6:") || strings.Contains(rendered, "inet6-address:") {
		t.Fatalf("QNAP runtime rendered an IPv6 data plane:\n%s", rendered)
	}
}
