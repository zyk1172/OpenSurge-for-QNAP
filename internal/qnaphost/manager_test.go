package qnaphost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
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

func TestEnsurePolicySlotsFreeTreatsMissingRouteTableAsEmpty(t *testing.T) {
	runner := &fakeRunner{steps: []fakeRunnerStep{
		{output: []byte("0: from all lookup local\n")},
		{
			output: []byte("Error: ipv4: FIB table does not exist. Dump terminated\n"),
			err:    errors.New("ip route exited with status 2"),
		},
	}}
	manager := &Manager{runner: runner}

	if err := manager.ensurePolicySlotsFreeLocked(context.Background()); err != nil {
		t.Fatalf("ensurePolicySlotsFreeLocked: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected rule and route-table inspections, got %d calls: %#v", len(runner.calls), runner.calls)
	}
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
