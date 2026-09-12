package qnaphost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
)

type fakeRunner struct {
	output []byte
	err    error
	calls  []string
	steps  []fakeRunnerStep
}

type fakeRunnerStep struct {
	output []byte
	err    error
}

func (f *fakeRunner) Run(_ context.Context, _ []byte, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
	if len(f.steps) > 0 {
		step := f.steps[0]
		f.steps = f.steps[1:]
		return step.output, step.err
	}
	return f.output, f.err
}

func TestDetectHostUsesMountedNetworkNamespaceRoute(t *testing.T) {
	runner := &fakeRunner{output: []byte("192.168.2.241 dev br0 src 192.168.2.240 uid 0\n")}
	manager := &Manager{netNSPath: "/run/test-host-netns", runner: runner}

	iface, source, err := manager.detectHostLocked(context.Background(), "192.168.2.241")
	if err != nil {
		t.Fatalf("detectHostLocked: %v", err)
	}
	if iface != "br0" || source != "192.168.2.240" {
		t.Fatalf("unexpected host route: iface=%q source=%q", iface, source)
	}
	if len(runner.calls) != 1 || !strings.Contains(runner.calls[0], "nsenter --net=/run/test-host-netns -- ip -4 route get 192.168.2.241") {
		t.Fatalf("unexpected nsenter call: %#v", runner.calls)
	}
}

func TestIntentPersistsOptInWithoutTouchingQTSConfig(t *testing.T) {
	manager := &Manager{statePath: filepath.Join(t.TempDir(), stateFileName), runner: &fakeRunner{}}
	manager.mu.Lock()
	if err := manager.writeIntentLocked(true); err != nil {
		manager.mu.Unlock()
		t.Fatalf("writeIntentLocked: %v", err)
	}
	enabled, err := manager.readIntentLocked()
	manager.mu.Unlock()
	if err != nil {
		t.Fatalf("readIntentLocked: %v", err)
	}
	if !enabled {
		t.Fatal("expected persisted NAS host takeover intent")
	}
}

