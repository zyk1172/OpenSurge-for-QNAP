package controlapi

import (
	"context"
	"errors"
	"net"
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

	mihomoFailureProcessMissing       = "process_missing"
	mihomoFailureControllerRefused    = "controller_refused"
	containerFailureDNSMissing        = "dns_missing"
	containerFailureIncomplete        = "gateway_incomplete"
	containerFailureDataPlaneMissing  = "data_plane_missing"
	containerFailureDNSUnresponsive   = "dns_unresponsive"
	containerFailureControllerRefused = "nas_controller_refused"

	autoMihomoRecoveryInterval         = 5 * time.Second
	mihomoRecoveryHealthyConfirmations = 2
	mihomoRecoveryUnknownLimit         = 6
)

var errConfirmedGatewayUnhealthy = errors.New("confirmed gateway failure after recovery")

func nasRecoveryReason(reason string) bool {
	return reason == containerFailureIncomplete || reason == containerFailureDNSMissing || reason == containerFailureDataPlaneMissing || reason == containerFailureDNSUnresponsive || reason == containerFailureControllerRefused
}

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
	awaitingHealth  bool
	retryAt         time.Time
	retryAttempts   int
	now             func() time.Time
}

func newMihomoRecoveryController() *mihomoRecoveryController {
	return &mihomoRecoveryController{state: mihomoRecoveryIdle, now: time.Now}
}

func (c *mihomoRecoveryController) snapshot() MihomoRecoveryStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := MihomoRecoveryStatus{State: c.state, Reason: c.reason, Error: c.error, RetryAttempts: c.retryAttempts}
	// The Web recovery contract already recognizes gateway_incomplete as a
	// full NAS recovery. Keep the internal refusal counter separate from Mac,
	// without exposing an unknown reason that selects a partial engine restart.
	if status.Reason == containerFailureControllerRefused {
		status.Reason = containerFailureIncomplete
	}
	if !c.retryAt.IsZero() {
		status.NextRetryAt = c.retryAt.UTC().Format(time.RFC3339)
	}
	return status
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
	c.awaitingHealth = false
	c.retryAt = time.Time{}
	c.retryAttempts = 0
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

// observePending means the same throttled DNS sample is still awaiting a new
// socket observation. It must not count as another independent unknown sample.
func (c *mihomoRecoveryController) observePending() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operationActive {
		return
	}
	c.healthyCount = 0
	c.refusedCount = 0
}

func (c *mihomoRecoveryController) observeFailure(reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operationActive {
		return false
	}
	c.healthyCount = 0
	c.unknownCount = 0
	// Repeated unreadable samples can end the confirmation phase without
	// proving a failure. A later confirmed NAS failure must still be eligible
	// for a throttled recovery, rather than retaining that unknown guard forever.
	if c.state == mihomoRecoveryFailed && c.awaitingHealth && c.retryAt.IsZero() && nasRecoveryReason(c.reason) && nasRecoveryReason(reason) {
		c.reason = reason
		c.error = "gateway remained unhealthy after full recovery"
		c.scheduleTimeoutRetry(errConfirmedGatewayUnhealthy)
		return false
	}
	// A transient failed restart must not strand desired-running forever after
	// the host recovers. Only an actual fresh failure observation can retry,
	// and only after the cooldown; unknown probes and manual stop never do.
	if c.state == mihomoRecoveryFailed && !c.retryAt.IsZero() && !c.now().Before(c.retryAt) {
		c.attempted = false
		c.state = mihomoRecoveryObserving
	}
	if c.attempted && c.state == mihomoRecoveryRecovering {
		c.state = mihomoRecoveryFailed
		c.reason = reason
		c.error = "mihomo remained unhealthy after restart"
		if nasRecoveryReason(reason) {
			c.error = "gateway remained unhealthy after full recovery"
			c.scheduleTimeoutRetry(errConfirmedGatewayUnhealthy)
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
	if reason == mihomoFailureControllerRefused || reason == containerFailureControllerRefused {
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
	c.awaitingHealth = false
	c.retryAt = time.Time{}
	return true
}

func (c *mihomoRecoveryController) beginManual(reason ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = mihomoRecoveryRecovering
	c.error = ""
	c.attempted = true
	c.healthyCount = 0
	c.unknownCount = 0
	c.operationActive = true
	c.awaitingHealth = false
	c.retryAt = time.Time{}
	c.retryAttempts = 0
	if len(reason) > 0 {
		c.reason = reason[0]
	}
}

func recoveryPermanentFailure(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	// A composite cleanup error can contain both a timeout and an ownership
	// refusal. The refusal remains a permanent boundary, not a retry trigger.
	for _, permanent := range []string{"identity changed", "refusing to", "permission denied", "operation not permitted", "config revision conflict", "invalid config"} {
		if strings.Contains(text, permanent) {
			return true
		}
	}
	return false
}

func recoveryTimeout(err error) bool {
	if err == nil || recoveryPermanentFailure(err) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return true
	}
	// Network command errors preserve timeout details in their diagnostic
	// message, while their underlying error can be exec's "signal: killed".
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "context deadline exceeded") || strings.Contains(text, " timed out after ")
}

func (c *mihomoRecoveryController) scheduleTimeoutRetry(err error) {
	// Limit this new policy to full NAS recovery, not the upstream Mac runner.
	if !nasRecoveryReason(c.reason) {
		return
	}
	if !recoveryTimeout(err) && !errors.Is(err, errConfirmedGatewayUnhealthy) {
		return
	}
	c.retryAttempts++
	delay := 30 * time.Second
	for i := 1; i < c.retryAttempts && delay < 15*time.Minute; i++ {
		delay *= 2
	}
	if delay > 15*time.Minute {
		delay = 15 * time.Minute
	}
	c.retryAt = c.now().Add(delay)
}

func (c *mihomoRecoveryController) observeStopped() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operationActive {
		return
	}
	c.state, c.reason, c.error = mihomoRecoveryIdle, "", ""
	c.attempted = false
	c.awaitingHealth = false
	c.retryAt = time.Time{}
	c.retryAttempts, c.healthyCount, c.unknownCount, c.refusedCount = 0, 0, 0, 0
}

