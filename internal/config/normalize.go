package config

import (
	"fmt"
	"net"
	"strings"
)

// Normalize materializes derived/default-compatible values into cfg before the
// configuration is validated or frozen for application. It also rejects
// macOS-only settings whose silent acceptance would create a second routing
// owner or pretend to enable functionality the QNAP/Linux backend does not have.
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

	if cfg.Transparent.TUNAutoRoute {
		return fmt.Errorf("transparent.tun_auto_route must be false on OpenSurge for QNAP; Linux policy routing is owned by OpenSurge")
	}
	if cfg.LocalSystemProxy.Enabled {
		return fmt.Errorf("local_system_proxy.enabled is a macOS-only feature and is not supported on OpenSurge for QNAP")
	}
	if cfg.Transparent.TUNIPv6 != "" && cfg.Transparent.TUNIPv6 != TUNIPv6Off {
		return fmt.Errorf("downstream IPv6 takeover is not supported in OpenSurge for QNAP v1; set transparent.tun_ipv6: off")
	}
	return nil
}
