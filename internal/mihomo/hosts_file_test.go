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

func TestNativeHostsYAMLIsMergedAfterTraditionalHosts(t *testing.T) {
	combined := JoinProfileHostsInputs(
		"1.1.1.1 exact.example\n0.0.0.0 legacy.example",
		"'*.example.com': 10.0.0.2\n'+.example.net': 10.0.0.3\nexact.example: 9.9.9.9\nredirect.example: target.example",
	)
	standard, native, err := SplitProfileHostsInputs(combined)
	if err != nil {
		t.Fatalf("SplitProfileHostsInputs() error = %v", err)
	}
	if !strings.Contains(standard, "legacy.example") || !strings.Contains(native, "*.example.com") {
		t.Fatalf("split inputs lost content: standard=%q native=%q", standard, native)
	}

	hosts, err := parseHostsFileContent(combined)
	if err != nil {
		t.Fatalf("parseHostsFileContent() error = %v", err)
	}
	rendered, err := encodeYAMLNode(hosts)
	if err != nil {
		t.Fatalf("encodeYAMLNode() error = %v", err)
	}
	for _, want := range []string{"*.example.com", "+.example.net", "10.0.0.2", "10.0.0.3", "redirect.example", "target.example", "legacy.example"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("native hosts output missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "1.1.1.1") || !strings.Contains(rendered, "9.9.9.9") {
		t.Fatalf("native hosts must override the same exact key:\n%s", rendered)
	}
	if strings.Contains(rendered, "OPENSURGE NATIVE") {
		t.Fatalf("OpenSurge persistence markers leaked into Mihomo hosts:\n%s", rendered)
	}
}

func TestNativeHostsYAMLAcceptsFullHostsWrapper(t *testing.T) {
	if err := ValidateNativeProfileHostsYAML("hosts:\n  '*.wrapped.example': 192.0.2.10\n  multi.example: [192.0.2.11, 192.0.2.12]\n"); err != nil {
		t.Fatalf("ValidateNativeProfileHostsYAML() error = %v", err)
	}
}

func TestNativeHostsYAMLRejectsInvalidMapping(t *testing.T) {
	for _, body := range []string{
		"- not-a-mapping\n",
		"'*.example.com': {bad: value}\n",
		"'*.example.com': []\n",
	} {
		if err := ValidateNativeProfileHostsYAML(body); err == nil {
			t.Fatalf("ValidateNativeProfileHostsYAML(%q) unexpectedly succeeded", body)
		}
	}
}

func TestSplitProfileHostsInputsRejectsBrokenMarkers(t *testing.T) {
	if _, _, err := SplitProfileHostsInputs("1.1.1.1 example.test\n" + profileHostsNativeBegin + "\n'*.example': 2.2.2.2\n"); err == nil {
		t.Fatal("expected incomplete native hosts marker error")
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
