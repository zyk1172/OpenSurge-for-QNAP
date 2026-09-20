package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/mihomo"
	"open-mihomo-gateway/internal/runtime"
)

const policyWorkspaceSourceFixture = `proxy-groups:
  - {name: Original, type: select, proxies: [DIRECT]}
rules:
  - DOMAIN,source.example,Original
  - MATCH,DIRECT
`

const policyWorkspaceOverlayFixture = `schema-version: 1
enabled: true
proxies:
  add:
    - {name: Overlay-Node, type: direct}
proxy-groups:
  add:
    - {name: Added, type: select, proxies: [Overlay-Node, DIRECT]}
rules:
  prepend:
    - DOMAIN,overlay.example,Added
`


func TestPolicyGroupUsesNativeGroupProbe(t *testing.T) {
	for _, value := range []string{"URLTest", "url-test", "Fallback", "LoadBalance", "load-balance"} {
		if !policyGroupUsesNativeGroupProbe(value) {
			t.Fatalf("%q should use Mihomo native group probing", value)
		}
	}
	for _, value := range []string{"Selector", "select", "DIRECT"} {
		if policyGroupUsesNativeGroupProbe(value) {
			t.Fatalf("%q must keep per-node/manual probing semantics", value)
		}
	}
}

func TestWorkspaceCandidateCompositionMatrix(t *testing.T) {
	for _, tt := range []struct {
		name     string
		source   bool
		overlay  bool
		exitNode bool
	}{
		{name: "empty managed"},
		{name: "Tailscale only", exitNode: true},
		{name: "overlay only", overlay: true},
		{name: "overlay and Tailscale", overlay: true, exitNode: true},
		{name: "source without overlay", source: true},
		{name: "source and Tailscale", source: true, exitNode: true},
		{name: "source and overlay", source: true, overlay: true},
		{name: "source overlay and Exit", source: true, overlay: true, exitNode: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path, cfg := workspaceCandidateTestConfig(t)
			input := PolicyWorkspaceInput{Request: PolicyWorkspaceRequest{Action: "read"}}
			if tt.source {
				cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
				cfg.Mihomo.Profile = filepath.Join(filepath.Dir(path), "original.yaml")
				if err := writeAtomic(cfg.Mihomo.Profile, []byte(policyWorkspaceSourceFixture), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.overlay {
				input.Overlay = []byte(policyWorkspaceOverlayFixture)
			}
			if tt.exitNode {
				cfg.Tailscale.Enabled = true
				cfg.Tailscale.ExitNode = "100.90.3.4"
				cfg.Tailscale.AuthKeyFile = filepath.Join(filepath.Dir(path), "tailscale-key")
				cfg.Tailscale.StateDir = filepath.Join(filepath.Dir(path), "tailscale-state")
				if err := writeAtomic(cfg.Tailscale.AuthKeyFile, []byte("tskey-auth-workspace-test"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			candidate, base, err := workspaceCandidate(path, cfg, input)
			if err != nil {
				t.Fatal(err)
			}
			if tt.overlay {
				if candidate.Mihomo.ProfileMode != config.MihomoProfileModeImported || base == nil {
					t.Fatalf("overlay workspace did not materialize an importable candidate: %#v / %#v", candidate.Mihomo, base)
				}
			} else if base != nil || candidate.Mihomo.ProfileMode != map[bool]string{false: config.MihomoProfileModeManaged, true: config.MihomoProfileModeImported}[tt.source] {
				t.Fatalf("workspace without an overlay unnecessarily changed its base: %#v / %#v", candidate.Mihomo, base)
			}
			final, err := mihomo.RenderConfig(candidate)
			if err != nil {
				t.Fatal(err)
			}
			groups := workspaceTestGroups(t, final)
			if _, ok := groups["Original"]; ok != tt.source {
				t.Fatalf("source group presence=%t, want %t", ok, tt.source)
			}
			if _, ok := groups["Added"]; ok != tt.overlay {
				t.Fatalf("overlay group presence=%t, want %t", ok, tt.overlay)
			}
			if _, ok := groups[config.TailscaleExitGroupName]; ok != tt.exitNode {
				t.Fatalf("Tailscale Exit group presence=%t, want %t", ok, tt.exitNode)
			}
			if tt.exitNode {
				wantCandidates := map[string][]any{}
				if tt.source {
					wantCandidates["Original"] = []any{"DIRECT", config.TailscaleExitGroupName}
				}
				if tt.overlay {
					wantCandidates["Added"] = []any{"Overlay-Node", "DIRECT", config.TailscaleExitGroupName}
				}
				for name, want := range wantCandidates {
					if !reflect.DeepEqual(groups[name]["proxies"], want) {
						t.Fatalf("%s candidates=%#v, want %#v", name, groups[name]["proxies"], want)
					}
				}
			}
			if tt.source {
				data, err := os.ReadFile(cfg.Mihomo.Profile)
				if err != nil || string(data) != policyWorkspaceSourceFixture {
					t.Fatalf("workspace rewrote original source: %v", err)
				}
			}
			if _, err := os.Stat(runtime.NewPaths(cfg).StateFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("composition created gateway runtime state: %v", err)
			}
		})
	}
}

func TestWorkspaceCandidateOverlayOnlyPersistsExactNextStartupGraph(t *testing.T) {
	path, cfg := workspaceCandidateTestConfig(t)
	if err := writeAtomic(path, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate, base, err := workspaceCandidate(path, cfg, PolicyWorkspaceInput{Overlay: []byte(policyWorkspaceOverlayFixture)})
	if err != nil {
		t.Fatal(err)
	}
	if err := persistPolicyWorkspaceCandidate(t.Context(), path, cfg, candidate, base); err != nil {
		t.Fatal(err)
	}
	desired, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if desired.Mihomo.ProfileMode != config.MihomoProfileModeImported || desired.Mihomo.ProfileOverlayDigest != mihomo.ProfileOverlayDigest([]byte(policyWorkspaceOverlayFixture)) {
		t.Fatalf("overlay-only desired config was not activated: %#v", desired.Mihomo)
	}
	final, err := mihomo.RenderConfig(desired)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Proxies []map[string]any `yaml:"proxies"`
		Groups  []map[string]any `yaml:"proxy-groups"`
		Rules   []string         `yaml:"rules"`
	}
	if err := yaml.Unmarshal([]byte(final), &document); err != nil {
		t.Fatal(err)
	}
	if !namedWorkspaceEntry(document.Proxies, "Overlay-Node") {
		t.Fatalf("next-start graph lost overlay node: %s", final)
	}
	if !namedWorkspaceEntry(document.Groups, "Added") {
		t.Fatalf("next-start graph lost overlay selector: %s", final)
	}
	if !containsWorkspaceString(document.Rules, "DOMAIN,overlay.example,Added") {
		t.Fatalf("next-start graph lost overlay rule: %#v", document.Rules)
	}
}

func TestStartPolicyWorkspaceOverlayOnlyPersistsAndStartsExactCandidate(t *testing.T) {
	path, cfg := workspaceCandidateTestConfig(t)
	if err := writeAtomic(path, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	input := PolicyWorkspaceInput{
		Request:              PolicyWorkspaceRequest{Action: "read"},
		Revision:             fileDigest(path),
		ExpectedGatewayState: policyWorkspaceGatewayStopped,
		Overlay:              []byte(policyWorkspaceOverlayFixture),
	}
	started := 0
	phases := []string{}
	err := startPolicyWorkspace(t.Context(), path, input, policyWorkspaceStartDeps{
		geteuid: func() int { return 0 },
		startLocked: func(_ context.Context, candidate config.Config, commit func() error) error {
			phases = append(phases, "validate")
			current, readErr := os.ReadFile(path)
			if readErr != nil || string(current) != config.Render(cfg) {
				t.Fatalf("candidate was persisted before engine validation: err=%v\n%s", readErr, current)
			}
			if err := commit(); err != nil {
				return err
			}
			phases = append(phases, "commit", "start")
			started++
			lock, lockErr := runtime.AcquireLifecycleLock(candidate)
			if lockErr == nil {
				_ = lock.Release()
				t.Fatal("gateway starter ran without the workspace lifecycle lock")
			}
			if !errors.Is(lockErr, runtime.ErrLifecycleOperationInProgress) {
				t.Fatalf("second lifecycle lock error=%v", lockErr)
			}
			desired, loadErr := config.Load(path)
			if loadErr != nil {
				t.Fatalf("persisted candidate cannot be loaded: %v", loadErr)
			}
			if config.Render(desired) != config.Render(candidate) {
				t.Fatalf("started candidate differs from persisted desired config:\n%s\n%s", config.Render(candidate), config.Render(desired))
			}
			final, renderErr := mihomo.RenderConfig(candidate)
			if renderErr != nil {
				t.Fatal(renderErr)
			}
			if !namedWorkspaceEntry(workspaceTestDocument(t, final).Groups, "Added") || !strings.Contains(final, "DOMAIN,overlay.example,Added") {
				t.Fatalf("direct start lost overlay-only graph:\n%s", final)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started != 1 {
		t.Fatalf("gateway starts=%d, want 1", started)
	}
	if !reflect.DeepEqual(phases, []string{"validate", "commit", "start"}) {
		t.Fatalf("start transaction phases=%#v", phases)
	}
}

func TestStartPolicyWorkspaceFailsClosedBeforeReplacingDesiredConfig(t *testing.T) {
	for _, tt := range []struct {
		name    string
		input   func(string) PolicyWorkspaceInput
		wantErr string
		engine  error
	}{
		{
			name: "revision conflict",
			input: func(string) PolicyWorkspaceInput {
				return PolicyWorkspaceInput{Request: PolicyWorkspaceRequest{Action: "read"}, Revision: "stale", ExpectedGatewayState: policyWorkspaceGatewayStopped, Overlay: []byte(policyWorkspaceOverlayFixture)}
			},
			wantErr: "revision conflict",
		},
		{
			name: "captured while running",
			input: func(revision string) PolicyWorkspaceInput {
				return PolicyWorkspaceInput{Request: PolicyWorkspaceRequest{Action: "read"}, Revision: revision, ExpectedGatewayState: policyWorkspaceGatewayRunning}
			},
			wantErr: "captured while the gateway was stopped",
		},
		{
			name: "invalid overlay",
			input: func(revision string) PolicyWorkspaceInput {
				return PolicyWorkspaceInput{Request: PolicyWorkspaceRequest{Action: "read"}, Revision: revision, ExpectedGatewayState: policyWorkspaceGatewayStopped, Overlay: []byte("enabled: [invalid")}
			},
			wantErr: "yaml",
		},
		{
			name: "engine validation",
			input: func(revision string) PolicyWorkspaceInput {
				return PolicyWorkspaceInput{Request: PolicyWorkspaceRequest{Action: "read"}, Revision: revision, ExpectedGatewayState: policyWorkspaceGatewayStopped, Overlay: []byte(policyWorkspaceOverlayFixture)}
			},
			wantErr: "engine rejected candidate",
			engine:  errors.New("engine rejected candidate"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path, cfg := workspaceCandidateTestConfig(t)
			original := config.Render(cfg)
			if err := writeAtomic(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			starts := 0
			err := startPolicyWorkspace(t.Context(), path, tt.input(fileDigest(path)), policyWorkspaceStartDeps{
				geteuid: func() int { return 0 },
				startLocked: func(_ context.Context, _ config.Config, commit func() error) error {
					if tt.engine != nil {
						return tt.engine
					}
					if err := commit(); err != nil {
						return err
					}
					starts++
					return nil
				},
			})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.wantErr)) {
				t.Fatalf("error=%v, want %q", err, tt.wantErr)
			}
			if starts != 0 {
				t.Fatalf("invalid candidate started %d times", starts)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil || string(data) != original {
				t.Fatalf("invalid candidate replaced desired config: err=%v\n%s", readErr, data)
			}
		})
	}
}

func TestStartPolicyWorkspaceExpiredValidationCannotPersistOrStart(t *testing.T) {
	path, cfg := workspaceCandidateTestConfig(t)
	original := config.Render(cfg)
	if err := writeAtomic(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	starts := 0
	err := startPolicyWorkspace(ctx, path, PolicyWorkspaceInput{
		Request:              PolicyWorkspaceRequest{Action: "read"},
		Revision:             fileDigest(path),
		ExpectedGatewayState: policyWorkspaceGatewayStopped,
		Overlay:              []byte(policyWorkspaceOverlayFixture),
	}, policyWorkspaceStartDeps{
		geteuid: func() int { return 0 },
		startLocked: func(_ context.Context, _ config.Config, commit func() error) error {
			cancel()
			if err := commit(); err != nil {
				return err
			}
			starts++
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want expired validation failure", err)
	}
	if starts != 0 {
		t.Fatalf("expired candidate started %d times", starts)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != original {
		t.Fatalf("expired candidate replaced desired config: err=%v\n%s", readErr, data)
	}
}

func TestWorkspaceCandidateOverlayOnlyEmbedsAndRestoresManagedUpstream(t *testing.T) {
	path, cfg := workspaceCandidateTestConfig(t)
	cfg.UpstreamProxy = config.UpstreamProxyConfig{
		Enabled: true, Name: "Managed-Upstream", Type: "socks5", Server: "127.0.0.1", Port: 18080,
		Username: "user", Password: "password", MatchDomain: "upstream.example",
	}
	if err := writeAtomic(path, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate, base, err := workspaceCandidate(path, cfg, PolicyWorkspaceInput{Overlay: []byte(policyWorkspaceOverlayFixture)})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Mihomo.ProfileMode != config.MihomoProfileModeImported || candidate.UpstreamProxy.Enabled {
		t.Fatalf("materialized managed upstream violates imported-mode invariant: %#v / %#v", candidate.Mihomo, candidate.UpstreamProxy)
	}
	if base == nil || !reflect.DeepEqual(base.UpstreamProxy, cfg.UpstreamProxy) {
		t.Fatalf("managed upstream recovery metadata=%#v, want %#v", base, cfg.UpstreamProxy)
	}
	if err := config.Validate(candidate); err != nil {
		t.Fatalf("overlay-only desired config is not loadable: %v", err)
	}
	final, err := mihomo.RenderConfig(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(final, `name: "Managed-Upstream"`) != 1 || !strings.Contains(final, "DOMAIN,upstream.example,open-surge-egress") || !strings.Contains(final, "DOMAIN,overlay.example,Added") {
		t.Fatalf("materialized graph lost or duplicated managed upstream/overlay:\n%s", final)
	}
	if err := persistPolicyWorkspaceCandidate(t.Context(), path, cfg, candidate, base); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("persisted overlay-only config cannot start: %v", err)
	}
	persistWorkspaceBaseForTest(t, cfg, base)
	restored, restoredBase, err := workspaceCandidate(path, candidate, PolicyWorkspaceInput{Overlay: []byte("schema-version: 1\nenabled: false\n")})
	if err != nil {
		t.Fatal(err)
	}
	if restoredBase != nil || restored.Mihomo.ProfileMode != config.MihomoProfileModeManaged || restored.Mihomo.Profile != "" || !reflect.DeepEqual(restored.UpstreamProxy, cfg.UpstreamProxy) {
		t.Fatalf("disabling standalone overlay did not restore managed upstream: %#v / %#v", restored, restoredBase)
	}
	if err := config.Validate(restored); err != nil {
		t.Fatalf("restored managed config is invalid: %v", err)
	}
}

func TestWorkspaceCandidateRepeatedCompositionAndDisabledOverlayRestoreBase(t *testing.T) {
	for _, imported := range []bool{false, true} {
		t.Run(map[bool]string{false: "managed base", true: "imported base"}[imported], func(t *testing.T) {
			path, cfg := workspaceCandidateTestConfig(t)
			if imported {
				cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
				cfg.Mihomo.Profile = filepath.Join(filepath.Dir(path), "source.yaml")
				if err := writeAtomic(cfg.Mihomo.Profile, []byte(policyWorkspaceSourceFixture), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			input := PolicyWorkspaceInput{Overlay: []byte(policyWorkspaceOverlayFixture)}
			first, base, err := workspaceCandidate(path, cfg, input)
			if err != nil {
				t.Fatal(err)
			}
			persistWorkspaceBaseForTest(t, cfg, base)
			second, secondBase, err := workspaceCandidate(path, first, input)
			if err != nil {
				t.Fatalf("same overlay was applied twice instead of recomposed from base: %v", err)
			}
			if config.Render(first) != config.Render(second) || !reflect.DeepEqual(base, secondBase) {
				t.Fatalf("repeated workspace composition is not idempotent:\n%#v\n%#v", base, secondBase)
			}
			disabled, disabledBase, err := workspaceCandidate(path, second, PolicyWorkspaceInput{Overlay: []byte("schema-version: 1\nenabled: false\n")})
			if err != nil {
				t.Fatal(err)
			}
			if disabled.Mihomo.ProfileOverlayDigest != "" {
				t.Fatalf("disabled overlay retained digest: %#v", disabled.Mihomo)
			}
			if !imported && (disabled.Mihomo.ProfileMode != cfg.Mihomo.ProfileMode || disabled.Mihomo.Profile != cfg.Mihomo.Profile || disabledBase != nil) {
				t.Fatalf("disabling overlay-only workspace did not restore managed base: %#v", disabled.Mihomo)
			}
			final, err := mihomo.RenderConfig(disabled)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(final, "overlay.example") || workspaceTestGroups(t, final)["Added"] != nil {
				t.Fatalf("disabled overlay was retained:\n%s", final)
			}
		})
	}
}

func TestWorkspaceCandidateRejectsMissingOrMismatchedRawSource(t *testing.T) {
	path, cfg := workspaceCandidateTestConfig(t)
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = filepath.Join(filepath.Dir(path), "missing.yaml")
	if _, _, err := workspaceCandidate(path, cfg, PolicyWorkspaceInput{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing raw source error=%v, want honest missing-file failure", err)
	}
	cfg.Mihomo.ProfileOverlayDigest = "previous-overlay"
	if _, _, err := workspaceCandidate(path, cfg, PolicyWorkspaceInput{Overlay: []byte(policyWorkspaceOverlayFixture)}); err == nil || !strings.Contains(err.Error(), "raw source is unavailable") {
		t.Fatalf("legacy composed source error=%v", err)
	}
	cfg.Mihomo.ProfileSourceDigest = "another-source"
	if _, _, err := workspaceCandidate(path, cfg, PolicyWorkspaceInput{Source: []byte(policyWorkspaceSourceFixture)}); err == nil || !strings.Contains(err.Error(), "selected profile changed") {
		t.Fatalf("mismatched source error=%v", err)
	}
}

func TestWorkspaceCandidateKeepsUnchangedLegacyCompositionWithoutRawSource(t *testing.T) {
	path, cfg := workspaceCandidateTestConfig(t)
	composition, err := mihomo.ComposeProfileOverlay([]byte(policyWorkspaceSourceFixture), mustWorkspaceOverlay(t, policyWorkspaceOverlayFixture))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = filepath.Join(filepath.Dir(path), "legacy-composed.yaml")
	cfg.Mihomo.ProfileOverlayDigest = mihomo.ProfileOverlayDigest([]byte(policyWorkspaceOverlayFixture))
	if err := writeAtomic(cfg.Mihomo.Profile, []byte(composition.ProfileYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate, base, err := workspaceCandidate(path, cfg, PolicyWorkspaceInput{Overlay: []byte(policyWorkspaceOverlayFixture)})
	if err != nil || base != nil || config.Render(candidate) != config.Render(cfg) {
		t.Fatalf("unchanged legacy composition was rewritten: candidate=%#v base=%#v err=%v", candidate.Mihomo, base, err)
	}
	if _, _, err := workspaceCandidate(path, cfg, PolicyWorkspaceInput{Overlay: []byte("schema-version: 1\nenabled: false\n")}); err == nil || !strings.Contains(err.Error(), "raw source is unavailable") {
		t.Fatalf("legacy workspace silently discarded overlay without raw source: %v", err)
	}
}

func TestWorkspaceCandidatePreservesRelativeProviderPaths(t *testing.T) {
	path, cfg := workspaceCandidateTestConfig(t)
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = filepath.Join(filepath.Dir(path), "source", "original.yaml")
	const source = `proxy-providers:
  subscription: {type: file, path: ./nodes.yaml}
proxy-groups:
  - {name: Main, type: select, use: [subscription]}
rules: ['MATCH,Main']
`
	if err := writeAtomic(cfg.Mihomo.Profile, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate, _, err := workspaceCandidate(path, cfg, PolicyWorkspaceInput{})
	if err != nil {
		t.Fatal(err)
	}
	final, err := mihomo.RenderConfig(candidate)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Providers map[string]struct {
			Path string `yaml:"path"`
		} `yaml:"proxy-providers"`
	}
	if err := yaml.Unmarshal([]byte(final), &document); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(cfg.Mihomo.Profile), "nodes.yaml")
	if got := document.Providers["subscription"].Path; got != want {
		t.Fatalf("workspace moved relative provider path from %q to %q", want, got)
	}
}

func TestWorkspaceCandidateAnchorsMaterializedProviderPathsToManagedData(t *testing.T) {
	path, cfg := workspaceCandidateTestConfig(t)
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = filepath.Join(filepath.Dir(path), "effective", "previous.yaml")
	const source = `proxy-providers:
  subscription: {type: file, path: ./nodes.yaml}
proxy-groups:
  - {name: Main, type: select, use: [subscription]}
rules: ['MATCH,Main']
`
	input := PolicyWorkspaceInput{Source: []byte(source), Overlay: []byte(policyWorkspaceOverlayFixture)}
	cfg.Mihomo.ProfileSourceDigest = mihomo.ProfileOverlayDigest(input.Source)
	candidate, _, err := workspaceCandidate(path, cfg, input)
	if err != nil {
		t.Fatal(err)
	}
	final, err := mihomo.RenderConfig(candidate)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Providers map[string]struct {
			Path string `yaml:"path"`
		} `yaml:"proxy-providers"`
	}
	if err := yaml.Unmarshal([]byte(final), &document); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(candidate.Mihomo.Profile), "nodes.yaml")
	if got := document.Providers["subscription"].Path; got != want {
		t.Fatalf("materialized provider path=%q, want root-managed work path %q", got, want)
	}
	if filepath.Dir(candidate.Mihomo.Profile) != filepath.Join(filepath.Dir(path), "data") {
		t.Fatalf("workspace profile escaped managed data directory: %q", candidate.Mihomo.Profile)
	}
}

func TestWorkspaceBaseMetadataIsOutsideWritableProviderDirectory(t *testing.T) {
	path, cfg := workspaceCandidateTestConfig(t)
	candidate, base, err := workspaceCandidate(path, cfg, PolicyWorkspaceInput{Overlay: []byte(policyWorkspaceOverlayFixture)})
	if err != nil {
		t.Fatal(err)
	}
	if err := persistPolicyWorkspaceCandidate(t.Context(), path, cfg, candidate, base); err != nil {
		t.Fatal(err)
	}
	metadataPath := policyWorkspaceBasePath(candidate)
	workDir := filepath.Dir(candidate.Mihomo.Profile)
	if filepath.Dir(metadataPath) == workDir || strings.HasPrefix(metadataPath, workDir+string(os.PathSeparator)) {
		t.Fatalf("workspace base metadata %q is writable from mihomo work directory %q", metadataPath, workDir)
	}
	// Simulate a provider whose ordinary relative cache name used to collide
	// with the flat OpenSurge metadata filename.
	if err := writeAtomic(filepath.Join(workDir, "policy-workspace-base.json"), []byte("provider cache\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var persisted policyWorkspaceBase
	if err := json.Unmarshal(data, &persisted); err != nil || !reflect.DeepEqual(persisted, *base) {
		t.Fatalf("provider cache damaged workspace metadata: metadata=%#v err=%v", persisted, err)
	}
	if _, _, err := workspaceCandidate(path, candidate, PolicyWorkspaceInput{Overlay: []byte(policyWorkspaceOverlayFixture)}); err != nil {
		t.Fatalf("provider cache collision broke repeated composition: %v", err)
	}
}

func TestWorkspaceCommitFailureRestoresBaseRecord(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires filesystem permission enforcement")
	}
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new record", true: "existing record"}[existing], func(t *testing.T) {
			path, cfg := workspaceCandidateTestConfig(t)
			path = filepath.Join(filepath.Dir(path), "desired", "config.yaml")
			original := config.Render(cfg)
			if err := writeAtomic(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			candidate, base, err := workspaceCandidate(path, cfg, PolicyWorkspaceInput{Overlay: []byte(policyWorkspaceOverlayFixture)})
			if err != nil {
				t.Fatal(err)
			}
			basePath := policyWorkspaceBasePath(cfg)
			if existing {
				if err := writeAtomic(basePath, []byte("previous recovery record"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := snapshotFile(basePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Dir(path), 0o500); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(filepath.Dir(path), 0o700)
			if err := persistPolicyWorkspaceCandidate(t.Context(), path, cfg, candidate, base); err == nil {
				t.Fatal("unwritable desired directory accepted commit")
			}
			after, err := snapshotFile(basePath)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("base record was not restored: before=%+v after=%+v err=%v", before, after, err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != original {
				t.Fatalf("failed commit changed desired: %v", err)
			}
		})
	}
}

func TestPolicyWorkspaceHTTPUsesServerInputsAndHoldsOneLease(t *testing.T) {
	server := newTestServer(t)
	runner := &fakePolicyWorkspaceRunner{response: PolicyWorkspaceResponse{SchemaVersion: SchemaVersion, Mode: "prepared", Revision: "prepared-revision", Groups: []mihomo.ProxyGroup{}}}
	server.policyWorkspaceRunner = runner
	t.Cleanup(func() { _ = server.policyWorkspaceLease.Close() })
	if err := server.store.SaveProfileOverlay([]byte(policyWorkspaceOverlayFixture)); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"action":"read"}`, `{"action":"select","group":"Main","policy":"DIRECT"}`, `{"action":"test","names":["NodeA"]}`} {
		response := performAuthorized(server, http.MethodPost, "/api/v1/policy-workspace", []byte(body))
		if response.Code != http.StatusOK {
			t.Fatalf("workspace status=%d body=%s", response.Code, response.Body.String())
		}
		var result PolicyWorkspaceResponse
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Mode != "prepared" || result.Revision != "prepared-revision" {
			t.Fatalf("workspace result=%#v err=%v", result, err)
		}
	}
	if runner.holds != 1 || len(runner.inputs) != 3 {
		t.Fatalf("workspace holds=%d actions=%d", runner.holds, len(runner.inputs))
	}
	for _, input := range runner.inputs {
		if input.Revision != fileDigest(server.configPath) || string(input.Overlay) != policyWorkspaceOverlayFixture || len(input.Source) != 0 {
			t.Fatalf("workspace did not use authoritative stopped inputs: %#v", input)
		}
	}
	if err := server.policyWorkspaceLease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.policyWorkspaceLease.Close(); err != nil || runner.closes != 1 {
		t.Fatalf("lease close=%v count=%d", err, runner.closes)
	}
	response := performAuthorized(server, http.MethodPost, "/api/v1/policy-workspace", []byte(`{"action":"read"}`))
	if response.Code != http.StatusBadGateway || len(runner.inputs) != 3 {
		t.Fatalf("closed workspace accepted new work: %d %s", response.Code, response.Body.String())
	}
}

func TestPolicyWorkspaceHTTPRejectsInvalidRequestsBeforeAcquiringLease(t *testing.T) {
	server := newTestServer(t)
	runner := &fakePolicyWorkspaceRunner{}
	server.policyWorkspaceRunner = runner
	for _, body := range []string{
		`{"action":"unsupported"}`,
		`{"action":"select","group":"open-surge/mac-global","policy":"DIRECT"}`,
		`{"action":"select","group":"Main","policy":"open-surge/mac-global"}`,
		`{"action":"select","group":"Main"}`,
		`{"action":"test","names":[]}`,
		`{"action":"read","source":"browser must not supply engine YAML"}`,
	} {
		response := performAuthorized(server, http.MethodPost, "/api/v1/policy-workspace", []byte(body))
		if response.Code < 400 || response.Code >= 500 {
			t.Fatalf("invalid request accepted: %s => %d %s", body, response.Code, response.Body.String())
		}
	}
	if runner.holds != 0 || len(runner.inputs) != 0 {
		t.Fatalf("invalid requests acquired helper lease: %#v", runner)
	}
}

func TestPolicyWorkspaceHTTPErrorsRemainErrorsAndHoldCanRetry(t *testing.T) {
	server := newTestServer(t)
	runner := &fakePolicyWorkspaceRunner{holdErr: errors.New("helper unavailable")}
	server.policyWorkspaceRunner = runner
	t.Cleanup(func() { _ = server.policyWorkspaceLease.Close() })
	response := performAuthorized(server, http.MethodPost, "/api/v1/policy-workspace", []byte(`{"action":"read"}`))
	if response.Code != http.StatusBadGateway || len(runner.inputs) != 0 {
		t.Fatalf("lease error status=%d body=%s", response.Code, response.Body.String())
	}
	runner.holdErr = nil
	runner.runErr = errors.New("prepared controller unavailable")
	response = performAuthorized(server, http.MethodPost, "/api/v1/policy-workspace", []byte(`{"action":"read"}`))
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "prepared controller unavailable") || runner.holds != 2 {
		t.Fatalf("workspace error status=%d body=%s holds=%d", response.Code, response.Body.String(), runner.holds)
	}
	if err := server.policyWorkspaceLease.Close(); err != nil || runner.closes != 1 {
		t.Fatalf("failed action leaked lease: closes=%d err=%v", runner.closes, err)
	}
}

func TestPolicyWorkspaceRunningInputsIgnoreDraftAndSource(t *testing.T) {
	server := newTestServer(t)
	cfg, err := config.LoadRuntime(server.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(runtime.NewPaths(cfg).StateFile, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.store.SaveProfileOverlay([]byte("invalid draft: [")); err != nil {
		t.Fatal(err)
	}
	input, err := server.policyWorkspaceInput(PolicyWorkspaceRequest{Action: "read"})
	if err != nil || input.ExpectedGatewayState != policyWorkspaceGatewayRunning || len(input.Overlay) != 0 || len(input.Source) != 0 {
		t.Fatalf("running policy input consumed drafts: %#v / %v", input, err)
	}
}

func TestPolicyWorkspaceRejectsGatewayStateTransitionAfterSnapshot(t *testing.T) {
	path, cfg := workspaceCandidateTestConfig(t)
	if err := writeAtomic(path, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	input := PolicyWorkspaceInput{
		Request:              PolicyWorkspaceRequest{Action: "read"},
		Revision:             fileDigest(path),
		ExpectedGatewayState: policyWorkspaceGatewayRunning,
	}
	if _, err := runPolicyWorkspace(t.Context(), path, input); err == nil || !strings.Contains(err.Error(), "gateway state changed") {
		t.Fatalf("running snapshot silently became a stopped workspace: %v", err)
	}
	if err := writeAtomic(runtime.NewPaths(cfg).StateFile, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	input.ExpectedGatewayState = policyWorkspaceGatewayStopped
	if _, err := runPolicyWorkspace(t.Context(), path, input); err == nil || !strings.Contains(err.Error(), "gateway state changed") {
		t.Fatalf("stopped snapshot silently switched to the running core: %v", err)
	}
}

func TestPolicyWorkspaceStoppedInputUsesSelectedSnapshotAndFallsBackToRootDesired(t *testing.T) {
	server := newTestServer(t)
	source, err := server.importReader("workspace-source", "mihomo_profile", "file:source.yaml", strings.NewReader(policyWorkspaceSourceFixture))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadRuntime(server.configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = filepath.Join(filepath.Dir(server.configPath), "data", "root-owned-profile.yaml")
	cfg.Mihomo.ProfileSourceDigest = source.Digest
	if err := writeAtomic(cfg.Mihomo.Profile, []byte(policyWorkspaceSourceFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(server.configPath, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.store.SaveProfileOverlay([]byte(policyWorkspaceOverlayFixture)); err != nil {
		t.Fatal(err)
	}
	input, err := server.policyWorkspaceInput(PolicyWorkspaceRequest{Action: "read"})
	if err != nil || input.ExpectedGatewayState != policyWorkspaceGatewayStopped || string(input.Source) != policyWorkspaceSourceFixture || input.Revision != fileDigest(server.configPath) {
		t.Fatalf("selected source input=%#v err=%v", input, err)
	}
	if err := writeAtomic(source.SnapshotPath, []byte("rules: ['MATCH,REJECT']\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err = server.policyWorkspaceInput(PolicyWorkspaceRequest{Action: "read"})
	if err != nil || len(input.Source) != 0 {
		t.Fatalf("changed optional snapshot blocked root desired fallback: input=%#v err=%v", input, err)
	}
	candidate, _, err := workspaceCandidate(server.configPath, cfg, input)
	if err != nil {
		t.Fatalf("root desired profile could not replace the optional source snapshot: %v", err)
	}
	final, err := mihomo.RenderConfig(candidate)
	if err != nil || workspaceTestGroups(t, final)["Added"] == nil || workspaceTestGroups(t, final)["Original"] == nil {
		t.Fatalf("root desired fallback lost composition: err=%v\n%s", err, final)
	}
}

func TestPolicyWorkspaceHTTPInvalidStoredOverlayDoesNotAcquireLease(t *testing.T) {
	server := newTestServer(t)
	runner := &fakePolicyWorkspaceRunner{}
	server.policyWorkspaceRunner = runner
	if err := server.store.SaveProfileOverlay([]byte("invalid draft: [")); err != nil {
		t.Fatal(err)
	}
	response := performAuthorized(server, http.MethodPost, "/api/v1/policy-workspace", []byte(`{"action":"read"}`))
	if response.Code != http.StatusUnprocessableEntity || runner.holds != 0 || len(runner.inputs) != 0 {
		t.Fatalf("invalid overlay launched workspace: status=%d holds=%d body=%s", response.Code, runner.holds, response.Body.String())
	}
}

func TestPolicyWorkspaceEmptyOrAbsentOverlayNormalizesToDisabledDocument(t *testing.T) {
	for _, tt := range []struct {
		name string
		data []byte
		save bool
	}{
		{name: "absent"},
		{name: "empty", data: []byte(" \n\t"), save: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newTestServer(t)
			if tt.save {
				if err := server.store.SaveProfileOverlay(tt.data); err != nil {
					t.Fatal(err)
				}
			}
			data, document, revision, err := server.loadProfileOverlay()
			if err != nil {
				t.Fatal(err)
			}
			if len(data) == 0 || revision != mihomo.ProfileOverlayDigest(data) || document.SchemaVersion != mihomo.ProfileOverlaySchemaVersion || document.Enabled {
				t.Fatalf("normalized overlay data=%q revision=%q document=%#v", data, revision, document)
			}
		})
	}
}

func TestHelperPolicyLeaseRequiresHolderAndStopsAfterLastRelease(t *testing.T) {
	_, cfg := workspaceCandidateTestConfig(t)
	var stops atomic.Int32
	manager := newHelperPolicyLeases()
	manager.stop = func(config.Config) error { stops.Add(1); return nil }
	if _, err := manager.run(cfg, func() (PolicyWorkspaceResponse, error) {
		return PolicyWorkspaceResponse{}, nil
	}); err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("workspace ran without a lease: %v", err)
	}
	manager.acquire(cfg)
	manager.acquire(cfg)
	if _, err := manager.run(cfg, func() (PolicyWorkspaceResponse, error) {
		return PolicyWorkspaceResponse{Mode: "prepared"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	manager.release(context.Background(), cfg)
	if stops.Load() != 0 {
		t.Fatal("prepared engine stopped while another policy lease remained")
	}
	manager.release(context.Background(), cfg)
	if stops.Load() != 1 {
		t.Fatalf("last policy lease did not stop the prepared engine: calls=%d", stops.Load())
	}
}

func TestHelperPolicyLeaseCleanupRetriesAfterCanceledRequest(t *testing.T) {
	_, cfg := workspaceCandidateTestConfig(t)
	var stops atomic.Int32
	manager := newHelperPolicyLeases()
	manager.stop = func(config.Config) error {
		if stops.Add(1) == 1 {
			return errors.New("gateway transition still owns lifecycle lock")
		}
		return nil
	}
	manager.acquire(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	manager.release(ctx, cfg)
	if stops.Load() != 2 {
		t.Fatalf("cleanup retries=%d, want 2", stops.Load())
	}
	if elapsed := time.Since(started); elapsed < 200*time.Millisecond {
		t.Fatalf("canceled request caused a busy cleanup retry: %s", elapsed)
	}
}

func workspaceCandidateTestConfig(t *testing.T) (string, config.Config) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Runtime.Dir = filepath.Join(dir, "runtime")
	return filepath.Join(dir, "config.yaml"), cfg
}

func workspaceTestGroups(t *testing.T, final string) map[string]map[string]any {
	t.Helper()
	document := workspaceTestDocument(t, final)
	groups := map[string]map[string]any{}
	for _, group := range document.Groups {
		groups[group["name"].(string)] = group
	}
	return groups
}

type workspaceRenderedDocument struct {
	Groups []map[string]any `yaml:"proxy-groups"`
}

func workspaceTestDocument(t *testing.T, final string) workspaceRenderedDocument {
	t.Helper()
	var document workspaceRenderedDocument
	if err := yaml.Unmarshal([]byte(final), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func namedWorkspaceEntry(entries []map[string]any, name string) bool {
	for _, entry := range entries {
		if entry["name"] == name {
			return true
		}
	}
	return false
}

func containsWorkspaceString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func persistWorkspaceBaseForTest(t *testing.T, cfg config.Config, base *policyWorkspaceBase) {
	t.Helper()
	data, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(policyWorkspaceBasePath(cfg), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustWorkspaceOverlay(t *testing.T, body string) mihomo.ProfileOverlayDocument {
	t.Helper()
	document, err := mihomo.ParseProfileOverlay([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return document
}

type fakePolicyWorkspaceRunner struct {
	inputs   []PolicyWorkspaceInput
	holds    int
	closes   int
	holdErr  error
	runErr   error
	response PolicyWorkspaceResponse
}

func (f *fakePolicyWorkspaceRunner) PolicyWorkspace(_ context.Context, _ string, input PolicyWorkspaceInput) (PolicyWorkspaceResponse, error) {
	f.inputs = append(f.inputs, input)
	return f.response, f.runErr
}

func (f *fakePolicyWorkspaceRunner) HoldPolicyWorkspace(context.Context, string) (io.Closer, error) {
	f.holds++
	if f.holdErr != nil {
		return nil, f.holdErr
	}
	return workspaceTestCloser(func() error { f.closes++; return nil }), nil
}

type workspaceTestCloser func() error

func (f workspaceTestCloser) Close() error { return f() }
