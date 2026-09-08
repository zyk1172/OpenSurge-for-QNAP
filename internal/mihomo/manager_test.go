package mihomo

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/runtime"
)

func TestConfigDirUsesGeneratedConfigDirForManagedMode(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Runtime.Dir = dir
	cfg.Mihomo.Config = filepath.Join(dir, "mihomo.yaml")

	manager := New(cfg, runtime.NewPaths(cfg))
	if got := manager.configDir(); got != dir {
		t.Fatalf("configDir() = %q, want %q", got, dir)
	}
}

func TestConfigDirUsesProfileDirForImportedMode(t *testing.T) {
	dir := t.TempDir()
	profileDir := filepath.Join(dir, "profile-home")
	cfg := config.Default()
	cfg.Runtime.Dir = filepath.Join(dir, "runtime")
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = filepath.Join(profileDir, "profile.yaml")

	manager := New(cfg, runtime.NewPaths(cfg))
	if got := manager.configDir(); got != profileDir {
		t.Fatalf("configDir() = %q, want %q", got, profileDir)
	}
}

func TestValidateConfigWithTimeoutReportsSlowEngine(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "mihomo")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'initializing geodata\\n'\nsleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := validateConfigWithTimeout(10*time.Millisecond, binary, dir, filepath.Join(dir, "mihomo.yaml"))
	if err == nil || !strings.Contains(err.Error(), "timed out after 10ms") {
		t.Fatalf("validateConfigWithTimeout() error = %v", err)
	}
}

func TestValidateConfigHonorsActionContext(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "mihomo")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := validateConfigContext(ctx, configValidationTimeout, binary, dir, filepath.Join(dir, "mihomo.yaml"))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("validator ignored enclosing action deadline: elapsed=%s err=%v", time.Since(started), err)
	}
}

