package mihomo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/runtime"
)

func TestStartupWaitsObserveLifecycleCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	cfg.Mihomo.APIAddr = server.URL
	m := New(cfg, runtime.NewPaths(cfg))
	for _, stage := range []string{"api", "tun", "socks", "ipv6"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			started := time.Now()
			var err error
			switch stage {
			case "api":
				err = m.waitForAPIContext(ctx, os.Getpid(), 30*time.Second)
			case "tun":
				err = m.waitForTUNContext(ctx, os.Getpid(), 30*time.Second)
			case "socks":
				err = m.waitForSmartDNSProxyListenerContext(ctx, os.Getpid(), 30*time.Second)
			case "ipv6":
				err = m.waitForIPv6PacketListenerContext(ctx, os.Getpid(), 30*time.Second)
			}
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 1500*time.Millisecond {
				t.Fatalf("stage=%s elapsed=%v err=%v", stage, time.Since(started), err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.waitForAPIContext(ctx, os.Getpid(), time.Second); !errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel was reported as timeout: %v", err)
	}
}
