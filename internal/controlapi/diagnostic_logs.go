package controlapi

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/logfile"
	"open-mihomo-gateway/internal/runtime"
)

type DiagnosticLogInfo struct {
	SizeBytes    int64  `json:"size_bytes"`
	SampledLines int    `json:"sampled_lines"`
	Truncated    bool   `json:"truncated"`
	ReadError    string `json:"read_error,omitempty"`
}

type DiagnosticSignal struct {
	Emitter  string `json:"emitter"`
	Kind     string `json:"kind"`
	Protocol string `json:"protocol,omitempty"`
	SourceIP string `json:"source_ip,omitempty"`
	Target   string `json:"target,omitempty"`
	Policy   string `json:"policy,omitempty"`
	Rule     string `json:"rule,omitempty"`
	Reason   string `json:"reason"`
	Count    int    `json:"count"`
}

// Counts describe the bounded log sample, never lifetime totals or gateway
// health. SourceIP identifies a host, not an application such as Transmission.
type DiagnosticSignals struct {
	SampledLines       int                `json:"sampled_lines"`
	ConnectionFailures int                `json:"connection_failures"`
	OtherWarnings      int                `json:"other_warnings"`
	OmittedGroups      int                `json:"omitted_groups"`
	Groups             []DiagnosticSignal `json:"groups"`
}

func readDiagnosticLog(path string, limit int, cfg config.Config) ([]string, DiagnosticLogInfo) {
	tail, err := logfile.ReadTail(path, limit, logfile.ReadLimit)
	info := DiagnosticLogInfo{SizeBytes: tail.Size, SampledLines: len(tail.Lines), Truncated: tail.Truncated}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		info.ReadError = err.Error()
	}
	for i := range tail.Lines {
		for _, secret := range []string{cfg.Mihomo.Secret, cfg.UpstreamProxy.Password, cfg.UpstreamProxy.Username} {
			if secret != "" {
				tail.Lines[i] = strings.ReplaceAll(tail.Lines[i], secret, "[REDACTED]")
			}
		}
	}
	return tail.Lines, info
}

func diagnosticLogs(paths runtime.Paths, cfg config.Config) (map[string][]string, map[string]DiagnosticLogInfo, DiagnosticSignals) {
	logs := map[string][]string{}
	info := map[string]DiagnosticLogInfo{}
	samples := map[string][]string{}
	for name, path := range map[string]string{"mihomo": paths.MihomoLog, "dnsmasq": paths.DNSMasqLog, "smartdns": paths.SmartDNSLog} {
		lines, meta := readDiagnosticLog(path, 4096, cfg)
		samples[name], info[name] = lines, meta
		if len(lines) > 80 {
			lines = lines[len(lines)-80:]
		}
		logs[name] = lines
	}
	return logs, info, summarizeDiagnosticSignals(samples)
}

var connectionWarning = regexp.MustCompile(`\[(TCP|UDP)\] dial (.*?) \(match ([^)]*)\) (\S+) --> (\S+) error: (.*)`)

