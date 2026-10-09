package controlapi

import (
	"net/http"
	"testing"

	"open-mihomo-gateway/internal/gateway"
)

func TestFullGatewayRecoveryHTTPIsNASOnly(t *testing.T) {
	s := newTestServer(t)
	response := performAuthorized(s, http.MethodPost, "/api/v1/gateway/recover-gateway", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("non-container runner exposed NAS recovery: %d %s", response.Code, response.Body.String())
	}
}

func TestContainerRecoveryRestoresAllDNSDependencies(t *testing.T) {
	for _, tc := range []struct {
		name, runtime, dns, localDNS, mihomo, wantAction, wantReason string
		desired                                                      bool
	}{
		{"failed start journal", "incomplete", "stopped", "stopped", "stopped", "recover-gateway", containerFailureIncomplete, true},
		{"legacy partial restart on NAS", "active", "stopped", "stopped", "running", "recover-gateway", containerFailureDNSMissing, true},
		{"SmartDNS exit", "active", "stopped", "running", "running", "recover-gateway", containerFailureDNSMissing, true},
		{"local DNS exit with DHCP disabled", "active", "running", "stopped", "running", "recover-gateway", containerFailureDNSMissing, true},
		{"rollback cleared runtime", "none", "stopped", "stopped", "stopped", "recover-gateway", containerFailureIncomplete, true},
		{"previous namespace", "interrupted", "stopped", "stopped", "stopped", "recover-gateway", containerFailureIncomplete, true},
		{"manual stop", "incomplete", "stopped", "stopped", "stopped", "", "", false},
		{"engine only exit", "active", "running", "running", "stopped", "restart-mihomo", mihomoFailureProcessMissing, true},
		{"healthy", "active", "running", "running", "running", "restart-mihomo", "", true},
		{"unknown DNS", "active", "unknown", "running", "running", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := gateway.Status{Gateway: "running", DesiredRunning: tc.desired, RuntimeState: tc.runtime, DNS: tc.dns, LocalDNS: tc.localDNS, DHCP: "disabled", Mihomo: tc.mihomo}
			action, reason := automaticRecoveryAction(status, true)
			if action != tc.wantAction || reason != tc.wantReason {
				t.Fatalf("recovery=(%q,%q), want (%q,%q)", action, reason, tc.wantAction, tc.wantReason)
			}
		})
	}
}

func TestFullGatewayRecoveryKeepsOneAttemptUntilFreshHealth(t *testing.T) {
	c := newMihomoRecoveryController()
	if !c.observeFailure(containerFailureDNSMissing) || !c.begin(containerFailureDNSMissing) {
		t.Fatal("missing DNS did not request full recovery")
	}
	c.finishAutomatic(nil)
	if c.observeFailure(containerFailureDNSMissing) || c.snapshot().State != mihomoRecoveryFailed {
		t.Fatal("successful command falsely confirmed DNS recovery or retried")
	}
	c.observeHealthy()
	c.observeUnknown()
	c.observeHealthy()
	if c.snapshot().State != mihomoRecoveryRecovering {
		t.Fatal("non-consecutive health samples rearmed recovery")
	}
	c.observeHealthy()
	if !c.observeFailure(containerFailureIncomplete) {
		t.Fatal("new incident was not eligible after two healthy samples")
	}
}
