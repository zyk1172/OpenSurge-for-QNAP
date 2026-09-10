package mihomo

import (
	"strings"
	"testing"
)

func TestParseHostsFileContent(t *testing.T) {
	hosts, err := parseHostsFileContent("\ufeff# local overrides\n0.0.0.0 ads.example.test tracker.example.test # blocked\n192.168.2.10 nas.home\n2001:db8::10 nas.home\n192.168.2.10 nas.home\n")
	if err != nil {
		t.Fatalf("parseHostsFileContent() error = %v", err)
	}
	rendered, err := encodeYAMLNode(hosts)
	if err != nil {
		t.Fatalf("encodeYAMLNode() error = %v", err)
	}
	for _, want := range []string{"ads.example.test", "tracker.example.test", "nas.home", "0.0.0.0", "192.168.2.10", "2001:db8::10"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("parsed hosts missing %q:\n%s", want, rendered)
		}
	}
	if strings.Count(rendered, "192.168.2.10") != 1 {
		t.Fatalf("duplicate host/IP mapping was not collapsed:\n%s", rendered)
	}
}

func TestParseHostsFileContentRejectsInvalidLines(t *testing.T) {
	for _, body := range []string{"example.test\n", "not-an-ip example.test\n"} {
		if _, err := parseHostsFileContent(body); err == nil {
			t.Fatalf("parseHostsFileContent(%q) unexpectedly succeeded", body)
		}
	}
}

func TestProfileHostsRoundTripInjection(t *testing.T) {
	base := []byte("proxies: []\nrules:\n  - MATCH,DIRECT\nhosts:\n  existing.example: 1.1.1.1\n")
	hosts, err := profileHostsFromYAML(base)
	if err != nil {
		t.Fatalf("profileHostsFromYAML() error = %v", err)
	}
	overlay, err := parseHostsFileContent("2.2.2.2 added.example\n3.3.3.3 existing.example\n")
	if err != nil {
		t.Fatalf("parseHostsFileContent() error = %v", err)
	}
	merged := mergeProfileHosts(hosts, overlay)
	injected, err := injectProfileHosts([]byte("proxies: []\nrules:\n  - MATCH,DIRECT\n"), merged)
	if err != nil {
		t.Fatalf("injectProfileHosts() error = %v", err)
	}
	text := string(injected)
	if !strings.Contains(text, "added.example") || !strings.Contains(text, "2.2.2.2") {
		t.Fatalf("injected profile missing added host:\n%s", text)
	}
	if strings.Contains(text, "1.1.1.1") || !strings.Contains(text, "3.3.3.3") {
		t.Fatalf("overlay host did not replace imported host:\n%s", text)
	}
}
