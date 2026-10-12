package smartdns

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/proxy"
	"gopkg.in/yaml.v3"
	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/mihomo"
	"open-mihomo-gateway/internal/process"
	"open-mihomo-gateway/internal/runtime"
)

// Uses both pinned engines in a disposable namespace, including a proxy node
// with a DNS hostname and rules that route ordinary loopback traffic DIRECT.
func TestSmartDNSProxyWithPinnedMihomo(t *testing.T) {
	if os.Getenv("OPEN_SURGE_SMARTDNS_INTEGRATION_TESTS") != "1" {
		t.Skip("requires pinned SmartDNS and Mihomo in a disposable namespace")
	}
	engine := os.Getenv("OPEN_SURGE_MIHOMO_INTEGRATION_BIN")
	if engine == "" {
		engine = "mihomo"
	}
	version, err := exec.Command(engine, "-v").CombinedOutput()
	if err != nil || !strings.Contains(string(version), "v1.19.30") {
		t.Fatalf("requires pinned Mihomo v1.19.30: %v %s", err, version)
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var hostIP string
	for _, addr := range addresses {
		ip, _, err := net.ParseCIDR(addr.String())
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
	cert := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "resolver.test"}, DNSNames: []string{"resolver.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	for _, variant := range []struct {
		name, transport string
		proxy, replace  bool
		conflict        bool
	}{
		{"DoH proxy and replacement", "https", true, true, false},
		{"DoT proxy and replacement", "tls", true, true, false},
		{"replacement without proxy", "https", false, true, false},
		{"proxy without replacement", "https", true, false, false},
		{"proxy port conflict fails closed", "https", true, true, true},
	} {
		t.Run(variant.name, func(t *testing.T) {
			var queries sync.Map
			answer := func(data []byte) []byte {
				var request dnsmessage.Message
				if err := request.Unpack(data); err != nil || len(request.Questions) == 0 {
					return nil
				}
				question := request.Questions[0]
				counter, _ := queries.LoadOrStore(question.Name.String(), &atomic.Int32{})
				counter.(*atomic.Int32).Add(1)
				response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, RecursionAvailable: true}, Questions: request.Questions}
				if question.Type == dnsmessage.TypeA {
					ip := [4]byte{203, 0, 113, 10}
					if question.Name.String() == "proxy-node.test." {
						copy(ip[:], net.ParseIP(hostIP).To4())
					}
					response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 30}, Body: &dnsmessage.AResource{A: ip}}}
				}
				packed, _ := response.Pack()
				return packed
			}
			countQueries := func(name string) int32 {
				if counter, ok := queries.Load(name); ok {
					return counter.(*atomic.Int32).Load()
				}
				return 0
			}
			upstreamPort := startProxyDNSFixture(t, variant.transport, certificate, answer)
			var connects atomic.Int32
			var unavailable atomic.Bool
			var tunnels sync.Map
			closeTunnels := func() { tunnels.Range(func(conn, _ any) bool { _ = conn.(net.Conn).Close(); return true }) }
			proxyServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connects.Add(1)
				if r.Method != http.MethodConnect || unavailable.Load() {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				if r.Host != net.JoinHostPort(hostIP, fmt.Sprint(upstreamPort)) {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				upstream, err := net.DialTimeout("tcp", r.Host, time.Second)
				if err != nil {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				conn, buffered, err := w.(http.Hijacker).Hijack()
				if err != nil {
					_ = upstream.Close()
					return
				}
				tunnels.Store(conn, true)
				defer tunnels.Delete(conn)
				defer conn.Close()
				defer upstream.Close()
				_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
				_ = buffered.Flush()
				go func() { _, _ = io.Copy(upstream, buffered); _ = upstream.Close() }()
				_, _ = io.Copy(conn, upstream)
			}))
			_ = proxyServer.Listener.Close()
			proxyServer.Listener, err = net.Listen("tcp", "0.0.0.0:0")
			if err != nil {
				t.Fatal(err)
			}
			proxyPort := proxyServer.Listener.Addr().(*net.TCPAddr).Port
			proxyServer.Start()
			t.Cleanup(func() { closeTunnels(); proxyServer.Close() })
			cfg := config.Default()
			cfg.Runtime.Dir = t.TempDir()
			cfg.Gateway.LANIP, cfg.DNS.Listen = "192.0.2.240", "192.0.2.240"
			cfg.Gateway.UpstreamInterface = "eth0"
			cfg.Transparent.Mode = "off" // no real TUN or host route mutations
			cfg.DHCP.Enabled = false
			if !variant.replace {
				cfg.Gateway.Mode = config.GatewayModeSameLAN
			}
			profile := fmt.Sprintf(`proxies:
  - {name: node, type: http, server: proxy-node.test, port: %d}
proxy-groups:
  - {name: DNS-PROXY, type: select, proxies: [node]}
rules: ["MATCH,DIRECT"]
dns:
  nameserver: [192.0.2.254]
  fallback: [192.0.2.253]
  fallback-filter: {geoip: false}
  proxy-server-nameserver: [192.0.2.252]
  fake-ip-filter: [lookup.test, blocked.test]
`, proxyPort)
			cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
			cfg.Mihomo.Profile = filepath.Join(cfg.Runtime.Dir, "profile.yaml")
			if err := os.WriteFile(cfg.Mihomo.Profile, []byte(profile), 0600); err != nil {
				t.Fatal(err)
			}
			apiPort, err := validationLoopbackPort()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Mihomo.APIAddr = fmt.Sprintf("127.0.0.1:%d", apiPort)
			cfg.DNS.ResolverMode = "custom"
			cfg.DNS.ResolverProxyEnabled, cfg.DNS.MihomoUseSmartDNS = variant.proxy, variant.replace
			cfg.DNS.ResolverProxyTarget = "DNS-PROXY"
			endpoint := fmt.Sprintf("%s://resolver.test:%d", variant.transport, upstreamPort)
			if variant.transport == "https" {
				endpoint += "/dns-query"
			}
			cfg.DNS.ResolverUpstreams = []config.ResolverUpstream{{Address: endpoint, BootstrapIPv4: hostIP}}
			engineConfig, err := mihomo.RenderConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := yaml.Unmarshal([]byte(engineConfig), &root); err != nil {
				t.Fatal(err)
			}
			// This test CA is added only to disposable engine fixtures.
			root["tls"] = map[string]any{"custom-certifactes": []string{ca}}
			engineData, err := yaml.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			enginePath := filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
			if err := os.WriteFile(enginePath, engineData, 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command(engine, "-t", "-d", cfg.Runtime.Dir, "-f", enginePath).CombinedOutput(); err != nil {
				t.Fatalf("engine rejected rendered config: %v %s", err, output)
			}
			cfg.Mihomo.Binary, cfg.Mihomo.Config = engine, enginePath
			paths := runtime.NewPaths(cfg)
			if err := runtime.Ensure(paths); err != nil {
				t.Fatal(err)
			}
			manager := mihomo.New(cfg, paths)
			engineLog := paths.MihomoLog
			var occupied net.Listener
			if variant.conflict {
				occupied, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", config.SmartDNSProxyPort))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = occupied.Close() })
			}
			pid, err := manager.Start()
			if variant.conflict {
				if err == nil || !strings.Contains(err.Error(), "SmartDNS proxy listener failed") {
					t.Fatalf("occupied DNS proxy port was accepted: pid=%d err=%v", pid, err)
				}
				conn, dialErr := net.DialTimeout("tcp", occupied.Addr().String(), time.Second)
				if dialErr != nil {
					t.Fatalf("foreign listener was modified: %v", dialErr)
				}
				_ = conn.Close()
				return
			}
			if err != nil {
				data, _ := os.ReadFile(engineLog)
				t.Fatalf("Mihomo startup failed: %v\n%s", err, data)
			}
			t.Cleanup(func() { _ = process.StopPID(pid, time.Second) })
			deadline := time.Now().Add(5 * time.Second)
			ready := false
			for time.Now().Before(deadline) {
				conn, err := net.DialTimeout("tcp", cfg.Mihomo.APIAddr, 100*time.Millisecond)
				if err == nil {
					_ = conn.Close()
					ready = true
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !ready {
				data, _ := os.ReadFile(engineLog)
				t.Fatalf("Mihomo did not start: %s", data)
			}
			if variant.proxy {
				deadline := time.Now().Add(3 * time.Second)
				ready := false
				for time.Now().Before(deadline) {
					data, _ := os.ReadFile(engineLog)
					if strings.Contains(string(data), "SOCKS[open-surge/smartdns] proxy listening at: 127.0.0.1:7854") {
						ready = true
						break
					}
					time.Sleep(25 * time.Millisecond)
				}
				if !ready {
					data, _ := os.ReadFile(engineLog)
					t.Fatalf("dedicated DNS proxy did not listen: %s", data)
				}
				// SmartDNS has not started yet: resolving the node must still work.
				dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", config.SmartDNSProxyPort), nil, &net.Dialer{Timeout: 3 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				conn, err := dialer.Dial("tcp", net.JoinHostPort(hostIP, fmt.Sprint(upstreamPort)))
				if err != nil {
					data, _ := os.ReadFile(engineLog)
					t.Fatalf("node bootstrap depended on stopped SmartDNS: %v\n%s", err, data)
				}
				pool := x509.NewCertPool()
				pool.AppendCertsFromPEM([]byte(ca))
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				tlsConn := tls.Client(conn, &tls.Config{RootCAs: pool, ServerName: "resolver.test", MinVersion: tls.VersionTLS12})
				if err := tlsConn.Handshake(); err != nil {
					_ = conn.Close()
					data, _ := os.ReadFile(engineLog)
					t.Fatalf("encrypted upstream could not bootstrap before SmartDNS starts: %v\n%s", err, data)
				}
				_ = tlsConn.Close()
				if countQueries("proxy-node.test.") == 0 || connects.Load() == 0 {
					t.Fatalf("node DNS or pinned proxy was bypassed: node queries=%d, connects=%d", countQueries("proxy-node.test."), connects.Load())
				}
			}
			smartConfig, err := RenderConfigWithResolvers(cfg, runtime.NewPaths(cfg), nil)
			if err != nil {
				t.Fatal(err)
			}
			caPath := filepath.Join(cfg.Runtime.Dir, "ca.pem")
			if err := os.WriteFile(caPath, []byte(ca), 0600); err != nil {
				t.Fatal(err)
			}
			smartConfig = strings.ReplaceAll(smartConfig, "ca-file /etc/ssl/certs/ca-certificates.crt", "ca-file "+caPath)
			smartConfig = strings.ReplaceAll(smartConfig, "speed-check-mode tcp:443,tcp:80", "speed-check-mode none")
			dnsPort, err := validationLoopbackPort()
			if err != nil {
				t.Fatal(err)
			}
			front := fmt.Sprintf("127.0.0.1:%d", dnsPort)
			// Keep the dedicated resolver listener; rewrite only the LAN address.
			smartConfig = strings.ReplaceAll(smartConfig, fmt.Sprintf("%s:%d", cfg.DNS.Listen, cfg.DNS.Port), front)
			smartPath := filepath.Join(cfg.Runtime.Dir, "smartdns.conf")
			if err := os.WriteFile(smartPath, []byte(smartConfig), 0600); err != nil {
				t.Fatal(err)
			}
			smartLog := startProxyTestProcess(t, "smartdns", exec.Command("smartdns", "-c", smartPath, "-f", "-x", "-p", "-"), cfg.Runtime.Dir)
			if answer := queryProxyDNS(front, "lookup.test.", 5*time.Second); answer != "203.0.113.10" {
				m, _ := os.ReadFile(engineLog)
				s, _ := os.ReadFile(smartLog)
				t.Fatalf("real DNS through configured path = %q\nMihomo: %s\nSmartDNS: %s", answer, m, s)
			}
			if variant.replace {
				if answer := queryProxyDNS(front, "fake.test.", 2*time.Second); !strings.HasPrefix(answer, "198.18.") {
					t.Fatalf("fake-IP contract changed: %q", answer)
				}
				if answer := queryProxyDNS(config.SmartDNSResolverAddr, "real.test.", 2*time.Second); answer != "203.0.113.10" {
					t.Fatalf("private listener is not pinned to Resolver View: %q", answer)
				}
			}
			if variant.proxy {
				if connects.Load() < 2 {
					t.Fatal("SmartDNS did not query the upstream via Mihomo")
				}
				unavailable.Store(true)
				closeTunnels()
				if answer := queryProxyDNS(front, "blocked.test.", 4*time.Second); answer != "" {
					t.Fatalf("proxy outage returned an uncached answer: %s", answer)
				}
				// SmartDNS can back off reconnecting after its connection is lost.
				// The unavailable proxy and closed tunnels must never result in a
				// direct query, regardless of when the next retry is attempted.
				if countQueries("blocked.test.") != 0 {
					t.Fatal("failed proxy was bypassed by a direct DNS query")
				}
			} else if connects.Load() != 0 {
				t.Fatal("disabled proxy still forwarded DNS through a proxy")
			}
		})
	}
	t.Run("private resolver port conflict fails closed", func(t *testing.T) {
		udp, err := net.ListenPacket("udp", config.SmartDNSResolverAddr)
		if err != nil {
			t.Fatal(err)
		}
		defer udp.Close()
		tcp, err := net.Listen("tcp", config.SmartDNSResolverAddr)
		if err != nil {
			t.Fatal(err)
		}
		defer tcp.Close()
		cfg := config.Default()
		cfg.Runtime.Dir = t.TempDir()
		cfg.Gateway.LANIP, cfg.DNS.Listen = "192.0.2.240", "192.0.2.240"
		cfg.DHCP.Enabled = false
		cfg.DNS.ResolverMode, cfg.DNS.MihomoUseSmartDNS = "custom", true
		cfg.DNS.ResolverUpstreams = []config.ResolverUpstream{{Address: "https://resolver.test:1/dns-query", BootstrapIPv4: hostIP}}
		rendered, err := RenderConfigWithResolvers(cfg, runtime.NewPaths(cfg), nil)
		if err != nil {
			t.Fatal(err)
		}
		port, err := validationLoopbackPort()
		if err != nil {
			t.Fatal(err)
		}
		rendered = strings.ReplaceAll(rendered, fmt.Sprintf("%s:%d", cfg.DNS.Listen, cfg.DNS.Port), fmt.Sprintf("127.0.0.1:%d", port))
		path := filepath.Join(cfg.Runtime.Dir, "conflict.conf")
		if err := os.WriteFile(path, []byte(rendered), 0600); err != nil {
			t.Fatal(err)
		}
		log, err := os.Create(filepath.Join(cfg.Runtime.Dir, "conflict.log"))
		if err != nil {
			t.Fatal(err)
		}
		defer log.Close()
		command := exec.Command("smartdns", "-c", path, "-f", "-x", "-p", "-")
		command.Stdout, command.Stderr = log, log
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		select {
		case err := <-done:
			data, _ := os.ReadFile(log.Name())
			if err == nil || !strings.Contains(string(data), ":5354") {
				t.Fatalf("private resolver conflict not rejected: %v\n%s", err, data)
			}
		case <-time.After(2 * time.Second):
			_ = command.Process.Kill()
			<-done
			t.Fatal("SmartDNS continued running after its private resolver bind failed")
		}
	})
}

