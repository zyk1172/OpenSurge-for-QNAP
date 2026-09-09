package config

import (
	"fmt"
	"net"
	"strings"
)

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
	// Downstream IPv6 takeover depended on the removed macOS BPF packet broker.
	// Refuse rather than silently downgrade because a silent IPv6 bypass could
	// violate the operator's routing expectations.
	if cfg.Transparent.TUNIPv6 != "" && cfg.Transparent.TUNIPv6 != TUNIPv6Off {
		return fmt.Errorf("downstream IPv6 takeover is not supported in OpenSurge for QNAP v1; set transparent.tun_ipv6: off")
	}
	return nil
}
