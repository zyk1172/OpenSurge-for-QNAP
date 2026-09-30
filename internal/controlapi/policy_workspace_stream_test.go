package controlapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"open-mihomo-gateway/internal/mihomo"
)

func TestPolicyWorkspaceReportsFinishedNodesBeforeSlowNodes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	notified := make(chan mihomo.ProxyDelayResult, 16)
	ctx = withPolicyWorkspaceResults(ctx, func(result mihomo.ProxyDelayResult) { notified <- result })
	names := []string{"slow", "fast", "3", "4", "5", "6", "7", "8", "9"}
	var active, peak atomic.Int32
	done := make(chan []mihomo.ProxyDelayResult, 1)
	go func() {
		done <- measurePolicyWorkspaceNodes(ctx, names, func(ctx context.Context, name string) mihomo.ProxyDelayResult {
			n := active.Add(1)
			defer active.Add(-1)
			for previous := peak.Load(); n > previous && !peak.CompareAndSwap(previous, n); previous = peak.Load() {
			}
			if name != "fast" {
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return mihomo.ProxyDelayResult{Name: name, Status: "reachable", DelayMS: 42}
		})
	}()
	select {
	case result := <-notified:
		if result.Name != "fast" || result.DelayMS != 42 {
			t.Fatalf("first result = %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("fast result waited for slow nodes")
	}
	select {
	case <-done:
		t.Fatal("batch finished before slow node was released")
	default:
	}
	close(release)
	select {
	case results := <-done:
		for index, result := range results {
			if result.Name != names[index] {
				t.Fatalf("final order = %+v", results)
			}
		}
	case <-ctx.Done():
		t.Fatal("workers did not finish")
	}
	if peak.Load() > proxyHealthConcurrency {
		t.Fatalf("concurrency = %d", peak.Load())
	}
	if len(notified) != len(names)-1 {
		t.Fatalf("missing node results: %d", len(notified))
	}
}

func TestPolicyWorkspaceCancellationStopsQueuedProbes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 12)
	done := make(chan struct{})
	go func() {
		measurePolicyWorkspaceNodes(ctx, make([]string, 12), func(ctx context.Context, _ string) mihomo.ProxyDelayResult {
			started <- struct{}{}
			<-ctx.Done()
			return mihomo.ProxyDelayResult{}
		})
		close(done)
	}()
	for range proxyHealthConcurrency {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("workers did not start")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workers did not cancel")
	}
	if len(started) != 0 {
		t.Fatal("queued probes started after cancellation")
	}
}

type streamingWorkspaceRunner struct {
	run func(context.Context) (PolicyWorkspaceResponse, error)
}

func (f streamingWorkspaceRunner) PolicyWorkspace(ctx context.Context, _ string, _ PolicyWorkspaceInput) (PolicyWorkspaceResponse, error) {
	return f.run(ctx)
}
func (streamingWorkspaceRunner) HoldPolicyWorkspace(context.Context, string) (io.Closer, error) {
	return workspaceTestCloser(func() error { return nil }), nil
}

func TestPolicyWorkspaceHTTPFlushesNodeResultsBeforeCompletion(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			server := newTestServer(t)
			defer server.policyWorkspaceLease.Close()
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			server.policyWorkspaceRunner = streamingWorkspaceRunner{run: func(ctx context.Context) (PolicyWorkspaceResponse, error) {
				report := policyWorkspaceResultReporter(ctx)
				if report == nil {
					return PolicyWorkspaceResponse{}, errors.New("missing result observer")
				}
				report(mihomo.ProxyDelayResult{Name: "fast", Status: "reachable", DelayMS: 42})
				select {
				case <-release:
				case <-ctx.Done():
					return PolicyWorkspaceResponse{}, ctx.Err()
				}
				if fail {
					return PolicyWorkspaceResponse{}, errors.New("controller disconnected")
				}
				return PolicyWorkspaceResponse{Mode: "prepared", Revision: "test"}, nil
			}}
			httpServer := httptest.NewServer(server.Handler())
			defer httpServer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			request, _ := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/api/v1/policy-workspace", strings.NewReader(`{"action":"test","names":["fast","slow"]}`))
			request.Header.Set("Authorization", "Bearer "+server.token)
			request.Header.Set("Accept", "text/event-stream")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
				t.Fatalf("stream response: %s content-type=%q", response.Status, response.Header.Get("Content-Type"))
			}
			reader := bufio.NewReader(response.Body)
			readEvent := func() policyWorkspaceEvent {
				t.Helper()
				line, err := reader.ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				var event policyWorkspaceEvent
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
					t.Fatal(err)
				}
				if _, err := reader.ReadString('\n'); err != nil {
					t.Fatal(err)
				}
				return event
			}
			first := readEvent()
			if first.Type != "result" || first.Result == nil || first.Result.Name != "fast" {
				t.Fatalf("first = %+v", first)
			}
			once.Do(func() { close(release) })
			last := readEvent()
			if fail {
				if last.Type != "error" || last.Error != "controller disconnected" {
					t.Fatalf("error = %+v", last)
				}
			} else if last.Type != "complete" || last.Workspace == nil || last.Workspace.Mode != "prepared" {
				t.Fatalf("complete = %+v", last)
			}
		})
	}
}
