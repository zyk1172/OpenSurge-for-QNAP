package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This gate needs no TUN, root, Internet DNS or host routes. It exercises the
// pinned real core's live config reload with in-memory Fake-IP allocation.
func TestPinnedCorePreservesFakeIPAcrossReload(t *testing.T) {
	if os.Getenv("OPEN_SURGE_MIHOMO_CORE_TESTS") != "1" {
		t.Skip("requires explicitly enabled pinned real core")
	}
	binary := os.Getenv("OPEN_SURGE_MIHOMO_INTEGRATION_BIN")
	if binary == "" {
		binary = "mihomo"
	}
	version, err := exec.Command(binary, "-v").CombinedOutput()
	if err != nil || !strings.Contains(string(version), "v1.19.33") {
		t.Fatalf("requires pinned Mihomo v1.19.33: %v %s", err, version)
	}
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	api := tcp.Addr().String()
	_ = tcp.Close()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dns := udp.LocalAddr().String()
	_ = udp.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	config := fmt.Sprintf("external-controller: %q\nallow-lan: false\nipv6: false\nlog-level: warning\nprofile: {store-fake-ip: false}\ntun: {enable: false}\ndns:\n  enable: true\n  listen: %q\n  ipv6: false\n  enhanced-mode: fake-ip\n  fake-ip-range: 198.18.0.1/24\n  nameserver: ['127.0.0.1:9']\nrules: ['MATCH,DIRECT']\n", api, dns)
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "core.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-d", dir, "-f", path)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = log.Close()
		if t.Failed() {
			data, _ := os.ReadFile(filepath.Join(dir, "core.log"))
			t.Log(string(data))
		}
	})
	client := &http.Client{Timeout: time.Second}
	t.Cleanup(client.CloseIdleConnections)
	ready := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		response, err := client.Get("http://" + api + "/version")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatal("real core controller did not become ready")
	}
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", dns)
	}}
	lookup := func(name string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		ips, err := resolver.LookupIP(ctx, "ip4", name)
		if err != nil || len(ips) != 1 || !net.ParseIP("198.18.0.0").Mask(net.CIDRMask(24, 32)).Equal(ips[0].Mask(net.CIDRMask(24, 32))) {
			t.Fatalf("Fake-IP %s: %v %v", name, ips, err)
		}
		return ips[0].String()
	}
	allocations := map[string]string{}
	for cycle := 0; cycle < 4; cycle++ {
		name := fmt.Sprintf("allocation-%d.example.test", cycle)
		ip := lookup(name)
		for other, allocated := range allocations {
			if allocated == ip {
				t.Fatalf("address %s reused for %s and %s after reload", ip, other, name)
			}
		}
		allocations[name] = ip
		payload, _ := json.Marshal(map[string]string{"path": path})
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "http://"+api+"/configs?force=true", strings.NewReader(string(payload)))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("reload status=%d", response.StatusCode)
		}
		for domain, allocated := range allocations {
			if got := lookup(domain); got != allocated {
				t.Fatalf("mapping changed after reload: %s %s -> %s", domain, allocated, got)
			}
		}
	}
}
