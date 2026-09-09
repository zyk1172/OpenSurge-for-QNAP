//go:build !linux

package factory

import (
	"fmt"
	"runtime"

	"open-mihomo-gateway/internal/platform"
)

// newBackend refuses to run the network data plane on a non-Linux host.
// OpenSurge for QNAP ships a Linux backend only; silently degrading would be
// worse than a clear error.
func newBackend() (platform.NetworkBackend, error) {
	return nil, fmt.Errorf("%w: %s/%s is not supported by OpenSurge for QNAP; run the Docker image on Linux",
		platform.ErrUnsupported, runtime.GOOS, runtime.GOARCH)
}
