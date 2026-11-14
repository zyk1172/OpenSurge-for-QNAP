package controlapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"open-mihomo-gateway/internal/config"
)

func TestInvalidDeploymentCanBeRepairedWithoutRelaxingCandidateChecks(t *testing.T) {
	path, valid := writeConfigFileTestConfig(t)
	invalid := valid
	invalid.Gateway.LANIP = "invalid-future-ip"
	broken := []byte(config.Render(invalid))
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHelperConfig("config-apply-file", path); err != nil {
		t.Fatal(err)
	}
	if err := withConfigurationLifecycleLock(path, func() error {
		if _, err := applyConfigFile(t.Context(), path, fileDigest(path), broken); err == nil {
			t.Fatal("invalid new candidate accepted")
		}
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(current, broken) {
			t.Fatal("invalid candidate modified config")
		}
		_, err = applyConfigFile(t.Context(), path, fileDigest(path), []byte(config.Render(valid)))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("repaired config invalid: %v", err)
	}
}

func TestStopAndOverviewRemainAvailableWithInvalidDeployment(t *testing.T) {
	server := newTestServer(t)
	cfg, err := config.LoadRuntime(server.configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Gateway.LANIP = "invalid-future-ip"
	if err := os.WriteFile(server.configPath, []byte(config.Render(cfg)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadRuntime(server.configPath); err == nil {
		t.Fatal("fixture must fail deployment validation")
	}
	if _, err := server.overview(t.Context()); err != nil {
		t.Fatalf("diagnostics blocked by deployment error: %v", err)
	}
	called := make(chan string, 1)
	server.runner = actionRunnerFunc(func(_ context.Context, action, _ string) error { called <- action; return nil })
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:61767/api/v1/gateway/stop", nil)
	request.Host = "127.0.0.1:61767"
	request.Header.Set("Authorization", "Bearer "+server.token)
	request.Header.Set("Idempotency-Key", "invalid-deployment-stop")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("stop blocked: %d %s", response.Code, response.Body.String())
	}
	waitForStoredOperation(t, server, "invalid-deployment-stop", "succeeded")
	select {
	case action := <-called:
		if action != "stop" {
			t.Fatalf("action=%s", action)
		}
	default:
		t.Fatal("stop runner was not called")
	}
	if _, err := loadHelperConfig("stop", server.configPath); err != nil {
		t.Fatalf("helper stop blocked: %v", err)
	}
	if _, err := loadHelperConfig("start", server.configPath); err == nil {
		t.Fatal("helper start must still reject invalid deployments")
	}
}
