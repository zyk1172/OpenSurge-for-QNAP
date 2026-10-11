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
	cfg.DNS.ResolverUpstreams = []ResolverUpstream{{Address: "https://cloudflare-dns.com/dns-query", BootstrapIPv4: "1.1.1.1"}, {Address: "tls://dns.google:853", BootstrapIPv4: "8.8.8.8"}, {Address: "tcp://9.9.9.9:53"}}
	path := filepath.Join(t.TempDir(), "opensurge.yaml")
	if err := os.WriteFile(path, []byte(Render(cfg)), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DNS.ResolverMode != "custom" || !loaded.DNS.ResolverAllowPlaintext || !reflect.DeepEqual(loaded.DNS.ResolverUpstreams, cfg.DNS.ResolverUpstreams) {
		t.Fatalf("settings lost: %#v", loaded.DNS)
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
