package process

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProbeCancellationRetainsCapacityUntilWorkerExits(t *testing.T) {
	pool := NewProbePool(1)
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		_, err := pool.Run(ctx, func() ([]byte, error) {
			close(started)
			<-release // Models a kernel wait that ignores cancellation.
			close(finished)
			return []byte("late output"), nil
		})
		returned <- err
	}()
	<-started
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled probe: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("probe caller remained blocked after cancellation")
	}
	for range 20 {
		_, err := pool.Run(t.Context(), func() ([]byte, error) {
			t.Error("a repeated request started more work while the old worker was blocked")
			return nil, nil
		})
		if !errors.Is(err, ErrProbeCapacity) {
			t.Fatalf("saturated pool: %v", err)
		}
	}
	select {
	case <-finished:
		t.Fatal("test worker was not blocked")
	default:
	}
}

func TestProbeCompletionReleasesCapacity(t *testing.T) {
	pool := NewProbePool(1)
	for range 20 {
		out, err := pool.Run(t.Context(), func() ([]byte, error) { return []byte("ok"), nil })
		if err != nil || string(out) != "ok" {
			t.Fatalf("completed probe: %q %v", out, err)
		}
	}
}

func TestCancelledProbeNeverStartsWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := NewProbePool(1).Run(ctx, func() ([]byte, error) {
		t.Fatal("cancelled request started work")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request: %v", err)
	}
}
