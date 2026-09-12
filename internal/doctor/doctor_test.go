package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/mihomo"
)

func TestCheckGatewayInterfaceTopology(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Interface = "en0"
	cfg.Gateway.UpstreamInterface = " en0 "
	check := checkGatewayInterfaceTopology(cfg.Gateway)
	if check.OK {
		t.Fatalf("checkGatewayInterfaceTopology() OK = true")
	}
	if check.Message == "" {
		t.Fatalf("checkGatewayInterfaceTopology() missing failure message")
	}

	cfg.Gateway.Interface = "en7"
	cfg.Gateway.UpstreamInterface = "en0"
	check = checkGatewayInterfaceTopology(cfg.Gateway)
	if !check.OK {
		t.Fatalf("checkGatewayInterfaceTopology() OK = false: %s", check.Message)
	}

	cfg.Gateway.Mode = config.GatewayModeSameLAN
	cfg.Gateway.Interface = "en0"
	cfg.Gateway.UpstreamInterface = " en0 "
	check = checkGatewayInterfaceTopology(cfg.Gateway)
	if !check.OK {
		t.Fatalf("checkGatewayInterfaceTopology() same_lan OK = false: %s", check.Message)
	}

	cfg.Gateway.UpstreamInterface = "en7"
	check = checkGatewayInterfaceTopology(cfg.Gateway)
	if check.OK {
		t.Fatalf("checkGatewayInterfaceTopology() same_lan with different interfaces OK = true")
	}

	cfg.Gateway.Mode = config.GatewayModeSameWiFiDHCP
	cfg.Gateway.UpstreamInterface = "en0"
	check = checkGatewayInterfaceTopology(cfg.Gateway)
	if !check.OK || !strings.Contains(check.Message, "same_wifi_dhcp") {
		t.Fatalf("checkGatewayInterfaceTopology() same_wifi_dhcp = %#v", check)
	}
}

func TestCheckInterfaceIPv4RejectsInvalidIP(t *testing.T) {
	check := checkInterfaceIPv4("en0", "not-an-ip")
	if check.OK {
		t.Fatalf("checkInterfaceIPv4() OK = true")
	}
	if check.Message != "invalid IPv4 address" {
		t.Fatalf("checkInterfaceIPv4() message = %q", check.Message)
	}
}

func TestStableIPv6NextHop(t *testing.T) {
	got := stableIPv6NextHop("192.168.2.241")
	if got == nil || got.String() != "fe80::1:0:c0a8:2f1" {
		t.Fatalf("stableIPv6NextHop() = %v", got)
	}
	if got := stableIPv6NextHop("not-an-ip"); got != nil {
		t.Fatalf("stableIPv6NextHop(invalid) = %v", got)
	}
}

func TestCheckWritableDirectoryExercisesDurableOperations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	check := checkWritableDirectory("persistent runtime storage", dir)
	if !check.OK {
		t.Fatalf("checkWritableDirectory() OK = false: %s", check.Message)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("storage probe left %d entries behind", len(entries))
	}
}

func TestCheckWritableDirectoryRejectsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	check := checkWritableDirectory("persistent runtime storage", path)
	if check.OK {
		t.Fatalf("checkWritableDirectory() OK = true for regular file")
	}
}

func TestCheckTUNDeviceRejectsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tun")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	check := checkTUNDevice(path)
	if check.OK {
		t.Fatalf("checkTUNDevice() OK = true for regular file")
	}
	if !strings.Contains(check.Message, "not a character device") {
		t.Fatalf("checkTUNDevice() message = %q", check.Message)
	}
}

func TestValidateMihomoConfigWithEngineRunsMihomoTestMode(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	binary := filepath.Join(dir, "mihomo")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + argsPath + "\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Mihomo.Binary = binary
	cfg.Runtime.Dir = filepath.Join(dir, "runtime")
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	if err := validateMihomoConfigWithEngine(cfg); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "-t\n") || !strings.Contains(string(args), "-f\n") {
		t.Fatalf("mihomo arguments = %q", args)
	}
}

func useRenderOnlyValidation(t *testing.T) {
	t.Helper()
	previous := validateMihomoConfig
	validateMihomoConfig = func(cfg config.Config) error {
		_, err := mihomo.RenderConfig(cfg)
		return err
	}
	t.Cleanup(func() { validateMihomoConfig = previous })
}
