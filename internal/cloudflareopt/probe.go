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
	requiredHTTPS := requiredHTTPSCandidateCount(settings)
	perTarget := probeHTTPSForTargets(budgetCtx, enabledTargets, coarse, options, requiredHTTPS, settings.HTTPSCandidateCount, progress)
	if len(perTarget) == 0 {
		return ScanOutput{}, fmt.Errorf("no target domain completed Cloudflare HTTPS/SNI validation")
	}

	speedByIP := probeDownloadCandidates(ctx, enabledTargets, perTarget, options, progress)

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

func requiredHTTPSCandidateCount(settings ScanSettings) int {
	if settings.MinDownloadMbps > 0 {
		return settings.HTTPSCandidateCount
	}
	required := settings.DownloadCandidateCount
	if required < 3 {
		required = 3
	}
	if required > settings.HTTPSCandidateCount {
		required = settings.HTTPSCandidateCount
	}
	if required < 1 {
		required = 1
	}
	return required
}

// probeHTTPSForTargets keeps the normal first window fast, then advances through
// later latency-sorted windows only for targets that still lack enough SNI-valid
// edges. Windows are rotated across targets so one difficult hostname cannot
// consume the whole scan budget before the others get a chance.
func probeHTTPSForTargets(ctx context.Context, targets []Target, candidates []tcpCandidate, options ScanOptions, required, windowSize int, progress func(ScanProgress)) map[string][]httpCandidate {
	perTarget := make(map[string][]httpCandidate, len(targets))
	if len(candidates) == 0 || len(targets) == 0 || required <= 0 {
		return perTarget
	}
	if windowSize <= 0 {
		windowSize = required
	}

	for start := 0; start < len(candidates) && ctx.Err() == nil; start += windowSize {
		end := start + windowSize
		if end > len(candidates) {
			end = len(candidates)
		}
		needsAnotherWindow := false
		for index, target := range targets {
			verified := perTarget[target.Domain]
			if len(verified) >= required {
				continue
			}
			needsAnotherWindow = true
			if ctx.Err() != nil {
				break
			}
			if progress != nil && start == 0 {
				progress(ScanProgress{Phase: "https", Completed: index, Total: len(targets)})
			}
			perTarget[target.Domain] = append(verified, probeHTTPSBatch(ctx, target, candidates[start:end], options)...)
		}
		if !needsAnotherWindow {
			break
		}
	}

	for _, target := range targets {
		verified := perTarget[target.Domain]
		if len(verified) == 0 {
			delete(perTarget, target.Domain)
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
		if len(verified) > windowSize {
			verified = verified[:windowSize]
		}
		perTarget[target.Domain] = verified
	}
	return perTarget
}

func probeHTTPSBatch(ctx context.Context, target Target, candidates []tcpCandidate, options ScanOptions) []httpCandidate {
	if len(candidates) == 0 {
		return nil
	}
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

type httpsValidationResponse struct {
	StatusCode  int
	Header      http.Header
	BodyPreview []byte
	TTFB        time.Duration
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

	validation, err := performHTTPSValidationRequest(ctx, client, http.MethodHead, requestURL)
	if err != nil {
		return httpCandidate{}, false
	}
	if !validHTTPSValidationResponse(validation.StatusCode, validation.Header, validation.BodyPreview) {
		return httpCandidate{}, false
	}

	// PT sites and WAF-protected origins commonly reject automated HEAD requests
	// with 403/405 even when the candidate Cloudflare edge is valid for the
	// target SNI. Re-check those statuses with a bounded GET so explicit
	// Cloudflare edge-IP errors (notably 1034) can be observed without treating
	// the status code itself as an unusable-IP verdict.
	if validation.StatusCode == http.StatusForbidden || validation.StatusCode == http.StatusMethodNotAllowed {
		if fallback, fallbackErr := performHTTPSValidationRequest(ctx, client, http.MethodGet, requestURL); fallbackErr == nil {
			if hasExplicitCloudflareEdgeIPError(fallback.Header, fallback.BodyPreview) {
				return httpCandidate{}, false
			}
			// The GET is a second opinion used mainly to expose Cloudflare edge-IP
			// errors that HEAD cannot carry in a body. If WAF/rate limiting changes
			// the GET status, keep the already-valid HEAD result rather than turning
			// a usable edge into a false negative.
			if validHTTPSValidationResponse(fallback.StatusCode, fallback.Header, fallback.BodyPreview) {
				validation = fallback
			}
		}
	}

	cfRay := validation.Header.Get("CF-Ray")
	colo := ""
	if index := strings.LastIndex(cfRay, "-"); index >= 0 && index+1 < len(cfRay) {
		colo = strings.ToUpper(strings.TrimSpace(cfRay[index+1:]))
	}
	return httpCandidate{tcpCandidate: candidate, TTFB: validation.TTFB, Colo: colo}, true
}

func performHTTPSValidationRequest(ctx context.Context, client *http.Client, method, requestURL string) (httpsValidationResponse, error) {
	request, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return httpsValidationResponse{}, err
	}
	request.Header.Set("User-Agent", "OpenSurge-Cloudflare-Optimizer/1")
	if method == http.MethodGet {
		request.Header.Set("Range", "bytes=0-8191")
	}

	var ttfb time.Duration
	started := time.Now()
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { ttfb = time.Since(started) }}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	response, err := client.Do(request)
	if err != nil {
		return httpsValidationResponse{}, err
	}
	bodyPreview, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	_ = response.Body.Close()
	if ttfb == 0 {
		ttfb = time.Since(started)
	}
	return httpsValidationResponse{
		StatusCode:  response.StatusCode,
		Header:      response.Header.Clone(),
		BodyPreview: bodyPreview,
		TTFB:        ttfb,
	}, nil
}

