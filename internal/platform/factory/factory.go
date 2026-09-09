// Package factory selects the concrete platform.NetworkBackend for the host.
//
// It lives in its own package because the dependency direction is fixed:
// platform defines the contract, platform/linux implements it, so platform
// itself must never import an implementation. Putting New() in platform would
// create an import cycle.
package factory

import (
	"fmt"
	"runtime"

	"open-mihomo-gateway/internal/platform"
)

// New returns the network backend for this host.
func New() (platform.NetworkBackend, error) {
	return newBackend()
}

// ErrUnsupported is returned on platforms with no backend.
var ErrUnsupported = platform.ErrUnsupported

// Describe reports which backend this build would select, for diagnostics.
func Describe() string {
	if runtime.GOOS != "linux" {
		return fmt.Sprintf("unsupported (%s/%s)", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("%s (%s/%s)", platform.BackendLinuxNFTables, runtime.GOOS, runtime.GOARCH)
}
