package controlapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
)

// A Web/API topology save rewrites the authoritative YAML through
// config.Render. QNAP's real upstream router is intentionally not exposed as an
// editable ControlConfig field, so the save path must carry the already-loaded
// value through unchanged. Losing it makes Host Takeover impossible after the
// next container or NAS restart.
func TestApplyControlConfigPreservesUpstreamGateway(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "opensurge.yaml")
	cfg := config.Default()
	cfg.Gateway.UpstreamGateway = "192.168.2.1"
	cfg.Runtime.Dir = filepath.Join(dir, "runtime")
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	cfg.DHCP.Enabled = false
	if err := os.WriteFile(configPath, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}

	input := controlConfigFrom(cfg, fileDigest(configPath))
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyControlConfig(configPath, fileDigest(configPath), payload); err != nil {
		t.Fatalf("applyControlConfig() error = %v", err)
	}

	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load(saved config) error = %v", err)
	}
	if loaded.Gateway.UpstreamGateway != cfg.Gateway.UpstreamGateway {
		t.Fatalf("upstream gateway after API save = %q, want %q", loaded.Gateway.UpstreamGateway, cfg.Gateway.UpstreamGateway)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `upstream_gateway: "192.168.2.1"`) {
		t.Fatalf("saved config does not persist upstream_gateway:\n%s", data)
	}
}

func TestApplyControlConfigPersistsLANProxy(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "opensurge.yaml")
	cfg := config.Default()
	cfg.Runtime.Dir = filepath.Join(dir, "runtime")
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	if err := os.WriteFile(configPath, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}

	input := controlConfigFrom(cfg, fileDigest(configPath))
	input.LANProxy = &LANProxyConfigInput{Enabled: true, SOCKSPort: 17891}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyControlConfig(configPath, fileDigest(configPath), payload); err != nil {
		t.Fatalf("applyControlConfig() error = %v", err)
	}

	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.LANProxy.Enabled || loaded.LANProxy.SOCKSPort != 17891 {
		t.Fatalf("LAN proxy after API save = %+v", loaded.LANProxy)
	}
}

func TestControlConfigPersistsResolverSettingsAndPreservesThemForLegacyClients(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opensurge.yaml")
	cfg := config.Default()
	cfg.Runtime.Dir = filepath.Join(dir, "runtime")
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	if err := os.WriteFile(path, []byte(config.Render(cfg)), 0600); err != nil {
		t.Fatal(err)
	}
	input := controlConfigFrom(cfg, fileDigest(path))
	mode, allow := "custom", false
	upstreams := []config.ResolverUpstream{{Address: "https://dns.quad9.net/dns-query", BootstrapIPv4: "9.9.9.9"}}
	input.DNS.ResolverMode = &mode
	input.DNS.ResolverUpstreams = &upstreams
	input.DNS.ResolverAllowPlaintext = &allow
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = applyControlConfig(path, fileDigest(path), payload); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DNS.ResolverMode != "custom" || len(loaded.DNS.ResolverUpstreams) != 1 || loaded.DNS.ResolverUpstreams[0] != upstreams[0] {
		t.Fatalf("settings not persisted: %#v", loaded.DNS)
	}
	legacy := controlConfigFrom(loaded, fileDigest(path))
	legacy.DNS.ResolverMode = nil
	legacy.DNS.ResolverUpstreams = nil
	legacy.DNS.ResolverAllowPlaintext = nil
	payload, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = applyControlConfig(path, fileDigest(path), payload); err != nil {
		t.Fatal(err)
	}
	loaded, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DNS.ResolverMode != "custom" || len(loaded.DNS.ResolverUpstreams) != 1 || loaded.DNS.ResolverUpstreams[0] != upstreams[0] {
		t.Fatalf("legacy save erased upstreams: %#v", loaded.DNS)
	}
}