func TestWaitForTUNWaitsForEnabledRuntimeState(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		enabled := requests.Add(1) >= 2
		_ = json.NewEncoder(w).Encode(map[string]any{"tun": map[string]any{"enable": enabled, "device": "utun123"}})
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Mihomo.APIAddr = server.URL
	cfg.Runtime.Dir = t.TempDir()
	manager := New(cfg, runtime.NewPaths(cfg))
	if err := os.MkdirAll(filepath.Dir(manager.paths.MihomoLog), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.paths.MihomoLog, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.waitForTUN(os.Getpid(), time.Second); err != nil {
		t.Fatal(err)
	}
	if requests.Load() < 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestWaitForTUNReturnsLoggedStartupFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tun":{"enable":false,"device":"utun123"}}`))
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Mihomo.APIAddr = server.URL
	cfg.Runtime.Dir = t.TempDir()
	manager := New(cfg, runtime.NewPaths(cfg))
	if err := os.MkdirAll(filepath.Dir(manager.paths.MihomoLog), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.paths.MihomoLog, []byte("[error] Start TUN listening error: configure tun interface: add route: 1.0.0.0/8: file exists\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := manager.waitForTUN(os.Getpid(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "1.0.0.0/8: file exists") {
		t.Fatalf("waitForTUN() error = %v", err)
	}
}

func TestStopStartedProcessAllowsGracefulTUNCleanup(t *testing.T) {
	var gotPID int
	var gotTimeout time.Duration
	manager := Manager{
		stopPID: func(pid int, timeout time.Duration) error {
			gotPID = pid
			gotTimeout = timeout
			return nil
		},
	}

	manager.stopStartedProcess(1234)

	if gotPID != 1234 || gotTimeout != 3*time.Second {
		t.Fatalf("stopStartedProcess() called with pid=%d timeout=%s", gotPID, gotTimeout)
	}
}

func TestStopRemovesOwnedIPv6PacketListenerSocket(t *testing.T) {
	// Keep the Unix socket path short on macOS while remaining portable to the
	// Linux CI runner, where /private/tmp does not exist.
	dir, err := os.MkdirTemp("/tmp", "os6-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	cfg := config.Default()
	cfg.Runtime.Dir = dir
	cfg.Transparent.TUNIPv6 = config.TUNIPv6Always
	paths := runtime.NewPaths(cfg)
	address := &net.UnixAddr{Name: paths.IPv6PacketSocket, Net: "unixgram"}
	listener, err := net.ListenUnixgram("unixgram", address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	manager := New(cfg, paths)
	manager.stopPID = func(int, time.Duration) error { return nil }

	if err := manager.Stop(1234); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(paths.IPv6PacketSocket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("IPv6 packet listener socket remained after stop: %v", err)
	}
}

func TestStopRefusesNonSocketIPv6PacketListenerPath(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	cfg.Transparent.TUNIPv6 = config.TUNIPv6Always
	paths := runtime.NewPaths(cfg)
	if err := os.WriteFile(paths.IPv6PacketSocket, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := New(cfg, paths)
	manager.stopPID = func(int, time.Duration) error { return nil }

	err := manager.Stop(1234)
	if err == nil || !strings.Contains(err.Error(), "refusing to remove non-socket") {
		t.Fatalf("Stop() error = %v", err)
	}
	if data, readErr := os.ReadFile(paths.IPv6PacketSocket); readErr != nil || string(data) != "keep" {
		t.Fatalf("non-socket listener path was changed: data=%q err=%v", data, readErr)
	}
}

func TestEnrichTUNRouteErrorIgnoresUnrelatedErrors(t *testing.T) {
	const detail = "configure tun interface: permission denied"
	if got := enrichTUNRouteError(detail); got != detail {
		t.Fatalf("enrichTUNRouteError() = %q", got)
	}
}

func TestPatchedMihomoAcceptsIPv6PacketListenerConfig(t *testing.T) {
	binary := os.Getenv("OPENSURGE_TEST_PATCHED_MIHOMO")
	if binary == "" {
		t.Skip("OPENSURGE_TEST_PATCHED_MIHOMO is not set")
	}
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Runtime.Dir = dir
	cfg.Mihomo.Binary = binary
	cfg.Mihomo.Config = filepath.Join(dir, "mihomo.yaml")
	cfg.DNS.IPv6 = true
	cfg.Transparent.Mode = config.TransparentModeTUN
	cfg.Transparent.TUNIPv6 = config.TUNIPv6Always
	manager := New(cfg, runtime.NewPaths(cfg))
	if err := manager.ValidateConfig(); err != nil {
		t.Fatalf("patched Mihomo rejected OpenSurge IPv6 listener config: %v", err)
	}
}

func TestPatchedMihomoAcceptsManagedTailscaleOutboundConfig(t *testing.T) {
	binary := os.Getenv("OPENSURGE_TEST_PATCHED_MIHOMO")
	if binary == "" {
		t.Skip("OPENSURGE_TEST_PATCHED_MIHOMO is not set")
	}
	dir := t.TempDir()
	authKey := filepath.Join(dir, "tailscale-auth-key")
	if err := os.WriteFile(authKey, []byte("tskey-auth-validation-only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Runtime.Dir = dir
	cfg.Mihomo.Binary = binary
	cfg.Mihomo.Config = filepath.Join(dir, "mihomo.yaml")
	cfg.Transparent.Mode = config.TransparentModeTUN
	cfg.Tailscale = config.TailscaleConfig{
		Enabled:                true,
		DisplayName:            "Test Tailnet",
		Hostname:               "opensurge-test",
		ControlURL:             "https://controlplane.tailscale.com",
		AuthKeyFile:            authKey,
		StateDir:               filepath.Join(dir, "tailscale-state"),
		AcceptRoutes:           true,
		MagicDNSSuffixes:       []string{"example.ts.net"},
		PeerCIDRs:              []string{"100.82.10.7/32"},
		SubnetRoutes:           []string{"10.20.0.0/16"},
		AllowMac:               true,
		ExitNode:               "100.90.3.4",
		ExitNodeAllowLANAccess: true,
	}
	manager := New(cfg, runtime.NewPaths(cfg))
	if err := manager.ValidateConfig(); err != nil {
		t.Fatalf("patched Mihomo rejected managed Tailscale outbound config: %v", err)
	}
}

func waitForFileText(requiredPath, logPath, want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pathReady := requiredPath == ""
		if !pathReady {
			_, err := os.Lstat(requiredPath)
			pathReady = err == nil
		}
		if pathReady && strings.Contains(readTestFile(logPath), want) {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %q", want)
}

func readTestFile(path string) string {
	body, _ := os.ReadFile(path)
	return string(body)
}

func testIPv6UDPPacket(source, destination netip.Addr, sourcePort, destinationPort uint16, payload []byte) []byte {
	packet := make([]byte, 40+8+len(payload))
	packet[0] = 0x60
	binary.BigEndian.PutUint16(packet[4:6], uint16(8+len(payload)))
	packet[6] = 17
	packet[7] = 64
	copy(packet[8:24], source.AsSlice())
	copy(packet[24:40], destination.AsSlice())
	udp := packet[40:]
	binary.BigEndian.PutUint16(udp[0:2], sourcePort)
	binary.BigEndian.PutUint16(udp[2:4], destinationPort)
	binary.BigEndian.PutUint16(udp[4:6], uint16(len(udp)))
	copy(udp[8:], payload)
	binary.BigEndian.PutUint16(udp[6:8], ipv6TransportChecksum(source, destination, 17, udp))
	return packet
}

func testIPv6TCPSYNPacket(source, destination netip.Addr, sourcePort, destinationPort uint16) []byte {
	return testIPv6TCPPacket(source, destination, sourcePort, destinationPort, 1, 0, 0x02)
}

func testIPv6TCPPacket(source, destination netip.Addr, sourcePort, destinationPort uint16, sequence, acknowledgment uint32, flags byte) []byte {
	packet := make([]byte, 40+20)
	packet[0] = 0x60
	binary.BigEndian.PutUint16(packet[4:6], 20)
	packet[6] = 6
	packet[7] = 64
	copy(packet[8:24], source.AsSlice())
	copy(packet[24:40], destination.AsSlice())
	tcp := packet[40:]
	binary.BigEndian.PutUint16(tcp[0:2], sourcePort)
	binary.BigEndian.PutUint16(tcp[2:4], destinationPort)
	binary.BigEndian.PutUint32(tcp[4:8], sequence)
	binary.BigEndian.PutUint32(tcp[8:12], acknowledgment)
	tcp[12] = 5 << 4
	tcp[13] = flags
	binary.BigEndian.PutUint16(tcp[14:16], 65535)
	binary.BigEndian.PutUint16(tcp[16:18], ipv6TransportChecksum(source, destination, 6, tcp))
	return packet
}

func ipv6TransportChecksum(source, destination netip.Addr, nextHeader byte, segment []byte) uint16 {
	pseudo := make([]byte, 40+len(segment))
	copy(pseudo[:16], source.AsSlice())
	copy(pseudo[16:32], destination.AsSlice())
	binary.BigEndian.PutUint32(pseudo[32:36], uint32(len(segment)))
	pseudo[39] = nextHeader
	copy(pseudo[40:], segment)
	var sum uint32
	for index := 0; index+1 < len(pseudo); index += 2 {
		sum += uint32(binary.BigEndian.Uint16(pseudo[index : index+2]))
	}
	if len(pseudo)%2 != 0 {
		sum += uint32(pseudo[len(pseudo)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	checksum := ^uint16(sum)
	if checksum == 0 {
		return 0xffff
	}
	return checksum
}
