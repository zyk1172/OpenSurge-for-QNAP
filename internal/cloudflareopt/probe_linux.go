//go:build linux

package cloudflareopt

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func directDialer(interfaceName, sourceIPv4 string, timeout time.Duration) *net.Dialer {
	dialer := &net.Dialer{Timeout: timeout}
	if ip := net.ParseIP(strings.TrimSpace(sourceIPv4)); ip != nil && ip.To4() != nil {
		dialer.LocalAddr = &net.TCPAddr{IP: ip.To4()}
	}
	if interfaceName == "" {
		return dialer
	}
	dialer.Control = func(_, _ string, raw syscall.RawConn) error {
		var socketErr error
		if err := raw.Control(func(fd uintptr) {
			socketErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, interfaceName)
		}); err != nil {
			return err
		}
		return socketErr
	}
	return dialer
}

// VerifyDirectRoute proves that a representative Cloudflare candidate leaves
// through the configured physical interface and never through a TUN device.
// It is read-only: no policy rule or route is installed or removed.
func VerifyDirectRoute(ctx context.Context, interfaceName, sourceIPv4, candidate string) error {
	interfaceName = strings.TrimSpace(interfaceName)
	if interfaceName == "" {
		return fmt.Errorf("physical egress interface is empty")
	}
	args := []string{"-4", "route", "get", candidate, "oif", interfaceName}
	if sourceIPv4 != "" {
		args = append(args, "from", sourceIPv4)
	}
	output, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip route get: %w: %s", err, strings.TrimSpace(string(output)))
	}
	line := strings.TrimSpace(string(output))
	if line == "" {
		return fmt.Errorf("ip route get returned no route")
	}
	fields := strings.Fields(line)
	device := ""
	for index := 0; index+1 < len(fields); index++ {
		if fields[index] == "dev" {
			device = fields[index+1]
			break
		}
	}
	if device != interfaceName {
		return fmt.Errorf("route uses %q instead of physical interface %q", device, interfaceName)
	}
	if strings.Contains(strings.ToLower(device), "tun") || strings.Contains(strings.ToLower(line), " tun") {
		return fmt.Errorf("route unexpectedly traverses a TUN device: %s", line)
	}
	return nil
}
