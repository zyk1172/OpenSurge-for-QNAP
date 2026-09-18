package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"open-mihomo-gateway/internal/cloudflareopt"
	"open-mihomo-gateway/internal/config"
)

const cloudflareOptimizerMaxBody = 256 << 10

type cloudflareOptimizerAPI struct {
	configPath string
	storeDir   string
	runner     ActionRunner
	next       http.Handler

	mu      sync.Mutex
	scanMu  sync.Mutex
	started sync.Once
}

func WrapCloudflareOptimizer(next http.Handler, configPath, storeDir string, runner ActionRunner) http.Handler {
	api := &cloudflareOptimizerAPI{configPath: configPath, storeDir: storeDir, runner: runner, next: next}
	api.started.Do(func() { go api.schedulerLoop() })
	return api
}

func (a *cloudflareOptimizerAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/cloudflare-opt" {
		switch r.Method {
		case http.MethodGet:
			a.handleGet(w, r)
		case http.MethodPut:
			a.handlePut(w, r)
		default:
			w.Header().Set("Allow", "GET, PUT")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if r.URL.Path == "/api/v1/cloudflare-opt/scan" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		a.handleScan(w, r)
		return
	}
	if r.URL.Path == "/api/v1/cloudflare-opt/check" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		a.handleCheck(w, r)
		return
	}
	a.next.ServeHTTP(w, r)
}

func (a *cloudflareOptimizerAPI) handleGet(w http.ResponseWriter, _ *http.Request) {
	cfg, err := a.loadConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cloudflare_optimizer_config_invalid", err.Error())
		return
	}
	state, err := a.loadState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cloudflare_optimizer_state_invalid", err.Error())
		return
	}
	a.populateNextRun(&state, cfg)
	a.populateNextHealthCheck(&state, cfg)
	writeJSON(w, http.StatusOK, cloudflareopt.Response{Config: cfg, State: state})
}

