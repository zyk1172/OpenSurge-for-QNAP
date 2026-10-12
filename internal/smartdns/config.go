package smartdns

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/device"
	"open-mihomo-gateway/internal/dhcp"
	"open-mihomo-gateway/internal/mihomo"
	"open-mihomo-gateway/internal/runtime"
)

const (
	GatewayGroup  = "opensurge-gateway"
	ResolverGroup = "opensurge-resolver"
	LocalGroup    = "opensurge-local"
	mihomoDNS     = "127.0.0.1:1053"
)

// RenderConfig renders the LAN DNS front door. System resolvers are used only
// for Resolver View; Gateway View has exactly one upstream: Mihomo fake-IP DNS.
func RenderConfig(cfg config.Config, paths runtime.Paths) (string, error) {
	if cfg.DNS.ResolverMode == "custom" {
		return RenderConfigWithResolvers(cfg, paths, nil)
	}
	resolvers, err := systemResolvers("/etc/resolv.conf", cfg)
	if err != nil {
		return "", err
	}
	return RenderConfigWithResolvers(cfg, paths, resolvers)
}

// RenderConfigWithResolvers is deterministic and testable. The resolver list is
// intentionally independent from cfg.DNS.Upstream: that legacy field describes
// the old dnsmasq -> Mihomo chain and must never become a Real-IP fallback for
// Gateway View.
func RenderConfigWithResolvers(cfg config.Config, paths runtime.Paths, resolvers []string) (string, error) {
	listen := strings.TrimSpace(cfg.DNS.Listen)
	if listen == "" {
		listen = strings.TrimSpace(cfg.Gateway.LANIP)
	}
	if ip := net.ParseIP(listen); ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("smartdns listen address must be IPv4")
	}
	if cfg.DNS.Port <= 0 || cfg.DNS.Port > 65535 {
		return "", fmt.Errorf("smartdns listen port must be between 1 and 65535")
	}
	if err := config.ValidateResolverConfig(cfg); err != nil {
		return "", err
	}
	resolvers = normalizeResolvers(resolvers, cfg)
	if cfg.DNS.ResolverMode != "custom" && len(resolvers) == 0 {
		if fallback := net.ParseIP(strings.TrimSpace(cfg.Gateway.UpstreamGateway)); fallback != nil && fallback.To4() != nil && fallback.String() != listen && fallback.String() != "100.100.100.100" {
			resolvers = []string{fallback.To4().String()}
		}
	}
	if cfg.DNS.ResolverMode != "custom" && len(resolvers) == 0 {
		return "", fmt.Errorf("resolver view has no real DNS upstream")
	}

	defaultView := device.DNSViewResolver
	if cfg.Gateway.Mode == config.GatewayModeIsolatedLAN || cfg.Gateway.Mode == config.GatewayModeSameWiFiDHCP {
		defaultView = device.DNSViewGateway
	}

	var out strings.Builder
	out.WriteString("server-name opensurge\n")
	if cfg.DNS.ResolverProxyEnabled {
		fmt.Fprintf(&out, "proxy-server socks5://127.0.0.1:%d -name opensurge-dns\n", config.SmartDNSProxyPort)
	}
	for _, upstream := range cfg.DNS.ResolverUpstreams {
		endpoint, _ := config.ParseResolverUpstream(upstream, cfg)
		if cfg.DNS.ResolverMode == "custom" && endpoint.Encrypted {
			// Explicit CA configuration makes SmartDNS fail closed if trust roots
			// cannot be loaded, instead of disabling certificate verification.
			out.WriteString("ca-file /etc/ssl/certs/ca-certificates.crt\n")
			break
		}
	}
	out.WriteString("log-level info\n")
	out.WriteString("log-console yes\n")
	out.WriteString("cache-size 32768\n")
	out.WriteString("max-reply-ip-num 8\n")
	out.WriteString("group-begin " + GatewayGroup + " -inherit none\n")
	out.WriteString("speed-check-mode none\n")
	out.WriteString("dualstack-ip-selection no\n")
	out.WriteString("serve-expired no\n")
	out.WriteString("group-end\n")
	out.WriteString("group-begin " + LocalGroup + " -inherit none\n")
	out.WriteString("speed-check-mode none\n")
	out.WriteString("dualstack-ip-selection no\n")
	out.WriteString("serve-expired no\n")
	out.WriteString("group-end\n")
	out.WriteString("group-begin " + ResolverGroup + " -inherit none\n")
	out.WriteString("prefetch-domain yes\n")
	out.WriteString("serve-expired yes\n")
	out.WriteString("serve-expired-ttl 600\n")
	out.WriteString("serve-expired-reply-ttl 3\n")
	if cfg.DNS.ResolverProxyEnabled {
		// Direct IP probes measure the NAS path, not the chosen proxy's path.
		out.WriteString("speed-check-mode none\n")
		out.WriteString("response-mode fastest-response\n")
	} else {
		out.WriteString("speed-check-mode tcp:443,tcp:80\n")
		out.WriteString("response-mode fastest-ip\n")
	}
	out.WriteString("dualstack-ip-selection no\n")
	out.WriteString("group-end\n")

	listenTarget := net.JoinHostPort(listen, fmt.Sprintf("%d", cfg.DNS.Port))
	defaultOptions := resolverOptions()
	if defaultView == device.DNSViewGateway {
		defaultOptions = gatewayOptions()
	}
	fmt.Fprintf(&out, "bind %s %s\n", listenTarget, defaultOptions)
	fmt.Fprintf(&out, "bind-tcp %s %s\n", listenTarget, defaultOptions)
	if cfg.DNS.MihomoUseSmartDNS {
		// Feeding Mihomo from the LAN bind would recurse in Gateway View.
		// The private listener always obtains real answers from Resolver View.
		fmt.Fprintf(&out, "bind %s %s\n", config.SmartDNSResolverAddr, resolverOptions())
		fmt.Fprintf(&out, "bind-tcp %s %s\n", config.SmartDNSResolverAddr, resolverOptions())
	}

	fmt.Fprintf(&out, "server %s -group %s -exclude-default-group\n", mihomoDNS, GatewayGroup)
	if cfg.DNS.ResolverMode == "custom" {
		hasEncrypted := false
		for _, upstream := range cfg.DNS.ResolverUpstreams {
			endpoint, _ := config.ParseResolverUpstream(upstream, cfg)
			hasEncrypted = hasEncrypted || endpoint.Encrypted
		}
		for _, upstream := range cfg.DNS.ResolverUpstreams {
			endpoint, _ := config.ParseResolverUpstream(upstream, cfg)
			directive := map[string]string{"https": "server-https", "tls": "server-tls", "udp": "server", "tcp": "server-tcp"}[endpoint.Scheme]
			fmt.Fprintf(&out, "%s %s -group %s -exclude-default-group", directive, endpoint.Target, ResolverGroup)
			if endpoint.Encrypted {
				fmt.Fprintf(&out, " -host-ip %s -host-name %s -tls-host-verify %s", endpoint.BootstrapIPv4, endpoint.Hostname, endpoint.Hostname)
			} else if hasEncrypted {
				// Plaintext is a user-authorized fallback, never a faster competitor to
				// a working encrypted resolver. Do not add system/router fallbacks.
				out.WriteString(" -fallback")
			}
			if cfg.DNS.ResolverProxyEnabled {
				out.WriteString(" -proxy opensurge-dns")
			}
			out.WriteString("\n")
		}
	} else {
		for _, resolver := range resolvers {
			fmt.Fprintf(&out, "server %s -group %s -exclude-default-group\n", resolver, ResolverGroup)
		}
	}
	if dhcp.ShouldRun(cfg) {
		localDNS := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", dhcp.LocalDNSPort))
		fmt.Fprintf(&out, "server %s -group %s -exclude-default-group\n", localDNS, LocalGroup)
		if domain := strings.Trim(strings.ToLower(strings.TrimSpace(cfg.DHCP.Domain)), "."); domain != "" {
			for _, group := range []string{GatewayGroup, ResolverGroup} {
				fmt.Fprintf(&out, "domain-rules /%s/ -nameserver %s -no-cache -no-serve-expired -group %s\n", domain, LocalGroup, group)
			}
		}
	}

	bundle := cfg.DevicePolicy.Bundle
	if bundle != nil {
		devices := append([]device.CompiledDevice(nil), bundle.Compiled.Devices...)
		sort.Slice(devices, func(i, j int) bool { return devices[i].IPv4 < devices[j].IPv4 })
		for _, managed := range devices {
			view := managed.DNSView
			if view == "" || view == device.DNSViewAuto {
				if managed.GatewayTarget == device.GatewayTargetUpstreamRouter {
					view = device.DNSViewResolver
				} else {
					view = device.DNSViewGateway
				}
			}
			options := resolverOptions()
			if view == device.DNSViewGateway {
				options = gatewayOptions()
			}
			fmt.Fprintf(&out, "client-rules %s/32 %s\n", managed.IPv4, options)
		}
		writeLocalDeviceRules(&out, cfg, bundle)
	}

	renderedMihomo, err := mihomo.RenderConfig(cfg)
	if err != nil {
		return "", fmt.Errorf("render effective mihomo config for resolver hosts: %w", err)
	}
	hostRules, err := compatibleResolverHosts([]byte(renderedMihomo))
	if err != nil {
		return "", err
	}
	for _, rule := range hostRules {
		fmt.Fprintf(&out, "domain-rules /%s/ -address %s -group %s\n", rule.Domain, strings.Join(rule.IPs, ","), ResolverGroup)
	}
	// A different upstream, TLS identity, view, hosts rule or proxy profile
	// gets a fresh cache. Even with persistence disabled, name an owned path:
	// SmartDNS unlinks its cache-file on exit when cache-persist is no.
	policy := out.String()
	if cfg.DNS.ResolverProxyEnabled {
		policy += "\n" + renderedMihomo
	}
	digest := sha256.Sum256([]byte(policy))
	cacheFile := filepath.Join(paths.Dir, "smartdns-cache", fmt.Sprintf("resolver-%x.cache", digest))
	fmt.Fprintf(&out, "cache-file %q\n", cacheFile)
	if cfg.DNS.ResolverMode == "custom" && !cfg.DNS.ResolverAllowPlaintext {
		// Keep warm, authenticated real-IP answers across restarts.
		out.WriteString("cache-persist yes\ncache-checkpoint-time 3600\n")
	} else {
		// A plaintext fallback may have supplied cached answers. Do not carry
		// those, or legacy system/router answers, into a later clean session.
		out.WriteString("cache-persist no\n")
	}
	return out.String(), nil
}

