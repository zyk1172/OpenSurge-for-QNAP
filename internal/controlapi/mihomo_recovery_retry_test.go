package controlapi

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
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
