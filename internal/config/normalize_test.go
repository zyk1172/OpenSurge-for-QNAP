package config

import (
	"strings"
	"testing"
)

func TestNormalizeMaterializesDerivedLinuxValues(t *testing.T) {
	cfg := Default()
	cfg.Gateway.LANIP = "192.168.2.240"
	cfg.Gateway.LANPrefixLen = 24
	cfg.Gateway.LANCIDR = ""
	cfg.Transparent.RouteTableID = 20241
	cfg.Transparent.RouteRulePriority = 0

	if err := Normalize(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Gateway.LANCIDR != "192.168.2.0/24" {
		t.Fatalf("LANCIDR = %q, want 192.168.2.0/24", cfg.Gateway.LANCIDR)
	}
	if cfg.Transparent.RouteRulePriority != 20241 {
		t.Fatalf("RouteRulePriority = %d, want 20241", cfg.Transparent.RouteRulePriority)
	}
}

func TestNormalizeRejectsMacOSOnlyRoutingOwnership(t *testing.T) {
	cfg := Default()
	cfg.Transparent.TUNAutoRoute = true
	if err := Normalize(&cfg); err == nil || !strings.Contains(err.Error(), "tun_auto_route") {
		t.Fatalf("Normalize() error = %v, want tun_auto_route rejection", err)
	}
}

func TestNormalizePreservesLegacySystemProxyFieldUntilSchemaCleanup(t *testing.T) {
	cfg := Default()
	cfg.LocalSystemProxy.Enabled = true
	if err := Normalize(&cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.LocalSystemProxy.Enabled {
		t.Fatal("Normalize unexpectedly erased legacy local_system_proxy field")
	}
}

func TestNormalizeRetiresLegacyQNAPIPv6RuntimeMarkers(t *testing.T) {
	cfg := Default()
	cfg.DNS.IPv6 = true
	cfg.Transparent.TUNIPv6 = TUNIPv6Always
	cfg.Transparent.IPv6SharedL2Ready = true
	cfg.Transparent.IPv6PacketBrokerBinary = "native-linux-tun"
	cfg.Transparent.IPv6PacketMTU = 1500

	if err := Normalize(&cfg); err != nil {
		t.Fatal(err)
	}
	// Legacy schema values remain visible long enough for validation/API
	// compatibility. DNS IPv6 is operationally suppressed by the QNAP mihomo
	// Manager when TUN IPv6 is off; an auto/always TUN request is rejected by
	// validation because the retired runtime marker has been removed.
	if !cfg.DNS.IPv6 {
		t.Fatal("Normalize unexpectedly erased legacy DNS IPv6 schema value")
	}
	if cfg.Transparent.TUNIPv6 != TUNIPv6Always {
		t.Fatalf("TUNIPv6 = %q, want always preserved for validation", cfg.Transparent.TUNIPv6)
	}
	if cfg.Transparent.IPv6SharedL2Ready {
		t.Fatal("IPv6SharedL2Ready remained enabled")
	}
	if cfg.Transparent.IPv6PacketBrokerBinary != "" {
		t.Fatalf("IPv6PacketBrokerBinary = %q, want empty", cfg.Transparent.IPv6PacketBrokerBinary)
	}
	if cfg.Transparent.IPv6PacketMTU != 0 {
		t.Fatalf("IPv6PacketMTU = %d, want 0", cfg.Transparent.IPv6PacketMTU)
	}
}
