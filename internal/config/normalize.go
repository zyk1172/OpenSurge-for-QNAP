package config

import (
	"fmt"
	"net"
	"strings"
)

const NativeLinuxIPv6Runtime = "native-linux-tun"

// Normalize materializes derived/default-compatible values into cfg before the
// configuration is validated or frozen for application. Only settings that
// would make the Linux data plane unsafe are rejected here. Legacy control-plane
// fields that no longer have a Linux runtime effect are preserved until the Web
// schema cleanup phase so imported/upstream configuration can still round-trip.
func Normalize(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if cfg.Transparent.RouteRulePriority == 0 && cfg.Transparent.RouteTableID != 0 {
		cfg.Transparent.RouteRulePriority = cfg.Transparent.RouteTableID
	}
	if strings.TrimSpace(cfg.Gateway.LANCIDR) == "" {
		scope, err := cfg.LANScope()
		if err != nil {
			return err
		}
		if scope.Network == nil {
			return fmt.Errorf("cannot derive gateway.lan_cidr from gateway.lan_ip / gateway.lan_prefix_len")
		}
		cfg.Gateway.LANCIDR = scope.Network.String()
	} else {
		ip, network, err := net.ParseCIDR(strings.TrimSpace(cfg.Gateway.LANCIDR))
		if err != nil || ip.To4() == nil {
			return fmt.Errorf("gateway.lan_cidr must be an IPv4 CIDR")
		}
		cfg.Gateway.LANCIDR = network.String()
	}

	// Linux policy routing is owned exclusively by OpenSurge. Allowing mihomo to
	// auto-route would create a second, unauditable route owner and break exact
	// rollback semantics.
	if cfg.Transparent.TUNAutoRoute {
		return fmt.Errorf("transparent.tun_auto_route must be false on OpenSurge for QNAP; Linux policy routing is owned by OpenSurge")
	}

	// The QNAP port uses Mihomo's native dual-stack TUN. Older configs still carry
	// the upstream macOS packet-broker fields and the validator keeps accepting
	// them for round-trip compatibility. Materialize harmless compatibility values
	// here so an existing QNAP config can enable IPv6 without requiring hidden
	// fields that the Web UI no longer exposes.
	if cfg.Transparent.TUNIPv6 != "" && cfg.Transparent.TUNIPv6 != TUNIPv6Off {
		if strings.TrimSpace(cfg.Transparent.IPv6PacketBrokerBinary) == "" {
			cfg.Transparent.IPv6PacketBrokerBinary = NativeLinuxIPv6Runtime
		}
		if cfg.Transparent.IPv6PacketMTU == 0 {
			cfg.Transparent.IPv6PacketMTU = 1500
		}
	}

	// QNAP same-LAN IPv6 no longer replaces the client's default router. Mihomo
	// DNS must synthesize IPv6 fake-IP answers, and the main router must forward
	// only that fake prefix to OpenSurge. Reuse the existing persisted readiness
	// acknowledgement so older schema-v1 clients continue to round-trip safely.
	if cfg.Gateway.Mode == GatewayModeSameLAN && cfg.Transparent.TUNIPv6 != "" && cfg.Transparent.TUNIPv6 != TUNIPv6Off {
		if !cfg.DNS.IPv6 {
			return fmt.Errorf("QNAP same-LAN IPv6 DNS steering requires dns.ipv6: true so Mihomo can return fake IPv6 answers")
		}
		if !cfg.Transparent.IPv6SharedL2Ready {
			return fmt.Errorf("QNAP same-LAN IPv6 DNS steering requires transparent.ipv6_shared_l2_ready: true after the main router uses OpenSurge DNS and routes %s to OpenSurge", MihomoFakeIPv6Range)
		}
	}
	return nil
}
