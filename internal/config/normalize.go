package config

import (
	"fmt"
	"net"
	"strings"
)

// Normalize materializes derived/default-compatible values into cfg before the
// configuration is validated or frozen for application. OpenSurge for QNAP is
// intentionally IPv4-only. Historical IPv6 schema fields remain parseable for
// upgrade/API compatibility, while the retired QNAP runtime markers are
// discarded. A requested IPv6 TUN remains visible to validation so it is
// rejected explicitly instead of being silently treated as supported.
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

	// QNAP IPv6 data-plane support is retired. Remove the old native-runtime
	// marker and readiness acknowledgement so no historical configuration can
	// resurrect the experimental Linux IPv6 path. TUNIPv6 itself is deliberately
	// left untouched here: validateTransparent rejects auto/always with the
	// normal unsupported-QNAP error. DNS.IPv6 is retained only as a legacy
	// schema value; the QNAP mihomo Manager suppresses it whenever TUN IPv6 is
	// off, which is the only valid QNAP runtime state.
	cfg.Transparent.IPv6SharedL2Ready = false
	cfg.Transparent.IPv6PacketBrokerBinary = ""
	cfg.Transparent.IPv6PacketMTU = 0

	return nil
}
