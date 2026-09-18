package cloudflareopt

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CheckCurrentHealth performs a lightweight verification of the currently
// selected addresses. It reuses the same physical-interface-bound dial path as
// full optimization, so health checks cannot silently traverse the proxy TUN.
func CheckCurrentHealth(ctx context.Context, targets []Target, results []TargetResult, options ScanOptions, health HealthSettings) ([]HealthResult, bool, error) {
	if err := validateHealth(health); err != nil {
		return nil, false, err
	}
	if len(results) == 0 {
		return []HealthResult{}, true, nil
	}

	targetByDomain := make(map[string]Target, len(targets))
	for _, target := range targets {
		if target.Enabled {
			targetByDomain[normalizeDomain(target.Domain)] = target
		}
	}

	// Health checks must stay cheap even when the selected full-scan preset is
	// intentionally deep.
	settings := options.Settings
	if settings.TCPAttempts > 2 {
		settings.TCPAttempts = 2
	}
	if settings.TCPTimeoutMS > 1000 {
		settings.TCPTimeoutMS = 1000
	}
	if settings.HTTPTimeoutMS > 2500 {
		settings.HTTPTimeoutMS = 2500
	}
	options.Settings = settings

	if options.VerifyRoute != nil {
		for _, result := range results {
			if ip := strings.TrimSpace(result.Selected.IP); ip != "" {
				if err := options.VerifyRoute(ctx, ip); err != nil {
					return nil, false, fmt.Errorf("direct route preflight failed: %w", err)
				}
				break
			}
		}
	}

	now := options.Now
	if now == nil {
		now = time.Now
	}
	checked := make([]HealthResult, 0, len(results))
	allHealthy := true

	for _, result := range results {
		if err := ctx.Err(); err != nil {
			return checked, false, err
		}
		domain := normalizeDomain(result.Domain)
		target, ok := targetByDomain[domain]
		if !ok {
			continue
		}
		ip := strings.TrimSpace(result.Selected.IP)
		item := HealthResult{Domain: domain, IP: ip, CheckedAt: now().UTC()}

		candidate, reachable := probeTCP(ctx, ip, options)
		if !reachable {
			item.LossRate = 1
			item.Error = "TCP/443 unreachable"
			checked = append(checked, item)
			allHealthy = false
			continue
		}
		item.LatencyMS = candidate.Latency.Milliseconds()
		item.LossRate = candidate.LossRate
		if item.LatencyMS > int64(health.LatencyThresholdMS) {
			item.Error = fmt.Sprintf("latency %d ms exceeds %d ms threshold", item.LatencyMS, health.LatencyThresholdMS)
			checked = append(checked, item)
			allHealthy = false
			continue
		}
		if item.LossRate > health.LossRateThreshold {
			item.Error = fmt.Sprintf("loss %.0f%% exceeds %.0f%% threshold", item.LossRate*100, health.LossRateThreshold*100)
			checked = append(checked, item)
			allHealthy = false
			continue
		}

		verified, ok := probeHTTPS(ctx, target, candidate, options)
		if !ok {
			item.Error = "HTTPS/SNI verification failed"
			checked = append(checked, item)
			allHealthy = false
			continue
		}
		item.TTFBMS = verified.TTFB.Milliseconds()
		item.Healthy = true
		checked = append(checked, item)
	}
	return checked, allHealthy, nil
}
