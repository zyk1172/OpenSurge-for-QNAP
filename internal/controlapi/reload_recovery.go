package controlapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/gateway"
)

// ConfigurationRollback records observation, not merely restoration of a file.
// It also crosses the Helper boundary when the configuration action fails.
type ConfigurationRollback struct {
	ConfigRestored       bool   `json:"config_restored"`
	RestartAttempted     bool   `json:"restart_attempted"`
	GatewayState         string `json:"gateway_state"`
	PreviousGatewayReady bool   `json:"previous_gateway_ready"`
}

type helperReloadError struct {
	message string
	phase   gateway.ReloadPhase
}

func (e *helperReloadError) Error() string { return e.message }
func (e *helperReloadError) Unwrap() error {
	return &gateway.ReloadError{Phase: e.phase, Err: errors.New(e.message)}
}

func reloadGatewayState(status gateway.Status, err error) string {
	if err != nil || status.Gateway == "" || status.RuntimeState == "" {
		return "unknown"
	}
	if status.Gateway == "stopped" && status.RuntimeState == "none" {
		return "stopped"
	}
	if status.Gateway == "running" && status.RuntimeState == "active" &&
		status.MihomoError == "" && strings.HasPrefix(status.Mihomo, "running") &&
		status.TUNError == "" && (status.TUN == "ready" || status.TUN == "disabled") &&
		(status.DNS == "running" || status.DNS == "legacy-dnsmasq") &&
		(status.LocalDNS == "running" || status.LocalDNS == "disabled") &&
		(!status.DHCPEnabled || status.DHCP == "running") &&
		status.Forwarding == "enabled" && status.RoutingError == "" && status.Routing == "applied" &&
		(status.NFTables == "applied" || status.NFTables == "not_required") {
		return "running"
	}
	return "degraded"
}

func restorePreviousGateway(ctx context.Context, cfg config.Config, restored bool, reloadErr error, deps profileApplyDeps) (*ConfigurationRollback, error) {
	result := &ConfigurationRollback{ConfigRestored: restored, GatewayState: "unknown"}
	var failure *gateway.ReloadError
	knownPhase := errors.As(reloadErr, &failure)
	previousPreserved := knownPhase && (failure.Phase == gateway.ReloadValidation || failure.Phase == gateway.ReloadStop)
	// Restoring a gateway already stopped by this transaction is cleanup even
	// when the original request was canceled. It has its own bounded budget.
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	var recoveryErr error
	if restored {
		exists, err := deps.stateExists(cfg)
		if err != nil {
			recoveryErr = fmt.Errorf("inspect gateway after failed reload: %w", err)
		} else if !exists && !(knownPhase && failure.Phase == gateway.ReloadStop) {
			result.RestartAttempted = true
			recoveryErr = deps.start(recoveryCtx, cfg)
			previousPreserved = recoveryErr == nil
		}
	}
	if deps.status == nil {
		return result, errors.Join(recoveryErr, errors.New("final gateway health probe unavailable"))
	}
	statusCtx, cancelStatus := context.WithTimeout(recoveryCtx, 5*time.Second)
	defer cancelStatus()
	status, err := deps.status(statusCtx, cfg)
	result.GatewayState = reloadGatewayState(status, err)
	result.PreviousGatewayReady = restored && previousPreserved && recoveryErr == nil && result.GatewayState == "running"
	if err != nil {
		recoveryErr = errors.Join(recoveryErr, fmt.Errorf("inspect final gateway health: %w", err))
	}
	return result, recoveryErr
}

func configurationRollbackError(kind string, reloadErr, restoreErr, recoveryErr error, rollback *ConfigurationRollback) error {
	message := "previous config restored"
	if kind == "Tailscale" {
		message = "previous config and auth key restored"
	}
	if restoreErr != nil {
		message = fmt.Sprintf("restore previous config failed: %v", restoreErr)
	}
	if rollback.PreviousGatewayReady {
		message += "; previous running gateway preserved or restored; health confirmed"
	} else {
		message += "; previous gateway recovery unconfirmed; final gateway state: " + rollback.GatewayState
	}
	return fmt.Errorf("apply %s reload failed: %w; %s", kind, errors.Join(reloadErr, restoreErr, recoveryErr), message)
}

func writeConfigurationError(w http.ResponseWriter, status int, code string, err error, rollback *ConfigurationRollback) {
	var details map[string]any
	if rollback != nil {
		details = map[string]any{"rollback": rollback}
	}
	writeJSON(w, status, ErrorResponse{SchemaVersion: SchemaVersion, Error: APIError{Code: code, Message: err.Error(), Details: details}})
}