func (a *cloudflareOptimizerAPI) handlePut(w http.ResponseWriter, r *http.Request) {
	var cfg cloudflareopt.Config
	if err := decodeJSON(r, &cfg, cloudflareOptimizerMaxBody); err != nil {
		writeError(w, http.StatusBadRequest, "cloudflare_optimizer_invalid", err.Error())
		return
	}
	cfg = cloudflareopt.Normalize(cfg)
	if err := cloudflareopt.Validate(cfg); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "cloudflare_optimizer_invalid", err.Error())
		return
	}
	if err := a.saveConfig(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "cloudflare_optimizer_save_failed", err.Error())
		return
	}
	state, err := a.loadState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cloudflare_optimizer_state_invalid", err.Error())
		return
	}

	filtered := retainEnabledOptimizerResults(state.Results, cfg.Targets)
	previousHealth := append([]cloudflareopt.HealthResult(nil), state.Health...)
	state.Health = retainEnabledOptimizerHealth(state.Health, cfg.Targets)
	if !sameOptimizerResults(state.Results, filtered) {
		previous := append([]cloudflareopt.TargetResult(nil), state.Results...)
		state.Results = filtered
		if err := a.saveState(state); err != nil {
			writeError(w, http.StatusInternalServerError, "cloudflare_optimizer_save_failed", err.Error())
			return
		}
		gatewayCfg, err := config.Load(a.configPath)
		if err != nil {
			state.Results = previous
			state.Health = previousHealth
			_ = a.saveState(state)
			writeError(w, http.StatusInternalServerError, "config_invalid", err.Error())
			return
		}
		if err := a.applyResults(r.Context(), gatewayCfg); err != nil {
			state.Results = previous
			state.Health = previousHealth
			_ = a.saveState(state)
			writeError(w, http.StatusUnprocessableEntity, "cloudflare_optimizer_apply_failed", err.Error())
			return
		}
	}
	a.populateNextRun(&state, cfg)
	a.populateNextHealthCheck(&state, cfg)
	if err := a.saveState(state); err != nil {
		writeError(w, http.StatusInternalServerError, "cloudflare_optimizer_save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cloudflareopt.Response{Config: cfg, State: state})
}

func (a *cloudflareOptimizerAPI) handleScan(w http.ResponseWriter, r *http.Request) {
	response, err := a.runScan(r.Context())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "cloudflare_optimizer_scan_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *cloudflareOptimizerAPI) handleCheck(w http.ResponseWriter, r *http.Request) {
	response, _, err := a.runHealthCheck(r.Context())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "cloudflare_optimizer_health_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *cloudflareOptimizerAPI) runScan(ctx context.Context) (cloudflareopt.Response, error) {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()

	cfg, err := a.loadConfig()
	if err != nil {
		return cloudflareopt.Response{}, err
	}
	if err := cloudflareopt.Validate(cfg); err != nil {
		return cloudflareopt.Response{}, err
	}
	if !hasEnabledOptimizerTargets(cfg.Targets) {
		return cloudflareopt.Response{}, fmt.Errorf("no enabled Cloudflare optimizer targets")
	}

	gatewayCfg, err := config.Load(a.configPath)
	if err != nil {
		return cloudflareopt.Response{}, err
	}
	state, err := a.loadState()
	if err != nil {
		return cloudflareopt.Response{}, err
	}
	previousResults := append([]cloudflareopt.TargetResult(nil), state.Results...)
	now := time.Now().UTC()
	state.SchemaVersion = cloudflareopt.SchemaVersion
	state.Running = true
	state.Checking = false
	state.StartedAt = &now
	state.LastError = ""
	if err := a.saveState(state); err != nil {
		return cloudflareopt.Response{}, err
	}

	finish := func(runErr error) {
		finished := time.Now().UTC()
		state.Running = false
		state.StartedAt = nil
		state.LastRunAt = &finished
		if runErr != nil {
			state.LastError = runErr.Error()
		}
		a.populateNextRun(&state, cfg)
		a.populateNextHealthCheck(&state, cfg)
		_ = a.saveState(state)
	}

	output, err := cloudflareopt.RunScan(ctx, cfg.Targets, cloudflareopt.ScanOptions{
		Interface:  gatewayCfg.Gateway.Interface,
		SourceIPv4: gatewayCfg.Gateway.LANIP,
		Settings:   cfg.Scan,
		VerifyRoute: func(ctx context.Context, candidate string) error {
			return cloudflareopt.VerifyDirectRoute(ctx, gatewayCfg.Gateway.Interface, gatewayCfg.Gateway.LANIP, candidate)
		},
	}, nil)
	if err != nil {
		finish(err)
		return cloudflareopt.Response{}, err
	}

	// A hard scan budget may end after some hostnames have already produced a
	// fresh result. Keep the last verified result for enabled hostnames omitted
	// from this run instead of temporarily deleting their Hosts entry. Explicitly
	// disabled or removed targets are filtered out by configuration updates.
	mergedResults := mergeBoundedScanResults(previousResults, output.Results, cfg.Targets)
	changed := !sameOptimizerResults(previousResults, mergedResults)
	state.Results = mergedResults
	if changed {
		// Runtime reconciliation reads the persisted state, so publish the new
		// result atomically before invoking the transactional gateway reload.
		if err := a.saveState(state); err != nil {
			finish(err)
			return cloudflareopt.Response{}, err
		}
		if err := a.applyResults(ctx, gatewayCfg); err != nil {
			state.Results = previousResults
			_ = a.saveState(state)
			// Best-effort restore if the failed lifecycle got far enough to observe
			// the provisional result. The lifecycle itself already rolls its config
			// transaction back; this second pass restores the optimizer input.
			_ = a.applyResults(context.Background(), gatewayCfg)
			finish(err)
			return cloudflareopt.Response{}, err
		}
	}
	healthNow := time.Now().UTC()
	state.Health = healthFromScanResults(output.Results, healthNow)
	state.LastHealthCheckAt = &healthNow
	finish(nil)
	return cloudflareopt.Response{Config: cfg, State: state}, nil
}

func (a *cloudflareOptimizerAPI) runHealthCheck(ctx context.Context) (cloudflareopt.Response, bool, error) {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()

	cfg, err := a.loadConfig()
	if err != nil {
		return cloudflareopt.Response{}, false, err
	}
	if err := cloudflareopt.Validate(cfg); err != nil {
		return cloudflareopt.Response{}, false, err
	}
	if !hasEnabledOptimizerTargets(cfg.Targets) {
		return cloudflareopt.Response{}, false, fmt.Errorf("no enabled Cloudflare optimizer targets")
	}
	gatewayCfg, err := config.Load(a.configPath)
	if err != nil {
		return cloudflareopt.Response{}, false, err
	}
	state, err := a.loadState()
	if err != nil {
		return cloudflareopt.Response{}, false, err
	}
	if len(state.Results) == 0 {
		a.populateNextRun(&state, cfg)
		a.populateNextHealthCheck(&state, cfg)
		return cloudflareopt.Response{Config: cfg, State: state}, false, nil
	}

	state.Checking = true
	state.LastError = ""
	if err := a.saveState(state); err != nil {
		return cloudflareopt.Response{}, false, err
	}
	finish := func(checkErr error, health []cloudflareopt.HealthResult) {
		checkedAt := time.Now().UTC()
		state.Checking = false
		state.LastHealthCheckAt = &checkedAt
		if checkErr != nil {
			state.LastError = checkErr.Error()
		} else {
			state.Health = health
		}
		a.populateNextRun(&state, cfg)
		a.populateNextHealthCheck(&state, cfg)
		_ = a.saveState(state)
	}

	health, allHealthy, err := cloudflareopt.CheckCurrentHealth(ctx, cfg.Targets, state.Results, cloudflareopt.ScanOptions{
		Interface:  gatewayCfg.Gateway.Interface,
		SourceIPv4: gatewayCfg.Gateway.LANIP,
		Settings:   cfg.Scan,
		VerifyRoute: func(ctx context.Context, candidate string) error {
			return cloudflareopt.VerifyDirectRoute(ctx, gatewayCfg.Gateway.Interface, gatewayCfg.Gateway.LANIP, candidate)
		},
	}, cfg.Health)
	if err != nil {
		finish(err, nil)
		return cloudflareopt.Response{}, false, err
	}
	finish(nil, health)
	return cloudflareopt.Response{Config: cfg, State: state}, !allHealthy, nil
}

func (a *cloudflareOptimizerAPI) applyResults(ctx context.Context, cfg config.Config) error {
	if state, exists := currentBootRuntimeState(cfg); exists && state.ProfileDigest != "" {
		if err := a.runner.Run(ctx, "reload", a.configPath); err != nil {
			return fmt.Errorf("apply optimizer results: %w", err)
		}
		return nil
	}
	// No running gateway exists. The next start/recovery reconciles the persisted
	// optimizer state into the effective profile before Mihomo starts.
	return nil
}

func (a *cloudflareOptimizerAPI) schedulerLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for now := range ticker.C {
		cfg, err := a.loadConfig()
		if err != nil || !hasEnabledOptimizerTargets(cfg.Targets) {
			continue
		}
		state, err := a.loadState()
		if err != nil || state.Running || state.Checking {
			continue
		}
		a.populateNextRun(&state, cfg)
		a.populateNextHealthCheck(&state, cfg)

		if state.NextRunAt != nil && !now.Before(*state.NextRunAt) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Scan.BudgetSeconds+20)*time.Second)
			_, _ = a.runScan(ctx)
			cancel()
			continue
		}
		if state.NextHealthCheckAt == nil || now.Before(*state.NextHealthCheckAt) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, needsScan, checkErr := a.runHealthCheck(ctx)
		cancel()
		if checkErr != nil || !needsScan {
			continue
		}
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(cfg.Scan.BudgetSeconds+20)*time.Second)
		_, _ = a.runScan(ctx)
		cancel()
	}
}

