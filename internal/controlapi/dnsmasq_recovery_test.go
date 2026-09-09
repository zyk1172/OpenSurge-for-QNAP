package controlapi

import (
	"errors"
	"testing"
)

func TestDNSMasqRecoveryAttemptsOnlyOncePerIncident(t *testing.T) {
	controller := newDNSMasqRecoveryController()
	if !controller.observeMissing() {
		t.Fatal("first missing observation should request recovery")
	}
	if !controller.begin() {
		t.Fatal("first recovery should begin")
	}
	controller.finishAutomatic(nil)
	if controller.observeMissing() {
		t.Fatal("same incident retried before health confirmation")
	}
	if controller.state != dnsmasqRecoveryFailed {
		t.Fatalf("state after failed health confirmation = %q", controller.state)
	}
}

func TestDNSMasqRecoveryRequiresTwoHealthyConfirmationsToReset(t *testing.T) {
	controller := newDNSMasqRecoveryController()
	if !controller.observeMissing() || !controller.begin() {
		t.Fatal("failed to start recovery incident")
	}
	controller.finishAutomatic(nil)
	controller.observeHealthy()
	if controller.state != dnsmasqRecoveryRecovering || !controller.attempted {
		t.Fatalf("controller reset too early: state=%q attempted=%v", controller.state, controller.attempted)
	}
	controller.observeHealthy()
	if controller.state != dnsmasqRecoveryIdle || controller.attempted {
		t.Fatalf("controller did not reset after consecutive health: state=%q attempted=%v", controller.state, controller.attempted)
	}
	if !controller.observeMissing() {
		t.Fatal("new incident should be eligible after healthy reset")
	}
}

func TestDNSMasqRecoveryFailureIsBounded(t *testing.T) {
	controller := newDNSMasqRecoveryController()
	if !controller.observeMissing() || !controller.begin() {
		t.Fatal("failed to start recovery incident")
	}
	controller.finishAutomatic(errors.New("dnsmasq failed to bind port 53"))
	if controller.state != dnsmasqRecoveryFailed {
		t.Fatalf("state = %q", controller.state)
	}
	if controller.observeMissing() {
		t.Fatal("failed automatic recovery must not loop")
	}
	if controller.error == "" {
		t.Fatal("failure reason was not retained")
	}
}

func TestDNSMasqRecoveryUnknownSampleDoesNotRearmIncident(t *testing.T) {
	controller := newDNSMasqRecoveryController()
	if !controller.observeMissing() || !controller.begin() {
		t.Fatal("failed to start recovery incident")
	}
	controller.finishAutomatic(nil)
	controller.observeUnknown()
	if controller.observeMissing() {
		t.Fatal("unknown sample rearmed a recovery incident")
	}
}

func TestContainerRecoveryRunnerScope(t *testing.T) {
	if !containerRecoveryRunner(ContainerRunner{}) {
		t.Fatal("ContainerRunner must enable dnsmasq recovery")
	}
	if containerRecoveryRunner(DirectRunner{}) {
		t.Fatal("DirectRunner must not silently opt macOS helper builds into container recovery")
	}
}
