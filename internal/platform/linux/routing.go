//go:build linux

package linux

import (
	"context"
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
// actually changed.
//
// It is a no-op when the target already holds the desired value. That matters
// in a container: Docker mounts /proc/sys read-only unless the operator opts in
// via `sysctls:`, so an unconditional write would fail on a host that is already
// configured correctly. Skipping the write also makes every call idempotent.
func writeProcSys(path, value string) (changed bool, err error) {
	if previous, readErr := readProcSys(path); readErr == nil && previous == value {
		return false, nil
	} else if readErr != nil {
		if writable := procSysWritable(path); !writable {
			return false, platform.NewError(platform.CodeForwardingUnavailable,
				fmt.Sprintf("%s is not writable; add sysctls: to the Compose file (see README) instead of running privileged", path))
		}
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		return false, platform.NewError(platform.CodeForwardingUnavailable,
			fmt.Sprintf("write %s: %v", path, err)).Wrap(err)
	}
	return true, nil
}

// procSysWritable probes whether a /proc/sys knob can be written at all.
// Opening O_WRONLY without writing is enough: on a read-only mount the open
// fails with EROFS. The file is never modified.
func procSysWritable(path string) bool {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

// rpFilterTargets lists the interfaces whose reverse-path filter must be
// relaxed. Asymmetric routing between the TUN and the LAN interface is normal
// for a transparent gateway, and a strict rp_filter silently drops those
// packets, which is one of the hardest failures to diagnose.
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

// setRPFilter disables reverse-path filtering on the given interfaces and
// returns the previous values for restoration.
func (b *Backend) setRPFilter(targets []string) (map[string]string, error) {
	previous := map[string]string{}
	for _, target := range targets {
		path := filepath.Join(procConfDir, target, "rp_filter")
		before, err := readProcSys(path)
		if err != nil {
			// The TUN interface may not exist yet; that is not an error.
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

// restoreRPFilter puts back the captured rp_filter values.
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

// ruleSpec is the content-scoped description of the OpenSurge policy rule.
// Matching on content (fwmark + table), rather than on priority alone, is what
// keeps removal from deleting a foreign rule that happens to use the same pref.
func ruleSpec(cfg platform.RoutingConfig) string {
	return fmt.Sprintf("fwmark %s lookup %d", markHex(cfg.FwMark), cfg.TableID)
}

func markHex(mark uint32) string {
	return fmt.Sprintf("0x%x", mark)
}

// rulePresent reports whether our policy rule already exists.
func (b *Backend) rulePresent(ctx context.Context, cfg platform.RoutingConfig) (bool, error) {
	out, err := b.runner.output(ctx, b.runner.ipPath, "rule", "show")
	if err != nil {
		return false, err
	}
	wantTable := fmt.Sprintf("lookup %d", cfg.TableID)
	// ip prints the mark with and without zero padding depending on version, so
	// accept both renderings.
	wantMarks := []string{
		markHex(cfg.FwMark),
		fmt.Sprintf("0x%08x", cfg.FwMark),
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, wantTable) {
			continue
		}
		for _, mark := range wantMarks {
			if strings.Contains(line, mark) {
				return true, nil
			}
		}
	}
	return false, nil
}

// applyPolicyRouting installs the dedicated routing table and the fwmark rule.
// It is idempotent: re-running with the same configuration converges instead of
// duplicating state or failing.
func (b *Backend) applyPolicyRouting(ctx context.Context, cfg platform.RoutingConfig) error {
	if err := validateInterfaceName(cfg.LANInterface); err != nil {
		return err
	}
	if _, err := validateCIDR(cfg.LANCIDR); err != nil {
		return err
	}
	if cfg.TableID == 0 {
		return platform.NewError(platform.CodeInvalidArgument, "routing table id must be set")
	}
	if cfg.FwMark == 0 {
		return platform.NewError(platform.CodeInvalidArgument, "fwmark must be non-zero")
	}
	table := strconv.FormatUint(uint64(cfg.TableID), 10)

	// LAN traffic must stay on the LAN even inside our table, so a mis-marked
	// packet degrades to direct delivery instead of disappearing into the TUN.
	if err := b.runner.run(ctx, b.runner.ipPath, "route", "replace", cfg.LANCIDR,
		"dev", cfg.LANInterface, "scope", "link", "table", table); err != nil {
		return err
	}

	if cfg.DirectFallback {
		if _, err := validateIPv4(cfg.UpstreamGateway); err != nil {
			return err
		}
		if err := b.runner.run(ctx, b.runner.ipPath, "route", "replace", "default",
			"via", cfg.UpstreamGateway, "dev", cfg.LANInterface, "table", table); err != nil {
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
		args := []string{"rule", "add", "fwmark", markHex(cfg.FwMark),
			"table", table, "pref", strconv.FormatUint(uint64(cfg.RulePriority), 10)}
		if err := b.runner.run(ctx, b.runner.ipPath, args...); err != nil {
			return err
		}
	}
	return nil
}

// removePolicyRouting deletes only the rule and table OpenSurge created.
// Flushing is scoped to our own table id and the rule match is content-based,
// so nothing belonging to QNAP, Container Station or the user is touched.
func (b *Backend) removePolicyRouting(ctx context.Context, cfg platform.RoutingConfig) error {
	if cfg.TableID == 0 || cfg.FwMark == 0 {
		return nil
	}
	table := strconv.FormatUint(uint64(cfg.TableID), 10)
	var failures []string

	present, err := b.rulePresent(ctx, cfg)
	if err != nil {
		failures = append(failures, fmt.Sprintf("check rule: %v", err))
	} else if present {
		if err := b.runner.run(ctx, b.runner.ipPath, "rule", "del",
			"fwmark", markHex(cfg.FwMark), "table", table); err != nil && !isNotExist(err) {
			failures = append(failures, fmt.Sprintf("delete rule: %v", err))
		}
	}
	// Flushing a table we own is the only reliable way to clear every route we
	// may have added to it, including stale ones from an interrupted run.
	if err := b.runner.run(ctx, b.runner.ipPath, "route", "flush", "table", table); err != nil {
		if !isNotExist(err) {
			failures = append(failures, fmt.Sprintf("flush table %s: %v", table, err))
		}
	}
	if len(failures) > 0 {
		joined := strings.Join(failures, "; ")
		return platform.NewError(platform.CodeCommandFailed, "remove policy routing: "+joined).
			WithDetail("failures", joined)
	}
	return nil
}

// listRoutes returns the OpenSurge routing table for diagnostics.
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

// listRules returns the policy rules for diagnostics.
func (b *Backend) listRules(ctx context.Context) (string, error) {
	out, err := b.runner.output(ctx, b.runner.ipPath, "rule", "show")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
