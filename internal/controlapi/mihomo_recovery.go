package controlapi

import (
	"context"
	"strings"
	"sync"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/gateway"
)

const (
	mihomoRecoveryIdle       = "idle"
	mihomoRecoveryObserving  = "observing"
	mihomoRecoveryRecovering = "recovering"
	mihomoRecoveryFailed     = "failed"

	mihomoFailureProcessMissing    = "process_missing"
	mihomoFailureControllerRefused = "controller_refused"
	containerFailureDNSMissing     = "dns_missing"
	containerFailureIncomplete     = "gateway_incomplete"
	containerFailureDataPlaneMissing = "data_plane_missing"
	containerFailureDNSUnresponsive  = "dns_unresponsive"

	autoMihomoRecoveryInterval         = 5 * time.Second
	mihomoRecoveryHealthyConfirmations = 2
	mihomoRecoveryUnknownLimit         = 6
)

type mihomoRecoveryController struct {
	mu              sync.Mutex
	state           string
	reason          string
	error           string
	refusedCount    int
	healthyCount    int
	unknownCount    int
	attempted       bool
	operationActive bool
}

func newMihomoRecoveryController() *mihomoRecoveryController {
	return &mihomoRecoveryController{state: mihomoRecoveryIdle}
}

func (c *mihomoRecoveryController) snapshot() MihomoRecoveryStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return MihomoRecoveryStatus{State: c.state, Reason: c.reason, Error: c.error}
}

func (c *mihomoRecoveryController) observeHealthy() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operationActive {
		return
	}
	c.unknownCount = 0
	if c.attempted {
		c.healthyCount++
		c.state = mihomoRecoveryRecovering
		c.error = ""
		if c.healthyCount < mihomoRecoveryHealthyConfirmations {
			return
		}
	}
	c.state = mihomoRecoveryIdle
	c.reason = ""
	c.error = ""
	c.refusedCount = 0
	c.healthyCount = 0
	c.attempted = false
}

func (c *mihomoRecoveryController) observeUnknown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operationActive {
		return
	}
	// An unreadable or inapplicable sample proves neither recovery nor
	// continued failure. Keep the one-attempt incident guard, while requiring
	// future failure and health confirmations to be consecutive again.
	c.healthyCount = 0
	c.refusedCount = 0
	if c.attempted && c.state == mihomoRecoveryRecovering {
		c.unknownCount++
		if c.unknownCount >= mihomoRecoveryUnknownLimit {
			c.state = mihomoRecoveryFailed
			c.error = "gateway health remained unconfirmed after recovery"
		}
	}
}

func (c *mihomoRecoveryController) observeFailure(reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operationActive {
		return false
	}
	c.healthyCount = 0
	c.unknownCount = 0
	if c.attempted && c.state == mihomoRecoveryRecovering {
		c.state = mihomoRecoveryFailed
		c.reason = reason
		c.error = "mihomo remained unhealthy after restart"
		if reason == containerFailureDNSMissing || reason == containerFailureIncomplete || reason == containerFailureDataPlaneMissing || reason == containerFailureDNSUnresponsive {
			c.error = "gateway remained unhealthy after full recovery"
		}
		return false
	}
	if c.attempted || c.state == mihomoRecoveryFailed {
		return false
	}
	if c.reason != reason {
		c.reason = reason
		c.refusedCount = 0
	}
	c.state = mihomoRecoveryObserving
	c.error = ""
	if reason == mihomoFailureProcessMissing || reason == containerFailureDNSMissing || reason == containerFailureIncomplete || reason == containerFailureDataPlaneMissing || reason == containerFailureDNSUnresponsive {
		return true
	}
	if reason == mihomoFailureControllerRefused {
		c.refusedCount++
		return c.refusedCount >= 2
	}
	return false
}

func (c *mihomoRecoveryController) begin(reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attempted || c.state == mihomoRecoveryRecovering || c.state == mihomoRecoveryFailed {
		return false
	}
	c.state = mihomoRecoveryRecovering
	c.reason = reason
	c.error = ""
	c.attempted = true
	c.healthyCount = 0
	c.unknownCount = 0
	c.operationActive = true
	return true
}

func (c *mihomoRecoveryController) beginManual() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = mihomoRecoveryRecovering
	c.error = ""
	c.attempted = true
	c.healthyCount = 0
	c.unknownCount = 0
	c.operationActive = true
}

func (c *mihomoRecoveryController) finishAutomatic(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.operationActive = false
	if err != nil {
		c.state = mihomoRecoveryFailed
		c.error = err.Error()
		return
	}
	// The command completing is not proof that the controller is healthy. Keep
	// the incident in recovery until a fresh status observation confirms it;
	// otherwise observeFailure exposes the manual fallback without retrying.
	c.state = mihomoRecoveryRecovering
	c.error = ""
}

func (c *mihomoRecoveryController) finishManual(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.operationActive = false
	c.attempted = true
	c.refusedCount = 0
	if err != nil {
		c.state = mihomoRecoveryFailed
		c.error = err.Error()
		return
	}
	c.state = mihomoRecoveryRecovering
	c.error = ""
}

