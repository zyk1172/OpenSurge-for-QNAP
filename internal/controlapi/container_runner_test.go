package controlapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/macosnetwork"
)

func TestContainerRunnerRejectsMacOSHostNetworkMutations(t *testing.T) {
	runner := ContainerRunner{}

	if err := runner.SetManual(context.Background(), "/tmp/config.yaml", macosnetwork.ManualConfig{}); err == nil || !strings.Contains(err.Error(), "QNAP/Linux container refuses") {
		t.Fatalf("SetManual should fail closed on QNAP/Linux, got %v", err)
	}
	if err := runner.SetDHCP(context.Background(), "/tmp/config.yaml", "Wi-Fi"); err == nil || !strings.Contains(err.Error(), "QNAP/Linux container refuses") {
		t.Fatalf("SetDHCP should fail closed on QNAP/Linux, got %v", err)
	}
	if servers, err := runner.ProbeDHCP(context.Background(), "/tmp/config.yaml", "eth0", time.Second); err == nil || servers != nil || !strings.Contains(err.Error(), "QNAP/Linux container refuses") {
		t.Fatalf("ProbeDHCP should fail closed on QNAP/Linux, got servers=%v err=%v", servers, err)
	}
}

func TestContainerTopologyRejectsMacOSDHCPTakeover(t *testing.T) {
	if err := validateContainerTopology(config.GatewayModeSameWiFiDHCP); err == nil {
		t.Fatal("same_wifi_dhcp must be rejected by the QNAP/Linux container")
	}
	for _, mode := range []string{"same_lan", "isolated_lan"} {
		if err := validateContainerTopology(mode); err != nil {
			t.Fatalf("mode %q should remain available: %v", mode, err)
		}
	}
}
