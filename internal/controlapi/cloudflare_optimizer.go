package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
	state, _ := a.loadState()
	a.populateNextRun(&state, cfg)
	_ = a.saveState(state)
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

	gatewayCfg, err := config.Load(a.configPath)
	if err != nil {
		return cloudflareopt.Response{}, err
	}
	state, _ := a.loadState()
	now := time.Now().UTC()
	state.SchemaVersion = cloudflareopt.SchemaVersion
	state.Running = true
	state.StartedAt = &now
	state.LastError = ""
	_ = a.saveState(state)

	finish := func(runErr error) {
		finished := time.Now().UTC()
		state.Running = false
		state.StartedAt = nil
		state.LastRunAt = &finished
		if runErr != nil {
			state.LastError = runErr.Error()
		}
		a.populateNextRun(&state, cfg)
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

	changed := !sameOptimizerResults(state.Results, output.Results)
	state.Results = output.Results
	if changed {
		if err := a.applyResults(ctx, gatewayCfg, output.Results); err != nil {
			finish(err)
			return cloudflareopt.Response{}, err
		}
	}
	finish(nil)
	return cloudflareopt.Response{Config: cfg, State: state}, nil
}

func (a *cloudflareOptimizerAPI) applyResults(ctx context.Context, cfg config.Config, results []cloudflareopt.TargetResult) error {
	if len(results) == 0 {
		return nil
	}
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
		if err != nil || !cfg.Enabled {
			continue
		}
		state, err := a.loadState()
		if err != nil || state.Running {
			continue
		}
		a.populateNextRun(&state, cfg)
		if state.NextRunAt == nil || now.Before(*state.NextRunAt) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Scan.BudgetSeconds+20)*time.Second)
		_, _ = a.runScan(ctx)
		cancel()
	}
}

func (a *cloudflareOptimizerAPI) populateNextRun(state *cloudflareopt.State, cfg cloudflareopt.Config) {
	if !cfg.Enabled {
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
	state := cloudflareopt.State{SchemaVersion: cloudflareopt.SchemaVersion, Results: []cloudflareopt.TargetResult{}}
	if err := readJSON(filepath.Join(a.storeDir, "cloudflare-optimizer-state.json"), &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}
		return cloudflareopt.State{}, err
	}
	if state.Results == nil {
		state.Results = []cloudflareopt.TargetResult{}
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

func sameOptimizerResults(left, right []cloudflareopt.TargetResult) bool {
	if len(left) != len(right) {
		return false
	}
	index := map[string]string{}
	for _, result := range left {
		index[strings.ToLower(strings.TrimSpace(result.Domain))] = strings.TrimSpace(result.Selected.IP)
	}
	for _, result := range right {
		if index[strings.ToLower(strings.TrimSpace(result.Domain))] != strings.TrimSpace(result.Selected.IP) {
			return false
		}
	}
	return true
}
