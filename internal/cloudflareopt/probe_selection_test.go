package cloudflareopt

import (
	"testing"
	"time"
)

func TestBuildCandidateResultsKeepsHTTPSVerifiedFallbackWhenDownloadFails(t *testing.T) {
	verified := []httpCandidate{
		{tcpCandidate: tcpCandidate{IP: "104.18.1.1", Latency: 20 * time.Millisecond}, TTFB: 30 * time.Millisecond},
		{tcpCandidate: tcpCandidate{IP: "104.18.1.2", Latency: 25 * time.Millisecond}, TTFB: 35 * time.Millisecond},
	}
	got := buildCandidateResults(verified, map[string]float64{"104.18.1.1": 0, "104.18.1.2": 42.5})
	if len(got) != 2 {
		t.Fatalf("verified fallback candidates = %#v", got)
	}
	if got[0].DownloadMbps != 0 || got[1].DownloadMbps != 42.5 {
		t.Fatalf("download measurements were not preserved: %#v", got)
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


func TestCandidateBetterFallsBackToQualityWhenAllDownloadsFail(t *testing.T) {
	betterQuality := CandidateResult{IP: "104.18.1.1", DownloadMbps: 0, LossRate: 0, TTFBMS: 20, LatencyMS: 10}
	worseQuality := CandidateResult{IP: "104.18.1.2", DownloadMbps: 0, LossRate: 0, TTFBMS: 60, LatencyMS: 45}
	if !candidateBetter(betterQuality, worseQuality) {
		t.Fatal("when all download probes fail, verified candidates should fall back to TTFB/latency ordering")
	}
}
