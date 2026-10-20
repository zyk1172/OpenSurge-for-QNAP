package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolverSettingsRoundTrip(t *testing.T) {
	cfg := Default()
	cfg.DNS.ResolverMode = "custom"
	cfg.DNS.ResolverAllowPlaintext = true
	cfg.DNS.ResolverProxyEnabled = true
	cfg.DNS.ResolverProxyTarget = "节点选择"
	cfg.DNS.MihomoUseSmartDNS = true
	cfg.DNS.ResolverUpstreams = []ResolverUpstream{{Address: "https://cloudflare-dns.com/dns-query", BootstrapIPv4: "1.1.1.1"}, {Address: "tls://dns.google:853", BootstrapIPv4: "8.8.8.8"}, {Address: "tcp://9.9.9.9:53"}}
	cfg.DNS.ProxyNodeUpstreams = []ResolverUpstream{{Address: "https://dns.alidns.com/dns-query", BootstrapIPv4: "223.5.5.5"}}
	path := filepath.Join(t.TempDir(), "opensurge.yaml")
	if err := os.WriteFile(path, []byte(Render(cfg)), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.DNS, cfg.DNS) {
		t.Fatalf("settings lost: %#v", loaded.DNS)
	}
}

func TestIndependentProxyNodeResolverValidation(t *testing.T) {
	cfg := Default()
	cfg.DNS.ResolverMode = "custom"
	cfg.DNS.ResolverProxyEnabled = true
	cfg.DNS.ResolverProxyTarget = "node"
	cfg.DNS.ResolverAllowPlaintext = true
	cfg.DNS.ResolverUpstreams = []ResolverUpstream{{Address: "tcp://9.9.9.9:53"}}
	valid := ResolverUpstream{Address: "https://dns.alidns.com/dns-query", BootstrapIPv4: "223.5.5.5"}
	cfg.DNS.ProxyNodeUpstreams = []ResolverUpstream{valid}
	if err := ValidateResolverConfig(cfg); err != nil {
		t.Fatalf("ordinary pool need not bootstrap a proxy: %v", err)
	}
	for _, invalid := range [][]ResolverUpstream{
		{{Address: "udp://223.5.5.5"}},
		{{Address: "tcp://223.5.5.5"}},
		{{Address: valid.Address}},
		{{Address: valid.Address, BootstrapIPv4: cfg.DNS.Listen}},
		{{Address: valid.Address + "?token=unsupported", BootstrapIPv4: valid.BootstrapIPv4}},
		{valid, valid},
		make([]ResolverUpstream, 17),
	} {
		cfg.DNS.ProxyNodeUpstreams = invalid
		if err := ValidateResolverConfig(cfg); err == nil {
			t.Fatalf("invalid node pool accepted: %#v", invalid)
		}
	}
}

func TestLegacyProxyNodeResolversDoNotAddProviders(t *testing.T) {
	cfg := Default()
	cfg.DNS.ResolverMode = "custom"
	cfg.DNS.MihomoUseSmartDNS = true
	cfg.DNS.ResolverAllowPlaintext = true
	encrypted := ResolverUpstream{Address: "tls://dns.google", BootstrapIPv4: "8.8.8.8"}
	cfg.DNS.ResolverUpstreams = []ResolverUpstream{encrypted, {Address: "tcp://9.9.9.9"}}
	if err := ValidateResolverConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ProxyNodeResolverUpstreams(cfg), []ResolverUpstream{encrypted}) || len(cfg.DNS.ProxyNodeUpstreams) != 0 {
		t.Fatal("legacy bootstrap changed providers or mutated saved configuration")
	}
	cfg.DNS.ProxyNodeUpstreams = []ResolverUpstream{{Address: "https://doh.pub/dns-query", BootstrapIPv4: "1.12.12.12"}}
	if !reflect.DeepEqual(ProxyNodeResolverUpstreams(cfg), cfg.DNS.ProxyNodeUpstreams) {
		t.Fatal("explicit node pool was mixed with ordinary resolvers")
	}
}

