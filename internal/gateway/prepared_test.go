package gateway

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/runtime"
)

func TestStartAndStopRefuseUnreleasedPreparedEngine(t *testing.T) {
	for _, action := range []string{"start", "stop"} {
		t.Run(action, func(t *testing.T) {
			cfg := config.Default()
			cfg.Gateway.Interface = "lan0"
			cfg.Gateway.UpstreamInterface = "wan0"
			cfg.Runtime.Dir = t.TempDir()
			cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
			manager := Manager{cfg: cfg, paths: runtime.NewPaths(cfg), deps: gatewayDeps{
				geteuid: func() int { return 0 }, loadState: runtime.LoadState,
				stopPrepared: func(config.Config) error { return errors.New("prepared process still owns cache") },
			}}
			var err error
			if action == "start" {
				err = manager.Start(context.Background())
			} else {
				err = manager.Stop(context.Background())
			}
			if err == nil || !strings.Contains(err.Error(), "prepared process still owns cache") {
				t.Fatalf("%s proceeded before prepared process exit: %v", action, err)
			}
		})
	}
}

func TestStopReleasesPreparedEngineWithoutGatewayState(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	stops := 0
	manager := Manager{cfg: cfg, paths: runtime.NewPaths(cfg), deps: gatewayDeps{
		geteuid: func() int { return 0 }, loadState: runtime.LoadState, removeState: runtime.RemoveState,
		stopPrepared: func(config.Config) error { stops++; return nil },
		newBackend:   func() (platform.NetworkBackend, error) { return &fakeBackend{}, nil },
	}}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stops != 1 {
		t.Fatalf("stopped gateway did not reconcile prepared engine: calls=%d", stops)
	}
}

func TestCandidateStartCommitsAfterFinalValidation(t *testing.T) {
	for _, tt := range []struct {
		name, cancelPhase                        string
		validationErr, commitErr, startErr       error
		wantCommitted, wantForwarding, wantStart bool
	}{
		{name: "success", wantCommitted: true, wantForwarding: true, wantStart: true},
		{name: "invalid", validationErr: errors.New("invalid core config")},
		{name: "commit failure", commitErr: errors.New("cannot save desired")},
		{name: "cancel before commit", cancelPhase: "saving_config"},
		{name: "cancel after commit", cancelPhase: "saving_runtime", wantCommitted: true},
		{name: "cancel after forwarding", cancelPhase: "starting_mihomo", wantCommitted: true, wantForwarding: true},
		{name: "network start failure", startErr: errors.New("core could not start"), wantCommitted: true, wantForwarding: true, wantStart: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Gateway.Interface, cfg.Gateway.UpstreamInterface = "lan0", "wan0"
			cfg.Runtime.Dir = t.TempDir()
			cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
			var events []string
			core := &fakeMihomo{validateErr: tt.validationErr, startErr: tt.startErr, events: &events}
			dns := &fakeDHCP{}
			firewall := &fakeBackend{}
			manager := Manager{cfg: cfg, paths: runtime.NewPaths(cfg), deps: gatewayDeps{
				geteuid: func() int { return 0 }, loadState: runtime.LoadState,
				saveState: runtime.SaveState, removeState: runtime.RemoveState, ensure: runtime.Ensure,
				stopPrepared:    func(config.Config) error { events = append(events, "stop-prepared"); return nil },
				newMihomo:       func(config.Config, runtime.Paths) mihomoService { return core },
				newDHCP:         func(config.Config, runtime.Paths) dhcpService { return dns },
				newBackend:      func() (platform.NetworkBackend, error) { return firewall, nil },
				interfaces:      func() ([]net.Interface, error) { return []net.Interface{{Name: "lan0"}}, nil },
				interfaceByName: func(name string) (*net.Interface, error) { return &net.Interface{Name: name}, nil },
				interfaceAddrs: func(*net.Interface) ([]net.Addr, error) {
					return []net.Addr{&net.IPNet{IP: net.ParseIP(cfg.Gateway.LANIP), Mask: net.CIDRMask(24, 32)}}, nil
				},
				currentBoot: func() (runtime.BootSession, error) { return runtime.BootSession{ID: "test-boot"}, nil },
				now:         time.Now,
			}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx = WithProgress(ctx, func(p Progress) {
				if p.Phase == tt.cancelPhase {
					cancel()
				}
			})
			committed := false
			err := manager.StartCandidateLocked(ctx, func() error {
				if !slices.Equal(events, []string{"stop-prepared", "mihomo-write", "mihomo-validate"}) {
					t.Fatalf("candidate was not validated exactly once after prepared exit: %v", events)
				}
				if core.startCalled || firewall.natCalls > 0 || firewall.routingCalls > 0 {
					t.Fatal("network takeover preceded commit")
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if tt.commitErr != nil {
					return tt.commitErr
				}
				committed = true
				return nil
			})
			if (err == nil) != (tt.name == "success") {
				t.Fatalf("start error=%v", err)
			}
			if tt.cancelPhase != "" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if committed != tt.wantCommitted || firewall.forwardingAttempted != tt.wantForwarding || core.startCalled != tt.wantStart {
				t.Fatalf("committed=%t forwarding=%t core start=%t", committed, firewall.forwardingAttempted, core.startCalled)
			}
			if err != nil {
				if _, exists, stateErr := runtime.LoadState(manager.paths.StateFile); stateErr != nil || exists {
					t.Fatalf("failed candidate retained runtime: exists=%t err=%v", exists, stateErr)
				}
				if tt.wantForwarding && !firewall.forwardingRestored {
					t.Fatal("forwarding was not restored after the failed start")
				}
			}
		})
	}
}
