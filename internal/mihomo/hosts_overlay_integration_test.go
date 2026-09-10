package mihomo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
)

func TestComposeProfileOverlayPreservesAndOverridesHosts(t *testing.T) {
	source := []byte(`proxies: []
rules:
  - MATCH,DIRECT
hosts:
  keep.example: 10.0.0.1
  override.example: 10.0.0.2
dns:
  nameserver:
    - 1.1.1.1
`)
	document := DefaultProfileOverlayDocument()
	document.Enabled = true
	document.DNS.Merge["use-hosts"] = true
	document.DNS.Merge["use-system-hosts"] = false
	document.DNS.Merge[profileOverlayHostsFileField] = "# imported from a hosts file\n0.0.0.0 ads.example\n192.168.2.10 override.example nas.home\n"

	composition, err := ComposeProfileOverlay(source, document)
	if err != nil {
		t.Fatalf("ComposeProfileOverlay() error = %v", err)
	}
	for _, want := range []string{
		"hosts:",
		"keep.example",
		"10.0.0.1",
		"override.example",
		"192.168.2.10",
		"ads.example",
		"0.0.0.0",
		"nas.home",
		"use-hosts: true",
		"use-system-hosts: false",
	} {
		if !strings.Contains(composition.ProfileYAML, want) {
			t.Fatalf("composed profile missing %q:\n%s", want, composition.ProfileYAML)
		}
	}
	if strings.Contains(composition.ProfileYAML, "10.0.0.2") {
		t.Fatalf("overlay hosts file did not override imported hostname:\n%s", composition.ProfileYAML)
	}
	if strings.Contains(composition.ProfileYAML, profileOverlayHostsFileField+":") {
		t.Fatalf("project-only hosts-file field leaked into mihomo YAML:\n%s", composition.ProfileYAML)
	}
}

func TestProfileOverlayRejectsInvalidHostsFile(t *testing.T) {
	document := DefaultProfileOverlayDocument()
	document.Enabled = true
	document.DNS.Merge[profileOverlayHostsFileField] = "not-an-ip example.test\n"

	if err := ValidateProfileOverlay(document); err == nil || !strings.Contains(err.Error(), "invalid IP address") {
		t.Fatalf("ValidateProfileOverlay() error = %v, want invalid IP address", err)
	}
}

func TestRenderConfigPreservesImportedNativeHosts(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile.yaml")
	if err := os.WriteFile(profile, []byte(`proxies: []
rules:
  - MATCH,DIRECT
hosts:
  nas.home: 192.168.2.10
  dual.example:
    - 1.1.1.1
    - 2606:4700:4700::1111
dns:
  use-hosts: true
  use-system-hosts: false
  nameserver:
    - 1.1.1.1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = profile
	rendered, err := RenderConfig(cfg)
	if err != nil {
		t.Fatalf("RenderConfig() error = %v", err)
	}
	for _, want := range []string{
		"hosts:",
		"nas.home",
		"192.168.2.10",
		"dual.example",
		"2606:4700:4700::1111",
		"use-hosts: true",
		"use-system-hosts: false",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, rendered)
		}
	}
	if strings.Index(rendered, "hosts:") > strings.Index(rendered, "dns:") {
		t.Fatalf("expected top-level hosts section before dns section:\n%s", rendered)
	}
}
