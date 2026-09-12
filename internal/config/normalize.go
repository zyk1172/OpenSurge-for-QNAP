package config

import (
	"fmt"
	"net"
	"strings"
)

// Normalize materializes derived/default-compatible values into cfg before the
// configuration is validated or frozen for application. OpenSurge for QNAP is
// intentionally IPv4-only: legacy IPv6 fields are still parsed so existing
// persistent configurations continue to load, but they are migrated to the
// supported OFF state before validation and runtime rendering.
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

	// QNAP IPv6 support is deliberately disabled. Keep accepting the historical
	// schema so installations upgraded from the former IPv6 experiments do not
	// become unreadable, then normalize every persisted runtime switch back to
	// OFF. A subsequent Web/API save persists this migration to disk.
	cfg.DNS.IPv6 = false
	cfg.Transparent.TUNIPv6 = TUNIPv6Off
	cfg.Transparent.IPv6SharedL2Ready = false
	cfg.Transparent.IPv6PacketBrokerBinary = ""
	cfg.Transparent.IPv6PacketMTU = 0

	return nil
}
