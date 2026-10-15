package controlapi

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"open-mihomo-gateway/internal/gateway"
)

func TestTransientFullRecoveryRetriesAfterCooldown(t *testing.T) {
	now := time.Now()
	c := newMihomoRecoveryController()
	c.now = func() time.Time { return now }
	if !c.begin(containerFailureIncomplete) {
		t.Fatal("initial recovery rejected")
	}
	c.finishAutomatic(fmt.Errorf("network probe: %w", context.DeadlineExceeded))
	if got := c.snapshot(); got.State != mihomoRecoveryFailed || got.NextRetryAt == "" || got.RetryAttempts != 1 {
		t.Fatalf("timeout state = %+v", got)
	}
	if c.observeFailure(containerFailureIncomplete) {
		t.Fatal("retried without cooldown")
	}
	now = now.Add(29 * time.Second)
	if c.observeFailure(containerFailureIncomplete) {
		t.Fatal("retried early")
	}
	now = now.Add(time.Second)
	c.observeUnknown()
	if c.snapshot().State != mihomoRecoveryFailed {
		t.Fatal("unknown probe initiated recovery")
	}
	if !c.observeFailure(containerFailureIncomplete) || !c.begin(containerFailureIncomplete) {
		t.Fatal("fresh confirmed failure did not allow retry")
	}
	c.finishAutomatic(fmt.Errorf("network command timed out after 5s"))
	if c.retryAt.Sub(now) != time.Minute {
		t.Fatalf("second cooldown = %s", c.retryAt.Sub(now))
	}
	now = now.Add(time.Minute)
	if !c.observeFailure(containerFailureIncomplete) || !c.begin(containerFailureIncomplete) {
		t.Fatal("second retry rejected")
	}
	c.finishAutomatic(nil)
	c.observeHealthy()
	c.observeHealthy()
	if got := c.snapshot(); got.State != mihomoRecoveryIdle || got.RetryAttempts != 0 || got.NextRetryAt != "" {
		t.Fatalf("recovered = %+v", got)
	}
}

func TestConfirmedFailureAfterSuccessfulNASRecoveryRetries(t *testing.T) {
	for _, reason := range []string{containerFailureIncomplete, containerFailureDNSMissing, containerFailureDataPlaneMissing, containerFailureDNSUnresponsive, containerFailureControllerRefused} {
		t.Run(reason, func(t *testing.T) {
			now := time.Now()
			c := newMihomoRecoveryController()
			c.now = func() time.Time { return now }
			c.begin(reason)
			c.finishAutomatic(nil)
			c.observeHealthy() // one sample does not establish sustained recovery
			if c.observeFailure(reason) {
				t.Fatal("repeated a full recovery without cooldown")
			}
			if got := c.snapshot(); got.State != mihomoRecoveryFailed || got.NextRetryAt == "" || got.RetryAttempts != 1 {
				t.Fatalf("confirmed failure stranded the gateway: %+v", got)
			}
			now = now.Add(30 * time.Second)
			c.observeUnknown()
			if c.snapshot().State != mihomoRecoveryFailed {
				t.Fatal("unknown health initiated recovery")
			}
			eligible := c.observeFailure(reason)
			if reason == containerFailureControllerRefused {
				if eligible {
					t.Fatal("controller refusal needs two fresh samples")
				}
				eligible = c.observeFailure(reason)
			}
			if !eligible || !c.begin(reason) {
				t.Fatal("confirmed failure did not rearm recovery")
			}
			c.finishAutomatic(nil)
			c.observeHealthy()
			c.observeHealthy()
			if got := c.snapshot(); got.State != mihomoRecoveryIdle || got.RetryAttempts != 0 {
				t.Fatalf("healthy confirmations did not clear recovery: %+v", got)
			}
		})
	}
}

func TestNASBootLifecycleContentionDoesNotConsumeRecovery(t *testing.T) {
	for _, manual := range []bool{false, true} {
		c := newMihomoRecoveryController()
		c.begin(containerFailureIncomplete)
		if manual {
			c.finishManual(gateway.ErrLifecycleOperationInProgress)
		} else {
			c.finishAutomatic(gateway.ErrLifecycleOperationInProgress)
		}
		if got := c.snapshot(); got.State != mihomoRecoveryObserving || got.Error != "" || got.NextRetryAt != "" {
			t.Fatalf("boot lock race became permanent failure: %+v", got)
		}
		if !c.observeFailure(containerFailureIncomplete) || !c.begin(containerFailureIncomplete) {
			t.Fatal("lock contention consumed incident")
		}
	}
	unsafe := newMihomoRecoveryController()
	unsafe.begin(containerFailureIncomplete)
	unsafe.finishAutomatic(errors.Join(gateway.ErrLifecycleOperationInProgress, errors.New("identity changed; refusing to signal")))
	if unsafe.observeFailure(containerFailureIncomplete) || unsafe.snapshot().State != mihomoRecoveryFailed {
		t.Fatal("compound ownership refusal was ignored")
	}
}

