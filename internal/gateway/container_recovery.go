package gateway

import (
	"context"
	"fmt"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/runtime"
)

type Readiness struct {
	Ready          bool   `json:"ready"`
	DesiredRunning bool   `json:"desired_running"`
	Gateway        string `json:"gateway"`
	RuntimeState   string `json:"runtime_state"`
	Reason         string `json:"reason,omitempty"`
}

// SetDesiredRunningConfig records the QNAP/Linux service intent separately from
// ephemeral runtime state. A clean stop writes false before teardown; a start
// writes true before bringing the data plane up. This makes container restart
// recovery distinguishable from an intentionally stopped gateway.
func SetDesiredRunningConfig(configPath string, running bool) error {
	cfg, err := config.LoadRuntime(configPath)
	if err != nil {
		return err
	}
	paths := runtime.NewPaths(cfg)
	if err := runtime.Ensure(paths); err != nil {
		return err
	}
	return runtime.SaveGatewayDesiredState(runtime.GatewayDesiredStatePath(paths.Dir), running)
}

// RecoverConfigAfterContainerRestart restores a gateway that was intended to
// be running before the container stopped. Existing installations without a
// desired-state file are migrated conservatively: a persisted runtime state
// means the gateway had been running; no runtime state means it was stopped.
func RecoverConfigAfterContainerRestart(ctx context.Context, configPath string) (bool, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return false, err
	}
	return New(cfg).RecoverAfterContainerRestart(ctx)
}

func (m Manager) RecoverAfterContainerRestart(ctx context.Context) (bool, error) {
	deps := m.gatewayDeps()
	if deps.geteuid() != 0 {
		return false, fmt.Errorf("container restart recovery requires root privileges")
	}
	if err := deps.ensure(m.paths); err != nil {
		return false, err
	}

	recovered := false
	err := m.withLifecycleLock(func() error {
		state, stateExists, err := deps.loadState(m.paths.StateFile)
		if err != nil {
			return err
		}
		desiredPath := runtime.GatewayDesiredStatePath(m.paths.Dir)
		desired, desiredExists, err := runtime.LoadGatewayDesiredState(desiredPath)
		if err != nil {
			return fmt.Errorf("load gateway desired state: %w", err)
		}
		if !desiredExists {
			// Backward-compatible migration for installs created before the
			// desired-state file existed. Runtime state is only retained while the
			// gateway is active/interrupted; a successful manual stop removes it.
			desired.Running = stateExists
			if err := runtime.SaveGatewayDesiredState(desiredPath, desired.Running); err != nil {
				return fmt.Errorf("persist inferred gateway desired state: %w", err)
			}
		}

		if stateExists {
			boot, err := currentBoot(deps)
			if err != nil {
				return fmt.Errorf("determine current boot session: %w", err)
			}
			if !state.BelongsToBoot(boot) {
				// A recreated Docker network namespace is a recovery boundary. The
				// persisted cleanup recipe is content-scoped and stale PIDs are never
				// signalled by cleanupInterruptedRuntime.
				if err := m.cleanupInterruptedRuntime(ctx, deps, state); err != nil {
					return fmt.Errorf("reconcile interrupted gateway runtime: %w", err)
				}
				stateExists = false
			} else if !desired.Running {
				// A stop may have been interrupted inside the same namespace. Finish
				// the normal teardown, but do not turn the gateway back on.
				if err := m.stop(ctx); err != nil {
					return fmt.Errorf("finish interrupted gateway stop: %w", err)
				}
				stateExists = false
			} else {
				status, statusErr := m.Status(ctx)
				if statusErr == nil && status.Gateway == "running" && status.RuntimeState == "active" {
					return nil
				}
				// State belongs to this namespace but its processes/data plane are
				// degraded. Reconcile it before a clean start rather than layering a
				// second set of routes/processes over an uncertain runtime.
				if err := m.stop(ctx); err != nil {
					return fmt.Errorf("reconcile degraded gateway runtime: %w", err)
				}
				stateExists = false
			}
		}

		if !desired.Running {
			return nil
		}
		if stateExists {
			return fmt.Errorf("gateway restart recovery could not clear the previous runtime")
		}
		if err := m.start(ctx); err != nil {
			return fmt.Errorf("restart gateway after container recovery: %w", err)
		}
		recovered = true
		return nil
	})
	return recovered, err
}

func ReadinessConfig(ctx context.Context, configPath string) (Readiness, error) {
	cfg, err := config.LoadRuntime(configPath)
	if err != nil {
		return Readiness{}, err
	}
	return New(cfg).Readiness(ctx)
}

// Readiness treats the control plane as ready only when the observed data plane
// matches the persisted desired state. An intentionally stopped gateway is
// healthy; a desired-running gateway with missing Mihomo/DNS/routes is not.
func (m Manager) Readiness(ctx context.Context) (Readiness, error) {
	_, stateExists, err := runtime.LoadState(m.paths.StateFile)
	if err != nil {
		return Readiness{}, err
	}
	desired, desiredExists, err := runtime.LoadGatewayDesiredState(runtime.GatewayDesiredStatePath(m.paths.Dir))
	if err != nil {
		return Readiness{}, err
	}
	desiredRunning := desired.Running
	if !desiredExists {
		// Compatibility before the first restart/start/stop under the new code.
		desiredRunning = stateExists
	}
	status, err := m.Status(ctx)
	if err != nil {
		return Readiness{}, err
	}
	result := Readiness{
		DesiredRunning: desiredRunning,
		Gateway:        status.Gateway,
		RuntimeState:   status.RuntimeState,
	}
	if desiredRunning {
		result.Ready = status.Gateway == "running" && status.RuntimeState == "active"
		if !result.Ready {
			result.Reason = "gateway_desired_running_but_data_plane_not_ready"
		}
		return result, nil
	}
	result.Ready = !stateExists && status.Gateway == "stopped" && status.RuntimeState == "none"
	if !result.Ready {
		result.Reason = "gateway_desired_stopped_but_runtime_not_clean"
	}
	return result, nil
}
