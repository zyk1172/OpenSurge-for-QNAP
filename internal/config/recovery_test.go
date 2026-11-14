package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRecoveryAllowsInvalidFutureDeployment(t *testing.T) {
	for _, field := range []string{"lan", "cidr", "proxy", "policy", "tun"} {
		t.Run(field, func(t *testing.T) {
			cfg := Default()
			cfg.Runtime.Dir = filepath.Join(t.TempDir(), "runtime")
			switch field {
			case "lan":
				cfg.Gateway.LANIP = "not-an-ip"
			case "cidr":
				cfg.Gateway.LANCIDR = "invalid"
			case "proxy":
				cfg.DNS.ResolverProxyTarget = "missing"
				cfg.DNS.ResolverProxyEnabled = true
			case "policy":
				cfg.DevicePolicy.File = "missing-policy.json"
			case "tun":
				cfg.Transparent.TUNAutoRoute = true
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			body := Render(cfg)
			if field == "cidr" {
				body = strings.Replace(body, "gateway:\n", "gateway:\n  lan_cidr: invalid\n", 1)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("invalid deployment was accepted")
			}
			got, err := LoadRecovery(path)
			if err != nil || got.Runtime.Dir != cfg.Runtime.Dir || got.Gateway.LANIP != cfg.Gateway.LANIP {
				t.Fatalf("recovery cfg=%+v err=%v", got.Gateway, err)
			}
		})
	}
}

func TestLoadRecoveryRejectsMalformedAndUnknownTargets(t *testing.T) {
	for _, body := range []string{"runtime:\n  dir: \"\"\n", "mihomo:\n  config: \"\"\n", "dns:\n  port: invalid\n", "gateway:\n  typo: eth0\n", "broken line\n"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRecovery(path); err == nil {
			t.Fatalf("accepted malformed recovery config: %q", body)
		}
	}
}
