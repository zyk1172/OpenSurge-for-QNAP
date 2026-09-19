package smartdns

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/cloudflareopt"
	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/device"
	"open-mihomo-gateway/internal/runtime"
)

func TestSameLANDefaultsUnknownClientsToResolverAndRegisteredGatewayToMihomo(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Mode = config.GatewayModeSameLAN
	cfg.Gateway.Interface = "eth0"
	cfg.Gateway.UpstreamInterface = "eth0"
	cfg.Gateway.LANIP = "192.168.2.240"
	cfg.DNS.Listen = cfg.Gateway.LANIP
	cfg.DHCP.Enabled = false
	bundle, err := device.CompilePolicyBundle(device.PolicySet{
		Profiles: []device.Profile{{ID: "home", DefaultPolicies: []string{"DIRECT"}}},
		Devices: []device.ManagedDevice{{
			ID: "phone", MAC: "aa:bb:cc:dd:ee:01", IPv4: "192.168.2.101", Profile: "home",
			GatewayTarget: device.GatewayTargetOpenSurge,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.DevicePolicy.Bundle = &bundle

	rendered, err := RenderConfigWithResolvers(cfg, runtime.NewPaths(cfg), []string{"192.168.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"group-begin opensurge-gateway -inherit none",
		"group-begin opensurge-resolver -inherit none",
		"group-begin opensurge-local -inherit none",
		"bind 192.168.2.240:53 -group opensurge-resolver -force-aaaa-soa",
		"server 127.0.0.1:1053 -group opensurge-gateway -exclude-default-group",
		"server 192.168.2.1 -group opensurge-resolver -exclude-default-group",
		"server 127.0.0.1:5353 -group opensurge-local -exclude-default-group",
		"domain-rules /lan/ -nameserver opensurge-local -no-cache -no-serve-expired -group opensurge-gateway",
		"domain-rules /lan/ -nameserver opensurge-local -no-cache -no-serve-expired -group opensurge-resolver",
		"client-rules 192.168.2.101/32 -group opensurge-gateway -no-speed-check -no-cache -no-dualstack-selection -force-aaaa-soa -no-serve-expired",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("SmartDNS config missing %q:\n%s", want, rendered)
		}
	}
}

func TestDHCPTopologiesDefaultToGatewayAndRouterBypassGetsResolver(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Mode = config.GatewayModeSameWiFiDHCP
	cfg.Gateway.Interface = "eth0"
	cfg.Gateway.UpstreamInterface = "eth0"
	cfg.Gateway.LANIP = "192.168.1.20"
	cfg.DNS.Listen = cfg.Gateway.LANIP
	bundle, err := device.CompilePolicyBundle(device.PolicySet{
		Profiles: []device.Profile{{ID: "home", DefaultPolicies: []string{"DIRECT"}}},
		Devices: []device.ManagedDevice{{
			ID: "console", MAC: "aa:bb:cc:dd:ee:05", IPv4: "192.168.1.190", Profile: "home",
			GatewayTarget: device.GatewayTargetUpstreamRouter,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.DevicePolicy.Bundle = &bundle

	rendered, err := RenderConfigWithResolvers(cfg, runtime.NewPaths(cfg), []string{"192.168.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "bind 192.168.1.20:53 -group opensurge-gateway -no-speed-check -no-cache") {
		t.Fatalf("DHCP topology did not default to Gateway View:\n%s", rendered)
	}
	if !strings.Contains(rendered, "client-rules 192.168.1.190/32 -group opensurge-resolver -force-aaaa-soa") {
		t.Fatalf("router bypass device did not switch to Resolver View:\n%s", rendered)
	}
}

func TestResolverOverrideCanKeepAnOpenSurgeGatewayDeviceOnRealDNS(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Mode = config.GatewayModeSameLAN
	cfg.Gateway.Interface = "eth0"
	cfg.Gateway.UpstreamInterface = "eth0"
	cfg.Gateway.LANIP = "192.168.2.240"
	cfg.DNS.Listen = cfg.Gateway.LANIP
	cfg.DHCP.Enabled = false
	bundle, err := device.CompilePolicyBundle(device.PolicySet{
		Profiles: []device.Profile{{ID: "home", DefaultPolicies: []string{"DIRECT"}}},
		Devices: []device.ManagedDevice{{
			ID: "lab", MAC: "aa:bb:cc:dd:ee:02", IPv4: "192.168.2.102", Profile: "home",
			GatewayTarget: device.GatewayTargetOpenSurge, DNSView: device.DNSViewResolver,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.DevicePolicy.Bundle = &bundle
	rendered, err := RenderConfigWithResolvers(cfg, runtime.NewPaths(cfg), []string{"192.168.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "client-rules 192.168.2.102/32 -group opensurge-resolver -force-aaaa-soa") {
		t.Fatalf("resolver override missing:\n%s", rendered)
	}
}

func TestEffectiveMihomoRealHostsAreScopedToResolverView(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile.yaml")
	if err := os.WriteFile(profile, []byte(`mixed-port: 7890
proxies: []
proxy-groups: []
hosts:
  "cdn.example.com": "104.18.1.2"
  "*.gateway-only.example": "104.18.1.3"
  "alias.example.com": "target.example.com"
rules:
  - MATCH,DIRECT
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = profile
	cfg.Gateway.Mode = config.GatewayModeSameLAN
	cfg.Gateway.Interface = "eth0"
	cfg.Gateway.UpstreamInterface = "eth0"
	cfg.Gateway.LANIP = "192.168.2.240"
	cfg.DNS.Listen = cfg.Gateway.LANIP
	cfg.DHCP.Enabled = false

	rendered, err := RenderConfigWithResolvers(cfg, runtime.NewPaths(cfg), []string{"192.168.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "domain-rules /cdn.example.com/ -address 104.18.1.2 -group opensurge-resolver") {
		t.Fatalf("compatible host mapping was not compiled for Resolver View:\n%s", rendered)
	}
	for _, forbidden := range []string{"gateway-only.example", "alias.example.com/ -address"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("gateway-only Mihomo host leaked into Resolver View: %q\n%s", forbidden, rendered)
		}
	}
}

func TestRegisteredLANNamesAreSharedAcrossBothViews(t *testing.T) {
	cfg := config.Default()
	cfg.DHCP.Domain = "lan"
	bundle, err := device.CompilePolicyBundle(device.PolicySet{
		Profiles: []device.Profile{{ID: "home", DefaultPolicies: []string{"DIRECT"}}},
		Devices: []device.ManagedDevice{{
			ID: "nas", Name: "storage", MAC: "aa:bb:cc:dd:ee:03", IPv4: "192.168.50.10", Profile: "home",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.DevicePolicy.Bundle = &bundle
	rendered, err := RenderConfigWithResolvers(cfg, runtime.NewPaths(cfg), []string{"192.168.50.254"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"domain-rules /storage/ -address 192.168.50.10 -group opensurge-gateway",
		"domain-rules /storage/ -address 192.168.50.10 -group opensurge-resolver",
		"domain-rules /storage.lan/ -address 192.168.50.10 -group opensurge-gateway",
		"domain-rules /storage.lan/ -address 192.168.50.10 -group opensurge-resolver",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("local rule missing %q:\n%s", want, rendered)
		}
	}
}

func TestCloudflareOptimizerWinsResolverHostPrecedence(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile.yaml")
	base := []byte(`mixed-port: 7890
proxies: []
proxy-groups: []
hosts:
  "cdn.example.com": "192.0.2.10"
  "other.example.com": "192.0.2.20"
dns:
  fake-ip-filter: []
rules:
  - MATCH,DIRECT
`)
	optimized, err := cloudflareopt.ApplyResultsToProfile(base, []cloudflareopt.TargetResult{{
		Domain: "cdn.example.com",
		Selected: cloudflareopt.CandidateResult{IP: "104.18.42.212"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, optimized, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = profile
	cfg.Gateway.Mode = config.GatewayModeSameLAN
	cfg.Gateway.Interface = "eth0"
	cfg.Gateway.UpstreamInterface = "eth0"
	cfg.Gateway.LANIP = "192.168.2.240"
	cfg.DNS.Listen = cfg.Gateway.LANIP
	cfg.DHCP.Enabled = false

	rendered, err := RenderConfigWithResolvers(cfg, runtime.NewPaths(cfg), []string{"192.168.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "domain-rules /cdn.example.com/ -address 104.18.42.212 -group opensurge-resolver") {
		t.Fatalf("Cloudflare optimizer result did not reach Resolver View:\n%s", rendered)
	}
	if strings.Contains(rendered, "domain-rules /cdn.example.com/ -address 192.0.2.10") {
		t.Fatalf("underlying host mapping overrode Cloudflare optimizer result:\n%s", rendered)
	}
}
