//go:build linux

package factory

import (
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/platform/linux"
)

func newBackend() (platform.NetworkBackend, error) {
	return linux.New()
}