func summarizeDiagnosticSignals(samples map[string][]string) DiagnosticSignals {
	result := DiagnosticSignals{Groups: []DiagnosticSignal{}}
	groups := map[DiagnosticSignal]int{}
	for emitter, lines := range samples {
		result.SampledLines += len(lines)
		for _, line := range lines {
			lower := strings.ToLower(line)
			if !strings.Contains(lower, "level=warning") && !strings.Contains(lower, "level=warn") && !strings.Contains(lower, "level=error") && !strings.Contains(lower, "level=fatal") && !regexpWarningLevel(lower) && !strings.HasPrefix(lower, "panic:") {
				continue
			}
			entry := DiagnosticSignal{Emitter: emitter, Kind: "unclassified", Reason: diagnosticReason(lower)}
			message := line
			if _, encoded, ok := strings.Cut(line, "msg="); ok {
				if decoded, err := strconv.Unquote(encoded); err == nil {
					message = decoded
				}
			}
			if match := connectionWarning.FindStringSubmatch(message); match != nil {
				entry.Kind = "connection"
				entry.Protocol = strings.ToLower(match[1])
				entry.SourceIP, _, _ = net.SplitHostPort(match[4])
				if net.ParseIP(entry.SourceIP) == nil {
					entry.SourceIP = ""
				}
				// Only return valid endpoints, never an arbitrary log fragment/URL.
				if host, port, err := net.SplitHostPort(match[5]); err == nil && !strings.ContainsAny(host, "/?#@") {
					entry.Target = net.JoinHostPort(host, port)
				}
				entry.Policy, entry.Rule = match[2], match[3]
				entry.Reason = diagnosticReason(strings.ToLower(match[6]))
				result.ConnectionFailures++
			} else {
				result.OtherWarnings++
				if emitter == "smartdns" || emitter == "dnsmasq" || strings.Contains(lower, "dns") {
					entry.Kind = "dns"
				}
				if strings.Contains(lower, "fatal") || strings.Contains(lower, "panic") || strings.Contains(lower, "listener") || strings.Contains(lower, "tun listening") {
					entry.Kind = "service"
				}
			}
			groups[entry]++
		}
	}
	for entry, count := range groups {
		entry.Count = count
		result.Groups = append(result.Groups, entry)
	}
	sort.Slice(result.Groups, func(i, j int) bool {
		a, b := result.Groups[i], result.Groups[j]
		if signalPriority(a.Kind) != signalPriority(b.Kind) {
			return signalPriority(a.Kind) < signalPriority(b.Kind)
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return signalKey(a) < signalKey(b)
	})
	if len(result.Groups) > 32 {
		result.OmittedGroups = len(result.Groups) - 32
		result.Groups = result.Groups[:32]
	}
	return result
}

func signalKey(s DiagnosticSignal) string {
	return strings.Join([]string{s.Emitter, s.Kind, s.Protocol, s.SourceIP, s.Target, s.Policy, s.Rule, s.Reason}, "\x00")
}
func regexpWarningLevel(line string) bool {
	return strings.Contains(line, " warn ") || strings.Contains(line, " error ") || strings.Contains(line, " fatal ") || strings.Contains(line, "[warn]") || strings.Contains(line, "[error]")
}
func diagnosticReason(line string) string {
	switch {
	case strings.Contains(line, "no such host"), strings.Contains(line, "servfail"):
		return "dns_resolution"
	case strings.Contains(line, "timeout"), strings.Contains(line, "timed out"), strings.Contains(line, "deadline exceeded"):
		return "timeout"
	case strings.Contains(line, "refused"):
		return "connection_refused"
	case strings.Contains(line, "unreachable"), strings.Contains(line, "no route to host"):
		return "unreachable"
	case strings.Contains(line, "reset"):
		return "connection_reset"
	case strings.Contains(line, "tls"), strings.Contains(line, "certificate"):
		return "tls"
	default:
		return "other"
	}
}

// Maintenance never restarts a service. The lifecycle lock excludes startup,
// stop and reload (including other CLI processes) while rotating owned logs.
func (s *Server) maintainRuntimeLogs(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	previousError := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		err := s.rotateRuntimeLogs()
		if err == nil {
			previousError = ""
			continue
		}
		if errors.Is(err, runtime.ErrLifecycleOperationInProgress) {
			continue
		}
		if err.Error() != previousError {
			log.Printf("runtime log maintenance failed: %v", err)
			previousError = err.Error()
		}
	}
}

func (s *Server) rotateRuntimeLogs() error {
	cfg, err := config.LoadRuntime(s.configPath)
	if err != nil {
		return err
	}
	return runtime.WithLifecycleLock(cfg, func() error {
		paths := runtime.NewPaths(cfg)
		var result error
		for _, path := range []string{paths.MihomoLog, paths.SmartDNSLog, paths.DNSMasqLog} {
			result = errors.Join(result, logfile.Rotate(path, logfile.MaxSize, false))
		}
		return result
	})
}

func signalPriority(kind string) int {
	switch kind {
	case "service":
		return 0
	case "dns":
		return 1
	case "unclassified":
		return 2
	default:
		return 3
	}
}
