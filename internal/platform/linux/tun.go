//go:build linux

package linux

import (
	"context"
	"fmt"
	"os"
	"time"

	"open-mihomo-gateway/internal/platform"
)

// procTUN is the Linux TUN device node. mihomo opens it to create the data
// plane device; OpenSurge only needs to confirm it is present and usable.
const procTUN = "/dev/net/tun"

// pollInterval controls how often WaitForTUN re-checks for the device.
const pollInterval = 200 * time.Millisecond

// ensureTUN verifies the host exposes a usable TUN facility.
func (b *Backend) ensureTUN(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(procTUN)
	if err != nil {
		if os.IsNotExist(err) {
			return platform.NewError(platform.CodeTUNUnavailable,
				"/dev/net/tun is not present; enable TUN on the host and pass the device into the container").
				WithDetail("path", procTUN)
		}
		return platform.NewError(platform.CodeTUNUnavailable, "stat /dev/net/tun").Wrap(err)
	}
	if info.Mode()&os.ModeDevice == 0 {
		return platform.NewError(platform.CodeTUNUnavailable, "/dev/net/tun is not a device node").
			WithDetail("path", procTUN)
	}
	// Open read-write to confirm the capability is actually granted rather than
	// merely visible; a container can see the node without CAP_NET_ADMIN.
	file, err := os.OpenFile(procTUN, os.O_RDWR, 0)
	if err != nil {
		return platform.NewError(platform.CodeTUNUnavailable,
			"/dev/net/tun cannot be opened; the container needs CAP_NET_ADMIN").
			WithDetail("path", procTUN).Wrap(err)
	}
	return file.Close()
}

// waitForTUN blocks until the proxy core has created the named device.
// The device is created by mihomo, so OpenSurge must tolerate the window
// between process start and device appearance, and must fail cleanly if it
// never appears rather than proceeding with a half-built data plane.
func (b *Backend) waitForTUN(ctx context.Context, device string) (platform.NetworkInterface, error) {
	if err := validateInterfaceName(device); err != nil {
		return platform.NetworkInterface{}, err
	}
	timeout := b.tunTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		iface, err := b.interfaceByName(deadline, device)
		if err == nil {
			return iface, nil
		}
		var coded *platform.Error
		if !asPlatformError(err, &coded) || coded.Code != platform.CodeInterfaceNotFound {
			return platform.NetworkInterface{}, err
		}
		lastErr = err
		select {
		case <-deadline.Done():
			return platform.NetworkInterface{}, platform.NewError(platform.CodeTUNTimeout,
				fmt.Sprintf("TUN device %s did not appear within %s", device, timeout)).
				WithDetails(map[string]string{
					"device":  device,
					"timeout": timeout.String(),
				}).Wrap(lastErr)
		case <-time.After(pollInterval):
		}
	}
}

// asPlatformError unwraps a *platform.Error from an error chain.
func asPlatformError(err error, target **platform.Error) bool {
	if err == nil {
		return false
	}
	if coded, ok := err.(*platform.Error); ok {
		*target = coded
		return true
	}
	unwrapper, ok := err.(interface{ Unwrap() error })
	if ok {
		return asPlatformError(unwrapper.Unwrap(), target)
	}
	return false
}
