package controlapi

import (
	"context"
	"sync"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/gateway"
)

const (
	dnsmasqRecoveryIdle       = "idle"
	dnsmasqRecoveryObserving  = "observing"
	dnsmasqRecoveryRecovering = "recovering"
	dnsmasqRecoveryFailed     = "failed"

	dnsmasqRecoveryHealthyConfirmations = 2
)

type dnsmasqRecoveryController struct {
	mu              sync.Mutex
	state           string
	error           string
	healthyCount    int
	attempted       bool
	operationActive bool
}

func newDNSMasqRecoveryController() *dnsmasqRecoveryController {
	return &dnsmasqRecoveryController{state: dnsmasqRecoveryIdle}
}

func (c *dnsmasqRecoveryController) observeHealthy() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operationActive {
		return
	}
	if c.attempted {
		c.healthyCount++
		c.state = dnsmasqRecoveryRecovering
		c.error = ""
		if c.healthyCount < dnsmasqRecoveryHealthyConfirmations {
			return
		}
	}
	c.state = dnsmasqRecoveryIdle
	c.error = ""
	c.healthyCount = 0
	c.attempted = false
}

func (c *dnsmasqRecoveryController) observeUnknown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operationActive {
		return
	}
	c.healthyCount = 0
}

func (c *dnsmasqRecoveryController) observeMissing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operationActive {
		return false
	}
	c.healthyCount = 0
	if c.attempted && c.state == dnsmasqRecoveryRecovering {
		c.state = dnsmasqRecoveryFailed
		c.error = "dnsmasq remained unhealthy after restart"
		return false
	}
	if c.attempted || c.state == dnsmasqRecoveryFailed {
		return false
	}
	c.state = dnsmasqRecoveryObserving
	c.error = ""
	return true
}

func (c *dnsmasqRecoveryController) begin() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attempted || c.state == dnsmasqRecoveryRecovering || c.state == dnsmasqRecoveryFailed {
		return false
	}
	c.state = dnsmasqRecoveryRecovering
	c.error = ""
	c.attempted = true
	c.healthyCount = 0
	c.operationActive = true
	return true
}

func (c *dnsmasqRecoveryController) finishAutomatic(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.operationActive = false
	if err != nil {
		c.state = dnsmasqRecoveryFailed
		c.error = err.Error()
		return
	}
	c.state = dnsmasqRecoveryRecovering
	c.error = ""
}

func containerRecoveryRunner(runner ActionRunner) bool {
	switch runner.(type) {
	case ContainerRunner, *ContainerRunner:
		return true
	default:
		return false
	}
}

func (s *Server) evaluateDNSMasqRecovery(ctx context.Context, controller *dnsmasqRecoveryController) {
	// The QNAP/Linux container owns dnsmasq directly. The macOS build still uses
	// a privileged helper and is intentionally left on its existing semantics.
	if controller == nil || !containerRecoveryRunner(s.runner) {
		if controller != nil {
			controller.observeUnknown()
		}
		return
	}
	cfg, err := config.LoadRuntime(s.configPath)
	if err != nil {
		controller.observeUnknown()
		return
	}
	if busy, err := gateway.LifecycleOperationInProgress(cfg); err != nil || busy {
		controller.observeUnknown()
		return
	}
	status, err := s.gatewayStatus(ctx, cfg)
	if err != nil || status.RuntimeState != "active" {
		controller.observeUnknown()
		return
	}
	if status.DHCP == "running" {
		controller.observeHealthy()
		return
	}
	if status.DHCP != "stopped" {
		controller.observeUnknown()
		return
	}

	// Recheck after observation so an external CLI lifecycle action cannot race
	// the watchdog between status sampling and restart dispatch.
	if busy, err := gateway.LifecycleOperationInProgress(cfg); err != nil || busy {
		controller.observeUnknown()
		return
	}
	recovery, err := s.store.Recovery()
	if err != nil || !mihomoRecoveryStageAllowed(cfg.Gateway.Mode, recovery.Stage) {
		controller.observeUnknown()
		return
	}
	if !controller.observeMissing() {
		return
	}
	if !s.lifecycleMu.TryLock() {
		return
	}
	if !controller.begin() {
		s.lifecycleMu.Unlock()
		return
	}

	op := newOperation("auto-restart-dnsmasq-"+randomToken(8), "restart-dnsmasq")
	if err := s.store.CreateOperation(op); err != nil {
		s.lifecycleMu.Unlock()
		controller.finishAutomatic(err)
		return
	}
	go s.runOperationLocked(op, cfg.Gateway.Mode, recovery, nil, controller.finishAutomatic)
}