func (c *mihomoRecoveryController) finishAutomatic(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.operationActive = false
	c.awaitingHealth = err == nil
	if c.deferLifecycleContention(err) {
		return
	}
	if err != nil {
		c.state = mihomoRecoveryFailed
		c.error = err.Error()
		c.scheduleTimeoutRetry(err)
		return
	}
	// The command completing is not proof that the controller is healthy. Keep
	// the incident in recovery until a fresh status observation confirms it;
	// confirmed NAS failures then retry with a cooldown, while unknown health
	// observations do not initiate another destructive recovery.
	c.state = mihomoRecoveryRecovering
	c.error = ""
}

func (c *mihomoRecoveryController) finishManual(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.operationActive = false
	c.awaitingHealth = err == nil
	c.attempted = true
	c.refusedCount = 0
	if c.deferLifecycleContention(err) {
		return
	}
	if err != nil {
		c.state = mihomoRecoveryFailed
		c.error = err.Error()
		c.scheduleTimeoutRetry(err)
		return
	}
	c.state = mihomoRecoveryRecovering
	c.error = ""
}

// A boot retry worker or CLI can acquire the shared lock after our advisory
// check. That action has not run; it must not consume the recovery incident.
func (c *mihomoRecoveryController) deferLifecycleContention(err error) bool {
	if !nasRecoveryReason(c.reason) || !errors.Is(err, gateway.ErrLifecycleOperationInProgress) || recoveryPermanentFailure(err) {
		return false
	}
	c.state, c.error = mihomoRecoveryObserving, ""
	c.attempted = false
	c.refusedCount, c.healthyCount, c.unknownCount = 0, 0, 0
	c.retryAt = time.Time{}
	return true
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
	if isContainer && !status.DesiredRunning {
		s.mihomoRecovery.observeStopped()
		return
	}

	action, reason := automaticRecoveryAction(status, isContainer)
	if isContainer && action == "restart-mihomo" && reason == "" && status.RuntimeState == "active" && status.Gateway == "running" && status.DNS == "running" && (status.LocalDNS == "running" || status.LocalDNS == "disabled") && s.dnsHealth != nil {
		switch s.dnsHealth.sample(ctx, cfg) {
		case dnsProbeUnavailable:
			action, reason = "recover-gateway", containerFailureDNSUnresponsive
		case dnsProbeUnknown:
			s.mihomoRecovery.observePending()
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
		switch mihomoFailureReason(status) {
		case mihomoFailureProcessMissing:
			return "recover-gateway", containerFailureIncomplete
		case mihomoFailureControllerRefused:
			return "recover-gateway", containerFailureControllerRefused
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
