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

func TestNormalizeEnablesNativeLinuxIPv6TakeoverCompatibility(t *testing.T) {
	cfg := Default()
	cfg.Transparent.TUNIPv6 = TUNIPv6Always
	cfg.Transparent.IPv6PacketBrokerBinary = ""
	cfg.Transparent.IPv6PacketMTU = 0

	if err := Normalize(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Transparent.IPv6PacketBrokerBinary != "native-linux-tun" {
		t.Fatalf("IPv6PacketBrokerBinary = %q", cfg.Transparent.IPv6PacketBrokerBinary)
	}
	if cfg.Transparent.IPv6PacketMTU != 1500 {
		t.Fatalf("IPv6PacketMTU = %d, want 1500", cfg.Transparent.IPv6PacketMTU)
	}
}