func TestEnsurePolicySlotsFreeTreatsMissingRouteTablesAsEmpty(t *testing.T) {
	runner := &fakeRunner{steps: []fakeRunnerStep{
		{output: []byte("0: from all lookup local\n")},
		{
			output: []byte("Error: ipv4: FIB table does not exist. Dump terminated\n"),
			err:    errors.New("ip route exited with status 2"),
		},
		{output: []byte("0: from all lookup local\n20241: from all iif eth0 lookup 20241\n")},
		{
			output: []byte("Error: ipv4: FIB table does not exist. Dump terminated\n"),
			err:    errors.New("ip route exited with status 2"),
		},
	}}
	manager := &Manager{netNSPath: "/run/test-host-netns", runner: runner}

	if err := manager.ensurePolicySlotsFreeLocked(context.Background()); err != nil {
		t.Fatalf("ensurePolicySlotsFreeLocked: %v", err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("expected host/container rule and route-table inspections, got %d calls: %#v", len(runner.calls), runner.calls)
	}
	if !strings.Contains(runner.calls[2], "ip -4 rule show") || strings.Contains(runner.calls[2], "nsenter") {
		t.Fatalf("container policy inspection should stay in the container namespace: %q", runner.calls[2])
	}
}

func TestEnsurePolicySlotsFreeRejectsContainerDNSPriorityCollision(t *testing.T) {
	runner := &fakeRunner{steps: []fakeRunnerStep{
		{output: []byte("0: from all lookup local\n")},
		{output: nil},
		{output: []byte(containerDNSUDPRulePriority + ": from all lookup main\n")},
	}}
	manager := &Manager{netNSPath: "/run/test-host-netns", runner: runner}

	err := manager.ensurePolicySlotsFreeLocked(context.Background())
	if err == nil || !strings.Contains(err.Error(), containerDNSUDPRulePriority) {
		t.Fatalf("expected DNS priority collision, got %v", err)
	}
}

func TestPreflightL4PolicyRoutingUsesOnlyInertTemporaryRules(t *testing.T) {
	runner := &fakeRunner{}
	manager := &Manager{netNSPath: "/run/test-host-netns", runner: runner}

	if err := manager.preflightL4PolicyRoutingLocked(context.Background()); err != nil {
		t.Fatalf("preflightL4PolicyRoutingLocked: %v", err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("expected add/delete probes in host and container namespaces, got %#v", runner.calls)
	}
	for _, call := range runner.calls {
		if !strings.Contains(call, "from 192.0.2.1/32") || !strings.Contains(call, "ipproto udp") || !strings.Contains(call, "dport 65535") || !strings.Contains(call, "table main") {
			t.Fatalf("probe is not tightly scoped and inert: %q", call)
		}
	}
	if !strings.Contains(runner.calls[0], "nsenter --net=/run/test-host-netns") || strings.Contains(runner.calls[2], "nsenter") {
		t.Fatalf("expected first probe in host namespace and second in container namespace: %#v", runner.calls)
	}
}

func TestPreflightL4PolicyRoutingFailsBeforePersistentRoutingWhenKernelRejectsDPort(t *testing.T) {
	runner := &fakeRunner{steps: []fakeRunnerStep{{err: errors.New("RTNETLINK answers: Invalid argument")}}}
	manager := &Manager{netNSPath: "/run/test-host-netns", runner: runner}

	err := manager.preflightL4PolicyRoutingLocked(context.Background())
	if err == nil || !strings.Contains(err.Error(), "L4 policy routing") {
		t.Fatalf("expected clear L4 capability error, got %v", err)
	}
	if len(runner.calls) != 1 || strings.Contains(runner.calls[0], "route replace") {
		t.Fatalf("preflight unexpectedly changed persistent routing: %#v", runner.calls)
	}
}

func TestInstallDNSPolicyUsesHost20242AndContainerTUNTableWithoutNFTables(t *testing.T) {
	runner := &fakeRunner{}
	manager := &Manager{netNSPath: "/run/test-host-netns", runner: runner}
	cfg := configForDNSPolicyTest()

	if err := manager.installContainerDNSPolicyLocked(context.Background(), cfg, "192.168.2.240"); err != nil {
		t.Fatalf("installContainerDNSPolicyLocked: %v", err)
	}
	if err := manager.installHostDNSPolicyLocked(context.Background()); err != nil {
		t.Fatalf("installHostDNSPolicyLocked: %v", err)
	}

	joined := strings.Join(runner.calls, "\n")
	if strings.Contains(joined, " nft ") || strings.Contains(joined, "iptables") {
		t.Fatalf("DNS policy unexpectedly depends on a firewall backend:\n%s", joined)
	}
	for _, want := range []string{
		"ip -4 route replace default dev tun0 table " + containerDNSRouteTableID + " proto " + containerDNSRouteProtocol,
		"ip -4 rule add pref " + containerDNSUDPRulePriority + " from 192.168.2.240/32 iif eth0 ipproto udp dport 53 table " + containerDNSRouteTableID,
		"ip -4 rule add pref " + containerDNSTCPRulePriority + " from 192.168.2.240/32 iif eth0 ipproto tcp dport 53 table " + containerDNSRouteTableID,
		"nsenter --net=/run/test-host-netns -- ip -4 rule add pref " + hostDNSUDPRulePriority + " iif lo ipproto udp dport 53 table " + routeTableID,
		"nsenter --net=/run/test-host-netns -- ip -4 rule add pref " + hostDNSTCPRulePriority + " iif lo ipproto tcp dport 53 table " + routeTableID,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing command %q in:\n%s", want, joined)
		}
	}
}

func TestDisableHostTakeoverDoesNotInvokeNFTables(t *testing.T) {
	netNS := filepath.Join(t.TempDir(), "host-netns")
	if err := os.WriteFile(netNS, []byte("test"), 0o600); err != nil {
		t.Fatalf("create fake host netns mount: %v", err)
	}
	runner := &fakeRunner{}
	manager := &Manager{netNSPath: netNS, runner: runner}

	if err := manager.disableLocked(context.Background()); err != nil {
		t.Fatalf("disableLocked: %v", err)
	}
	joined := strings.Join(runner.calls, "\n")
	if strings.Contains(joined, "nft") || strings.Contains(joined, "iptables") {
		t.Fatalf("disable path must not depend on firewall tooling:\n%s", joined)
	}
	for _, want := range []string{hostDNSUDPRulePriority, hostDNSTCPRulePriority, proxyRulePriority, mainRulePriority, containerDNSUDPRulePriority, containerDNSTCPRulePriority} {
		if !strings.Contains(joined, "pref "+want) {
			t.Fatalf("expected scoped cleanup for priority %s:\n%s", want, joined)
		}
	}
}

func TestRuleLineContainsRequiresPriorityAndAllFragments(t *testing.T) {
	output := []byte("24090: from 192.168.2.240 iif lo ipproto udp dport 53 lookup 20242\n")
	if !ruleLineContains(output, "24090", "ipproto udp", "dport 53", "lookup 20242") {
		t.Fatal("expected rule to match")
	}
	if ruleLineContains(output, "24091", "ipproto udp") || ruleLineContains(output, "24090", "ipproto tcp") {
		t.Fatal("unexpected partial rule match")
	}
}

func configForDNSPolicyTest() config.Config {
	var cfg config.Config
	cfg.Gateway.Interface = "eth0"
	cfg.Transparent.TUNDevice = "tun0"
	return cfg
}

func TestRemoteManagementCannotMutateHostNetworkNamespace(t *testing.T) {
	manager := &Manager{}
	nextCalled := false
	handler := manager.Handler("internal-token", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	}))

	req := httptest.NewRequest(http.MethodPut, Endpoint, strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Authorization", "Bearer internal-token")
	req.Header.Set("X-OpenSurge-Remote-Management", "token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusNotFound)
	}
	if nextCalled {
		t.Fatal("remote management request reached the next handler")
	}
}

func TestHostRoutingEndpointRequiresInternalControlToken(t *testing.T) {
	manager := &Manager{}
	handler := manager.Handler("internal-token", http.NotFoundHandler())
	req := httptest.NewRequest(http.MethodGet, Endpoint, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}
