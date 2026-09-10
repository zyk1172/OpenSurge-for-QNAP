package mihomo

import (
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
)

func TestTUNModeKeepsMihomoInternalListenersOnLoopback(t *testing.T) {
	cfg := config.Default()
	cfg.Transparent.Mode = config.TransparentModeTUN
	rendered, err := RenderConfig(cfg)
	if err != nil {
		t.Fatalf("RenderConfig: %v", err)
	}
	for _, want := range []string{
		"allow-lan: false",
		`bind-address: "127.0.0.1"`,
		"listen: 127.0.0.1:1053",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("TUN config missing %q\n---\n%s", want, rendered)
		}
	}
}

func TestTransparentOffExplicitlyEnablesManualLANMixedPort(t *testing.T) {
	cfg := config.Default()
	cfg.Transparent.Mode = config.TransparentModeOff
	rendered, err := RenderConfig(cfg)
	if err != nil {
		t.Fatalf("RenderConfig: %v", err)
	}
	for _, want := range []string{
		"allow-lan: true",
		`bind-address: "*"`,
		"listen: 127.0.0.1:1053",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("manual proxy config missing %q\n---\n%s", want, rendered)
		}
	}
}
