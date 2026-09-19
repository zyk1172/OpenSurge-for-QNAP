package cloudflareopt

import (
	"testing"
	"time"
)

func TestBuildCandidateResultsRequiresMeasuredDownloadSpeed(t *testing.T) {
	verified := []httpCandidate{
		{tcpCandidate: tcpCandidate{IP: "104.18.1.1", Latency: 20 * time.Millisecond}, TTFB: 30 * time.Millisecond},
		{tcpCandidate: tcpCandidate{IP: "104.18.1.2", Latency: 25 * time.Millisecond}, TTFB: 35 * time.Millisecond},
	}
	got := buildCandidateResults(verified, map[string]float64{"104.18.1.1": 0, "104.18.1.2": 42.5}, true)
	if len(got) != 1 || got[0].IP != "104.18.1.2" || got[0].DownloadMbps != 42.5 {
		t.Fatalf("download-qualified candidates = %#v", got)
	}
}

func TestCandidateBetterPrefersMeasuredDownloadSpeed(t *testing.T) {
	faster := CandidateResult{IP: "104.18.1.2", DownloadMbps: 80, LossRate: 0, TTFBMS: 60, LatencyMS: 45}
	lowerLatency := CandidateResult{IP: "104.18.1.1", DownloadMbps: 20, LossRate: 0, TTFBMS: 20, LatencyMS: 10}
	if !candidateBetter(faster, lowerLatency) {
		t.Fatal("measured download speed should outrank lower latency after hard quality filters")
	}
}

func TestValidHTTPSValidationStatusRejects403FromSelectionQueue(t *testing.T) {
	if validHTTPSValidationStatus(403) {
		t.Fatal("HTTP 403 must not enter the HTTPS-validated candidate queue")
	}
}