func gatewayOptions() string {
	return "-group " + GatewayGroup + " -no-speed-check -no-cache -no-dualstack-selection -force-aaaa-soa -no-serve-expired"
}

func resolverOptions() string {
	return "-group " + ResolverGroup + " -force-aaaa-soa"
}

func normalizeResolvers(values []string, cfg config.Config) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	self := strings.TrimSpace(cfg.DNS.Listen)
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		ip := net.ParseIP(raw)
		if ip == nil || ip.To4() == nil {
			continue
		}
		value := ip.To4().String()
		// Loopback resolvers belong to the current namespace. Docker's embedded
		// 127.0.0.11 can forward to host-only DNS (for example Tailscale MagicDNS),
		// which is unreachable from QNET/macvlan. Tailscale 100.100.100.100
		// is also device-local and must never be copied from host DNS to an
		// isolated container resolver. Fall back to the real upstream router.
		if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || value == "100.100.100.100" || value == self || value == strings.TrimSpace(cfg.Gateway.LANIP) || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func systemResolvers(path string, cfg config.Config) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read system resolver list: %w", err)
	}
	var values []string
	for _, raw := range strings.Split(string(data), "\n") {
		fields := strings.Fields(raw)
		if len(fields) == 2 && fields[0] == "nameserver" {
			values = append(values, fields[1])
		}
	}
	return normalizeResolvers(values, cfg), nil
}

