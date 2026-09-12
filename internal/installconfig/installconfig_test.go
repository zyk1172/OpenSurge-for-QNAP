package installconfig

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/device"
)

func TestPrepareRelocatesMutableAndExecutablePaths(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile.yaml")
	policy := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(profile, []byte("rules:\n  - MATCH,DIRECT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy, []byte(`{"devices":[],"profiles":[],"templates":[],"rule_sets":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.yaml")
	data := `gateway:
  mode: "same_wifi_dhcp"
  interface: "en0"
  lan_ip: "192.168.1.20"
  upstream_interface: "en0"
dhcp:
  enabled: true
  range_start: "192.168.1.120"
  range_end: "192.168.1.199"
device_policy:
  file: "` + policy + `"
mihomo:
  profile_mode: "imported"
  profile: "` + profile + `"
transparent:
  mode: "tun"
runtime:
  dir: "` + filepath.Join(dir, "runtime") + `"
`
	if err := os.WriteFile(source, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "installed")
	cfg, err := Prepare(source, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{cfg.DHCP.Binary, cfg.Mihomo.Binary, cfg.Mihomo.Config, cfg.Mihomo.Profile, cfg.DevicePolicy.File, cfg.Transparent.IPv6PacketBrokerBinary, cfg.Runtime.Dir} {
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || len(relative) > 3 && relative[:3] == "../" {
			t.Fatalf("path escaped install root: %s", path)
		}
	}
	if want := filepath.Join(root, "bin", "opensurge-network"); cfg.Transparent.IPv6PacketBrokerBinary != want {
		t.Fatalf("IPv6 packet broker path = %q, want %q", cfg.Transparent.IPv6PacketBrokerBinary, want)
	}
	if info, err := os.Stat(cfg.Mihomo.Profile); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("profile mode: info=%v err=%v", info, err)
	}
}

func TestValidatePackageSourceRejectsExternalInputs(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	if err := os.WriteFile(source, []byte(`mihomo:
  profile_mode: "imported"
  profile: "/tmp/profile.yaml"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePackageSource(source); err == nil {
		t.Fatal("imported package seed was accepted")
	}
}

func TestPrepareEnablesEmptyDevicePolicyByDefault(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "seed.yaml")
	if err := Write(config.Default(), source); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePackageSource(source); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "installed")
	cfg, err := Prepare(source, root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DevicePolicy.File != filepath.Join(root, "data", "device-policy.json") {
		t.Fatalf("policy path = %q", cfg.DevicePolicy.File)
	}
	bundle, err := device.LoadPolicyBundle(cfg.DevicePolicy.File)
	if err != nil || len(bundle.Policy.Devices) != 0 || len(bundle.Policy.Profiles) != 0 {
		t.Fatalf("default policy = %#v, error=%v", bundle, err)
	}
	if info, err := os.Stat(cfg.DevicePolicy.File); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("policy permissions: %v, %v", info, err)
	}
}

func TestEnableDevicePolicyPreservesExistingDocuments(t *testing.T) {
	for _, state := range []string{"missing", "disabled with saved policy", "invalid saved policy", "configured custom policy"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			policyPath := filepath.Join(dir, "data", "device-policy.json")
			cfg := config.Default()
			cfg.DNS.IPv6 = true
			cfg.Runtime.Dir = filepath.Join(dir, "runtime")
			saved := []byte(`{"devices":[],"profiles":[{"id":"saved","default_policies":["DIRECT"]}],"templates":[],"rule_sets":[]}`)
			if state == "invalid saved policy" || state == "configured custom policy" {
				saved = []byte("{invalid draft\n")
			}
			if state == "configured custom policy" {
				policyPath = filepath.Join(dir, "custom-policy.json")
				cfg.DevicePolicy.File = policyPath
			}
			if state != "missing" {
				if err := writePrivateFile(policyPath, saved); err != nil {
					t.Fatal(err)
				}
			}
			if err := Write(cfg, path); err != nil {
				t.Fatal(err)
			}
			original, _ := os.ReadFile(path)
			err := EnableDevicePolicy(path)
			if (err != nil) != (state == "invalid saved policy") {
				t.Fatalf("EnableDevicePolicy() error = %v", err)
			}
			if state != "missing" {
				actual, err := os.ReadFile(policyPath)
				if err != nil || !bytes.Equal(actual, saved) {
					t.Fatalf("saved policy changed: %q, %v", actual, err)
				}
			}
			if state == "invalid saved policy" || state == "configured custom policy" {
				actual, _ := os.ReadFile(path)
				if !bytes.Equal(actual, original) {
					t.Fatal("existing configuration was rewritten")
				}
				return
			}
			updated, err := config.Load(path)
			if err != nil || updated.DevicePolicy.File != policyPath || !updated.DNS.IPv6 {
				t.Fatalf("upgraded configuration: policy=%q ipv6=%v error=%v", updated.DevicePolicy.File, updated.DNS.IPv6, err)
			}
			first, _ := os.ReadFile(path)
			if err := EnableDevicePolicy(path); err != nil {
				t.Fatal(err)
			}
			second, _ := os.ReadFile(path)
			if !bytes.Equal(first, second) {
				t.Fatal("repeated upgrade rewrote the configuration")
			}
		})
	}
}
