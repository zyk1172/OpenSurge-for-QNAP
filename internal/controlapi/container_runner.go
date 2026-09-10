package controlapi

import (
	"context"
	"fmt"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/gateway"
	"open-mihomo-gateway/internal/macosnetwork"
)

// ContainerRunner is the privileged runner used by the QNAP/Linux container.
// It embeds the direct lifecycle/configuration implementation, but deliberately
// fails closed for the upstream macOS host-network mutation workflow.
//
// QNAP v1 owns only the container network namespace. It must not attempt to
// reconfigure QTS, a Virtual Switch, or the NAS interface as if it were a Mac.
type ContainerRunner struct {
	DirectRunner
}

func (ContainerRunner) Run(ctx context.Context, action, configPath string) error {
	if action == "restart-dnsmasq" {
		return gateway.RestartDNSMasqConfig(ctx, configPath)
	}
	if action == "start" || action == "reload" {
		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}
		if err := validateContainerTopology(cfg.Gateway.Mode); err != nil {
			return err
		}
	}
	if action == "start" {
		// Persist service intent before touching the runtime. If the container is
		// interrupted during startup, the next container instance knows that the
		// gateway should be recovered rather than treated as intentionally stopped.
		if err := gateway.SetDesiredRunningConfig(configPath, true); err != nil {
			return fmt.Errorf("persist gateway start intent: %w", err)
		}
	}
	if action == "stop" {
		// Persist stop intent first so a container crash during teardown cannot
		// cause an unexpected automatic restart on the next boot.
		if err := gateway.SetDesiredRunningConfig(configPath, false); err != nil {
			return fmt.Errorf("persist gateway stop intent: %w", err)
		}
	}
	return (DirectRunner{}).Run(ctx, action, configPath)
}

func (ContainerRunner) StartPolicyWorkspace(ctx context.Context, configPath string, input PolicyWorkspaceInput) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := validateContainerTopology(cfg.Gateway.Mode); err != nil {
		return err
	}
	if err := gateway.SetDesiredRunningConfig(configPath, true); err != nil {
		return fmt.Errorf("persist gateway start intent: %w", err)
	}
	return (DirectRunner{}).StartPolicyWorkspace(ctx, configPath, input)
}

func (ContainerRunner) SetManual(context.Context, string, macosnetwork.ManualConfig) error {
	return unsupportedContainerHostNetworkMutation("set a static host IPv4 address")
}

func (ContainerRunner) SetDHCP(context.Context, string, string) error {
	return unsupportedContainerHostNetworkMutation("switch the host interface back to DHCP")
}

func (ContainerRunner) ProbeDHCP(context.Context, string, string, time.Duration) ([]string, error) {
	return nil, unsupportedContainerHostNetworkMutation("perform the macOS DHCP takeover probe")
}

func validateContainerTopology(mode string) error {
	if mode == config.GatewayModeSameWiFiDHCP {
		return fmt.Errorf("gateway mode %q is disabled in the QNAP/Linux container because it requires macOS host-network mutation; use same_lan for the QNAP v1 bypass-router deployment", mode)
	}
	return nil
}

func unsupportedContainerHostNetworkMutation(operation string) error {
	return fmt.Errorf("QNAP/Linux container refuses to %s: OpenSurge v1 does not modify QTS or Virtual Switch host networking; use the same_lan bypass-router workflow", operation)
}
