//go:build linux

package linux

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/platform"
)

type ipAddressInfo struct {
	AddrInfo []struct {
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
	} `json:"addr_info"`
}

type ip6RouteEntry struct {
	Dst  string `json:"dst"`
	Dev  string `json:"dev"`
	Type string `json:"type"`
}

func (b *Backend) tunIPv6TakeoverEnabled(ctx context.Context, device string) (bool, error) {
	if strings.TrimSpace(device) == "" {
		return false, nil
	}
	out, err := b.runner.output(ctx, b.runner.ipPath, "-6", "-j", "addr", "show", "dev", device)
	if err != nil {
		if isNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var interfaces []ipAddressInfo
	if err := json.Unmarshal(out, &interfaces); err != nil {
		return false, platform.NewError(platform.CodeCommandFailed, "parse IPv6 TUN address state").Wrap(err)
	}
	expected, err := netip.ParsePrefix(config.MihomoTUNIPv6)
	if err != nil {
		return false, err
	}
	for _, iface := range interfaces {
		for _, address := range iface.AddrInfo {
			parsed, parseErr := netip.ParseAddr(address.Local)
			if parseErr == nil && expected.Contains(parsed) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (b *Backend) ipv6PolicyRules(ctx context.Context) ([]ipRule, error) {
	out, err := b.runner.output(ctx, b.runner.ipPath, "-6", "-j", "rule", "show")
	if err != nil {
		return nil, err
	}
	var rules []ipRule
	if err := json.Unmarshal(out, &rules); err != nil {
		return nil, platform.NewError(platform.CodeCommandFailed, "parse ip -6 -j rule show output").Wrap(err)
	}
	return rules, nil
}

func (b *Backend) ipv6RulePresent(ctx context.Context, cfg platform.RoutingConfig) (bool, error) {
	rules, err := b.ipv6PolicyRules(ctx)
	if err != nil {
		return false, err
	}
	for _, rule := range rules {
		if ruleMatchesConfig(rule, cfg) {
			return true, nil
		}
	}
	return false, nil
}

func (b *Backend) ipv6GuardPresent(ctx context.Context, cfg platform.RoutingConfig) (bool, error) {
	rules, err := b.ipv6PolicyRules(ctx)
	if err != nil {
		return false, err
	}
	for _, rule := range rules {
		if guardRuleMatchesConfig(rule, cfg) {
			return true, nil
		}
	}
	return false, nil
}

func (b *Backend) ipv6RoutingTableMatches(ctx context.Context, cfg platform.RoutingConfig) (bool, error) {
	if cfg.TableID == 0 {
		return false, nil
	}
	out, err := b.runner.output(ctx, b.runner.ipPath, "-6", "-j", "route", "show", "table", strconv.FormatUint(uint64(cfg.TableID), 10))
	if err != nil {
		if isNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var routes []ip6RouteEntry
	if err := json.Unmarshal(out, &routes); err != nil {
		return false, platform.NewError(platform.CodeCommandFailed, "parse IPv6 policy routing table").Wrap(err)
	}
	lanRoute := false
	defaultRoute := false
	for _, route := range routes {
		destination := strings.TrimSpace(route.Dst)
		if destination == "" {
			destination = "default"
		}
		switch destination {
		case config.DownstreamIPv6Prefix:
			if lanRoute || route.Dev != cfg.LANInterface {
				return false, nil
			}
			lanRoute = true
		case "default":
			if defaultRoute {
				return false, nil
			}
			if cfg.DirectFallback {
				if route.Type != "prohibit" && route.Type != "unreachable" {
					return false, nil
				}
			} else if route.Dev != cfg.TUNDevice {
				return false, nil
			}
			defaultRoute = true
		default:
			return false, nil
		}
	}
	return len(routes) == 2 && lanRoute && defaultRoute, nil
}

func (b *Backend) applyIPv6PolicyRouting(ctx context.Context, cfg platform.RoutingConfig) (err error) {
	if effectiveRuleMode(cfg) != platform.RoutingRuleIngressInterface {
		return platform.NewError(platform.CodeInvalidArgument, "QNAP IPv6 takeover requires same-LAN ingress-interface routing")
	}
	if err := validateInterfaceName(cfg.LANInterface); err != nil {
		return err
	}
	if err := validateInterfaceName(cfg.TUNDevice); err != nil {
		return err
	}
	if cfg.TableID == 0 || cfg.RulePriority == 0 {
		return platform.NewError(platform.CodeInvalidArgument, "IPv6 routing table id and rule priority must be set")
	}

	// The container gets this stable ULA during entrypoint setup. Requiring it
	// here turns a QNET/kernel IPv6 problem into a failed gateway start instead of
	// a silent client-side bypass.
	gatewayReady, err := b.interfaceHasIPv6(ctx, cfg.LANInterface, config.DownstreamIPv6Gateway)
	if err != nil {
		return err
	}
	if !gatewayReady {
		return platform.NewError(platform.CodePolicyRoutingConflict,
			"QNAP IPv6 gateway ULA is missing from the LAN interface").
			WithDetail("expected", config.DownstreamIPv6Gateway).
			WithDetail("interface", cfg.LANInterface)
	}

	// Any partial failure is cleaned up locally. Gateway rollback will repeat the
	// exact cleanup, so setup remains idempotent and fail closed.
	success := false
	defer func() {
		if !success {
			_ = b.removeIPv6PolicyRouting(context.Background(), cfg)
		}
	}()

	table := strconv.FormatUint(uint64(cfg.TableID), 10)
	guardPriority, err := guardRulePriority(cfg)
	if err != nil {
		return err
	}
	guard, err := b.ipv6GuardPresent(ctx, cfg)
	if err != nil {
		return err
	}
	if !guard {
		if err := b.runner.run(ctx, b.runner.ipPath, "-6", "rule", "add", "pref", strconv.FormatUint(uint64(guardPriority), 10), "iif", cfg.LANInterface, "prohibit"); err != nil {
			return err
		}
	}
	if err := b.runner.run(ctx, b.runner.ipPath, "-6", "route", "replace", config.DownstreamIPv6Prefix, "dev", cfg.LANInterface, "table", table); err != nil {
		return err
	}
	if cfg.DirectFallback {
		// The QNAP config has an IPv4 upstream gateway but no authoritative IPv6
		// next hop. Direct fallback therefore blocks IPv6 instead of leaking it to
		// the main table/router outside OpenSurge policy.
		if err := b.runner.run(ctx, b.runner.ipPath, "-6", "route", "replace", "prohibit", "default", "table", table); err != nil {
			return err
		}
	} else if err := b.runner.run(ctx, b.runner.ipPath, "-6", "route", "replace", "default", "dev", cfg.TUNDevice, "table", table); err != nil {
		return err
	}
	present, err := b.ipv6RulePresent(ctx, cfg)
	if err != nil {
		return err
	}
	if !present {
		if err := b.runner.run(ctx, b.runner.ipPath, "-6", "rule", "add", "pref", strconv.FormatUint(uint64(cfg.RulePriority), 10), "iif", cfg.LANInterface, "table", table); err != nil {
			return err
		}
	}
	success = true
	return nil
}

func (b *Backend) removeIPv6PolicyRouting(ctx context.Context, cfg platform.RoutingConfig) error {
	if cfg.TableID == 0 || cfg.RulePriority == 0 || strings.TrimSpace(cfg.LANInterface) == "" {
		return nil
	}
	table := strconv.FormatUint(uint64(cfg.TableID), 10)
	var failures []string
	if present, err := b.ipv6RulePresent(ctx, cfg); err != nil {
		failures = append(failures, "check IPv6 rule: "+err.Error())
	} else if present {
		if err := b.runner.run(ctx, b.runner.ipPath, "-6", "rule", "del", "pref", strconv.FormatUint(uint64(cfg.RulePriority), 10), "iif", cfg.LANInterface, "table", table); err != nil && !isNotExist(err) {
			failures = append(failures, "delete IPv6 rule: "+err.Error())
		}
	}
	if err := b.runner.run(ctx, b.runner.ipPath, "-6", "route", "del", config.DownstreamIPv6Prefix, "dev", cfg.LANInterface, "table", table); err != nil && !isNotExist(err) {
		failures = append(failures, "delete IPv6 LAN route: "+err.Error())
	}
	// Do not depend on the TUN device still existing during crash recovery.
	if err := b.runner.run(ctx, b.runner.ipPath, "-6", "route", "del", "default", "table", table); err != nil && !isNotExist(err) {
		failures = append(failures, "delete IPv6 default route: "+err.Error())
	}
	if guard, err := b.ipv6GuardPresent(ctx, cfg); err != nil {
		failures = append(failures, "check IPv6 guard: "+err.Error())
	} else if guard {
		priority, priorityErr := guardRulePriority(cfg)
		if priorityErr != nil {
			failures = append(failures, "IPv6 guard priority: "+priorityErr.Error())
		} else if err := b.runner.run(ctx, b.runner.ipPath, "-6", "rule", "del", "pref", strconv.FormatUint(uint64(priority), 10), "iif", cfg.LANInterface, "prohibit"); err != nil && !isNotExist(err) {
			failures = append(failures, "delete IPv6 guard: "+err.Error())
		}
	}
	if len(failures) > 0 {
		return platform.NewError(platform.CodeCommandFailed, "remove IPv6 policy routing: "+strings.Join(failures, "; "))
	}
	return nil
}

func (b *Backend) ipv6PolicyRoutingPresent(ctx context.Context, cfg platform.RoutingConfig) (bool, error) {
	primary, err := b.ipv6RulePresent(ctx, cfg)
	if err != nil || !primary {
		return primary, err
	}
	guard, err := b.ipv6GuardPresent(ctx, cfg)
	if err != nil || !guard {
		return guard, err
	}
	return b.ipv6RoutingTableMatches(ctx, cfg)
}

func (b *Backend) interfaceHasIPv6(ctx context.Context, device, expected string) (bool, error) {
	out, err := b.runner.output(ctx, b.runner.ipPath, "-6", "-j", "addr", "show", "dev", device)
	if err != nil {
		return false, err
	}
	var interfaces []ipAddressInfo
	if err := json.Unmarshal(out, &interfaces); err != nil {
		return false, platform.NewError(platform.CodeCommandFailed, "parse LAN IPv6 address state").Wrap(err)
	}
	want, err := netip.ParseAddr(expected)
	if err != nil {
		return false, err
	}
	for _, iface := range interfaces {
		for _, address := range iface.AddrInfo {
			actual, parseErr := netip.ParseAddr(address.Local)
			if parseErr == nil && actual == want {
				return true, nil
			}
		}
	}
	return false, nil
}

func (b *Backend) applyPolicyRoutingWithIPv6(ctx context.Context, cfg platform.RoutingConfig) error {
	if err := b.applyPolicyRouting(ctx, cfg); err != nil {
		return err
	}
	enabled, err := b.tunIPv6TakeoverEnabled(ctx, cfg.TUNDevice)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	if err := b.applyIPv6PolicyRouting(ctx, cfg); err != nil {
		return err
	}
	return nil
}

func (b *Backend) policyRoutingWithIPv6Present(ctx context.Context, cfg platform.RoutingConfig) (bool, error) {
	present, err := b.policyRoutingPresent(ctx, cfg)
	if err != nil || !present {
		return present, err
	}
	enabled, err := b.tunIPv6TakeoverEnabled(ctx, cfg.TUNDevice)
	if err != nil || !enabled {
		return !enabled && present, err
	}
	return b.ipv6PolicyRoutingPresent(ctx, cfg)
}

func (b *Backend) removePolicyRoutingWithIPv6(ctx context.Context, cfg platform.RoutingConfig) error {
	v6Err := b.removeIPv6PolicyRouting(ctx, cfg)
	v4Err := b.removePolicyRouting(ctx, cfg)
	if v6Err != nil && v4Err != nil {
		return fmt.Errorf("IPv6 cleanup: %v; IPv4 cleanup: %w", v6Err, v4Err)
	}
	if v6Err != nil {
		return v6Err
	}
	return v4Err
}