func TestSmartDNSIntegrationRejectsCyclesAndDirectProxyBypass(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(c *Config) { c.DNS.ResolverMode = "system" },
		func(c *Config) { c.DNS.ResolverProxyTarget = "" },
		func(c *Config) { c.DNS.ResolverProxyTarget = "DIRECT" },
		func(c *Config) { c.DNS.ResolverProxyTarget = "node\nproxy: DIRECT" },
		func(c *Config) { c.DNS.ResolverProxyTarget = `node\"name` },
		func(c *Config) {
			c.DNS.ResolverUpstreams = []ResolverUpstream{{Address: "8.8.8.8"}}
			c.DNS.ResolverAllowPlaintext = true
		},
		func(c *Config) {
			c.DNS.ResolverUpstreams = append(c.DNS.ResolverUpstreams, ResolverUpstream{Address: "udp://8.8.8.8"})
			c.DNS.ResolverAllowPlaintext = true
		},
		func(c *Config) { c.DNS.ResolverUpstreams[0].Address += "?token=unsupported" },
		func(c *Config) { c.Mihomo.MixedPort = SmartDNSProxyPort },
		func(c *Config) { c.DNS.Port = 5354 },
	} {
		cfg := Default()
		cfg.DNS.ResolverMode = "custom"
		cfg.DNS.ResolverUpstreams = []ResolverUpstream{{Address: "https://doh.pub/dns-query", BootstrapIPv4: "1.12.12.12"}}
		cfg.DNS.ResolverProxyEnabled = true
		cfg.DNS.ResolverProxyTarget = "节点选择"
		cfg.DNS.MihomoUseSmartDNS = true
		mutate(&cfg)
		if err := ValidateResolverConfig(cfg); err == nil {
			t.Fatalf("accepted unsupported integration: %#v", cfg.DNS)
		}
	}
}

func TestResolverSettingsRejectInvalidOrUnsafeUpstreams(t *testing.T) {
	for _, test := range []struct {
		name     string
		upstream ResolverUpstream
		allow    bool
	}{
		{"injected option", ResolverUpstream{Address: "https://dns.google/dns-query -no-check-certificate", BootstrapIPv4: "8.8.8.8"}, false},
		{"comment", ResolverUpstream{Address: "https://dns.google/dns-query#comment", BootstrapIPv4: "8.8.8.8"}, false},
		{"unacknowledged plaintext", ResolverUpstream{Address: "8.8.8.8"}, false},
		{"TLS needs hostname", ResolverUpstream{Address: "tls://8.8.8.8", BootstrapIPv4: "8.8.8.8"}, false},
		{"missing fixed IP", ResolverUpstream{Address: "https://dns.google/dns-query"}, false},
		{"namespace stub", ResolverUpstream{Address: "https://dns.google/dns-query", BootstrapIPv4: "127.0.0.11"}, false},
		{"MagicDNS", ResolverUpstream{Address: "tls://dns.google", BootstrapIPv4: "100.100.100.100"}, false},
		{"self", ResolverUpstream{Address: "tcp://192.168.2.241"}, true},
		{"unsupported scheme", ResolverUpstream{Address: "http://dns.google/dns-query", BootstrapIPv4: "8.8.8.8"}, false},
		{"DoT query", ResolverUpstream{Address: "tls://dns.google?x=yes", BootstrapIPv4: "8.8.8.8"}, false},
		{"bad port", ResolverUpstream{Address: "tcp://8.8.8.8:65536"}, true},
		{"invalid bootstrapping", ResolverUpstream{Address: "https://dns.google/dns-query", BootstrapIPv4: "8.8.8.8 -no-check-certificate"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Gateway.LANIP = "192.168.2.241"
			cfg.DNS.Listen = cfg.Gateway.LANIP
			cfg.DNS.ResolverMode = "custom"
			cfg.DNS.ResolverAllowPlaintext = test.allow
			cfg.DNS.ResolverUpstreams = []ResolverUpstream{test.upstream}
			if err := ValidateResolverConfig(cfg); err == nil {
				t.Fatal("accepted unsafe or invalid resolver")
			}
		})
	}
	cfg := Default()
	cfg.DNS.ResolverMode = "custom"
	if err := ValidateResolverConfig(cfg); err == nil {
		t.Fatal("empty custom list accepted")
	}
	cfg.DNS.ResolverUpstreams = []ResolverUpstream{{Address: "tls://dns.google", BootstrapIPv4: "8.8.8.8"}, {Address: "tls://dns.google", BootstrapIPv4: "8.8.8.8"}}
	if err := ValidateResolverConfig(cfg); err == nil {
		t.Fatal("duplicate accepted")
	}
}