func TestNASUnknownHealthDoesNotStrandLaterConfirmedFailure(t *testing.T) {
	now := time.Now()
	c := newMihomoRecoveryController()
	c.now = func() time.Time { return now }
	c.begin(containerFailureIncomplete)
	c.finishAutomatic(nil)
	for i := 0; i < mihomoRecoveryUnknownLimit; i++ {
		c.observeUnknown()
	}
	if got := c.snapshot(); got.State != mihomoRecoveryFailed || got.NextRetryAt != "" {
		t.Fatalf("unknown health initiated recovery: %+v", got)
	}
	if c.observeFailure(containerFailureDNSMissing) || c.snapshot().NextRetryAt == "" {
		t.Fatal("later confirmed failure did not schedule a cooldown")
	}
	now = now.Add(30 * time.Second)
	if !c.observeFailure(containerFailureDNSMissing) || !c.begin(containerFailureDNSMissing) {
		t.Fatal("unknown-health guard stranded a confirmed failed NAS gateway")
	}
}

func TestNASControllerRefusalUsesFullRecoveryWithConfirmation(t *testing.T) {
	status := gateway.Status{DesiredRunning: true, RuntimeState: "active", Gateway: "degraded", Mihomo: "running", MihomoError: "dial tcp: connection refused", DNS: "running", LocalDNS: "running"}
	action, reason := automaticRecoveryAction(status, true)
	if action != "recover-gateway" || reason != containerFailureControllerRefused {
		t.Fatalf("partial engine restart cannot restore dependent DNS: %q %q", action, reason)
	}
	c := newMihomoRecoveryController()
	if c.observeFailure(reason) {
		t.Fatal("single refusal initiated recovery")
	}
	c.observeUnknown()
	if c.observeFailure(reason) {
		t.Fatal("nonconsecutive refusals initiated recovery")
	}
	if !c.observeFailure(reason) {
		t.Fatal("two fresh refusals did not initiate recovery")
	}
	if c.snapshot().Reason != containerFailureIncomplete {
		t.Fatal("NAS refusal must expose the Web full-recovery reason")
	}
	mac := newMihomoRecoveryController()
	mac.observeFailure(mihomoFailureControllerRefused)
	if mac.snapshot().Reason != mihomoFailureControllerRefused {
		t.Fatal("changed upstream Mac recovery reason")
	}
}

func TestStopCancelsTimeoutRetryAndPermanentErrorNeverLoops(t *testing.T) {
	c := newMihomoRecoveryController()
	c.beginManual(containerFailureIncomplete)
	c.finishManual(context.DeadlineExceeded)
	c.observeStopped()
	if got := c.snapshot(); got.NextRetryAt != "" || got.State != mihomoRecoveryIdle {
		t.Fatalf("stopped = %+v", got)
	}
	c.beginManual(containerFailureIncomplete)
	c.finishManual(errors.New("process identity changed; refusing to signal PID"))
	if got := c.snapshot(); got.NextRetryAt != "" {
		t.Fatalf("unsafe failure scheduled retry = %+v", got)
	}
	if c.observeFailure(containerFailureIncomplete) {
		t.Fatal("permanent failure retried")
	}
	if recoveryTimeout(fmt.Errorf("refusing to signal PID: %w", context.DeadlineExceeded)) {
		t.Fatal("composite ownership error became retryable")
	}
}

func TestTimeoutCooldownIsCappedAndMacPolicyUnchanged(t *testing.T) {
	c := newMihomoRecoveryController()
	c.reason = containerFailureIncomplete
	for i := 0; i < 20; i++ {
		c.scheduleTimeoutRetry(context.DeadlineExceeded)
	}
	if delta := time.Until(c.retryAt); delta > 15*time.Minute || delta < 14*time.Minute {
		t.Fatalf("unbounded cooldown = %s", delta)
	}
	mac := newMihomoRecoveryController()
	mac.begin(mihomoFailureProcessMissing)
	mac.finishAutomatic(context.DeadlineExceeded)
	if mac.snapshot().NextRetryAt != "" {
		t.Fatal("changed Mac recovery policy")
	}
}

func TestManualNASStartReplacesPreviousFailedIncident(t *testing.T) {
	c := newMihomoRecoveryController()
	c.begin(containerFailureIncomplete)
	c.finishAutomatic(errors.New("invalid config"))
	c.beginManual(containerFailureIncomplete)
	if got := c.snapshot(); got.State != mihomoRecoveryRecovering || got.Error != "" || got.RetryAttempts != 0 {
		t.Fatalf("manual start retained old incident: %+v", got)
	}
	c.finishManual(context.DeadlineExceeded)
	if got := c.snapshot(); got.NextRetryAt == "" || got.RetryAttempts != 1 {
		t.Fatalf("new manual start timeout did not schedule recovery: %+v", got)
	}
}
