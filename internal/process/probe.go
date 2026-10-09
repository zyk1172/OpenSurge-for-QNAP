package process

import (
	"context"
	"errors"
)

var ErrProbeCapacity = errors.New("network probes are still waiting for the host kernel; probe capacity exhausted")

// ProbePool bounds read-only work that may remain blocked even after a context
// is cancelled (for example exec/Wait on a Linux task in uninterruptible sleep).
// A timed-out worker retains its slot until it actually finishes. Never use
// this for mutations: returning early cannot prove their final side effects.
type ProbePool struct {
	slots chan struct{}
}

func NewProbePool(limit int) *ProbePool {
	if limit < 1 {
		panic("probe limit must be positive")
	}
	return &ProbePool{slots: make(chan struct{}, limit)}
}

func (p *ProbePool) Run(ctx context.Context, work func() ([]byte, error)) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case p.slots <- struct{}{}:
	default:
		return nil, ErrProbeCapacity
	}
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		var out []byte
		err := ctx.Err()
		if err == nil {
			out, err = work()
		}
		<-p.slots
		done <- result{out, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return result.out, result.err
	}
}
