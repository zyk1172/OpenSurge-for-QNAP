package gateway

import (
	"context"
	"net"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/runtime"
)

type capabilityBackend struct {
	fakeBackend
	caps platform.Capabilities
}

func (b *capabilityBackend) Capabilities(context.Context) (platform.Capabilities, error) {
	return b.caps, nil
}

func qnapNoNFTCapabilities() platform.Capabilities {
	return platform.Capabilities{
		TUNDeviceNode:       true,
		CapNetAdmin:         true,
		CapNetRaw:           true,
		NFTables:            false,
		NFTablesJSON:        false,
		IProute2:            true,
		IPv4ForwardSysctl:   true,
		IPv4ForwardReady:    true,
		Missing:             map[string]string{"nftables": "QNAP kernel has no nf_tables netlink backend"},
	}
}

func sameLANTestManager(t *testing.T) Manager {
	t.Helper()
	cfg := config.Default()
	cfg.Gateway.Mode = config.GatewayModeSameLAN
	cfg.Gateway.Interface = "eth0"
	cfg.Gateway.UpstreamInterface = "eth0"
	cfg.Gateway.LANIP = "192.168.2.241"
	cfg.Gateway.LANCIDR = "192.168.2.0/24"
	cfg.Gateway.UpstreamGateway = "192.168.2.101"
	cfg.DHCP.Enabled = false
	cfg.Transparent.Mode = config.TransparentModeTUN
	return Manager{
		cfg:   cfg,
		paths: runtime.NewPaths(cfg),
		deps: gatewayDeps{
			interfaceByName: func(name string) (*net.Interface, error) {
				return &net.Interface{Name: strings.TrimSpace(name)}, nil
			},
		},
	}
}

func TestPreflightSameLANAllowsTUNWithoutNFTables(t *testing.T) {
	manager := sameLANTestManager(t)
	backend := &capabilityBackend{caps: qnapNoNFTCapabilities()}
	if err := manager.preflight(context.Background(), backend, &fakeDHCP{}, &fakeMihomo{}, manager.deps); err != nil {
		t.Fatalf("same-LAN preflight rejected a TUN/iproute2-capable QNAP without nftables: %v", err)
	}
}

func TestPreflightIsolatedLANStillRequiresNFTables(t *testing.T) {
	manager := sameLANTestManager(t)
	manager.cfg.Gateway.Mode = config.GatewayModeIsolatedLAN
	manager.cfg.Gateway.UpstreamInterface = "eth1"
	backend := &capabilityBackend{caps: qnapNoNFTCapabilities()}
	err := manager.preflight(context.Background(), backend, &fakeDHCP{}, &fakeMihomo{}, manager.deps)
	if err == nil {
		t.Fatal("isolated-LAN preflight unexpectedly accepted a kernel without nftables")
	}
	if !strings.Contains(err.Error(), "nftables") {
		t.Fatalf("isolated-LAN preflight error = %q, want nftables requirement", err)
	}
}
