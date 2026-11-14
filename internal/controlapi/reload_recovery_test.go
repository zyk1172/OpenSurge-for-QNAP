package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/gateway"
)

func healthyReloadStatus() gateway.Status {
	return gateway.Status{Gateway: "running", RuntimeState: "active", Mihomo: "running", TUN: "ready", DNS: "running", LocalDNS: "running", Forwarding: "enabled", Routing: "applied", NFTables: "not_required"}
}

func TestProfileRollbackVerifiesHealthAndPreservesResidualFiles(t *testing.T) {
	for _, scenario := range []struct {
		name                             string
		phase                            gateway.ReloadPhase
		stateExists                      bool
		status                           gateway.Status
		probeErr, startErr               error
		wantRestart, wantReady, wantKeep bool
	}{
		{name: "old runtime preserved", phase: gateway.ReloadValidation, stateExists: true, status: healthyReloadStatus(), wantReady: true},
		{name: "cancel then restore", phase: gateway.ReloadStart, status: healthyReloadStatus(), wantRestart: true, wantReady: true},
		{name: "healthy candidate is not old runtime", phase: gateway.ReloadStart, stateExists: true, status: healthyReloadStatus(), wantKeep: true},
		{name: "partial cleanup", phase: gateway.ReloadStop, stateExists: true, status: gateway.Status{Gateway: "degraded", RuntimeState: "incomplete"}, wantKeep: true},
		{name: "stop failed without state", phase: gateway.ReloadStop, status: gateway.Status{Gateway: "stopped", RuntimeState: "none"}},
		{name: "restart failed", phase: gateway.ReloadStart, status: gateway.Status{Gateway: "degraded", RuntimeState: "incomplete"}, startErr: errors.New("restart failed"), wantRestart: true, wantKeep: true},
		{name: "health unknown", phase: gateway.ReloadStart, stateExists: true, probeErr: errors.New("probe failed"), wantKeep: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path, original := writeProfileApplyTestConfig(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			checks, starts := 0, 0
			cause := errors.New("candidate failed")
			result, err := applyProfile(ctx, path, fileDigest(path), profileApplyFixture(), profileApplySourceDigest(), "", profileApplyDeps{
				geteuid: func() int { return 0 }, validate: func(config.Config) error { return nil },
				stateExists: func(config.Config) (bool, error) { checks++; return checks == 1 || scenario.stateExists, nil },
				reload: func(context.Context, config.Config) error {
					cancel()
					return &gateway.ReloadError{Phase: scenario.phase, Err: cause}
				},
				start: func(ctx context.Context, _ config.Config) error {
					starts++
					deadline, ok := ctx.Deadline()
					if ctx.Err() != nil || !ok || time.Until(deadline) > 90*time.Second {
						t.Fatalf("recovery context: %v %v", ctx.Err(), deadline)
					}
					return scenario.startErr
				},
				status: func(ctx context.Context, _ config.Config) (gateway.Status, error) {
					if ctx.Err() != nil {
						t.Fatal(ctx.Err())
					}
					return scenario.status, scenario.probeErr
				},
			})
			if !errors.Is(err, cause) || result.Rollback == nil || result.Rollback.PreviousGatewayReady != scenario.wantReady || (starts > 0) != scenario.wantRestart {
				t.Fatalf("result=%+v starts=%d err=%v", result.Rollback, starts, err)
			}
			current, _ := os.ReadFile(path)
			if !bytes.Equal(current, original) {
				t.Fatal("previous config was not restored")
			}
			candidate := filepath.Join(filepath.Dir(path), "data", "imported-profile-"+fileDigestBytes(profileApplyFixture())[:16]+".yaml")
			_, statErr := os.Stat(candidate)
			if (statErr == nil) != scenario.wantKeep {
				t.Fatalf("candidate kept=%t want=%t err=%v", statErr == nil, scenario.wantKeep, statErr)
			}
		})
	}
}

func TestReloadHealthRejectsPartialNASDataPlane(t *testing.T) {
	for _, mutate := range []func(*gateway.Status){
		func(s *gateway.Status) { s.DNS = "stopped" }, func(s *gateway.Status) { s.LocalDNS = "stopped" },
		func(s *gateway.Status) { s.DHCPEnabled = true; s.DHCP = "stopped" }, func(s *gateway.Status) { s.MihomoError = "unreachable" },
		func(s *gateway.Status) { s.TUN = "unknown" }, func(s *gateway.Status) { s.Routing = "missing" },
		func(s *gateway.Status) { s.Forwarding = "unknown" }, func(s *gateway.Status) { s.NFTables = "missing" },
	} {
		s := healthyReloadStatus()
		mutate(&s)
		if reloadGatewayState(s, nil) == "running" {
			t.Fatalf("unready status accepted: %+v", s)
		}
	}
}

func TestHelperFailurePreservesReloadEvidence(t *testing.T) {
	dir, err := os.MkdirTemp("", "omg-helper-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "helper.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		var request HelperRequest
		if err = json.NewDecoder(conn).Decode(&request); err == nil {
			err = json.NewEncoder(conn).Encode(HelperResponse{Error: "apply failed", ReloadPhase: gateway.ReloadStart, Rollback: &ConfigurationRollback{ConfigRestored: true, GatewayState: "degraded"}})
		}
		done <- err
	}()
	result, err := (HelperClient{SocketPath: socket}).ApplyProfile(t.Context(), "/config.yaml", "revision", []byte("rules: []"), "", "")
	var failure *gateway.ReloadError
	if !errors.As(err, &failure) || failure.Phase != gateway.ReloadStart || result.Rollback == nil || result.Rollback.GatewayState != "degraded" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
