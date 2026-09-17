//go:build !linux

package cloudflareopt

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

func directDialer(_ string, sourceIPv4 string, timeout time.Duration) *net.Dialer {
	dialer := &net.Dialer{Timeout: timeout}
	if ip := net.ParseIP(strings.TrimSpace(sourceIPv4)); ip != nil && ip.To4() != nil {
		dialer.LocalAddr = &net.TCPAddr{IP: ip.To4()}
	}
	return dialer
}

func VerifyDirectRoute(context.Context, string, string, string) error {
	return fmt.Errorf("direct route verification is only supported on Linux")
}
