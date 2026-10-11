package smartdns

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
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
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/runtime"
)

func TestEncryptedResolversInPinnedSmartDNS(t *testing.T) {
	if os.Getenv("OPEN_SURGE_SMARTDNS_INTEGRATION_TESTS") != "1" {
		t.Skip("requires pinned SmartDNS in a disposable Linux container")
	}
	if _, err := exec.LookPath("smartdns"); err != nil {
		t.Fatal(err)
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
		t.Fatal("fixture requires a non-loopback IPv4 in its disposable namespace")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "resolver.test"}, DNSNames: []string{"resolver.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	answer := func(query []byte) []byte {
		var request dnsmessage.Message
		if err := request.Unpack(query); err != nil {
			return nil
		}
		response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, RecursionAvailable: true}, Questions: request.Questions}
		if len(request.Questions) > 0 {
			response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: request.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 30}, Body: &dnsmessage.AResource{A: [4]byte{203, 0, 113, 10}}}}
		}
		packed, _ := response.Pack()
		return packed
	}
	for _, transport := range []string{"https", "tls"} {
		t.Run(transport, func(t *testing.T) {
			listener, err := net.Listen("tcp", "0.0.0.0:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			var queries atomic.Int32
			if transport == "https" {
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/dns-query" {
						http.NotFound(w, r)
						return
					}
					data, err := io.ReadAll(r.Body)
					if err != nil {
						return
					}
					queries.Add(1)
					w.Header().Set("Content-Type", "application/dns-message")
					_, _ = w.Write(answer(data))
				}))
				_ = server.Listener.Close()
				server.Listener = listener
				server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
				server.StartTLS()
				t.Cleanup(server.Close)
			} else {
				listener = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
				t.Cleanup(func() { _ = listener.Close() })
				go func() {
					for {
						conn, err := listener.Accept()
						if err != nil {
							return
						}
						go func() {
							defer conn.Close()
							_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
							for {
								var size [2]byte
								if _, err := io.ReadFull(conn, size[:]); err != nil {
									return
								}
								data := make([]byte, binary.BigEndian.Uint16(size[:]))
								if _, err := io.ReadFull(conn, data); err != nil {
									return
								}
								queries.Add(1)
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
			plain, err := net.ListenPacket("udp", "0.0.0.0:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = plain.Close() })
			var plainQueries atomic.Int32
			go func() {
				buffer := make([]byte, 4096)
				for {
					count, peer, err := plain.ReadFrom(buffer)
					if err != nil {
						return
					}
					plainQueries.Add(1)
					_, _ = plain.WriteTo(answer(buffer[:count]), peer)
				}
			}()
			for _, identity := range []struct {
				name, hostname                    string
				wantAnswer, allowPlain, wantPlain bool
			}{
				{"valid certificate", "resolver.test", true, false, false},
				{"wrong TLS identity", "wrong.test", false, false, false},
				{"untrusted CA", "resolver.test", false, false, false},
				{"fast plaintext cannot beat valid TLS", "resolver.test", true, true, false},
				{"explicit plaintext fallback after TLS failure", "wrong.test", true, true, true},
			} {
				t.Run(identity.name, func(t *testing.T) {
					cfg := config.Default()
					cfg.Gateway.Mode = config.GatewayModeSameLAN
					cfg.DHCP.Enabled = false
					cfg.Runtime.Dir = t.TempDir()
					cfg.Gateway.LANIP = "192.0.2.240"
					cfg.DNS.Listen = cfg.Gateway.LANIP
					cfg.DNS.ResolverMode = "custom"
					endpoint := fmt.Sprintf("%s://%s:%d", transport, identity.hostname, port)
					if transport == "https" {
						endpoint += "/dns-query"
					}
					cfg.DNS.ResolverUpstreams = []config.ResolverUpstream{{Address: endpoint, BootstrapIPv4: hostIP}}
					if identity.allowPlain {
						cfg.DNS.ResolverAllowPlaintext = true
						cfg.DNS.ResolverUpstreams = append(cfg.DNS.ResolverUpstreams, config.ResolverUpstream{Address: fmt.Sprintf("udp://%s:%d", hostIP, plain.LocalAddr().(*net.UDPAddr).Port)})
					}
					rendered, err := RenderConfigWithResolvers(cfg, runtime.NewPaths(cfg), []string{hostIP})
					if err != nil {
						t.Fatal(err)
					}
					dnsPort, err := validationLoopbackPort()
					if err != nil {
						t.Fatal(err)
					}
					rendered = rewriteValidationBinds(rendered, dnsPort)
					if identity.name != "untrusted CA" {
						rendered = strings.ReplaceAll(rendered, "ca-file /etc/ssl/certs/ca-certificates.crt", "ca-file "+caPath)
					}
					rendered = strings.ReplaceAll(rendered, "speed-check-mode tcp:443,tcp:80", "speed-check-mode none")
					conf := filepath.Join(cfg.Runtime.Dir, "smartdns.conf")
					logPath := filepath.Join(cfg.Runtime.Dir, "smartdns.log")
					if err := os.WriteFile(conf, []byte(rendered), 0600); err != nil {
						t.Fatal(err)
					}
					log, err := os.Create(logPath)
					if err != nil {
						t.Fatal(err)
					}
					command := exec.Command("smartdns", "-c", conf, "-f", "-x", "-p", "-")
					command.Stdout = log
					command.Stderr = log
					if err := command.Start(); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait(); _ = log.Close() })
					query := dnsmessage.Message{Header: dnsmessage.Header{ID: 42, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("proof.example."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
					data, err := query.Pack()
					if err != nil {
						t.Fatal(err)
					}
					before := queries.Load()
					plainBefore := plainQueries.Load()
					accepted := false
					deadline := time.Now().Add(4 * time.Second)
					for time.Now().Before(deadline) {
						time.Sleep(50 * time.Millisecond)
						conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", dnsPort))
						if err != nil {
							t.Fatal(err)
						}
						_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
						_, _ = conn.Write(data)
						buffer := make([]byte, 4096)
						count, readErr := conn.Read(buffer)
						_ = conn.Close()
						if readErr == nil {
							var response dnsmessage.Message
							if err := response.Unpack(buffer[:count]); err == nil && len(response.Answers) > 0 {
								accepted = true
								break
							}
						}
					}
					if accepted != identity.wantAnswer {
						output, _ := os.ReadFile(logPath)
						t.Fatalf("accepted=%t, want=%t; SmartDNS log:\n%s", accepted, identity.wantAnswer, output)
					}
					if identity.wantAnswer && !identity.wantPlain && queries.Load() <= before {
						t.Fatal("answer did not originate at the authenticated fixture")
					}
					if (!identity.wantAnswer || identity.wantPlain) && queries.Load() != before {
						t.Fatal("wrong TLS identity reached the DNS application")
					}
					if identity.wantPlain && plainQueries.Load() <= plainBefore {
						t.Fatal("explicit fallback did not use the authorized plaintext server")
					}
					if !identity.wantPlain && plainQueries.Load() != plainBefore {
						t.Fatal("plaintext server raced a working encrypted server or was used without authorization")
					}

				})
			}
		})
	}
}
