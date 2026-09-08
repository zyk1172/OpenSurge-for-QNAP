//go:build linux

package linux

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"open-mihomo-gateway/internal/platform"
)

const (
	// procIPv4Forward is the Linux forwarding switch. Writing /proc directly
	// avoids depending on the sysctl binary, which is not always present in a
	// minimal container image.
	procIPv4Forward = "/proc/sys/net/ipv4/ip_forward"
	// procConfDir is where per-interface knobs such as rp_filter live.
	procConfDir = "/proc/sys/net/ipv4/conf"
)

// readProcSys reads a sysctl value from /proc.
func readProcSys(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// writeProcSys writes a sysctl value to /proc and reports whether the value
// actually changed. It is a no-op when the current value already matches.
func writeProcSys(path, value string) (changed bool, err error) {
	if previous, readErr := readProcSys(path); readErr == nil && previous == value {
		return false, nil
	} else if readErr != nil {
		if writable := procSysWritable(path); !writable {
			return false, platform.NewError(platform.CodeForwardingUnavailable,
				fmt.Sprintf("%s is not writable; configure the sysctl in Compose instead of running privileged", path))
		}
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		return false, platform.NewError(platform.CodeForwardingUnavailable,
			fmt.Sprintf("write %s: %v", path, err)).Wrap(err)
	}
	return true, nil
}

func procSysWritable(path string) bool {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func rpFilterTargets(cfg platform.RoutingConfig) []string {
	targets := []string{"all", "default"}
	if cfg.LANInterface != "" {
		targets = append(targets, cfg.LANInterface)
	}
	if cfg.TUNDevice != "" {
		targets = append(targets, cfg.TUNDevice)
	}
	return targets
}

func (b *Backend) setRPFilter(targets []string) (map[string]string, error) {
	previous := map[string]string{}
	for _, target := range targets {
		path := filepath.Join(procConfDir, target, "rp_filter")
		before, err := readProcSys(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return previous, platform.NewError(platform.CodeForwardingUnavailable,
				fmt.Sprintf("read %s", path)).Wrap(err)
		}
		previous[target] = before
		if before == "0" {
			continue
		}
		if _, err := writeProcSys(path, "0"); err != nil {
			return previous, err
		}
	}
	return previous, nil
}

func (b *Backend) restoreRPFilter(previous map[string]string) error {
	var failures []string
	for target, value := range previous {
		path := filepath.Join(procConfDir, target, "rp_filter")
		if _, err := readProcSys(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			failures = append(failures, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if _, err := writeProcSys(path, value); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", path, err))
		}
	}
	if len(failures) > 0 {
		return platform.NewError(platform.CodeCommandFailed, "restore rp_filter").
			WithDetail("failures", strings.Join(failures, "; "))
	}
	return nil
}

func ruleSpec(cfg platform.RoutingConfig) string {
	return fmt.Sprintf("pref %d fwmark %s lookup %d", cfg.RulePriority, markHex(cfg.FwMark), cfg.TableID)
}

func markHex(mark uint32) string {
	return fmt.Sprintf("0x%x", mark)
}

// ipRule is the subset of `ip -j rule show` relevant to ownership. Different
// iproute2 versions encode table/mark fields as either JSON strings or numbers,
// so RawMessage is parsed deliberately instead of relying on a fragile struct.
type ipRule map[string]json.RawMessage

func parseRawUint32(raw json.RawMessage) (uint32, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		value, err := strconv.ParseUint(strings.TrimSpace(text), 0, 32)
		if err == nil {
			return uint32(value), true
		}
		return 0, false
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		value, err := strconv.ParseUint(number.String(), 10, 32)
		if err == nil {
			return uint32(value), true
		}
	}
	return 0, false
}

func (b *Backend) policyRules(ctx context.Context) ([]ipRule, error) {
	out, err := b.runner.output(ctx, b.runner.ipPath, "-j", "rule", "show")
	if err != nil {
		return nil, err
	}
	var rules []ipRule
	if err := json.Unmarshal(out, &rules); err != nil {
		return nil, platform.NewError(platform.CodeCommandFailed, "parse ip -j rule show output").Wrap(err)
	}
	return rules, nil
}

func ruleField(rule ipRule, key string) (uint32, bool) {
	raw, ok := rule[key]
	if !ok {
		return 0, false
	}
	return parseRawUint32(raw)
}

func ruleMatchesConfig(rule ipRule, cfg platform.RoutingConfig) bool {
	priority, priorityOK := ruleField(rule, "priority")
	table, tableOK := ruleField(rule, "table")
	mark, markOK := ruleField(rule, "fwmark")
	return priorityOK && tableOK && markOK &&
		priority == cfg.RulePriority && table == cfg.TableID && mark == cfg.FwMark
}

// rulePresent reports whether the exact OpenSurge policy rule exists.
func (b *Backend) rulePresent(ctx context.Context, cfg platform.RoutingConfig) (bool, error) {
	rules, err := b.policyRules(ctx)
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

func (b *Backend) routingTableHasEntries(ctx context.Context, tableID uint32) (bool, error) {
	if tableID == 0 {
		return false, nil
	}
	out, err := b.runner.output(ctx, b.runner.ipPath, "-j", "route", "show", "table",
		strconv.FormatUint(uint64(tableID), 10))
	if err != nil {
		if isNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var routes []json.RawMessage
	if err := json.Unmarshal(out, &routes); err != nil {
		return false, platform.NewError(platform.CodeCommandFailed, "parse ip -j route output").Wrap(err)
	}
	return len(routes) > 0, nil
}

// validateOwnership proves every kernel identifier OpenSurge wants is free.
// "Uncommon" ids are not ownership. If anything already occupies the table,
// mark or priority, start fails before any mutation instead of guessing.
func (b *Backend) validateOwnership(ctx context.Context, cfg platform.NetworkConfig) error {
	tableName := strings.TrimSpace(cfg.NFTTableName)
	if tableName == "" {
		tableName = DefaultTableName
	}
	if err := validateTableName(tableName); err != nil {
		return err
	}
	if exists, err := b.tableExists(ctx, tableName); err != nil {
		return err
	} else if exists {
		return platform.NewError(platform.CodeNFTablesForeignTable,
			"the requested nftables table already exists; OpenSurge will not overwrite an unproven owner").
			WithDetail("table", tableName)
	}

	if cfg.RouteTableID == 0 || cfg.FwMark == 0 || cfg.RouteRulePriority == 0 {
		return platform.NewError(platform.CodeInvalidArgument,
			"route table id, fwmark and rule priority must all be non-zero before ownership validation")
	}
	if entries, err := b.routingTableHasEntries(ctx, cfg.RouteTableID); err != nil {
		return err
	} else if entries {
		return platform.NewError(platform.CodePolicyRoutingConflict,
			"the requested policy routing table already contains routes").
			WithDetail("table_id", strconv.FormatUint(uint64(cfg.RouteTableID), 10))
	}

	rules, err := b.policyRules(ctx)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		priority, priorityOK := ruleField(rule, "priority")
		table, tableOK := ruleField(rule, "table")
		mark, markOK := ruleField(rule, "fwmark")
		if (priorityOK && priority == cfg.RouteRulePriority) ||
			(tableOK && table == cfg.RouteTableID) ||
			(markOK && mark == cfg.FwMark) {
			return platform.NewError(platform.CodePolicyRoutingConflict,
				"an existing policy rule collides with OpenSurge's requested priority, table or fwmark").
				WithDetails(map[string]string{
					"rule_priority": strconv.FormatUint(uint64(cfg.RouteRulePriority), 10),
					"table_id":      strconv.FormatUint(uint64(cfg.RouteTableID), 10),
					"fw_mark":       markHex(cfg.FwMark),
				})
		}
	}
	return nil
}

// applyPolicyRouting installs the dedicated routing table and exact fwmark rule.
func (b *Backend) applyPolicyRouting(ctx context.Context, cfg platform.RoutingConfig) error {
	if err := validateInterfaceName(cfg.LANInterface); err != nil {
		return err
	}
	if _, err := validateCIDR(cfg.LANCIDR); err != nil {
		return err
	}
	if cfg.TableID == 0 || cfg.RulePriority == 0 {
		return platform.NewError(platform.CodeInvalidArgument, "routing table id and rule priority must be set")
	}
	if cfg.FwMark == 0 {
		return platform.NewError(platform.CodeInvalidArgument, "fwmark must be non-zero")
	}
	table := strconv.FormatUint(uint64(cfg.TableID), 10)

	if err := b.runner.run(ctx, b.runner.ipPath, "route", "replace", cfg.LANCIDR,
		"dev", cfg.LANInterface, "scope", "link", "table", table); err != nil {
		return err
	}

	if cfg.DirectFallback {
		if _, err := validateIPv4(cfg.UpstreamGateway); err != nil {
			return err
		}
		egress := cfg.LANInterface
		if strings.TrimSpace(cfg.UpstreamInterface) != "" {
			egress = cfg.UpstreamInterface
		}
		if err := validateInterfaceName(egress); err != nil {
			return err
		}
		if err := b.runner.run(ctx, b.runner.ipPath, "route", "replace", "default",
			"via", cfg.UpstreamGateway, "dev", egress, "table", table); err != nil {
			return err
		}
	} else {
		if err := validateInterfaceName(cfg.TUNDevice); err != nil {
			return err
		}
		if err := b.runner.run(ctx, b.runner.ipPath, "route", "replace", "default",
			"dev", cfg.TUNDevice, "table", table); err != nil {
			return err
		}
	}

	present, err := b.rulePresent(ctx, cfg)
	if err != nil {
		return err
	}
	if !present {
		args := []string{"rule", "add", "pref", strconv.FormatUint(uint64(cfg.RulePriority), 10),
			"fwmark", markHex(cfg.FwMark), "table", table}
		if err := b.runner.run(ctx, b.runner.ipPath, args...); err != nil {
			return err
		}
	}
	return nil
}

// removePolicyRouting removes only the exact rule and the two exact routes
// OpenSurge creates. It intentionally does not flush the whole routing table.
func (b *Backend) removePolicyRouting(ctx context.Context, cfg platform.RoutingConfig) error {
	if cfg.TableID == 0 || cfg.FwMark == 0 || cfg.RulePriority == 0 {
		return nil
	}
	table := strconv.FormatUint(uint64(cfg.TableID), 10)
	var failures []string

	present, err := b.rulePresent(ctx, cfg)
	if err != nil {
		failures = append(failures, fmt.Sprintf("check rule: %v", err))
	} else if present {
		if err := b.runner.run(ctx, b.runner.ipPath, "rule", "del",
			"pref", strconv.FormatUint(uint64(cfg.RulePriority), 10),
			"fwmark", markHex(cfg.FwMark), "table", table); err != nil && !isNotExist(err) {
			failures = append(failures, fmt.Sprintf("delete rule: %v", err))
		}
	}

	if strings.TrimSpace(cfg.LANCIDR) != "" && strings.TrimSpace(cfg.LANInterface) != "" {
		if err := b.runner.run(ctx, b.runner.ipPath, "route", "del", cfg.LANCIDR,
			"dev", cfg.LANInterface, "table", table); err != nil && !isNotExist(err) {
			failures = append(failures, fmt.Sprintf("delete LAN route: %v", err))
		}
	}
	if cfg.DirectFallback {
		egress := cfg.LANInterface
		if strings.TrimSpace(cfg.UpstreamInterface) != "" {
			egress = cfg.UpstreamInterface
		}
		if strings.TrimSpace(cfg.UpstreamGateway) != "" && strings.TrimSpace(egress) != "" {
			if err := b.runner.run(ctx, b.runner.ipPath, "route", "del", "default",
				"via", cfg.UpstreamGateway, "dev", egress, "table", table); err != nil && !isNotExist(err) {
				failures = append(failures, fmt.Sprintf("delete direct-fallback route: %v", err))
			}
		}
	} else if strings.TrimSpace(cfg.TUNDevice) != "" {
		if err := b.runner.run(ctx, b.runner.ipPath, "route", "del", "default",
			"dev", cfg.TUNDevice, "table", table); err != nil && !isNotExist(err) {
			failures = append(failures, fmt.Sprintf("delete TUN route: %v", err))
		}
	}

	if len(failures) > 0 {
		joined := strings.Join(failures, "; ")
		return platform.NewError(platform.CodeCommandFailed, "remove policy routing: "+joined).
			WithDetail("failures", joined)
	}
	return nil
}

func (b *Backend) listRoutes(ctx context.Context, tableID uint32) (string, error) {
	out, err := b.runner.output(ctx, b.runner.ipPath, "route", "show", "table",
		strconv.FormatUint(uint64(tableID), 10))
	if err != nil {
		if isNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(out), nil
}

func (b *Backend) listRules(ctx context.Context) (string, error) {
	out, err := b.runner.output(ctx, b.runner.ipPath, "rule", "show")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
