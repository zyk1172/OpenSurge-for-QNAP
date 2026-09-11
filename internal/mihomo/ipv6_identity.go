package mihomo

import (
	"fmt"
	"net/netip"
	"strings"

	"open-mihomo-gateway/internal/config"
)

// QNAPDeviceIPv6Address derives the stable ULA that a same-LAN client should
// use when IPv6 takeover is enabled. The final 32 bits mirror the device's
// fixed IPv4 address while the 0x0001 marker keeps client addresses away from
// the gateway's ::1 address.
func QNAPDeviceIPv6Address(ipv4 string) (string, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(ipv4))
	if err != nil || !addr.Is4() {
		return "", fmt.Errorf("invalid IPv4 address %q", ipv4)
	}
	prefix, err := netip.ParsePrefix(config.DownstreamIPv6Prefix)
	if err != nil {
		return "", err
	}
	bytes := prefix.Addr().As16()
	v4 := addr.As4()
	bytes[8], bytes[9] = 0, 1
	bytes[10], bytes[11] = 0, 0
	copy(bytes[12:], v4[:])
	return netip.AddrFrom16(bytes).String(), nil
}

func qnapDeviceIPv6CIDR(ipv4 string) (string, error) {
	address, err := QNAPDeviceIPv6Address(ipv4)
	if err != nil {
		return "", err
	}
	return address + "/128", nil
}

// The upstream macOS path preserves source MAC addresses through its BPF
// packet broker and therefore renders IN-USER rules. Linux TUN is layer 3, so
// QNAP clients instead use deterministic ULAs. Rewriting only the synthetic
// IN-USER selectors preserves the existing rule ordering and policy semantics.
func rewriteQNAPIPv6IdentityRules(policyYAML string, cfg config.Config) string {
	if cfg.Transparent.TUNIPv6 == config.TUNIPv6Off || cfg.DevicePolicy.Bundle == nil {
		return policyYAML
	}
	for _, managed := range cfg.DevicePolicy.Bundle.Compiled.Devices {
		cidr, err := qnapDeviceIPv6CIDR(managed.IPv4)
		if err != nil {
			continue
		}
		policyYAML = strings.ReplaceAll(
			policyYAML,
			"IN-USER,"+DeviceInboundUser(managed.ID),
			"SRC-IP-CIDR6,"+cidr,
		)
	}
	return policyYAML
}
