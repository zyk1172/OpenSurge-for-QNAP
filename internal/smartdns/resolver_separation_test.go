package smartdns

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/mihomo"
	"open-mihomo-gateway/internal/runtime"
)

func TestIndependentNodePoolNeverBecomesAnOrdinaryResolver(t *testing.T) {
	cfg := config.Default()
	cfg.DNS.ResolverMode = "custom"
	cfg.DNS.ResolverProxyEnabled, cfg.DNS.MihomoUseSmartDNS = true, true
	cfg.UpstreamProxy.Enabled = true
	cfg.UpstreamProxy.Server, cfg.UpstreamProxy.Port = "proxy-node.test", 18080
	cfg.DNS.ResolverProxyTarget = cfg.UpstreamProxy.Name
	cfg.DNS.ResolverUpstreams = []config.ResolverUpstream{
		{Address: "https://cloudflare-dns.com/dns-query", BootstrapIPv4: "1.1.1.1"},
		{Address: "https://dns.google/dns-query", BootstrapIPv4: "8.8.8.8"},
	}
	cfg.DNS.ProxyNodeUpstreams = []config.ResolverUpstream{{Address: "https://dns.alidns.com/dns-query", BootstrapIPv4: "223.5.5.5"}}
	engine, err := mihomo.RenderConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := yaml.Unmarshal([]byte(engine), &root); err != nil {
		t.Fatal(err)
	}
	dns := root["dns"].(map[string]any)
	if !reflect.DeepEqual(dns["proxy-server-nameserver"], []any{"https://dns.alidns.com/dns-query#DIRECT"}) ||
		!reflect.DeepEqual(dns["default-nameserver"], []any{"https://223.5.5.5:443/dns-query#DIRECT&name-cert-verify=dns.alidns.com"}) {
		t.Fatalf("node DNS leaked ordinary pool or lost authenticated bootstrap: %#v", dns)
	}
	if !reflect.DeepEqual(dns["nameserver"], []any{"udp://" + config.SmartDNSResolverAddr + "#DIRECT"}) {
		t.Fatal("ordinary Mihomo queries bypass SmartDNS")
	}
	smart, err := RenderConfigWithResolvers(cfg, runtime.NewPaths(cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(smart, "\n") {
		if strings.HasPrefix(line, "server-https ") && (!strings.Contains(line, " -proxy opensurge-dns") || strings.Contains(line, "dns.alidns.com")) {
			t.Fatalf("ordinary DNS gained direct/node fallback: %s", line)
		}
	}
	for _, upstream := range cfg.DNS.ResolverUpstreams {
		if !strings.Contains(smart, "server-https "+upstream.Address+" ") {
			t.Fatalf("ordinary upstream was removed: %s", upstream.Address)
		}
	}
}
