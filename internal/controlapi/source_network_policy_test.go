package controlapi

import (
	"net"
	"testing"
)

func TestSourceIPAllowedOnlyForPublicDestinations(t *testing.T) {
	allowed := []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"}
	for _, value := range allowed {
		if !sourceIPAllowed(net.ParseIP(value)) {
			t.Fatalf("expected public source destination to be allowed: %s", value)
		}
	}

	blocked := []string{
		"127.0.0.1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.2.1",
		"169.254.169.254",
		"100.64.0.1",
		"100.100.100.100",
		"192.0.2.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"240.0.0.1",
		"::1",
		"fc00::1",
		"fe80::1",
		"2001:db8::1",
	}
	for _, value := range blocked {
		if sourceIPAllowed(net.ParseIP(value)) {
			t.Fatalf("expected special/private source destination to be blocked: %s", value)
		}
	}
	if sourceIPAllowed(nil) {
		t.Fatal("nil IP must not be allowed")
	}
}
