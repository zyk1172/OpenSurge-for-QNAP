package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func qnapSameLANIPv6Config() Config {
	cfg := Default()
	cfg.Gateway.Mode = GatewayModeSameLAN
	cfg.Gateway.UpstreamInterface = cfg.Gateway.Interface
	cfg.DHCP.Enabled = false
	cfg.Transparent.Mode = TransparentModeTUN
	cfg.Transparent.TUNIPv6 = TUNIPv6Always
	cfg.Transparent.IPv6SharedL2Ready = true
	cfg.Transparent.IPv6PacketBrokerBinary = NativeLinuxIPv6Runtime
	cfg.Transparent.IPv6PacketMTU = 1500
	return cfg
}

func TestValidateQNAPSameLANManualIPv6(t *testing.T) {
	cfg := qnapSameLANIPv6Config()
	if err := Validate(cfg); err != nil {
		t.Fatalf("manual same-LAN IPv6 rejected: %v", err)
	}
}

func TestValidateQNAPSameLANAutomaticIPv6(t *testing.T) {
	cfg := qnapSameLANIPv6Config()
	cfg.Transparent.IPv6RAEnabled = true
	if err := Validate(cfg); err != nil {
		t.Fatalf("automatic same-LAN IPv6 rejected: %v", err)
	}
}

func TestValidateQNAPSameLANAutomaticIPv6RequiresReadiness(t *testing.T) {
	cfg := qnapSameLANIPv6Config()
	cfg.Transparent.IPv6RAEnabled = true
	cfg.Transparent.IPv6SharedL2Ready = false
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "ipv6_shared_l2_ready") {
		t.Fatalf("Validate() error = %v, want shared-L2 readiness error", err)
	}
}

func TestValidateQNAPIPv6RARequiresTakeover(t *testing.T) {
	cfg := qnapSameLANIPv6Config()
	cfg.Transparent.TUNIPv6 = TUNIPv6Off
	cfg.Transparent.IPv6RAEnabled = true
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "requires transparent.tun_ipv6") {
		t.Fatalf("Validate() error = %v, want takeover requirement", err)
	}
}

func TestValidateQNAPIPv6RAOnlySupportedOnSameLAN(t *testing.T) {
	cfg := qnapSameLANIPv6Config()
	cfg.Gateway.Mode = GatewayModeIsolatedLAN
	cfg.DHCP.Enabled = true
	cfg.Transparent.IPv6RAEnabled = true
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "only supported in gateway.mode same_lan") {
		t.Fatalf("Validate() error = %v, want same-LAN restriction", err)
	}
}

func TestRenderLoadPreservesQNAPIPv6RAEnabled(t *testing.T) {
	cfg := qnapSameLANIPv6Config()
	cfg.Transparent.IPv6RAEnabled = true
	path := filepath.Join(t.TempDir(), "opensurge.yaml")
	if err := os.WriteFile(path, []byte(Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !loaded.Transparent.IPv6RAEnabled {
		t.Fatal("IPv6RAEnabled was lost across render/load")
	}
	if !loaded.Transparent.IPv6SharedL2Ready {
		t.Fatal("IPv6SharedL2Ready was lost across render/load")
	}
}
