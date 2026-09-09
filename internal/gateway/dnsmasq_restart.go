package gateway

import (
	"context"
	"errors"
	"fmt"

	"open-mihomo-gateway/internal/config"
)

// RestartDNSMasq restarts only the dnsmasq process owned by the active gateway
// runtime. PID fingerprints prevent a stale/reused PID from being signalled.
func (m Manager) RestartDNSMasq(ctx context.Context) error {
	if m.gatewayDeps().geteuid() != 0 {
		return fmt.Errorf("restart-dnsmasq requires root privileges")
	}
	return m.withLifecycleLock(func() error { return m.restartDNSMasq(ctx) })
}

func (m Manager) restartDNSMasq(ctx context.Context) error {
	ReportProgress(ctx, "checking_runtime")
	deps := m.gatewayDeps()
	if deps.geteuid() != 0 {
		return fmt.Errorf("restart-dnsmasq requires root privileges")
	}
	state, exists, err := deps.loadState(m.paths.StateFile)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("gateway is not running; run start instead")
	}
	bootSession, err := currentBoot(deps)
	if err != nil {
		return fmt.Errorf("determine current boot session: %w", err)
	}
	if !state.BelongsToBoot(bootSession) {
		return fmt.Errorf("gateway runtime was interrupted by a restart; run stop to recover it first")
	}

	dnsmasqManager := deps.newDHCP(m.cfg, m.paths)
	if err := dnsmasqManager.Check(); err != nil {
		return fmt.Errorf("dnsmasq preflight failed: %w", err)
	}

	previousPID := state.PIDDNSMasq
	previousFingerprint := state.DNSMasqProcessFingerprint
	previousRunning := trackedProcessRunning(deps, previousPID, previousFingerprint, dnsmasqManager.Running)

	// Persist the transition before touching any process. If the previous PID is
	// stale or has been reused by an unrelated process, previousRunning is false
	// and we deliberately never signal it.
	state.PIDDNSMasq = 0
	state.DNSMasqProcessFingerprint = ""
	if err := deps.saveState(m.paths.StateFile, state); err != nil {
		return fmt.Errorf("mark dnsmasq restart in runtime state: %w", err)
	}

	if previousRunning {
		ReportProgress(ctx, "stopping_dns")
		if err := stopTrackedProcess(deps, "dnsmasq", previousPID, previousFingerprint, dnsmasqManager.Stop); err != nil {
			if trackedProcessRunning(deps, previousPID, previousFingerprint, dnsmasqManager.Running) {
				state.PIDDNSMasq = previousPID
				state.DNSMasqProcessFingerprint = previousFingerprint
			}
			return errors.Join(fmt.Errorf("stop dnsmasq pid %d: %w", previousPID, err), deps.saveState(m.paths.StateFile, state))
		}
	}

	archivedLog, err := archiveMihomoLog(m.paths.DNSMasqLog, deps.now())
	if err != nil {
		return fmt.Errorf("archive dnsmasq log before restart: %w", err)
	}

	ReportProgress(ctx, "starting_dns")
	newPID, err := dnsmasqManager.Start()
	if err != nil {
		return fmt.Errorf("start replacement dnsmasq process: %w", err)
	}
	if newPID <= 0 {
		return fmt.Errorf("replacement dnsmasq did not return a live pid; current topology may no longer require dnsmasq")
	}
	state.PIDDNSMasq = newPID
	state.DNSMasqProcessFingerprint, err = processFingerprint(deps, newPID)
	if err != nil || state.DNSMasqProcessFingerprint == "" {
		if err == nil {
			err = fmt.Errorf("replacement dnsmasq pid %d disappeared before its identity could be recorded", newPID)
		}
		return errors.Join(err, dnsmasqManager.Stop(newPID))
	}
	if err := deps.saveState(m.paths.StateFile, state); err != nil {
		return errors.Join(fmt.Errorf("save replacement dnsmasq pid: %w", err), dnsmasqManager.Stop(newPID))
	}

	fmt.Printf("dnsmasq restarted with pid %d\n", newPID)
	if archivedLog != "" {
		fmt.Printf("previous dnsmasq log archived at %s\n", archivedLog)
	}
	return nil
}

// RestartDNSMasqConfig follows the same lock-before-load rule as other path
// based lifecycle actions, avoiding a read-before-lock race with config writes.
func RestartDNSMasqConfig(ctx context.Context, configPath string) error {
	return runConfigLifecycle(ctx, configPath, configLifecycleDeps{
		loadRuntime: config.LoadRuntime,
		loadLocked:  config.LoadRuntime,
		runLocked: func(ctx context.Context, cfg config.Config) error {
			manager := New(cfg)
			return manager.restartDNSMasq(ctx)
		},
	})
}
