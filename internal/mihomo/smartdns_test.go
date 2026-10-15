package mihomo

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/runtime"
)

func TestSmartDNSIntegrationOverridesImportedDNSWithoutLosingFakeIP(t *testing.T) {
	profile := `proxies:
  - {name: node, type: http, server: proxy.example.com, port: 443}
proxy-groups:
  - {name: "节点选择", type: select, proxies: [node]}
rules: ["MATCH,DIRECT"]
hosts:
  app.test: 192.0.2.10
dns:
  nameserver: [192.0.2.1]
  fallback: [192.0.2.2]
  default-nameserver: [192.0.2.3]
  direct-nameserver: [192.0.2.4]
  nameserver-policy: {"+.test": 192.0.2.5}
  proxy-server-nameserver: [192.0.2.6]
  proxy-server-nameserver-policy: {"+.example.com": 192.0.2.7}
  respect-rules: true
  fake-ip-filter: [app.test]
  fake-ip-filter-mode: blacklist
  use-hosts: false
`
	cfg := config.Default()
	cfg.Transparent.Mode = "tun"
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = filepath.Join(t.TempDir(), "profile.yaml")
	if err := os.WriteFile(cfg.Mihomo.Profile, []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.DNS.ResolverMode = "custom"
	cfg.DNS.ResolverUpstreams = []config.ResolverUpstream{{Address: "https://dns.alidns.com/dns-query", BootstrapIPv4: "223.5.5.5"}, {Address: "tls://dns.google:853", BootstrapIPv4: "8.8.8.8"}}
	cfg.DNS.ResolverProxyTarget = "节点选择"
	for _, proxy := range []bool{false, true} {
		for _, replace := range []bool{false, true} {
			cfg.DNS.ResolverProxyEnabled, cfg.DNS.MihomoUseSmartDNS = proxy, replace
			rendered, err := RenderConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := yaml.Unmarshal([]byte(rendered), &root); err != nil {
				t.Fatal(err)
			}
			dns := root["dns"].(map[string]any)
			if dns["enhanced-mode"] != "fake-ip" || !reflect.DeepEqual(dns["fake-ip-filter"], []any{"app.test"}) || dns["fake-ip-filter-mode"] != "blacklist" || !strings.Contains(rendered, "tcp://any:53") {
				t.Fatalf("fake-IP contract lost:\n%s", rendered)
			}
			hosts := root["hosts"].(map[string]any)
			if hosts["app.test"] != "192.0.2.10" {
				t.Fatalf("hosts lost: %#v", hosts)
			}
			if replace {
				if !reflect.DeepEqual(dns["nameserver"], []any{"udp://" + config.SmartDNSResolverAddr + "#DIRECT"}) || !reflect.DeepEqual(dns["direct-nameserver"], dns["nameserver"]) || dns["respect-rules"] != false {
					t.Fatalf("SmartDNS path not pinned: %#v", dns)
				}
				for _, key := range []string{"fallback", "nameserver-policy", "proxy-server-nameserver-policy"} {
					if _, ok := dns[key]; ok {
						t.Fatalf("subscription path %s survives", key)
					}
				}
			} else if !reflect.DeepEqual(dns["nameserver"], []any{"192.0.2.1"}) {
				t.Fatal("disabled override modified normal nameservers")
			}
			if proxy || replace {
				if !reflect.DeepEqual(dns["proxy-server-nameserver"], []any{"https://dns.alidns.com/dns-query#DIRECT", "tls://dns.google:853#DIRECT"}) || !reflect.DeepEqual(hosts["dns.alidns.com"], []any{"223.5.5.5"}) {
					t.Fatalf("independent bootstrap lost: %#v, %#v", dns, hosts)
				}
				if !strings.Contains(rendered, "https://223.5.5.5:443/dns-query#DIRECT&name-cert-verify=dns.alidns.com") || strings.Contains(rendered, "skip-cert-verify") {
					t.Fatal("bootstrap does not authenticate the chosen DNS hostname")
				}
			} else if !reflect.DeepEqual(dns["proxy-server-nameserver"], []any{"192.0.2.6"}) {
				t.Fatal("disabled switches changed node DNS")
			}
			found := false
			if listeners, ok := root["listeners"].([]any); ok {
				for _, value := range listeners {
					listener := value.(map[string]any)
					if listener["name"] == "open-surge/smartdns" {
						found = true
						if listener["listen"] != "127.0.0.1" || listener["proxy"] != "节点选择" || listener["port"] != config.SmartDNSProxyPort || listener["udp"] != false {
							t.Fatalf("unsafe DNS proxy: %#v", listener)
						}
					}
				}
			}
			if found != proxy {
				t.Fatalf("proxy listener exists=%v with enabled=%v", found, proxy)
			}
		}
	}
	cfg.DNS.ResolverProxyEnabled = true
	cfg.DNS.ResolverProxyTarget = "missing-node"
	if _, err := RenderConfig(cfg); err == nil {
		t.Fatal("unknown DNS proxy accepted")
	}
}

func TestSmartDNSProxyReadinessRequiresItsOwnListener(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	paths := runtime.NewPaths(cfg)
	if err := os.MkdirAll(filepath.Dir(paths.MihomoLog), 0700); err != nil {
		t.Fatal(err)
	}
	manager := New(cfg, paths)
	for _, test := range []struct {
		log       string
		wantError bool
	}{
		{"RESTful API listening at: 127.0.0.1:9090", true},
		{"SOCKS[other] proxy listening at: 127.0.0.1:7854", true},
		{"SOCKS[open-surge/smartdns] proxy listening at: 0.0.0.0:7854", true},
		{"Listener open-surge/smartdns listen err: address already in use", true},
		{"SOCKS[open-surge/smartdns] proxy listening at: 127.0.0.1:7854", true}, // stale log is not a socket
	} {
		if err := os.WriteFile(paths.MihomoLog, []byte(test.log), 0600); err != nil {
			t.Fatal(err)
		}
		if err := manager.waitForSmartDNSProxyListener(os.Getpid(), 10*time.Millisecond); (err != nil) != test.wantError {
			t.Fatalf("readiness for %q: %v", test.log, err)
		}
	}
}

func TestSmartDNSProxyReadinessWithoutLogAndRejectsWrongProtocol(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:7854")
	if err != nil {
		t.Fatalf("reserve isolated test listener: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	var response atomic.Uint32
	requests := make(chan net.Conn)
	go func() {
		defer close(requests)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			requests <- c
		}
	}()
	go func() {
		for c := range requests {
			_ = c.SetDeadline(time.Now().Add(time.Second))
			var greeting [3]byte
			_, _ = io.ReadFull(c, greeting[:])
			_, _ = c.Write([]byte{5, byte(response.Load())})
			_ = c.Close()
		}
	}()
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	paths := runtime.NewPaths(cfg)
	if err := os.MkdirAll(filepath.Dir(paths.MihomoLog), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.MihomoLog, nil, 0600); err != nil {
		t.Fatal(err)
	}
	m := New(cfg, paths)
	if err := m.waitForSmartDNSProxyListener(os.Getpid(), 2*time.Second); err != nil {
		t.Fatalf("owned SOCKS5 listener rejected with no INFO log: %v", err)
	}
	response.Store(255)
	if err := m.waitForSmartDNSProxyListener(os.Getpid(), 250*time.Millisecond); err == nil {
		t.Fatal("owned socket with rejected SOCKS5 negotiation was accepted")
	}
	if err := probeSOCKS5("127.0.0.1:1", 50*time.Millisecond); err == nil {
		t.Fatal("closed port accepted")
	}
}

func TestSmartDNSProxyTimeoutCarriesDeadline(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	err := New(cfg, runtime.NewPaths(cfg)).waitForSmartDNSProxyListener(os.Getpid(), 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout lost its type: %v", err)
	}
}
