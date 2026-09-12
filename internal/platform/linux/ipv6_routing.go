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

// tunIPv6TakeoverEnabled reports whether mihomo actually created the IPv6 side
// of the TUN. QNAP keeps IPv6 policy state absent when the core did not expose
// the configured TUN address, so enabling DNS IPv6 cannot silently create a
// black-hole route before mihomo is ready.
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

func ipv6FakeRuleMatches(rule ipRule, cfg platform.RoutingConfig) bool {
	priority, priorityOK := ruleField(rule, "priority")
	table, tableOK := ruleField(rule, "table")
	if !priorityOK || !tableOK || priority != cfg.RulePriority || table != cfg.TableID {
		return false
	}
	destination, ok := ruleStringField(rule, "dst", "to")
	return ok && destination == config.MihomoFakeIPv6Range
}

func ipv6FakeGuardMatches(rule ipRule, cfg platform.RoutingConfig) bool {
	if cfg.RulePriority == ^uint32(0) {
		return false
	}
	priority, ok := ruleField(rule, "priority")
	if !ok || priority != cfg.RulePriority+1 {
		return false
	}
	destination, ok := ruleStringField(rule, "dst", "to")
	if !ok || destination != config.MihomoFakeIPv6Range {
		return false
	}
	action, ok := ruleStringField(rule, "action", "type")
	return ok && strings.EqualFold(action, "prohibit")
}

func (b *Backend) ipv6FakeRulePresent(ctx context.Context, cfg platform.RoutingConfig) (bool, error) {
	rules, err := b.ipv6PolicyRules(ctx)
	if err != nil {
		return false, err
	}
	for _, rule := range rules {
		if ipv6FakeRuleMatches(rule, cfg) {
			return true, nil
		}
	}
	return false, nil
}

func (b *Backend) ipv6FakeGuardPresent(ctx context.Context, cfg platform.RoutingConfig) (bool, error) {
	rules, err := b.ipv6PolicyRules(ctx)
	if err != nil {
		return false, err
	}
	for _, rule := range rules {
		if ipv6FakeGuardMatches(rule, cfg) {
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
	if len(routes) != 1 {
		return false, nil
	}
	destination := strings.TrimSpace(routes[0].Dst)
	return destination == config.MihomoFakeIPv6Range && routes[0].Dev == cfg.TUNDevice && routes[0].Type != "prohibit" && routes[0].Type != "unreachable", nil
}

// applyIPv6PolicyRouting deliberately does not install an ingress-interface
// default route. Clients keep the main router's ordinary IPv6 RA/default route
// and public IPv6 address. Only Mihomo's synthetic IPv6 range is routed to the
// TUN after the main router (or another upstream router) forwards that prefix to
// OpenSurge. This keeps normal IPv6 traffic out of the QNAP data plane.
func (b *Backend) applyIPv6PolicyRouting(ctx context.Context, cfg platform.RoutingConfig) (err error) {
	if err := validateInterfaceName(cfg.TUNDevice); err != nil {
		return err
	}
	if cfg.TableID == 0 || cfg.RulePriority == 0 {
		return platform.NewError(platform.CodeInvalidArgument, "IPv6 routing table id and rule priority must be set")
	}
	if cfg.RulePriority == ^uint32(0) {
		return platform.NewError(platform.CodeInvalidArgument, "IPv6 rule priority leaves no room for the fake-IP fail-closed guard")
	}

	success := false
	defer func() {
		if !success {
			_ = b.removeIPv6PolicyRouting(context.Background(), cfg)
		}
	}()

	table := strconv.FormatUint(uint64(cfg.TableID), 10)
	guardPriority := strconv.FormatUint(uint64(cfg.RulePriority+1), 10)
	guard, err := b.ipv6FakeGuardPresent(ctx, cfg)
	if err != nil {
		return err
	}
	if !guard {
		if err := b.runner.run(ctx, b.runner.ipPath, "-6", "rule", "add", "pref", guardPriority, "to", config.MihomoFakeIPv6Range, "prohibit"); err != nil {
			return err
		}
	}
	if err := b.runner.run(ctx, b.runner.ipPath, "-6", "route", "replace", config.MihomoFakeIPv6Range, "dev", cfg.TUNDevice, "table", table); err != nil {
		return err
	}
	present, err := b.ipv6FakeRulePresent(ctx, cfg)
	if err != nil {
		return err
	}
	if !present {
		if err := b.runner.run(ctx, b.runner.ipPath, "-6", "rule", "add", "pref", strconv.FormatUint(uint64(cfg.RulePriority), 10), "to", config.MihomoFakeIPv6Range, "table", table); err != nil {
			return err
		}
	}
	success = true
	return nil
}

func (b *Backend) removeIPv6PolicyRouting(ctx context.Context, cfg platform.RoutingConfig) error {
	if cfg.TableID == 0 || cfg.RulePriority == 0 {
		return nil
	}
	table := strconv.FormatUint(uint64(cfg.TableID), 10)
	var failures []string
	if present, err := b.ipv6FakeRulePresent(ctx, cfg); err != nil {
		failures = append(failures, "check IPv6 fake-IP rule: "+err.Error())
	} else if present {
		if err := b.runner.run(ctx, b.runner.ipPath, "-6", "rule", "del", "pref", strconv.FormatUint(uint64(cfg.RulePriority), 10), "to", config.MihomoFakeIPv6Range, "table", table); err != nil && !isNotExist(err) {
			failures = append(failures, "delete IPv6 fake-IP rule: "+err.Error())
		}
	}
	if err := b.runner.run(ctx, b.runner.ipPath, "-6", "route", "del", config.MihomoFakeIPv6Range, "dev", cfg.TUNDevice, "table", table); err != nil && !isNotExist(err) {
		failures = append(failures, "delete IPv6 fake-IP route: "+err.Error())
	}
	if guard, err := b.ipv6FakeGuardPresent(ctx, cfg); err != nil {
		failures = append(failures, "check IPv6 fake-IP guard: "+err.Error())
	} else if guard {
		if err := b.runner.run(ctx, b.runner.ipPath, "-6", "rule", "del", "pref", strconv.FormatUint(uint64(cfg.RulePriority+1), 10), "to", config.MihomoFakeIPv6Range, "prohibit"); err != nil && !isNotExist(err) {
			failures = append(failures, "delete IPv6 fake-IP guard: "+err.Error())
		}
	}
	if len(failures) > 0 {
		return platform.NewError(platform.CodeCommandFailed, "remove IPv6 fake-IP routing: "+strings.Join(failures, "; "))
	}
	return nil
}

func (b *Backend) ipv6PolicyRoutingPresent(ctx context.Context, cfg platform.RoutingConfig) (bool, error) {
	primary, err := b.ipv6FakeRulePresent(ctx, cfg)
	if err != nil || !primary {
		return primary, err
	}
	guard, err := b.ipv6FakeGuardPresent(ctx, cfg)
	if err != nil || !guard {
		return guard, err
	}
	return b.ipv6RoutingTableMatches(ctx, cfg)
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
	return b.applyIPv6PolicyRouting(ctx, cfg)
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
