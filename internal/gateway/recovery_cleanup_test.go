package gateway

import (
	"errors"
	"os"
	"testing"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/runtime"
)

func TestStopContinuesOwnedCleanupAfterPreparedFailure(t *testing.T) {
	for _, identityMatches := range []bool{true, false} {
		cfg := config.Default()
		cfg.Runtime.Dir = t.TempDir()
		paths := runtime.NewPaths(cfg)
		if err := runtime.Ensure(paths); err != nil {
			t.Fatal(err)
		}
		state := runtime.State{BootSessionID: "test-boot", Lifecycle: "running", PIDMihomo: os.Getpid(), MihomoProcessFingerprint: "owned", PIDDNSMasq: os.Getpid(), DNSMasqProcessFingerprint: "owned", NetworkSnapshot: platform.NewSnapshot(platform.BackendLinuxNFTables)}
		if err := runtime.SaveState(paths.StateFile, state); err != nil {
			t.Fatal(err)
		}
		dns, engine, network := &fakeDHCP{}, &fakeMihomo{}, &fakeBackend{}
		preparedErr := errors.New("prepared process identity unavailable")
		manager := Manager{cfg: cfg, paths: paths, deps: gatewayDeps{
			geteuid: func() int { return 0 }, loadState: runtime.LoadState, saveState: runtime.SaveState, removeState: runtime.RemoveState,
			stopPrepared: func(config.Config) error { return preparedErr },
			newDHCP:      func(config.Config, runtime.Paths) dhcpService { return dns }, newMihomo: func(config.Config, runtime.Paths) mihomoService { return engine },
			newBackend:     func() (platform.NetworkBackend, error) { return network, nil },
			currentBoot:    func() (runtime.BootSession, error) { return runtime.BootSession{ID: "test-boot"}, nil },
			processMatches: func(int, string) (bool, error) { return identityMatches, nil },
		}}
		if err := manager.Stop(t.Context()); !errors.Is(err, preparedErr) {
			t.Fatalf("lost prepared error: %v", err)
		}
		if network.restoreCalls != 1 || dns.stopCalled != identityMatches || engine.stopCalled != identityMatches {
			t.Fatalf("cleanup matches=%t routes=%d dns=%t core=%t", identityMatches, network.restoreCalls, dns.stopCalled, engine.stopCalled)
		}
		retained, exists, err := runtime.LoadState(paths.StateFile)
		if err != nil || !exists || retained.Lifecycle != "stopping" {
			t.Fatalf("lost retry state: exists=%t state=%+v err=%v", exists, retained, err)
		}
	}
}

func TestStopIntentSurvivesInvalidDeployment(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	cfg.Gateway.LANIP = "invalid"
	path := cfg.Runtime.Dir + "/config.yaml"
	if err := os.WriteFile(path, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetDesiredRunningConfig(path, false); err != nil {
		t.Fatal(err)
	}
	desired, exists, err := runtime.LoadGatewayDesiredState(runtime.GatewayDesiredStatePath(cfg.Runtime.Dir))
	if err != nil || !exists || desired.Running {
		t.Fatalf("stop intent: %+v exists=%t err=%v", desired, exists, err)
	}
	if err := SetDesiredRunningConfig(path, true); err == nil {
		t.Fatal("start intent accepted invalid deployment")
	}
}
