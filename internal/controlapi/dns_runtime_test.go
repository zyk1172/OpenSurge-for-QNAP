package controlapi

import (
	"reflect"
	"testing"
)

func TestParseDNSRuntimeUsesAppliedSmartDNSConfiguration(t *testing.T) {
	conf := []byte("bind 192.168.2.241:53 -group opensurge-resolver\n" +
		"bind-tcp 192.168.2.241:53 -group opensurge-resolver\n" +
		"server 127.0.0.1:1053 -group opensurge-gateway -exclude-default-group\n" +
		"server 192.168.2.1 -group opensurge-resolver -exclude-default-group\n" +
		"server 223.5.5.5 -group opensurge-resolver -exclude-default-group\n" +
		"server 127.0.0.1:5353 -group opensurge-local -exclude-default-group\n")
	got := parseDNSRuntime(conf)
	if !got.Configured || got.Listen != "192.168.2.241:53" || got.DefaultView != "resolver" ||
		got.GatewayUpstream != "127.0.0.1:1053" ||
		!reflect.DeepEqual(got.ResolverUpstreams, []string{"192.168.2.1", "223.5.5.5"}) {
		t.Fatalf("applied SmartDNS view parsed incorrectly: %#v", got)
	}
}

func TestParseDNSRuntimeDoesNotClaimEmptyOrPartialConfigApplied(t *testing.T) {
	for _, data := range []string{"", "# empty config\n", "bind 192.168.2.241:53 -group opensurge-resolver\n"} {
		if got := parseDNSRuntime([]byte(data)); got.Configured {
			t.Fatalf("partial configuration claimed applied: %#v", got)
		}
	}
}

func TestDNSRuntimeIncludesEncryptedAndFallbackUpstreams(t *testing.T) {
	data := []byte("bind 192.168.2.241:53 -group opensurge-resolver\n" +
		"bind 127.0.0.1:5354 -group opensurge-resolver\n" +
		"proxy-server socks5://127.0.0.1:7854 -name opensurge-dns\n" +
		"server 127.0.0.1:1053 -group opensurge-gateway\n" +
		"server-https https://cloudflare-dns.com/dns-query -group opensurge-resolver -host-ip 1.1.1.1 -tls-host-verify cloudflare-dns.com\n" +
		"server-tls dns.google:853 -group opensurge-resolver -host-ip 8.8.8.8\n" +
		"server-tcp 9.9.9.9:53 -group opensurge-resolver -fallback\n")
	got := parseDNSRuntime(data)
	if !got.Configured || got.Listen != "192.168.2.241:53" || got.MihomoResolverListen != "127.0.0.1:5354" || got.ResolverProxy != "socks5://127.0.0.1:7854" || !reflect.DeepEqual(got.ResolverUpstreams, []string{"https://cloudflare-dns.com/dns-query", "tls://dns.google:853", "tcp://9.9.9.9:53"}) {
		t.Fatalf("lost encrypted upstreams: %#v", got)
	}
}