func (s *Server) monitorMihomoRecovery(ctx context.Context) {
	dnsmasqRecovery := newDNSMasqRecoveryController()
	s.evaluateMihomoRecovery(ctx)
	s.evaluateDNSMasqRecovery(ctx, dnsmasqRecovery)
	ticker := time.NewTicker(autoMihomoRecoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.evaluateMihomoRecovery(ctx)
			s.evaluateDNSMasqRecovery(ctx, dnsmasqRecovery)
		}
	}
}

func (s *Server) evaluateMihomoRecovery(ctx context.Context) {
	cfg, err := config.LoadRuntime(s.configPath)
	if err != nil {
		s.mihomoRecovery.observeUnknown()
		return
	}
	if busy, err := gateway.LifecycleOperationInProgress(cfg); err != nil || busy {
		s.mihomoRecovery.observeUnknown()
		return
	}
	status, err := s.gatewayStatus(ctx, cfg)
	isContainer := containerRecoveryRunner(s.runner)
	if err != nil || !isContainer && status.RuntimeState != "active" {
		s.mihomoRecovery.observeUnknown()
		return
	}

	action, reason := automaticRecoveryAction(status, isContainer)
	if isContainer && action == "restart-mihomo" && reason == "" && status.RuntimeState == "active" && status.Gateway == "running" && status.DNS == "running" && (status.LocalDNS == "running" || status.LocalDNS == "disabled") && s.dnsHealth != nil {
		switch s.dnsHealth.sample(ctx, cfg) {
		case dnsProbeUnavailable:
			action, reason = "recover-gateway", containerFailureDNSUnresponsive
		case dnsProbeUnknown:
			s.mihomoRecovery.observeUnknown()
			return
		}
	}
	if action == "" {
		s.mihomoRecovery.observeUnknown()
		return
	}
	if reason == "" {
		s.mihomoRecovery.observeHealthy()
		return
	}
	// Close the observation-to-action race with an external omg lifecycle
	// command. The Manager lock remains the final guard if another operation
	// starts after this advisory check.
	if busy, err := gateway.LifecycleOperationInProgress(cfg); err != nil || busy {
		s.mihomoRecovery.observeUnknown()
		return
	}

	recovery, err := s.store.Recovery()
	if err != nil || !mihomoRecoveryStageAllowed(cfg.Gateway.Mode, recovery.Stage) {
		s.mihomoRecovery.observeUnknown()
		return
	}
	if !s.mihomoRecovery.observeFailure(reason) {
		return
	}
	if !s.lifecycleMu.TryLock() {
		return
	}
	if !s.mihomoRecovery.begin(reason) {
		s.lifecycleMu.Unlock()
		return
	}

	op := newOperation("auto-"+action+"-"+randomToken(8), action)
	if err := s.store.CreateOperation(op); err != nil {
		s.lifecycleMu.Unlock()
		s.mihomoRecovery.finishAutomatic(err)
		return
	}
	// A previous failed DNS socket sample must not mark a newly recovered
	// gateway unhealthy before its dependencies can be probed afresh.
	if s.dnsHealth != nil {
		s.dnsHealth.reset()
	}
	go s.runOperationLocked(op, cfg.Gateway.Mode, recovery, nil, s.mihomoRecovery.finishAutomatic)
}

// A cleanup journal, or a gateway missing a DNS dependency, cannot be healed
// by restarting only the engine. Include the legacy partial-start format and
// the state-less result of a successful rollback with running intent retained.
func automaticRecoveryAction(status gateway.Status, isContainer bool) (string, string) {
	if isContainer {
		if !status.DesiredRunning {
			return "", ""
		}
		switch status.RuntimeState {
		case "none", "incomplete", "interrupted":
			return "recover-gateway", containerFailureIncomplete
		case "active":
		default:
			return "", ""
		}
		if status.DNS == "stopped" || status.LocalDNS == "stopped" || status.DHCP == "stopped" {
			return "recover-gateway", containerFailureDNSMissing
		}
		// Only confirmed data-plane failures trigger the destructive full
		// recovery. Unknown observations under NAS I/O stalls are not proof.
		if status.Routing == "missing" || status.TUN == "failed" || status.NFTables == "missing" || status.Forwarding == "disabled" {
			return "recover-gateway", containerFailureDataPlaneMissing
		}
		// A missing/unknown DNS observation does not confirm a repaired engine.
		if status.DNS != "running" && status.DNS != "legacy-dnsmasq" {
			return "", ""
		}
		if mihomoFailureReason(status) == "" && status.Gateway != "running" {
			return "", ""
		}
	}
	return "restart-mihomo", mihomoFailureReason(status)
}

func mihomoFailureReason(status gateway.Status) string {
	if !strings.HasPrefix(status.Mihomo, "running") {
		return mihomoFailureProcessMissing
	}
	if connectionRefused(status.MihomoError) || connectionRefused(status.TUNError) {
		return mihomoFailureControllerRefused
	}
	return ""
}

func connectionRefused(value string) bool {
	return strings.Contains(strings.ToLower(value), "connection refused")
}

func mihomoRecoveryStageAllowed(topology, stage string) bool {
	if topology != config.GatewayModeSameWiFiDHCP {
		return true
	}
	return stage == RecoveryGatewayActive || stage == RecoveryClientValidated || stage == RecoveryClientValidationSkipped
}