func writeLocalDeviceRules(out *strings.Builder, cfg config.Config, bundle *device.PolicyBundle) {
	if bundle == nil {
		return
	}
	domain := strings.Trim(strings.ToLower(strings.TrimSpace(cfg.DHCP.Domain)), ".")
	for _, managed := range bundle.Policy.Devices {
		ip := net.ParseIP(strings.TrimSpace(managed.IPv4))
		if ip == nil || ip.To4() == nil {
			continue
		}
		name := sanitizeLocalName(managed.Name)
		if name == "" {
			name = sanitizeLocalName(managed.ID)
		}
		if name == "" {
			continue
		}
		for _, group := range []string{GatewayGroup, ResolverGroup} {
			fmt.Fprintf(out, "domain-rules /%s/ -address %s -group %s\n", name, ip.To4().String(), group)
			if domain != "" {
				fmt.Fprintf(out, "domain-rules /%s.%s/ -address %s -group %s\n", name, domain, ip.To4().String(), group)
			}
		}
	}
}

func sanitizeLocalName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || strings.ContainsAny(value, " /\\\t\r\n") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return ""
	}
	return value
}

type resolverHostRule struct {
	Domain string
	IPs    []string
}

func compatibleResolverHosts(rendered []byte) ([]resolverHostRule, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(rendered, &document); err != nil {
		return nil, fmt.Errorf("parse effective mihomo config for resolver hosts: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("effective mihomo config must be a mapping")
	}
	root := document.Content[0]
	var hosts *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "hosts" {
			hosts = root.Content[i+1]
			break
		}
	}
	if hosts == nil {
		return nil, nil
	}
	if hosts.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("effective mihomo hosts must be a mapping")
	}
	rules := make([]resolverHostRule, 0, len(hosts.Content)/2)
	for i := 0; i+1 < len(hosts.Content); i += 2 {
		domain := strings.ToLower(strings.TrimSpace(hosts.Content[i].Value))
		if !isExactHostDomain(domain) {
			continue
		}
		value := hosts.Content[i+1]
		values := []*yaml.Node{value}
		if value.Kind == yaml.SequenceNode {
			values = value.Content
		}
		var ips []string
		compatible := true
		for _, node := range values {
			if node.Kind != yaml.ScalarNode {
				compatible = false
				break
			}
			ip := net.ParseIP(strings.TrimSpace(node.Value))
			if ip == nil || ip.To4() == nil {
				compatible = false
				break
			}
			ips = append(ips, ip.To4().String())
		}
		if compatible && len(ips) > 0 {
			rules = append(rules, resolverHostRule{Domain: domain, IPs: dedupe(ips)})
		}
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Domain < rules[j].Domain })
	return rules, nil
}

func isExactHostDomain(value string) bool {
	if value == "" || strings.HasPrefix(value, "*.") || strings.HasPrefix(value, "+.") || strings.ContainsAny(value, " /\\\t\r\n") {
		return false
	}
	return true
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

// normalizeYAML is used by tests to make semantic config snippets easier to
// compare without depending on yaml.v3's formatting.
func normalizeYAML(data []byte) []byte {
	return bytes.TrimSpace(data)
}
