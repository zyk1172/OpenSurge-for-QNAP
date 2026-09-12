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

func TestNormalizeMigratesLegacyIPv6ConfigurationToOff(t *testing.T) {
	cfg := Default()
	cfg.DNS.IPv6 = true
	cfg.Transparent.TUNIPv6 = TUNIPv6Always
	cfg.Transparent.IPv6SharedL2Ready = true
	cfg.Transparent.IPv6PacketBrokerBinary = "native-linux-tun"
	cfg.Transparent.IPv6PacketMTU = 1500

	if err := Normalize(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.DNS.IPv6 {
		t.Fatal("DNS IPv6 remained enabled")
	}
	if cfg.Transparent.TUNIPv6 != TUNIPv6Off {
		t.Fatalf("TUNIPv6 = %q, want off", cfg.Transparent.TUNIPv6)
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
