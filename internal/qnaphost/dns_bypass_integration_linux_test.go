//go:build linux

package qnaphost

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// This runner stays inside the disposable test container namespace. Production
// continues to enter the mounted QNAP host namespace through nsenter.
type namespaceRunner struct{}

func (namespaceRunner) Run(ctx context.Context, _ []byte, name string, args ...string) ([]byte, error) {
	if name == "nsenter" {
		name, args = args[2], args[3:]
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func TestHostDNSBypassKernelRouting(t *testing.T) {
	if os.Getenv("OPEN_SURGE_QNAPHOST_NETWORK_TESTS") != "1" {
		t.Skip("requires an explicitly disposable network namespace")
	}
	ctx := t.Context()
	runner := namespaceRunner{}
	run := func(args ...string) []byte {
		t.Helper()
		out, err := runner.Run(ctx, nil, "ip", args...)
		if err != nil {
			t.Fatalf("ip %v: %s: %v", args, out, err)
		}
		return out
	}
	run("link", "add", "os-dns-main", "type", "veth", "peer", "name", "os-dns-proxy")
	t.Cleanup(func() { _, _ = runner.Run(context.Background(), nil, "ip", "link", "del", "os-dns-main") })
	for _, nic := range []string{"os-dns-main", "os-dns-proxy"} {
		run("link", "set", nic, "up")
	}
	run("addr", "add", "192.0.2.240/32", "dev", "os-dns-main")
	run("addr", "add", "198.51.100.240/32", "dev", "os-dns-main")
	run("route", "add", "203.0.113.0/24", "dev", "os-dns-main", "table", "main")
	run("route", "add", "default", "dev", "os-dns-proxy", "table", routeTableID, "proto", routeProtocol)
	priorities := defaultHostPriorities()
	udp, _, _, proxy := priorities.strings()
	run("rule", "add", "pref", proxy, "from", "192.0.2.240/32", "iif", "lo", "table", routeTableID)
	// An unrelated second-NIC DNS policy must survive cleanup at the same pref.
	run("rule", "add", "pref", udp, "from", "198.51.100.240/32", "iif", "lo", "ipproto", "udp", "dport", "53", "table", "main")
	manager := &Manager{runner: runner}
	if err := manager.installHostDNSBypassLocked(ctx, "192.0.2.240"); err != nil {
		t.Fatal(err)
	}
	lookup := func(proto, port string) string {
		return string(run("route", "get", "203.0.113.1", "from", "192.0.2.240", "ipproto", proto, "dport", port))
	}
	for _, proto := range []string{"udp", "tcp"} {
		if got := lookup(proto, "53"); !strings.Contains(got, "dev os-dns-main") {
			t.Fatalf("%s DNS not preserved: %s", proto, got)
		}
		if got := lookup(proto, "443"); !strings.Contains(got, "dev os-dns-proxy") {
			t.Fatalf("%s normal traffic escaped takeover: %s", proto, got)
		}
	}
	manager.cleanupHostDNSBypassLocked(ctx, run("rule", "show"), priorities)
	rules := string(run("rule", "show"))
	if strings.Contains(rules, "from 192.0.2.240 iif lo ipproto") {
		t.Fatalf("owned DNS rules survived cleanup: %s", rules)
	}
	if !strings.Contains(rules, "from 198.51.100.240 iif lo ipproto udp dport 53 lookup main") {
		t.Fatalf("unrelated source rule deleted: %s", rules)
	}
	for _, proto := range []string{"udp", "tcp"} {
		if got := lookup(proto, "53"); !strings.Contains(got, "dev os-dns-proxy") {
			t.Fatalf("%s cleanup did not restore takeover lookup: %s", proto, got)
		}
	}
}
