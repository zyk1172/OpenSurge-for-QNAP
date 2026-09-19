package cloudflareopt

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var cloudflareIPv4Ranges = []string{
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
}

type ScanOptions struct {
	Interface   string
	SourceIPv4  string
	Settings    ScanSettings
	VerifyRoute func(context.Context, string) error
	Now         func() time.Time
}

type ScanProgress struct {
	Phase     string
	Completed int
	Total     int
}

type ScanOutput struct {
	Results         []TargetResult
	RejectedDomains []string
	Elapsed         time.Duration
}

type tcpCandidate struct {
	IP        string
	Latency   time.Duration
	LossRate  float64
}

type httpCandidate struct {
	tcpCandidate
	TTFB time.Duration
	Colo string
}

func RunScan(ctx context.Context, targets []Target, options ScanOptions, progress func(ScanProgress)) (ScanOutput, error) {
	settings := options.Settings
	if err := validateScan(settings); err != nil {
		return ScanOutput{}, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	started := now()
	budgetCtx, cancel := context.WithTimeout(ctx, time.Duration(settings.BudgetSeconds)*time.Second)
	defer cancel()

	enabledTargets := make([]Target, 0, len(targets))
	for _, target := range targets {
		if target.Enabled {
			enabledTargets = append(enabledTargets, target)
		}
	}
	if len(enabledTargets) == 0 {
		return ScanOutput{}, fmt.Errorf("no enabled Cloudflare optimizer targets")
	}

	candidates, err := sampleCloudflareIPv4(settings.CandidateLimit)
	if err != nil {
		return ScanOutput{}, err
	}
	if len(candidates) == 0 {
		return ScanOutput{}, fmt.Errorf("Cloudflare IPv4 candidate pool is empty")
	}
	if options.VerifyRoute != nil {
		if err := options.VerifyRoute(budgetCtx, candidates[0]); err != nil {
			return ScanOutput{}, fmt.Errorf("direct route preflight failed: %w", err)
		}
	}
	if progress != nil {
		progress(ScanProgress{Phase: "tcp", Total: len(candidates)})
	}
	coarse := probeTCPBatch(budgetCtx, candidates, options, progress)
	if len(coarse) == 0 {
		return ScanOutput{}, fmt.Errorf("no reachable Cloudflare IPv4 candidates on TCP/443")
	}
	filtered := coarse[:0]
	for _, candidate := range coarse {
		if candidate.Latency.Milliseconds() > int64(settings.MaxLatencyMS) {
			continue
		}
		if candidate.LossRate > settings.MaxLossRate {
			continue
		}
		filtered = append(filtered, candidate)
	}
	coarse = filtered
	if len(coarse) == 0 {
		return ScanOutput{}, fmt.Errorf("no Cloudflare IPv4 candidates met latency <= %d ms and loss <= %.0f%%", settings.MaxLatencyMS, settings.MaxLossRate*100)
	}
	sort.Slice(coarse, func(i, j int) bool {
		if coarse[i].LossRate != coarse[j].LossRate {
			return coarse[i].LossRate < coarse[j].LossRate
		}
		return coarse[i].Latency < coarse[j].Latency
	})
	if len(coarse) > settings.HTTPSCandidateCount {
		coarse = coarse[:settings.HTTPSCandidateCount]
	}

	perTarget := make(map[string][]httpCandidate, len(enabledTargets))
	for index, target := range enabledTargets {
		if budgetCtx.Err() != nil {
			break
		}
		if progress != nil {
			progress(ScanProgress{Phase: "https", Completed: index, Total: len(enabledTargets)})
		}
		verified := probeHTTPSBatch(budgetCtx, target, coarse, options)
		if len(verified) == 0 {
			continue
		}
		sort.Slice(verified, func(i, j int) bool {
			if verified[i].LossRate != verified[j].LossRate {
				return verified[i].LossRate < verified[j].LossRate
			}
			if verified[i].TTFB != verified[j].TTFB {
				return verified[i].TTFB < verified[j].TTFB
			}
			return verified[i].Latency < verified[j].Latency
		})
		perTarget[target.Domain] = verified
	}
	if len(perTarget) == 0 {
		return ScanOutput{}, fmt.Errorf("no target domain completed Cloudflare HTTPS/SNI validation")
	}

	speedByIP := map[string]float64{}
	if settings.DownloadCandidateCount > 0 && ctx.Err() == nil {
		unique := make([]string, 0)
		seen := map[string]bool{}
		for _, target := range enabledTargets {
			verified := perTarget[target.Domain]
			limit := settings.DownloadCandidateCount
			if len(verified) < limit {
				limit = len(verified)
			}
			for _, candidate := range verified[:limit] {
				if !seen[candidate.IP] {
					seen[candidate.IP] = true
					unique = append(unique, candidate.IP)
				}
			}
		}
		if progress != nil {
			progress(ScanProgress{Phase: "download", Total: len(unique)})
		}
		for index, ip := range unique {
			if ctx.Err() != nil {
				break
			}
			speedByIP[ip] = probeDownload(ctx, ip, options)
			if progress != nil {
				progress(ScanProgress{Phase: "download", Completed: index + 1, Total: len(unique)})
			}
		}
	}

	results := make([]TargetResult, 0, len(enabledTargets))
	rejectedDomains := make([]string, 0)
	for _, target := range enabledTargets {
		verified := perTarget[target.Domain]
		if len(verified) == 0 {
			continue
		}
		converted := buildCandidateResults(verified, speedByIP)
		converted = filterByMinimumDownload(converted, settings.MinDownloadMbps)
		if len(converted) == 0 {
			if settings.MinDownloadMbps > 0 {
				rejectedDomains = append(rejectedDomains, target.Domain)
			}
			continue
		}
		sort.SliceStable(converted, func(i, j int) bool { return candidateBetter(converted[i], converted[j]) })
		alternatives := converted
		if len(alternatives) > 3 {
			alternatives = alternatives[:3]
		}
		results = append(results, TargetResult{Domain: target.Domain, Selected: converted[0], Alternatives: alternatives, UpdatedAt: now().UTC()})
	}
	if len(results) == 0 && len(rejectedDomains) == 0 {
		return ScanOutput{}, fmt.Errorf("Cloudflare optimizer did not produce any usable domain result")
	}
	return ScanOutput{Results: results, RejectedDomains: rejectedDomains, Elapsed: now().Sub(started)}, nil
}

func buildCandidateResults(verified []httpCandidate, speedByIP map[string]float64) []CandidateResult {
	converted := make([]CandidateResult, 0, len(verified))
	for _, candidate := range verified {
		speed := speedByIP[candidate.IP]
		converted = append(converted, CandidateResult{
			IP:           candidate.IP,
			LatencyMS:    candidate.Latency.Milliseconds(),
			LossRate:     candidate.LossRate,
			TTFBMS:       candidate.TTFB.Milliseconds(),
			DownloadMbps: speed,
			Colo:         candidate.Colo,
		})
	}
	return converted
}

func filterByMinimumDownload(candidates []CandidateResult, minimumMbps float64) []CandidateResult {
	if minimumMbps <= 0 {
		return candidates
	}
	filtered := make([]CandidateResult, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.DownloadMbps >= minimumMbps {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func candidateBetter(left, right CandidateResult) bool {
	if left.DownloadMbps != right.DownloadMbps {
		return left.DownloadMbps > right.DownloadMbps
	}
	if left.LossRate != right.LossRate {
		return left.LossRate < right.LossRate
	}
	if left.TTFBMS != right.TTFBMS {
		return left.TTFBMS < right.TTFBMS
	}
	return left.LatencyMS < right.LatencyMS
}

func sampleCloudflareIPv4(limit int) ([]string, error) {
	pool := make([]string, 0, 2048)
	for _, raw := range cloudflareIPv4Ranges {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, err
		}
		prefix = prefix.Masked()
		bits := prefix.Bits()
		if bits > 24 {
			addr := prefix.Addr().As4()
			addr[3] = byte(1 + rand.IntN(253))
			pool = append(pool, netip.AddrFrom4(addr).String())
			continue
		}
		base := prefix.Addr().As4()
		count := 1 << (24 - bits)
		baseValue := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
		for index := 0; index < count; index++ {
			value := baseValue + uint32(index<<8) + uint32(1+rand.IntN(253))
			addr := [4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)}
			pool = append(pool, netip.AddrFrom4(addr).String())
		}
	}
	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	if limit > 0 && len(pool) > limit {
		pool = pool[:limit]
	}
	return pool, nil
}

func probeTCPBatch(ctx context.Context, ips []string, options ScanOptions, progress func(ScanProgress)) []tcpCandidate {
	workers := options.Settings.TCPConcurrency
	if workers > len(ips) {
		workers = len(ips)
	}
	jobs := make(chan string)
	results := make(chan tcpCandidate, len(ips))
	var wg sync.WaitGroup
	var completed int
	var progressMu sync.Mutex
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				if ctx.Err() != nil {
					return
				}
				candidate, ok := probeTCP(ctx, ip, options)
				if ok {
					results <- candidate
				}
				progressMu.Lock()
				completed++
				current := completed
				progressMu.Unlock()
				if progress != nil {
					progress(ScanProgress{Phase: "tcp", Completed: current, Total: len(ips)})
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, ip := range ips {
			select {
			case jobs <- ip:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	close(results)
	out := make([]tcpCandidate, 0, len(results))
	for result := range results {
		out = append(out, result)
	}
	return out
}

func probeTCP(ctx context.Context, ip string, options ScanOptions) (tcpCandidate, bool) {
	attempts := options.Settings.TCPAttempts
	timeout := time.Duration(options.Settings.TCPTimeoutMS) * time.Millisecond
	var total time.Duration
	received := 0
	for attempt := 0; attempt < attempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		start := time.Now()
		conn, err := directDialer(options.Interface, options.SourceIPv4, timeout).DialContext(attemptCtx, "tcp4", net.JoinHostPort(ip, "443"))
		elapsed := time.Since(start)
		cancel()
		if err != nil {
			continue
		}
		_ = conn.Close()
		received++
		total += elapsed
	}
	if received == 0 {
		return tcpCandidate{}, false
	}
	return tcpCandidate{IP: ip, Latency: total / time.Duration(received), LossRate: float64(attempts-received) / float64(attempts)}, true
}

func probeHTTPSBatch(ctx context.Context, target Target, candidates []tcpCandidate, options ScanOptions) []httpCandidate {
	workers := 5
	if workers > len(candidates) {
		workers = len(candidates)
	}
	jobs := make(chan tcpCandidate)
	results := make(chan httpCandidate, len(candidates))
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for candidate := range jobs {
				if result, ok := probeHTTPS(ctx, target, candidate, options); ok {
					results <- result
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, candidate := range candidates {
			select {
			case jobs <- candidate:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	close(results)
	out := make([]httpCandidate, 0, len(results))
	for result := range results {
		out = append(out, result)
	}
	return out
}

func probeHTTPS(ctx context.Context, target Target, candidate tcpCandidate, options ScanOptions) (httpCandidate, bool) {
	timeout := time.Duration(options.Settings.HTTPTimeoutMS) * time.Millisecond
	dialer := directDialer(options.Interface, options.SourceIPv4, timeout)
	transport := &http.Transport{
		Proxy:               nil,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: timeout,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", net.JoinHostPort(candidate.IP, "443"))
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	requestURL := "https://" + target.Domain + target.TestPath
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, requestURL, nil)
	if err != nil {
		return httpCandidate{}, false
	}
	request.Header.Set("User-Agent", "OpenSurge-Cloudflare-Optimizer/1")
	var ttfb time.Duration
	started := time.Now()
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { ttfb = time.Since(started) }}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	response, err := client.Do(request)
	if err != nil {
		return httpCandidate{}, false
	}
	io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	_ = response.Body.Close()
	if !validHTTPSValidationStatus(response.StatusCode) {
		return httpCandidate{}, false
	}
	server := strings.ToLower(response.Header.Get("Server"))
	cfRay := response.Header.Get("CF-Ray")
	if server != "cloudflare" && cfRay == "" {
		return httpCandidate{}, false
	}
	colo := ""
	if index := strings.LastIndex(cfRay, "-"); index >= 0 && index+1 < len(cfRay) {
		colo = strings.ToUpper(strings.TrimSpace(cfRay[index+1:]))
	}
	if ttfb == 0 {
		ttfb = time.Since(started)
	}
	return httpCandidate{tcpCandidate: candidate, TTFB: ttfb, Colo: colo}, true
}

func validHTTPSValidationStatus(status int) bool {
	return status >= http.StatusOK && status < http.StatusBadRequest
}

func probeDownload(ctx context.Context, ip string, options ScanOptions) float64 {
	settings := options.Settings
	timeout := time.Duration(settings.DownloadSeconds) * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 250*time.Millisecond {
			return 0
		}
		if remaining < timeout {
			timeout = remaining
		}
	}
	dialer := directDialer(options.Interface, options.SourceIPv4, timeout)
	transport := &http.Transport{
		Proxy:               nil,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: timeout,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "speed.cloudflare.com"},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", net.JoinHostPort(ip, "443"))
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout}
	bytesText := strconv.FormatInt(settings.DownloadMaxBytes, 10)
	testURL := "https://speed.cloudflare.com/__down?bytes=" + url.QueryEscape(bytesText)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		return 0
	}
	request.Header.Set("User-Agent", "OpenSurge-Cloudflare-Optimizer/1")
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return 0
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0
	}
	read, _ := io.Copy(io.Discard, io.LimitReader(response.Body, settings.DownloadMaxBytes))
	elapsed := time.Since(started).Seconds()
	if elapsed <= 0 || read <= 0 {
		return 0
	}
	return float64(read) * 8 / elapsed / 1_000_000
}