func startProxyTestProcess(t *testing.T, name string, command *exec.Cmd, dir string) string {
	t.Helper()
	path := filepath.Join(dir, name+".log")
	log, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait(); _ = log.Close() })
	return path
}

func queryProxyDNS(address, domain string, timeout time.Duration) string {
	name, err := dnsmessage.NewName(domain)
	if err != nil {
		return ""
	}
	message := dnsmessage.Message{Header: dnsmessage.Header{ID: 1234, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
	data, _ := message.Pack()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("udp", address, 100*time.Millisecond)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(250 * time.Millisecond))
		_, _ = conn.Write(data)
		buffer := make([]byte, 4096)
		n, err := conn.Read(buffer)
		_ = conn.Close()
		if err == nil {
			var reply dnsmessage.Message
			if reply.Unpack(buffer[:n]) == nil {
				for _, answer := range reply.Answers {
					if a, ok := answer.Body.(*dnsmessage.AResource); ok {
						return net.IP(a.A[:]).String()
					}
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ""
}

func startProxyDNSFixture(t *testing.T, transport string, cert tls.Certificate, answer func([]byte) []byte) int {
	t.Helper()
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if transport == "https" {
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var data []byte
			if r.Method == http.MethodGet {
				data, _ = base64.RawURLEncoding.DecodeString(r.URL.Query().Get("dns"))
			} else {
				data, _ = io.ReadAll(r.Body)
			}
			w.Header().Set("Content-Type", "application/dns-message")
			_, _ = w.Write(answer(data))
		}))
		_ = server.Listener.Close()
		server.Listener = listener
		server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		server.StartTLS()
		t.Cleanup(server.Close)
	} else {
		listener = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		t.Cleanup(func() { _ = listener.Close() })
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				go func() {
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
					for {
						var size [2]byte
						if _, err := io.ReadFull(conn, size[:]); err != nil {
							return
						}
						data := make([]byte, binary.BigEndian.Uint16(size[:]))
						if _, err := io.ReadFull(conn, data); err != nil {
							return
						}
						response := answer(data)
						binary.BigEndian.PutUint16(size[:], uint16(len(response)))
						if _, err := conn.Write(append(size[:], response...)); err != nil {
							return
						}
					}
				}()
			}
		}()
	}
	return port
}
