//go:build linux

package runtimeimage

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func Mount(root string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("runtime image cache requires container root")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return fmt.Errorf("runtime image cache is restricted to the Docker container")
	}
	if err := unix.Mount(root, "/usr", "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind runtime cache to container /usr (requires SYS_ADMIN): %w", err)
	}
	if err := unix.Mount("", "/usr", "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
		_ = unix.Unmount("/usr", 0)
		return fmt.Errorf("make runtime image cache read-only: %w", err)
	}
	return nil
}
