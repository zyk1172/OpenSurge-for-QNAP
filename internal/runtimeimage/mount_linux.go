//go:build linux

package runtimeimage

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func Mount(root string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("runtime image cache requires container root")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return fmt.Errorf("runtime image cache is restricted to the Docker container")
	}
	// Docker can create the read-only bind before startup under docker-default
	// AppArmor. Compare inode/device and read-only state rather than mounting
	// again or relaxing the existing security profile.
	cached, err := os.Stat(root)
	if err != nil {
		return err
	}
	current, err := os.Stat("/usr")
	if err != nil {
		return err
	}
	a, aok := cached.Sys().(*syscall.Stat_t)
	b, bok := current.Sys().(*syscall.Stat_t)
	if aok && bok && a.Dev == b.Dev && a.Ino == b.Ino {
		var fs unix.Statfs_t
		if err := unix.Statfs("/usr", &fs); err != nil {
			return err
		}
		if fs.Flags&unix.ST_RDONLY == 0 {
			return fmt.Errorf("pre-mounted runtime cache must be read-only")
		}
		return nil
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
