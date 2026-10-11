package smartdns

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/runtime"
)

func TestResolverCacheRestartInPinnedSmartDNS(t *testing.T) {
	if os.Getenv("OPEN_SURGE_SMARTDNS_INTEGRATION_TESTS") != "1" {
		t.Skip("requires pinned SmartDNS in a disposable Linux container")
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var hostIP string
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.To4() != nil && !ip.IsLoopback() {
			hostIP = ip.String()
			break
		}
	}
	if hostIP == "" {
		t.Fatal("fixture requires a non-loopback IPv4")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "resolver.test"}, DNSNames: []string{"resolver.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	var realQueries, fakeQueries atomic.Int32
	var nextIP atomic.Uint32
	nextIP.Store(10)
	answer := func(data []byte, ip [4]byte) []byte {
		var request dnsmessage.Message
		if request.Unpack(data) != nil || len(request.Questions) == 0 {
			return nil
		}
		response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, RecursionAvailable: true}, Questions: request.Questions,
			Answers: []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: request.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 600}, Body: &dnsmessage.AResource{A: ip}}}}
		packed, _ := response.Pack()
		return packed
	}
	port := startProxyDNSFixture(t, "https", tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, func(query []byte) []byte {
		realQueries.Add(1)
		return answer(query, [4]byte{203, 0, 113, byte(nextIP.Load())})
	})
	fake, err := net.ListenPacket("udp", mihomoDNS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fake.Close() })
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, peer, err := fake.ReadFrom(buffer)
			if err != nil {
				return
			}
			fakeQueries.Add(1)
			_, _ = fake.WriteTo(answer(buffer[:n], [4]byte{198, 18, 0, byte(nextIP.Load())}), peer)
		}
	}()

	cfg := config.Default()
	cfg.Runtime.Dir = filepath.Join(t.TempDir(), "runtime cache") // quoted paths must work
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	cfg.Gateway.Mode = config.GatewayModeIsolatedLAN
	cfg.Gateway.LANIP = "192.0.2.240"
	cfg.DNS.Listen = cfg.Gateway.LANIP
	cfg.DNS.ResolverMode = "custom"
	cfg.DNS.ResolverUpstreams = []config.ResolverUpstream{{Address: fmt.Sprintf("https://resolver.test:%d/dns-query", port), BootstrapIPv4: hostIP}}
	cfg.DNS.MihomoUseSmartDNS = true
	cfg.DHCP.Enabled = false
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	lanPort, err := validationLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	lanAddress := fmt.Sprintf("127.0.0.1:%d", lanPort)
	prepare := func() Manager {
		manager := New(cfg, paths)
		if err := manager.WriteConfig(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(paths.SmartDNSConf)
		if err != nil {
			t.Fatal(err)
		}
		content := strings.ReplaceAll(string(data), "192.0.2.240:53", lanAddress)
		content = strings.ReplaceAll(content, "ca-file /etc/ssl/certs/ca-certificates.crt", "ca-file "+caPath)
		content = strings.ReplaceAll(content, "speed-check-mode tcp:443,tcp:80", "speed-check-mode none")
		if err := os.WriteFile(paths.SmartDNSConf, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return manager
	}
	manager := prepare()
	pid, err := manager.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop(pid) })
	if got := queryProxyDNS(config.SmartDNSResolverAddr, "warm-cache.example.", 3*time.Second); got != "203.0.113.10" {
		t.Fatalf("authenticated resolver answer = %q", got)
	}
	if got := queryProxyDNS(lanAddress, "fake-cache.example.", 3*time.Second); got != "198.18.0.10" {
		t.Fatalf("gateway fake-IP answer = %q", got)
	}
	if err := manager.Stop(pid); err != nil {
		t.Fatal(err)
	}
	pid = 0
	cache := renderedCacheFile(t, cfg)
	before, err := os.ReadFile(cache)
	if err != nil || len(before) == 0 {
		log, _ := os.ReadFile(paths.SmartDNSLog)
		t.Fatalf("cache was not persisted: %v\n%s", err, log)
	}
	queries := realQueries.Load()
	if err := manager.ValidateWrittenConfig(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(cache)
	if err != nil || !bytes.Equal(before, after) || realQueries.Load() != queries {
		t.Fatalf("configuration validation modified or prefetched the live cache: err=%v, bytes=%d -> %d, equal=%t, queries=%d -> %d", err, len(before), len(after), bytes.Equal(before, after), queries, realQueries.Load())
	}
	nextIP.Store(11)
	pid, err = manager.Start()
	if err != nil {
		t.Fatal(err)
	}
	if got := queryProxyDNS(config.SmartDNSResolverAddr, "warm-cache.example.", 3*time.Second); got != "203.0.113.10" || realQueries.Load() != queries {
		t.Fatalf("restart did not reuse the authenticated cache: %q, queries=%d -> %d", got, queries, realQueries.Load())
	}
	fakeBefore := fakeQueries.Load()
	if got := queryProxyDNS(lanAddress, "fake-cache.example.", 3*time.Second); got != "198.18.0.11" || fakeQueries.Load() <= fakeBefore {
		t.Fatal("Gateway View reused a persisted fake-IP")
	}
	if err := manager.Stop(pid); err != nil {
		t.Fatal(err)
	}
	pid = 0
	cfg.DNS.ResolverUpstreams[0].Address += "-v2"
	manager = prepare()
	pid, err = manager.Start()
	if err != nil {
		t.Fatal(err)
	}
	if got := queryProxyDNS(config.SmartDNSResolverAddr, "warm-cache.example.", 3*time.Second); got != "203.0.113.11" || realQueries.Load() <= queries {
		t.Fatal("changed upstream reused old cached answers")
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("obsolete cache was not pruned")
	}
}
