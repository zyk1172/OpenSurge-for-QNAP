package cloudflareopt

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestApplyResultsToProfileAddsHostsAndDeduplicatesRealIP(t *testing.T) {
	input := []byte(`hosts:
  manual.example.com: 192.0.2.8
dns:
  fake-ip-filter:
    - +.example.com
`)
	output, err := ApplyResultsToProfile(input, []TargetResult{{Domain: "api.example.com", Selected: CandidateResult{IP: "104.18.1.2"}}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(output)
	if !strings.Contains(text, "api.example.com") || !strings.Contains(text, "104.18.1.2") {
		t.Fatalf("missing optimizer host: %s", text)
	}
	var document map[string]any
	if err := yaml.Unmarshal(output, &document); err != nil {
		t.Fatal(err)
	}
	dns := document["dns"].(map[string]any)
	filters := dns["fake-ip-filter"].([]any)
	if len(filters) != 1 || filters[0] != "+.example.com" {
		t.Fatalf("semantic duplicate was added: %#v", filters)
	}
}

func TestApplyResultsToProfileReplacesOnlyEffectiveHost(t *testing.T) {
	input := []byte(`hosts:
  api.example.com: 192.0.2.9
  manual.example.com: 192.0.2.8
dns:
  fake-ip-filter: []
`)
	output, err := ApplyResultsToProfile(input, []TargetResult{{Domain: "api.example.com", Selected: CandidateResult{IP: "104.18.1.2"}}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(output)
	if strings.Contains(text, "192.0.2.9") || !strings.Contains(text, "192.0.2.8") || !strings.Contains(text, "104.18.1.2") {
		t.Fatalf("unexpected hosts output: %s", text)
	}
}

func TestApplyResultsToProfileRuleModeDetectsConflict(t *testing.T) {
	input := []byte(`dns:
  fake-ip-filter-mode: rule
  fake-ip-filter:
    - DOMAIN-SUFFIX,example.com,fake-ip
    - MATCH,fake-ip
`)
	_, err := ApplyResultsToProfile(input, []TargetResult{{Domain: "api.example.com", Selected: CandidateResult{IP: "104.18.1.2"}}})
	if err == nil || !strings.Contains(err.Error(), "real-IP conflict") {
		t.Fatalf("err = %v", err)
	}
}
