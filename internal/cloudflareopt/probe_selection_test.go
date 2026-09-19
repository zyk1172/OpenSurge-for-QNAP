package cloudflareopt

import (
	"net/http"
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

func TestHTTPSValidationAcceptsCloudflare403And405(t *testing.T) {
	cfRay := http.Header{}
	cfRay.Set("CF-Ray", "abc-SIN")
	if !validHTTPSValidationResponse(http.StatusForbidden, cfRay, nil) {
		t.Fatal("Cloudflare 403 should remain eligible after TLS/SNI and edge validation")
	}
	server := http.Header{}
	server.Set("Server", "cloudflare")
	if !validHTTPSValidationResponse(http.StatusMethodNotAllowed, server, nil) {
		t.Fatal("Cloudflare 405 should remain eligible because HEAD may be unsupported")
	}
}

func TestHTTPSValidationRejects403WithoutCloudflareEvidence(t *testing.T) {
	if validHTTPSValidationResponse(http.StatusForbidden, http.Header{}, nil) {
		t.Fatal("403 without Cloudflare edge evidence must be rejected")
	}
}

func TestHTTPSValidationRejectsExplicitEdgeIPRestrictedError(t *testing.T) {
	header := http.Header{}
	header.Set("CF-Ray", "abc-SIN")
	body := []byte("<html><title>Error 1034</title><p>Edge IP Restricted</p></html>")
	if validHTTPSValidationResponse(http.StatusForbidden, header, body) {
		t.Fatal("Cloudflare error 1034 must reject the candidate")
	}

	header.Set("CF-Error-Code", "1034")
	if validHTTPSValidationResponse(http.StatusForbidden, header, nil) {
		t.Fatal("Cloudflare error code header 1034 must reject the candidate")
	}
}

func TestHTTPSValidationStillRejectsServerErrors(t *testing.T) {
	header := http.Header{}
	header.Set("CF-Ray", "abc-SIN")
	if validHTTPSValidationResponse(http.StatusInternalServerError, header, nil) {
		t.Fatal("5xx response must not enter the validated candidate queue")
	}
}

func TestCandidateBetterFallsBackToQualityWhenAllDownloadsFail(t *testing.T) {
	betterQuality := CandidateResult{IP: "104.18.1.1", DownloadMbps: 0, LossRate: 0, TTFBMS: 20, LatencyMS: 10}
	worseQuality := CandidateResult{IP: "104.18.1.2", DownloadMbps: 0, LossRate: 0, TTFBMS: 60, LatencyMS: 45}
	if !candidateBetter(betterQuality, worseQuality) {
		t.Fatal("when all download probes fail, verified candidates should fall back to TTFB/latency ordering")
	}
}

func TestFilterByMinimumDownloadDropsSlowAndUnmeasuredCandidates(t *testing.T) {
	candidates := []CandidateResult{
		{IP: "104.18.1.1", DownloadMbps: 0},
		{IP: "104.18.1.2", DownloadMbps: 19.9},
		{IP: "104.18.1.3", DownloadMbps: 20},
		{IP: "104.18.1.4", DownloadMbps: 80},
	}
	got := filterByMinimumDownload(candidates, 20)
	if len(got) != 2 || got[0].IP != "104.18.1.3" || got[1].IP != "104.18.1.4" {
		t.Fatalf("minimum throughput filter = %#v", got)
	}
}

func TestFilterByMinimumDownloadDisabledPreservesFallback(t *testing.T) {
	candidates := []CandidateResult{
		{IP: "104.18.1.1", DownloadMbps: 0},
		{IP: "104.18.1.2", DownloadMbps: 42.5},
	}
	got := filterByMinimumDownload(candidates, 0)
	if len(got) != 2 {
		t.Fatalf("disabled minimum throughput filter removed candidates: %#v", got)
	}
}
