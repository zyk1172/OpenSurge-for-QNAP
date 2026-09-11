package mihomo

import (
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/device"
)

func TestQNAPDeviceIPv6AddressIsStableAndInsideTakeoverPrefix(t *testing.T) {
	got, err := QNAPDeviceIPv6Address("192.168.2.101")
	if err != nil {
		t.Fatal(err)
	}
	if got != "fdfe:dcba:9878:0:1:0:c0a8:265" {
		t.Fatalf("QNAPDeviceIPv6Address() = %q", got)
	}
}

func TestRewriteQNAPIPv6IdentityRulesUsesSourceULA(t *testing.T) {
	cfg := config.Default()
	cfg.Transparent.TUNIPv6 = config.TUNIPv6Always
	cfg.DevicePolicy.Bundle = &device.PolicyBundle{Compiled: device.CompiledPolicy{Devices: []device.CompiledDevice{{ID: "living-room", IPv4: "192.168.2.101"}}}}

	rendered := rewriteQNAPIPv6IdentityRules("  - IN-USER,device:living-room,DIRECT\n", cfg)
	if strings.Contains(rendered, "IN-USER") {
		t.Fatalf("legacy layer-2 identity leaked into QNAP IPv6 rules: %s", rendered)
	}
	if !strings.Contains(rendered, "SRC-IP-CIDR6,fdfe:dcba:9878:0:1:0:c0a8:265/128,DIRECT") {
		t.Fatalf("stable ULA identity missing: %s", rendered)
	}
}
