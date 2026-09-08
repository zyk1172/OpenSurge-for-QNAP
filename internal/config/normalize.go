package config

import (
	"fmt"
	"net"
	"strings"
)

// Normalize materializes derived/default-compatible values into cfg before the
// configuration is validated or frozen for application. Validation itself may
// inspect a copy, but lifecycle code must never depend on mutations performed on
// such a copy.
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
	return nil
}
