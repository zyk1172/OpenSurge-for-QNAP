//go:build linux

package linux

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReadOnlyCommandReturnsWhenOutputPipeRemainsOpen(t *testing.T) {
	// The shell exits, but its descendant holds stdout open. exec.Cmd.Wait
	// remains blocked on the copy goroutine after cancellation, like a stuck
	// kernel task. No mutation is dispatched through this read-only path.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := newRunner().output(ctx, "sh", "-c", "sleep 1 & wait")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("probe error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("cancelled probe waited for the descendant: %s", elapsed)
	}
}

func TestReadOnlyCommandPreservesOutputAndError(t *testing.T) {
	out, err := newRunner().output(t.Context(), "sh", "-c", "printf observed")
	if err != nil || string(out) != "observed" {
		t.Fatalf("output = %q, %v", out, err)
	}
	_, err = newRunner().output(t.Context(), "sh", "-c", "printf failure >&2; exit 2")
	if err == nil {
		t.Fatal("failed command reported success")
	}
}
