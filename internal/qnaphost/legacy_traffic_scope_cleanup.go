package qnaphost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	legacyTrafficScopeStateFile = "qnap-traffic-scopes.json"
	legacyTrafficScopeTable     = "20244"
	legacyTrafficScopeProto     = "244"
)

type legacyTrafficScopeState struct {
	Selectors []legacyTrafficScopeSelector `json:"selectors"`
}

type legacyTrafficScopeSelector struct {
	SourceIPv4       string `json:"source_ipv4"`
	IngressInterface string `json:"ingress_interface"`
	MainPriority     int    `json:"main_priority"`
	ProxyPriority    int    `json:"proxy_priority"`
}

// CleanupLegacyTrafficScopes removes routing state left by the retired
// per-container traffic-takeover feature. The persisted selector journal is
// used to delete only exact rules previously owned by OpenSurge.
func CleanupLegacyTrafficScopes(ctx context.Context, storeDir string) error {
	statePath := filepath.Join(storeDir, legacyTrafficScopeStateFile)
	payload, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var state legacyTrafficScopeState
	if err := json.Unmarshal(payload, &state); err != nil {
		return fmt.Errorf("read legacy traffic-scope state: %w", err)
	}

	netNSPath := strings.TrimSpace(os.Getenv("OPENSURGE_HOST_NETNS_PATH"))
	if netNSPath == "" {
		netNSPath = defaultHostNetNS
	}
	if _, err := os.Stat(netNSPath); err != nil {
		return fmt.Errorf("host network namespace is unavailable at %s; cannot remove legacy traffic-scope rules", netNSPath)
	}

	runHost := func(args ...string) ([]byte, error) {
		nsArgs := []string{"--net=" + netNSPath, "--", "ip"}
		nsArgs = append(nsArgs, args...)
		cmd := exec.CommandContext(ctx, "nsenter", nsArgs...)
		return cmd.CombinedOutput()
	}

	for _, selector := range state.Selectors {
		ip := net.ParseIP(strings.TrimSpace(selector.SourceIPv4))
		if ip == nil || ip.To4() == nil || selector.IngressInterface == "" {
			continue
		}
		source := ip.To4().String() + "/32"
		if selector.MainPriority > 0 {
			_, _ = runHost("-4", "rule", "del",
				"pref", strconv.Itoa(selector.MainPriority),
				"from", source,
				"iif", selector.IngressInterface,
				"table", "main",
				"suppress_prefixlength", "0")
		}
		if selector.ProxyPriority > 0 {
			_, _ = runHost("-4", "rule", "del",
				"pref", strconv.Itoa(selector.ProxyPriority),
				"from", source,
				"iif", selector.IngressInterface,
				"table", legacyTrafficScopeTable)
		}
	}

	// Table 20244 and route protocol 244 were reserved exclusively for the
	// retired feature. Restrict the cleanup to that protocol rather than
	// flushing unrelated routes.
	_, _ = runHost("-4", "route", "flush", "table", legacyTrafficScopeTable, "proto", legacyTrafficScopeProto)

	rules, err := runHost("-4", "rule", "show")
	if err != nil {
		return fmt.Errorf("verify legacy traffic-scope cleanup: %w", err)
	}
	for _, selector := range state.Selectors {
		ip := net.ParseIP(strings.TrimSpace(selector.SourceIPv4))
		if ip == nil || ip.To4() == nil || selector.IngressInterface == "" {
			continue
		}
		source := "from " + ip.To4().String()
		for _, priority := range []int{selector.MainPriority, selector.ProxyPriority} {
			if priority <= 0 {
				continue
			}
			prefix := strconv.Itoa(priority) + ":"
			for _, line := range strings.Split(string(rules), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, prefix) &&
					strings.Contains(line, source) &&
					strings.Contains(line, "iif "+selector.IngressInterface) {
					return fmt.Errorf("legacy traffic-scope rule still present at priority %d", priority)
				}
			}
		}
	}

	if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove legacy traffic-scope state: %w", err)
	}
	return nil
}
