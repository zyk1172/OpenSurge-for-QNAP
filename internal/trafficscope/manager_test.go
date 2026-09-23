package trafficscope

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls []string
}

func (f *fakeRunner) Run(_ context.Context, _ []byte, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
	return nil, nil
}

func TestNormalizeSelectorsAcceptsBridgeTupleAndRejectsDuplicates(t *testing.T) {
	selectors, err := normalizeSelectors([]Selector{{
		ID: "openlist",
		Label: "OpenList",
		Enabled: true,
		Type: selectorTypeSourceIIF,
		SourceIPv4: "10.0.3.6",
		IngressInterface: "lxcbr0",
	}})
	if err != nil {
		t.Fatalf("normalizeSelectors: %v", err)
	}
	if got := selectors[0].SourceIPv4; got != "10.0.3.6" {
		t.Fatalf("source IPv4 = %q", got)
	}
	_, err = normalizeSelectors([]Selector{
		{ID: "a", Type: selectorTypeSourceIIF, SourceIPv4: "10.0.3.6", IngressInterface: "lxcbr0"},
		{ID: "b", Type: selectorTypeSourceIIF, SourceIPv4: "10.0.3.6", IngressInterface: "lxcbr0"},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate traffic selector") {
		t.Fatalf("duplicate tuple error = %v", err)
	}
}

func TestNormalizeSelectorsRejectsLoopbackAndHostStyleIngress(t *testing.T) {
	for _, selector := range []Selector{
		{ID: "loopback-source", Type: selectorTypeSourceIIF, SourceIPv4: "127.0.0.1", IngressInterface: "lxcbr0"},
		{ID: "loopback-iif", Type: selectorTypeSourceIIF, SourceIPv4: "10.0.3.6", IngressInterface: "lo"},
	} {
		if _, err := normalizeSelectors([]Selector{selector}); err == nil {
			t.Fatalf("expected selector %+v to be rejected", selector)
		}
	}
}

func TestAllocatePriorityPairAvoidsExistingRules(t *testing.T) {
	used := usedPriorities([]byte("0: from all lookup local\n19995: from all lookup 52\n32766: from all lookup main\n"))
	main, proxy, err := allocatePriorityPair(used)
	if err != nil {
		t.Fatal(err)
	}
	if main == 19994 || proxy == 19995 {
		t.Fatalf("allocator collided with existing priority: main=%d proxy=%d", main, proxy)
	}
	if main >= proxy || used[main] != true || used[proxy] != true {
		t.Fatalf("unexpected allocated pair main=%d proxy=%d", main, proxy)
	}
}

func TestDisableUsesOnlyExactOwnedRulesAndRouteProtocol(t *testing.T) {
	netNS := filepath.Join(t.TempDir(), "host-netns")
	if err := os.WriteFile(netNS, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	manager := &Manager{netNSPath: netNS, runner: runner}
	selectors := []Selector{{
		ID: "openlist",
		SourceIPv4: "10.0.3.6",
		IngressInterface: "lxcbr0",
		MainPriority: 19992,
		ProxyPriority: 19993,
	}}
	if err := manager.disableLocked(context.Background(), selectors); err != nil {
		t.Fatalf("disableLocked: %v", err)
	}
	joined := strings.Join(runner.calls, "\n")
	for _, want := range []string{
		"pref 19992 from 10.0.3.6/32 iif lxcbr0 table main suppress_prefixlength 0",
		"pref 19993 from 10.0.3.6/32 iif lxcbr0 table " + routeTableID,
		"route flush table " + routeTableID + " proto " + routeProtocol,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing cleanup %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "nft") || strings.Contains(joined, "iptables") || strings.Contains(joined, "rule flush") {
		t.Fatalf("traffic-scope cleanup must stay ownership-scoped:\n%s", joined)
	}
}

func TestStatePersistsCleanupCoordinates(t *testing.T) {
	path := filepath.Join(t.TempDir(), stateFileName)
	manager := &Manager{statePath: path}
	want := Selector{
		ID: "immich",
		Label: "Immich",
		Enabled: true,
		Type: selectorTypeSourceIIF,
		SourceIPv4: "172.29.8.5",
		IngressInterface: "br-1cbbf626b32b",
		MainPriority: 19990,
		ProxyPriority: 19991,
	}
	if err := manager.writeStateLocked(persistedState{Selectors: []Selector{want}}); err != nil {
		t.Fatal(err)
	}
	got, err := manager.readStateLocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Selectors) != 1 || got.Selectors[0].MainPriority != want.MainPriority || got.Selectors[0].ProxyPriority != want.ProxyPriority {
		t.Fatalf("persisted selector = %+v", got.Selectors)
	}
}