func (a *cloudflareOptimizerAPI) populateNextRun(state *cloudflareopt.State, cfg cloudflareopt.Config) {
	if !cfg.Enabled || !hasEnabledOptimizerTargets(cfg.Targets) {
		state.NextRunAt = nil
		return
	}
	next, err := cloudflareopt.NextRun(cfg.Schedule, time.Now(), state.LastRunAt)
	if err != nil {
		state.LastError = err.Error()
		state.NextRunAt = nil
		return
	}
	next = next.UTC()
	state.NextRunAt = &next
}

func (a *cloudflareOptimizerAPI) populateNextHealthCheck(state *cloudflareopt.State, cfg cloudflareopt.Config) {
	if !cfg.Health.Enabled || !hasEnabledOptimizerTargets(cfg.Targets) || len(state.Results) == 0 {
		state.NextHealthCheckAt = nil
		return
	}
	var anchor time.Time
	if state.LastHealthCheckAt != nil && !state.LastHealthCheckAt.IsZero() {
		anchor = *state.LastHealthCheckAt
	} else if state.LastRunAt != nil && !state.LastRunAt.IsZero() {
		anchor = *state.LastRunAt
	} else {
		next := time.Now().UTC()
		state.NextHealthCheckAt = &next
		return
	}
	next := anchor.Add(time.Duration(cfg.Health.CheckIntervalMinutes) * time.Minute).UTC()
	state.NextHealthCheckAt = &next
}

func (a *cloudflareOptimizerAPI) loadConfig() (cloudflareopt.Config, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	path := filepath.Join(a.storeDir, "cloudflare-optimizer.json")
	var cfg cloudflareopt.Config
	if err := readJSON(path, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cloudflareopt.DefaultConfig(), nil
		}
		return cloudflareopt.Config{}, err
	}
	cfg = cloudflareopt.Normalize(cfg)
	return cfg, cloudflareopt.Validate(cfg)
}