func validHTTPSValidationResponse(status int, header http.Header, bodyPreview []byte) bool {
	if !hasCloudflareEdgeEvidence(header) {
		return false
	}
	if hasExplicitCloudflareEdgeIPError(header, bodyPreview) {
		return false
	}
	// For arbitrary user domains, a Cloudflare-backed 4xx still proves that the
	// candidate reached the intended edge/SNI. Treat edge-authenticated 2xx-4xx
	// responses as valid and reserve rejection for transport/TLS failures,
	// explicit edge-IP errors, and 5xx responses.
	return status >= http.StatusOK && status < http.StatusInternalServerError
}

func hasCloudflareEdgeEvidence(header http.Header) bool {
	server := strings.ToLower(strings.TrimSpace(header.Get("Server")))
	return strings.Contains(server, "cloudflare") || strings.TrimSpace(header.Get("CF-Ray")) != ""
}

func hasExplicitCloudflareEdgeIPError(header http.Header, bodyPreview []byte) bool {
	for _, key := range []string{"CF-Error-Code", "Cloudflare-Error-Code"} {
		if strings.TrimSpace(header.Get(key)) == "1034" {
			return true
		}
	}
	body := strings.ToLower(string(bodyPreview))
	for _, marker := range []string{
		"error 1034",
		"error code 1034",
		"error code: 1034",
		"edge ip restricted",
	} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

func initialDownloadIPs(targets []Target, perTarget map[string][]httpCandidate, perTargetLimit, maxUnique int) []string {
	if perTargetLimit <= 0 || maxUnique <= 0 {
		return nil
	}
	out := make([]string, 0, maxUnique)
	seen := map[string]bool{}
	for _, target := range targets {
		verified := perTarget[target.Domain]
		limit := perTargetLimit
		if len(verified) < limit {
			limit = len(verified)
		}
		for _, candidate := range verified[:limit] {
			if seen[candidate.IP] {
				continue
			}
			seen[candidate.IP] = true
			out = append(out, candidate.IP)
			if len(out) >= maxUnique {
				return out
			}
		}
	}
	return out
}

func orderedDownloadIPs(targets []Target, perTarget map[string][]httpCandidate, maxUnique int) []string {
	if maxUnique <= 0 {
		return nil
	}
	maxCandidates := 0
	for _, target := range targets {
		if count := len(perTarget[target.Domain]); count > maxCandidates {
			maxCandidates = count
		}
	}
	out := make([]string, 0, maxUnique)
	seen := map[string]bool{}
	for rank := 0; rank < maxCandidates; rank++ {
		for _, target := range targets {
			verified := perTarget[target.Domain]
			if rank >= len(verified) {
				continue
			}
			ip := verified[rank].IP
			if seen[ip] {
				continue
			}
			seen[ip] = true
			out = append(out, ip)
			if len(out) >= maxUnique {
				return out
			}
		}
	}
	return out
}

func allVerifiedTargetsMeetMinimumDownload(targets []Target, perTarget map[string][]httpCandidate, speedByIP map[string]float64, minimumMbps float64) bool {
	if minimumMbps <= 0 {
		return true
	}
	checkedTarget := false
	for _, target := range targets {
		verified := perTarget[target.Domain]
		if len(verified) == 0 {
			continue
		}
		checkedTarget = true
		matched := false
		for _, candidate := range verified {
			if speed, measured := speedByIP[candidate.IP]; measured && speed >= minimumMbps {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return checkedTarget
}

// probeDownloadCandidates mirrors CloudflareSpeedTest's useful -sl behaviour:
// run the normal bounded first set, then keep walking later verified candidates
// only when a minimum throughput was requested and some domain still has no
// passing IP. HTTPSCandidateCount remains the cap on unique download probes.
func probeDownloadCandidates(ctx context.Context, targets []Target, perTarget map[string][]httpCandidate, options ScanOptions, progress func(ScanProgress)) map[string]float64 {
	settings := options.Settings
	speedByIP := map[string]float64{}
	if settings.DownloadCandidateCount <= 0 || ctx.Err() != nil {
		return speedByIP
	}

	maxUnique := settings.HTTPSCandidateCount
	if maxUnique < settings.DownloadCandidateCount {
		maxUnique = settings.DownloadCandidateCount
	}

	var ordered []string
	minimumProbeCount := 0
	if settings.MinDownloadMbps > 0 {
		// A throughput floor is a global success condition, so rotate across
		// domains by candidate rank. This prevents an early target from consuming
		// the whole bounded queue before later targets receive a measurement.
		ordered = orderedDownloadIPs(targets, perTarget, maxUnique)
		minimumProbeCount = settings.DownloadCandidateCount
		if minimumProbeCount > len(ordered) {
			minimumProbeCount = len(ordered)
		}
	} else {
		ordered = initialDownloadIPs(targets, perTarget, settings.DownloadCandidateCount, maxUnique)
		minimumProbeCount = len(ordered)
	}
	if len(ordered) == 0 {
		return speedByIP
	}

	if progress != nil {
		progress(ScanProgress{Phase: "download", Total: len(ordered)})
	}
	for index, ip := range ordered {
		if ctx.Err() != nil {
			break
		}
		speedByIP[ip] = probeDownload(ctx, ip, options)
		if progress != nil {
			progress(ScanProgress{Phase: "download", Completed: index + 1, Total: len(ordered)})
		}
		if settings.MinDownloadMbps <= 0 {
			continue
		}
		if index+1 >= minimumProbeCount &&
			allVerifiedTargetsMeetMinimumDownload(targets, perTarget, speedByIP, settings.MinDownloadMbps) {
			break
		}
	}
	return speedByIP
}

func probeDownload(ctx context.Context, ip string, options ScanOptions) float64 {
	settings := options.Settings
	window := time.Duration(settings.DownloadSeconds) * time.Second
	headerTimeout := time.Duration(settings.HTTPTimeoutMS) * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= window+250*time.Millisecond {
			return 0
		}
	}

	dialer := directDialer(options.Interface, options.SourceIPv4, headerTimeout)
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   headerTimeout,
		ResponseHeaderTimeout: headerTimeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "speed.cloudflare.com"},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", net.JoinHostPort(ip, "443"))
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}

	bytesText := strconv.FormatInt(settings.DownloadMaxBytes, 10)
	testURL := "https://speed.cloudflare.com/__down?bytes=" + url.QueryEscape(bytesText)
	var totalRead int64
	var activeBody time.Duration

	for activeBody < window {
		if ctx.Err() != nil {
			return 0
		}
		remainingWindow := window - activeBody
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= remainingWindow+250*time.Millisecond {
			return 0
		}

		requestCtx, cancelRequest := context.WithCancel(ctx)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, testURL, nil)
		if err != nil {
			cancelRequest()
			return 0
		}
		request.Header.Set("User-Agent", "OpenSurge-Cloudflare-Optimizer/1")
		request.Header.Set("Accept-Encoding", "identity")

		response, err := client.Do(request)
		if err != nil {
			cancelRequest()
			return 0
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			cancelRequest()
			return 0
		}

		bodyStarted := time.Now()
		timer := time.AfterFunc(remainingWindow, cancelRequest)
		read, _ := io.Copy(io.Discard, response.Body)
		timer.Stop()
		_ = response.Body.Close()
		elapsed := time.Since(bodyStarted)
		if elapsed > remainingWindow {
			elapsed = remainingWindow
		}
		cancelRequest()

		if ctx.Err() != nil || read <= 0 || elapsed <= 0 {
			return 0
		}
		totalRead += read
		activeBody += elapsed

		// A full 200 MB response can complete before the requested sample
		// window on fast links. In that case issue another request on the same
		// transport/connection and keep accumulating active body time.
		if read >= settings.DownloadMaxBytes {
			continue
		}

		// The final read is expected to be canceled when the measurement window
		// expires. An earlier short response/error is not a trustworthy sample.
		if activeBody < window*8/10 {
			return 0
		}
		break
	}

	if totalRead <= 0 || activeBody <= 0 {
		return 0
	}
	return float64(totalRead) * 8 / activeBody.Seconds() / 1_000_000
}
