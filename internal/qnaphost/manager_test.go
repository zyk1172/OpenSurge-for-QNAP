package qnaphost

import (
	"context"
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
}

func (f *fakeRunner) Run(_ context.Context, _ []byte, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
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