func (a *cloudflareOptimizerAPI) saveConfig(cfg cloudflareopt.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	data, err := json.MarshalIndent(cloudflareopt.Normalize(cfg), "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(a.storeDir, "cloudflare-optimizer.json"), append(data, '\n'), 0o600)
}

func (a *cloudflareOptimizerAPI) loadState() (cloudflareopt.State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := cloudflareopt.State{SchemaVersion: cloudflareopt.SchemaVersion, Health: []cloudflareopt.HealthResult{}, Results: []cloudflareopt.TargetResult{}}
	if err := readJSON(filepath.Join(a.storeDir, "cloudflare-optimizer-state.json"), &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}
		return cloudflareopt.State{}, err
	}
	if state.Results == nil {
		state.Results = []cloudflareopt.TargetResult{}
	}
	if state.Health == nil {
		state.Health = []cloudflareopt.HealthResult{}
	}
	return state, nil
}

func (a *cloudflareOptimizerAPI) saveState(state cloudflareopt.State) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	state.SchemaVersion = cloudflareopt.SchemaVersion
	if state.Results == nil {
		state.Results = []cloudflareopt.TargetResult{}
	}
	if state.Health == nil {
		state.Health = []cloudflareopt.HealthResult{}
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(a.storeDir, "cloudflare-optimizer-state.json"), append(data, '\n'), 0o600)
}

func LoadCloudflareOptimizerResults(storeDir string) ([]cloudflareopt.TargetResult, error) {
	var state cloudflareopt.State
	if err := readJSON(filepath.Join(storeDir, "cloudflare-optimizer-state.json"), &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return append([]cloudflareopt.TargetResult(nil), state.Results...), nil
}

func hasEnabledOptimizerTargets(targets []cloudflareopt.Target) bool {
	for _, target := range targets {
		if target.Enabled {
			return true
		}
	}
	return false
}

func optimizerDomain(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func enabledOptimizerDomains(targets []cloudflareopt.Target) map[string]bool {
	out := map[string]bool{}
	for _, target := range targets {
		if target.Enabled {
			if domain := optimizerDomain(target.Domain); domain != "" {
				out[domain] = true
			}
		}
	}
	return out
}

func retainEnabledOptimizerResults(results []cloudflareopt.TargetResult, targets []cloudflareopt.Target) []cloudflareopt.TargetResult {
	enabled := enabledOptimizerDomains(targets)
	out := make([]cloudflareopt.TargetResult, 0, len(results))
	for _, result := range results {
		if enabled[optimizerDomain(result.Domain)] {
			out = append(out, result)
		}
	}
	sort.Slice(out, func(i, j int) bool { return optimizerDomain(out[i].Domain) < optimizerDomain(out[j].Domain) })
	return out
}

func retainEnabledOptimizerHealth(health []cloudflareopt.HealthResult, targets []cloudflareopt.Target) []cloudflareopt.HealthResult {
	enabled := enabledOptimizerDomains(targets)
	out := make([]cloudflareopt.HealthResult, 0, len(health))
	for _, item := range health {
		if enabled[optimizerDomain(item.Domain)] {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return optimizerDomain(out[i].Domain) < optimizerDomain(out[j].Domain) })
	return out
}

func healthFromScanResults(results []cloudflareopt.TargetResult, checkedAt time.Time) []cloudflareopt.HealthResult {
	out := make([]cloudflareopt.HealthResult, 0, len(results))
	for _, result := range results {
		out = append(out, cloudflareopt.HealthResult{
			Domain:    result.Domain,
			IP:        result.Selected.IP,
			Healthy:   true,
			LatencyMS: result.Selected.LatencyMS,
			LossRate:  result.Selected.LossRate,
			TTFBMS:    result.Selected.TTFBMS,
			CheckedAt: checkedAt,
		})
	}
	return out
}

func mergeBoundedScanResults(previous, fresh []cloudflareopt.TargetResult, targets []cloudflareopt.Target) []cloudflareopt.TargetResult {
	enabled := enabledOptimizerDomains(targets)
	byDomain := map[string]cloudflareopt.TargetResult{}
	for _, result := range previous {
		domain := optimizerDomain(result.Domain)
		if enabled[domain] {
			byDomain[domain] = result
		}
	}
	for _, result := range fresh {
		domain := optimizerDomain(result.Domain)
		if enabled[domain] {
			byDomain[domain] = result
		}
	}
	keys := make([]string, 0, len(byDomain))
	for domain := range byDomain {
		keys = append(keys, domain)
	}
	sort.Strings(keys)
	out := make([]cloudflareopt.TargetResult, 0, len(keys))
	for _, domain := range keys {
		out = append(out, byDomain[domain])
	}
	return out
}

func sameOptimizerResults(left, right []cloudflareopt.TargetResult) bool {
	if len(left) != len(right) {
		return false
	}
	index := map[string]string{}
	for _, result := range left {
		index[optimizerDomain(result.Domain)] = strings.TrimSpace(result.Selected.IP)
	}
	for _, result := range right {
		if index[optimizerDomain(result.Domain)] != strings.TrimSpace(result.Selected.IP) {
			return false
		}
	}
	return true
}
