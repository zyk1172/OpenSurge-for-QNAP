//go:build linux

package linux

import (
	"context"
	"fmt"

	"open-mihomo-gateway/internal/platform"
)

// enableIPv4Forwarding turns on forwarding and returns a closure that restores
// the previous value. The closure is returned instead of a stored field so the
// gateway transaction can decide when to run it, and can still run it after a
// cancellation using a fresh context.
func (b *Backend) enableIPv4Forwarding(ctx context.Context) (func(context.Context) error, string, error) {
	before, err := readProcSys(procIPv4Forward)
	if err != nil {
		return nil, "", platform.NewError(platform.CodeForwardingUnavailable,
			"read /proc/sys/net/ipv4/ip_forward; the container needs CAP_NET_ADMIN and a writable /proc/sys, or Compose must set sysctls: net.ipv4.ip_forward=1").
			Wrap(err)
	}
	if before != "1" {
		if _, err := writeProcSys(procIPv4Forward, "1"); err != nil {
			return nil, before, err
		}
	}
	restore := func(ctx context.Context) error {
		if before == "" {
			return nil
		}
		if _, err := writeProcSys(procIPv4Forward, before); err != nil {
			return platform.NewError(platform.CodeForwardingUnavailable,
				fmt.Sprintf("restore /proc/sys/net/ipv4/ip_forward to %s", before)).Wrap(err)
		}
		return nil
	}
	return restore, before, nil
}

// currentIPv4Forwarding reports the live value.
func (b *Backend) currentIPv4Forwarding() (string, error) {
	value, err := readProcSys(procIPv4Forward)
	if err != nil {
		return "", platform.NewError(platform.CodeForwardingUnavailable,
			"read /proc/sys/net/ipv4/ip_forward").Wrap(err)
	}
	return value, nil
}
