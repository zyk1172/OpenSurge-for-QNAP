package controlapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/gateway"
	"open-mihomo-gateway/internal/mihomo"
	"open-mihomo-gateway/internal/process"
	"open-mihomo-gateway/internal/runtime"
)

// This gate proves the source-free product path against a real mihomo core.
// It starts only the prepared authenticated loopback controller: no gateway
// Manager, DHCP/DNS listener, TUN, pf, forwarding or Tailscale identity.
func TestPolicyWorkspaceOverlayOnlyRealCore(t *testing.T) {
	binary := os.Getenv("OMG_PREPARED_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("set OMG_PREPARED_MIHOMO_BINARY to run the source-free prepared workspace gate")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := config.Default()
	cfg.DHCP.Enabled = false
	cfg.Runtime.Dir = filepath.Join(dir, "runtime")
	cfg.Mihomo.Binary = binary
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	if err := writeAtomic(path, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	const originalBase = `{"effective_path":"unused-previous-profile"}`
	if err := writeAtomic(policyWorkspaceBasePath(cfg), []byte(originalBase), 0o600); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := mihomo.StopPrepared(cfg); err != nil {
			t.Errorf("stop source-free prepared core: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	input := PolicyWorkspaceInput{
		Request:              PolicyWorkspaceRequest{Action: "read"},
		Revision:             fileDigest(path),
		ExpectedGatewayState: policyWorkspaceGatewayStopped,
		Overlay:              []byte(strings.ReplaceAll(policyWorkspaceOverlayFixture, "{name: Overlay-Node, type: direct}", "{name: Overlay-Node, type: socks5, server: 127.0.0.1, port: 1}")),
	}
	workspace, err := runPolicyWorkspace(ctx, path, input)
	if err != nil {
		t.Fatal(err)
	}
	group := findWorkspaceGroup(workspace.Groups, "Added")
	if group == nil || !containsWorkspaceString(group.Options, "Overlay-Node") || !containsWorkspaceString(group.Options, "DIRECT") {
		t.Fatalf("source-free selector is unavailable: %+v", workspace.Groups)
	}
	if !workspaceProxyProbeable(workspace, "Overlay-Node") {
		t.Fatalf("source-free node is not testable: %+v", workspace.Health.Proxies)
	}

	input.Request = PolicyWorkspaceRequest{Action: "select", Group: "Added", Policy: "DIRECT"}
	input.Revision = workspace.Revision
	workspace, err = runPolicyWorkspace(ctx, path, input)
	if err != nil {
		t.Fatal(err)
	}
	group = findWorkspaceGroup(workspace.Groups, "Added")
	if group == nil || group.Selected != "DIRECT" {
		t.Fatalf("source-free selector did not change: %+v", group)
	}

	// Every stopped action leaves desired and recovery records untouched. Probe
	// the fixture's closed loopback proxy; reachability is immaterial here.
	input.Request = PolicyWorkspaceRequest{Action: "test", Names: []string{"Overlay-Node"}}
	if _, err := runPolicyWorkspace(ctx, path, input); err != nil {
		t.Fatal(err)
	}
	if err := mihomo.StopPrepared(cfg); err != nil {
		t.Fatal(err)
	}
	input.Request = PolicyWorkspaceRequest{Action: "read"}
	workspace, err = runPolicyWorkspace(ctx, path, input)
	if err != nil {
		t.Fatal(err)
	}
	if group := findWorkspaceGroup(workspace.Groups, "Added"); group == nil || group.Selected != "DIRECT" {
		t.Fatalf("prepared restart lost native selection cache: %+v", group)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != config.Render(cfg) {
		t.Fatalf("preview changed desired: %v\n%s", err, data)
	}
	if data, err := os.ReadFile(policyWorkspaceBasePath(cfg)); err != nil || string(data) != originalBase {
		t.Fatalf("preview changed base recovery metadata: %v", err)
	}
	if _, exists, err := runtime.LoadState(runtime.NewPaths(cfg).StateFile); err != nil || exists {
		t.Fatalf("prepared workspace created gateway state: exists=%t err=%v", exists, err)
	}
}

func TestPolicyWorkspaceMissingDeviceEgressRealCore(t *testing.T) {
	binary := os.Getenv("OMG_PREPARED_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("set OMG_PREPARED_MIHOMO_BINARY for the real prepared-core egress fallback gate")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DHCP.Enabled = false
	cfg.Runtime.Dir = filepath.Join(dir, "runtime")
	cfg.Mihomo.Binary, cfg.Mihomo.Config = binary, filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	cfg.DevicePolicy.File = filepath.Join(dir, "devices.json")
	path := filepath.Join(dir, "config.yaml")
	policy := `{"devices":[{"id":"phone","mac":"aa:bb:cc:dd:ee:01","ipv4":"192.168.50.101","profile":"home","egress_mode":"dedicated"}],"profiles":[{"id":"home","default_policies":["Surviving","Vanishing"],"rules":[{"id":"ruleset","match":{"rule_sets":["media"]},"policies":["Surviving","Vanishing"]},{"id":"template","match":{"template":"media"},"action":"Vanishing"}]}],"rule_sets":[{"id":"media","behavior":"domain","payload":["media.example"]}],"templates":[{"id":"media","rule_sets":["media"]}]}`
	if err := writeAtomic(cfg.DevicePolicy.File, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := mihomo.StopPrepared(cfg); err != nil {
			t.Errorf("stop prepared core: %v", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	full := "schema-version: 1\nenabled: true\nproxy-groups:\n  add:\n    - {name: Surviving, type: select, proxies: [DIRECT]}\n    - {name: Vanishing, type: select, proxies: [DIRECT]}\n"
	input := PolicyWorkspaceInput{Request: PolicyWorkspaceRequest{Action: "read"}, Revision: fileDigest(path), ExpectedGatewayState: policyWorkspaceGatewayStopped, Overlay: []byte(full)}
	if _, err := runPolicyWorkspace(ctx, path, input); err != nil {
		t.Fatal(err)
	}
	for _, slot := range []string{"default", "ruleset"} {
		input.Request = PolicyWorkspaceRequest{Action: "select", Group: "device/phone/" + slot, Policy: "Vanishing"}
		if _, err := runPolicyWorkspace(ctx, path, input); err != nil {
			t.Fatal(err)
		}
	}
	input.Request = PolicyWorkspaceRequest{Action: "read"}
	input.Overlay = []byte(strings.ReplaceAll(full, "    - {name: Vanishing, type: select, proxies: [DIRECT]}\n", ""))
	for range 3 {
		response, err := runPolicyWorkspace(ctx, path, input)
		if err != nil {
			t.Fatal(err)
		}
		for _, slot := range []string{"default", "ruleset"} {
			if group := findWorkspaceGroup(response.Groups, "device/phone/"+slot); group != nil {
				t.Fatalf("omitted selector reactivated on preview: %+v", group)
			}
		}
	}
	input.Overlay = []byte(full)
	response, err := runPolicyWorkspace(ctx, path, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range []string{"default", "ruleset"} {
		group := findWorkspaceGroup(response.Groups, "device/phone/"+slot)
		if group == nil || group.Selected != "Vanishing" {
			t.Fatalf("restored %s selection = %+v", slot, group)
		}
	}
	after, err := os.ReadFile(cfg.DevicePolicy.File)
	if err != nil || string(after) != policy {
		t.Fatal("workspace rewrote original device settings")
	}
}

// This gate exercises the Web GUI's no-Policies-visit startup transaction up
// to (but deliberately excluding) gateway.Manager network takeover. A real
// mihomo binary must accept the source-free candidate before it is persisted
// and handed to the locked starter.
func TestStartPolicyWorkspaceOverlayOnlyRealValidation(t *testing.T) {
	binary := os.Getenv("OMG_PREPARED_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("set OMG_PREPARED_MIHOMO_BINARY to run the source-free direct-start validation gate")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := config.Default()
	cfg.DHCP.Enabled = false
	cfg.Runtime.Dir = filepath.Join(dir, "runtime")
	cfg.Mihomo.Binary = binary
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	if err := writeAtomic(path, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	started := false
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = startPolicyWorkspace(ctx, path, PolicyWorkspaceInput{
		Request:              PolicyWorkspaceRequest{Action: "read"},
		Revision:             fileDigest(path),
		ExpectedGatewayState: policyWorkspaceGatewayStopped,
		Overlay:              []byte(policyWorkspaceOverlayFixture),
	}, policyWorkspaceStartDeps{
		geteuid: func() int { return 0 },
		startLocked: func(ctx context.Context, candidate config.Config, commit func() error) error {
			if err := mihomo.StopPreparedLocked(candidate); err != nil {
				return err
			}
			paths := runtime.NewPaths(candidate)
			if err := runtime.Ensure(paths); err != nil {
				return err
			}
			manager := mihomo.New(candidate, paths)
			if err := manager.WriteConfig(); err != nil {
				return err
			}
			if err := manager.ValidateWrittenConfigContext(ctx); err != nil {
				return err
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != config.Render(cfg) {
				t.Fatalf("desired changed before final validation: %v", err)
			}
			if err := commit(); err != nil {
				return err
			}
			started = true
			final, err := mihomo.RenderConfig(candidate)
			if err != nil {
				return err
			}
			if workspaceTestGroups(t, final)["Added"] == nil || !strings.Contains(final, "DOMAIN,overlay.example,Added") {
				return fmt.Errorf("validated direct-start candidate lost source-free overlay")
			}
			if _, exists, err := runtime.LoadState(runtime.NewPaths(candidate).StateFile); err != nil || exists {
				return fmt.Errorf("validation-only direct start created gateway state: exists=%t err=%v", exists, err)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("validated candidate was not handed to the locked gateway starter")
	}
	desired, err := config.Load(path)
	if err != nil || desired.Mihomo.ProfileOverlayDigest != mihomo.ProfileOverlayDigest([]byte(policyWorkspaceOverlayFixture)) {
		t.Fatalf("validated source-free candidate was not persisted: cfg=%#v err=%v", desired.Mihomo, err)
	}
}

func TestPolicyWorkspaceMaterializedHTTPProvidersRealCore(t *testing.T) {
	binary := os.Getenv("OMG_PREPARED_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("set OMG_PREPARED_MIHOMO_BINARY to run the materialized provider workspace gate")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/proxies.yaml":
			_, _ = io.WriteString(w, "proxies:\n  - {name: provider-node, type: direct}\n")
		case "/rules.yaml":
			_, _ = io.WriteString(w, "payload:\n  - DOMAIN,provider.example\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := config.Default()
	cfg.DHCP.Enabled = false
	cfg.Runtime.Dir = filepath.Join(dir, "runtime")
	cfg.Mihomo.Binary = binary
	cfg.Mihomo.Config = filepath.Join(cfg.Runtime.Dir, "mihomo.yaml")
	cfg.Mihomo.ProfileMode = config.MihomoProfileModeImported
	cfg.Mihomo.Profile = filepath.Join(dir, "selected-source", "profile.yaml")
	source := fmt.Sprintf(`proxy-providers:
  subscription:
    type: http
    url: %q
    path: ./proxy-provider-cache.yaml
    interval: 3600
    health-check: {enable: false}
proxy-groups:
  - {name: ProviderGroup, type: select, use: [subscription]}
rule-providers:
  custom:
    type: http
    behavior: classical
    format: yaml
    url: %q
    path: ./rule-provider-cache.yaml
    interval: 3600
rules:
  - RULE-SET,custom,ProviderGroup
  - MATCH,DIRECT
`, origin.URL+"/proxies.yaml", origin.URL+"/rules.yaml")
	cfg.Mihomo.ProfileSourceDigest = mihomo.ProfileOverlayDigest([]byte(source))
	if err := writeAtomic(cfg.Mihomo.Profile, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := mihomo.StopPrepared(cfg); err != nil {
			t.Errorf("stop provider prepared core: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	workspace, err := runPolicyWorkspace(ctx, path, PolicyWorkspaceInput{
		Request:              PolicyWorkspaceRequest{Action: "read"},
		Revision:             fileDigest(path),
		ExpectedGatewayState: policyWorkspaceGatewayStopped,
		Source:               []byte(source),
		Overlay:              []byte("schema-version: 1\nenabled: true\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	group := findWorkspaceGroup(workspace.Groups, "ProviderGroup")
	providerDeadline := time.Now().Add(5 * time.Second)
	for (group == nil || !containsWorkspaceString(group.Options, "provider-node")) && time.Now().Before(providerDeadline) {
		time.Sleep(100 * time.Millisecond)
		workspace, err = runPolicyWorkspace(ctx, path, PolicyWorkspaceInput{
			Request:              PolicyWorkspaceRequest{Action: "read"},
			Revision:             workspace.Revision,
			ExpectedGatewayState: policyWorkspaceGatewayStopped,
			Source:               []byte(source),
			Overlay:              []byte("schema-version: 1\nenabled: true\n"),
		})
		if err != nil {
			t.Fatal(err)
		}
		group = findWorkspaceGroup(workspace.Groups, "ProviderGroup")
	}
	if group == nil || !containsWorkspaceString(group.Options, "provider-node") {
		logData, _ := os.ReadFile(filepath.Join(cfg.Runtime.Dir, "prepared-mihomo", "mihomo.log"))
		t.Fatalf("materialized proxy provider was not expanded: %+v\nprepared log:\n%s", workspace.Groups, logData)
	}
	desired, _, err := workspaceCandidate(path, cfg, PolicyWorkspaceInput{Source: []byte(source), Overlay: []byte("schema-version: 1\nenabled: true\n")})
	if err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(dir, "data")
	if filepath.Dir(desired.Mihomo.Profile) != workDir {
		t.Fatalf("materialized profile work directory=%q, want %q", filepath.Dir(desired.Mihomo.Profile), workDir)
	}
	final, err := mihomo.RenderConfig(desired)
	if err != nil {
		t.Fatal(err)
	}
	var renderedProviders struct {
		ProxyProviders map[string]struct {
			Path string `yaml:"path"`
		} `yaml:"proxy-providers"`
		RuleProviders map[string]struct {
			Path string `yaml:"path"`
		} `yaml:"rule-providers"`
	}
	if err := yaml.Unmarshal([]byte(final), &renderedProviders); err != nil {
		t.Fatal(err)
	}
	for name, cachePath := range map[string]string{
		"subscription": renderedProviders.ProxyProviders["subscription"].Path,
		"custom":       renderedProviders.RuleProviders["custom"].Path,
	} {
		if filepath.Dir(cachePath) != workDir {
			t.Fatalf("provider %q cache left managed work dir: %q", name, cachePath)
		}
		if _, err := os.Stat(cachePath); err != nil {
			t.Fatalf("provider %q cache was not materialized: %v", name, err)
		}
	}
	prepared, exists, err := mihomo.LoadPrepared(desired)
	if err != nil || !exists {
		t.Fatalf("prepared provider core state: exists=%t err=%v", exists, err)
	}
	providers, err := mihomo.FetchProviders(ctx, mihomo.PreparedConfig(desired, prepared))
	if err != nil {
		t.Fatal(err)
	}
	foundRuleProvider := false
	for _, provider := range providers.RuleProviders {
		if provider.Name == "custom" && provider.RuleCount == 1 {
			foundRuleProvider = true
		}
	}
	if !foundRuleProvider {
		t.Fatalf("materialized rule provider did not load: %+v", providers.RuleProviders)
	}
	if _, err := os.Stat(policyWorkspaceBasePath(desired)); !os.IsNotExist(err) {
		t.Fatalf("provider preview wrote base metadata: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != config.Render(cfg) {
		t.Fatalf("provider preview replaced desired: %v", err)
	}
	if strings.Contains(final, filepath.Join(dir, "selected-source")) {
		t.Fatalf("final provider paths escaped the managed mihomo work directory:\n%s", final)
	}
	if _, exists, err := runtime.LoadState(runtime.NewPaths(desired).StateFile); err != nil || exists {
		t.Fatalf("provider workspace created gateway state: exists=%t err=%v", exists, err)
	}
}

// The existing imported-egress Lab runner owns fixtures, client traffic and
// cleanup. This opt-in test replaces only its CLI start with the App's actual
// prepared-workspace handoff; a successful test deliberately leaves it running.
func TestPolicyWorkspaceLabStartupHandoff(t *testing.T) {
	path := os.Getenv("OMG_POLICY_WORKSPACE_LAB_CONFIG")
	if path == "" {
		t.Skip("run make lab-test-policy-workspace for real gateway takeover")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 || !filepath.IsAbs(path) || filepath.Dir(path) != cfg.Runtime.Dir ||
		cfg.Transparent.NFTTableName != config.DefaultNFTTableName || cfg.Transparent.Mode != "tun" ||
		cfg.Gateway.Mode != config.GatewayModeIsolatedLAN {
		t.Fatal("requires root and the isolated TUN Lab configuration")
	}
	if _, exists, err := runtime.LoadState(runtime.NewPaths(cfg).StateFile); err != nil || exists {
		t.Fatalf("Lab must be stopped: exists=%t err=%v", exists, err)
	}

	// Reuse the runner's controlled HTTP provider, DNS and domain rules as an
	// overlay. No imported source or Tailscale configuration remains selected.
	profile, err := os.ReadFile(cfg.Mihomo.Profile)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Providers map[string]map[string]any `yaml:"proxy-providers"`
		Groups    []map[string]any          `yaml:"proxy-groups"`
		DNS       map[string]any            `yaml:"dns"`
		Rules     []string                  `yaml:"rules"`
	}
	if err := yaml.Unmarshal(profile, &fixture); err != nil {
		t.Fatal(err)
	}
	overlay := mihomo.DefaultProfileOverlayDocument()
	overlay.Enabled = true
	overlay.ProxyProviders.Add, overlay.ProxyGroups.Add = fixture.Providers, fixture.Groups
	overlay.DNS.Merge = fixture.DNS
	for _, rule := range fixture.Rules {
		if !strings.HasPrefix(rule, "MATCH,") {
			overlay.Rules.Prepend = append(overlay.Rules.Prepend, rule)
		}
	}
	payload, err := mihomo.RenderProfileOverlay(overlay)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Mihomo.ProfileMode, cfg.Mihomo.Profile = config.MihomoProfileModeManaged, ""
	cfg.Mihomo.ProfileSourceDigest, cfg.Mihomo.ProfileOverlayDigest = "", ""
	cfg.UpstreamProxy.Enabled = false
	if cfg.Tailscale.Enabled {
		t.Fatal("this gate requires the source-free fixture without Tailscale")
	}
	if err := writeAtomic(path, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeBase, err := snapshotFile(policyWorkspaceBasePath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	input := PolicyWorkspaceInput{
		Request:              PolicyWorkspaceRequest{Action: "read"},
		Revision:             fileDigest(path),
		ExpectedGatewayState: policyWorkspaceGatewayStopped,
		Overlay:              payload,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for deadline := time.Now().Add(10 * time.Second); ; {
		workspace, err := runPolicyWorkspace(ctx, path, input)
		if err != nil {
			t.Fatal(err)
		}
		group := findWorkspaceGroup(workspace.Groups, "TunEgress")
		if group != nil && containsWorkspaceString(group.Options, "egress-proxy") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("controlled HTTP provider did not become available in preview")
		}
		time.Sleep(100 * time.Millisecond)
	}
	input.Request = PolicyWorkspaceRequest{Action: "select", Group: "TunEgress", Policy: "egress-proxy"}
	if _, err := runPolicyWorkspace(ctx, path, input); err != nil {
		t.Fatal(err)
	}
	afterBase, err := snapshotFile(policyWorkspaceBasePath(cfg))
	if err != nil || !reflect.DeepEqual(beforeBase, afterBase) || input.Revision != fileDigest(path) {
		t.Fatalf("preview or selection changed desired/base recovery metadata: %v", err)
	}
	prepared, exists, err := mihomo.LoadPrepared(cfg)
	if err != nil || !exists {
		t.Fatalf("prepared engine ownership is missing: %v", err)
	}
	t.Log("preview and selection preserved desired/base; provider selection saved in native cache")

	validationCount := 0
	ctx = gateway.WithProgress(ctx, func(progress gateway.Progress) {
		if progress.Phase == "validating_config" {
			validationCount++
		}
	})
	input.Request = PolicyWorkspaceRequest{Action: "read"}
	if err := (DirectRunner{}).StartPolicyWorkspace(ctx, path, input); err != nil {
		t.Fatal(err)
	}
	desired, err := config.Load(path)
	if err != nil || desired.Mihomo.ProfileOverlayDigest != mihomo.ProfileOverlayDigest(payload) || validationCount != 1 {
		t.Fatalf("App start did not validate and commit the overlay once: validations=%d err=%v", validationCount, err)
	}
	if alive, err := process.MatchesFingerprint(prepared.PID, prepared.ProcessFingerprint); err != nil || alive {
		t.Fatalf("prepared engine survived gateway handoff: alive=%t err=%v", alive, err)
	}
	if _, exists, err := mihomo.LoadPrepared(desired); err != nil || exists {
		t.Fatalf("prepared ownership record survived gateway handoff: %v", err)
	}
	if filepath.Dir(desired.Mihomo.Profile) != prepared.ConfigDirectory {
		t.Fatal("gateway changed the prepared engine's working/cache directory")
	}
	groups, err := mihomo.FetchProxyGroups(ctx, desired)
	if err != nil {
		t.Fatal(err)
	}
	if group := findWorkspaceGroup(groups, "TunEgress"); group == nil || group.Selected != "egress-proxy" {
		t.Fatalf("gateway did not preserve the prepared provider selection: %+v", group)
	}
	t.Log("App start committed the validated overlay; prepared engine exited; cache and selection survived")
	// The existing runner now proves DIRECT -> provider traffic and stop cleanup.
	if err := mihomo.SelectProxyGroup(ctx, desired, "TunEgress", "DIRECT"); err != nil {
		t.Fatal(err)
	}
}

func findWorkspaceGroup(groups []mihomo.ProxyGroup, name string) *mihomo.ProxyGroup {
	for index := range groups {
		if groups[index].Name == name {
			return &groups[index]
		}
	}
	return nil
}

func workspaceProxyProbeable(workspace PolicyWorkspaceResponse, name string) bool {
	for _, proxy := range workspace.Health.Proxies {
		if proxy.Name == name {
			return proxy.Probeable
		}
	}
	return false
}
