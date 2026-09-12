package qnaphost

import (
	"strings"
	"testing"
)

func TestChooseHostPrioritiesBeatsQNAPSourceRule(t *testing.T) {
	rules := []byte("0: from all lookup local\n20010: from 192.168.2.240 lookup 20010\n32766: from all lookup main\n")
	priorities, err := chooseHostPriorities(rules, "192.168.2.240")
	if err != nil {
		t.Fatalf("chooseHostPriorities: %v", err)
	}
	if priorities.Proxy >= 20010 {
		t.Fatalf("proxy priority %d must run before QNAP source rule 20010", priorities.Proxy)
	}
	if !priorities.valid() {
		t.Fatalf("invalid priority set: %+v", priorities)
	}
}

func TestChooseHostPrioritiesSkipsOccupiedSlots(t *testing.T) {
	rules := []byte("0: from all lookup local\n19999: from all lookup 123\n20010: from 192.168.2.240 lookup 20010\n")
	priorities, err := chooseHostPriorities(rules, "192.168.2.240")
	if err != nil {
		t.Fatalf("chooseHostPriorities: %v", err)
	}
	if priorities.Proxy == 19999 || priorities.DNSUDP == 19999 || priorities.DNSTCP == 19999 || priorities.Main == 19999 {
		t.Fatalf("selected occupied priority 19999: %+v", priorities)
	}
	if priorities.Proxy >= 20010 {
		t.Fatalf("priority block does not beat QNAP rule: %+v", priorities)
	}
}

func TestChooseHostPrioritiesRefusesProtectedLowRange(t *testing.T) {
	rules := []byte("0: from all lookup local\n1002: from 192.168.2.240 lookup 20010\n")
	_, err := chooseHostPriorities(rules, "192.168.2.240")
	if err == nil || !strings.Contains(err.Error(), "protected low-priority") {
		t.Fatalf("expected protected low-priority error, got %v", err)
	}
}

func TestRouteUsesGatewayTable(t *testing.T) {
	output := []byte("1.1.1.1 from 192.168.2.240 via 192.168.2.241 dev br0 table 20242\n")
	if !routeUsesGatewayTable(output, "192.168.2.241", "20242") {
		t.Fatal("expected route to be recognized as OpenSurge takeover")
	}
	if routeUsesGatewayTable(output, "192.168.2.1", "20010") {
		t.Fatal("unexpected QNAP route match")
	}
}
