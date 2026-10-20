package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// Both optional SmartDNS integration listeners are private to the container.
const (
	SmartDNSResolverAddr = "127.0.0.1:5354"
	SmartDNSProxyPort    = 7854
)

// ResolverEndpoint is validated before any address is emitted as a SmartDNS
// configuration token. Encrypted endpoints use a DNS name for TLS identity and
// a fixed IPv4 for transport, without a dependency on system bootstrap DNS.
type ResolverEndpoint struct {
	Scheme        string
	Target        string
	Hostname      string
	BootstrapIPv4 string
	Encrypted     bool
}

func ParseResolverUpstream(upstream ResolverUpstream, cfg Config) (ResolverEndpoint, error) {
	address := strings.TrimSpace(upstream.Address)
	if address == "" || len(address) > 2048 || strings.IndexFunc(address, unicode.IsSpace) >= 0 || strings.ContainsAny(address, "\"'#,|\\") {
		return ResolverEndpoint{}, fmt.Errorf("resolver upstream must be one DNS address without whitespace or configuration options")
	}
	if !strings.Contains(address, "://") {
		address = "udp://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return ResolverEndpoint{}, fmt.Errorf("invalid resolver upstream URL")
	}
	endpoint := ResolverEndpoint{Scheme: u.Scheme, Target: u.Host, Hostname: u.Hostname(), BootstrapIPv4: strings.TrimSpace(upstream.BootstrapIPv4)}
	switch u.Scheme {
	case "https":
		endpoint.Encrypted = true
		if u.Path == "" || u.Path == "/" {
			return ResolverEndpoint{}, fmt.Errorf("DoH upstream requires a query path, such as /dns-query")
		}
		endpoint.Target = u.String()
	case "tls":
		endpoint.Encrypted = true
		if u.Path != "" || u.RawQuery != "" {
			return ResolverEndpoint{}, fmt.Errorf("DoT upstream must be tls://hostname[:port]")
		}
	case "udp", "tcp":
		if u.Path != "" || u.RawQuery != "" {
			return ResolverEndpoint{}, fmt.Errorf("plain DNS upstream must be IPv4[:port]")
		}
	default:
		return ResolverEndpoint{}, fmt.Errorf("resolver upstream supports only https, tls, udp and tcp")
	}
	if strings.Contains(u.Host, ":") && u.Port() == "" {
		return ResolverEndpoint{}, fmt.Errorf("resolver upstream has an invalid port or IPv6 address")
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return ResolverEndpoint{}, fmt.Errorf("resolver upstream port must be between 1 and 65535")
		}
	}
	if endpoint.Encrypted {
		if !validResolverHostname(endpoint.Hostname) {
			return ResolverEndpoint{}, fmt.Errorf("encrypted resolver upstream must use its TLS hostname, not an IP address")
		}
		if !validResolverIPv4(endpoint.BootstrapIPv4, cfg) {
			return ResolverEndpoint{}, fmt.Errorf("encrypted resolver upstream requires a reachable bootstrap_ipv4, excluding loopback, OpenSurge and MagicDNS")
		}
	} else {
		if !validResolverIPv4(endpoint.Hostname, cfg) {
			return ResolverEndpoint{}, fmt.Errorf("plain resolver upstream must use a reachable IPv4, excluding loopback, OpenSurge and MagicDNS")
		}
		if endpoint.BootstrapIPv4 != "" {
			return ResolverEndpoint{}, fmt.Errorf("plain resolver upstream does not use bootstrap_ipv4")
		}
	}
	return endpoint, nil
}

func validResolverHostname(host string) bool {
	if len(host) > 253 || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	return true
}

func validResolverIPv4(value string, cfg Config) bool {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) || ip.IsLinkLocalUnicast() {
		return false
	}
	return value != "100.100.100.100" && !ip.Equal(net.ParseIP(cfg.DNS.Listen)) && !ip.Equal(net.ParseIP(cfg.Gateway.LANIP))
}

// ProxyNodeResolverUpstreams keeps old installations bootable without adding a
// DNS provider they did not choose. Once a separate list is saved, ordinary
// resolver edits never alter node/bootstrap DNS. The Web UI exposes the legacy
// inheritance and requires an explicit list before saving an integration.
func ProxyNodeResolverUpstreams(cfg Config) []ResolverUpstream {
	if len(cfg.DNS.ProxyNodeUpstreams) > 0 {
		return cfg.DNS.ProxyNodeUpstreams
	}
	var upstreams []ResolverUpstream
	for _, upstream := range cfg.DNS.ResolverUpstreams {
		endpoint, err := ParseResolverUpstream(upstream, cfg)
		if err == nil && endpoint.Encrypted {
			upstreams = append(upstreams, upstream)
		}
	}
	return upstreams
}

func ValidateResolverConfig(cfg Config) error {
	mode := cfg.DNS.ResolverMode
	if mode == "" {
		mode = "system"
	}
	if mode != "system" && mode != "custom" {
		return fmt.Errorf("dns.resolver_mode must be system or custom")
	}
	integration := cfg.DNS.ResolverProxyEnabled || cfg.DNS.MihomoUseSmartDNS
	if integration && mode != "custom" {
		return fmt.Errorf("SmartDNS proxy and Mihomo integration require custom trusted upstreams")
	}
	if cfg.DNS.ResolverProxyEnabled {
		target := cfg.DNS.ResolverProxyTarget
		if strings.TrimSpace(target) == "" || len(target) > 256 || strings.ContainsAny(target, "\"\\") || strings.IndexFunc(target, unicode.IsControl) >= 0 {
			return fmt.Errorf("dns.resolver_proxy_target requires a valid proxy or group name")
		}
		switch strings.ToUpper(target) {
		case "DIRECT", "REJECT", "REJECT-DROP", "REJECT-TINYGIF", "PASS", "COMPATIBLE", "GLOBAL":
			return fmt.Errorf("dns.resolver_proxy_target must select a proxy or group, not a built-in routing target")
		}
	}
	if len(cfg.DNS.ResolverUpstreams) > 16 {
		return fmt.Errorf("dns.resolver_upstreams supports at most 16 upstreams")
	}
	if mode == "custom" && len(cfg.DNS.ResolverUpstreams) == 0 {
		return fmt.Errorf("custom Resolver View requires at least one upstream")
	}
	seen := map[string]bool{}
	for _, upstream := range cfg.DNS.ResolverUpstreams {
		endpoint, err := ParseResolverUpstream(upstream, cfg)
		if err != nil {
			return fmt.Errorf("dns.resolver_upstreams: %w", err)
		}
		if !endpoint.Encrypted && !cfg.DNS.ResolverAllowPlaintext {
			return fmt.Errorf("plain DNS requires dns.resolver_allow_plaintext; prefer authenticated DoH/DoT")
		}
		if cfg.DNS.ResolverProxyEnabled && endpoint.Scheme == "udp" {
			return fmt.Errorf("SmartDNS proxy requires DoH, DoT or TCP upstreams; UDP cannot bypass the selected proxy")
		}
		key := endpoint.Scheme + "|" + endpoint.Target + "|" + endpoint.BootstrapIPv4
		if seen[key] {
			return fmt.Errorf("dns.resolver_upstreams contains a duplicate endpoint")
		}
		seen[key] = true
	}
	if len(cfg.DNS.ProxyNodeUpstreams) > 16 {
		return fmt.Errorf("dns.proxy_node_upstreams supports at most 16 upstreams")
	}
	nodeUpstreams := cfg.DNS.ProxyNodeUpstreams
	if integration {
		nodeUpstreams = ProxyNodeResolverUpstreams(cfg)
	}
	seen = map[string]bool{}
	for _, upstream := range nodeUpstreams {
		endpoint, err := ParseResolverUpstream(upstream, cfg)
		if err != nil {
			return fmt.Errorf("dns.proxy_node_upstreams: %w", err)
		}
		if !endpoint.Encrypted {
			return fmt.Errorf("dns.proxy_node_upstreams requires authenticated DoH or DoT")
		}
		u, _ := url.Parse(endpoint.Target)
		if u.RawQuery != "" {
			return fmt.Errorf("Mihomo encrypted bootstrap does not support DoH query parameters")
		}
		key := endpoint.Scheme + "|" + endpoint.Target + "|" + endpoint.BootstrapIPv4
		if seen[key] {
			return fmt.Errorf("dns.proxy_node_upstreams contains a duplicate endpoint")
		}
		seen[key] = true
	}
	if integration && len(nodeUpstreams) == 0 {
		return fmt.Errorf("SmartDNS integration requires at least one encrypted upstream for independent proxy-node bootstrap")
	}
	ports := []int{}
	if cfg.DNS.MihomoUseSmartDNS {
		ports = append(ports, 5354)
	}
	if cfg.DNS.ResolverProxyEnabled {
		ports = append(ports, SmartDNSProxyPort)
	}
	_, apiPort, _ := net.SplitHostPort(cfg.Mihomo.APIAddr)
	for _, port := range ports {
		if cfg.DNS.Port == port || cfg.Mihomo.MixedPort == port || apiPort == strconv.Itoa(port) || cfg.LANProxy.Enabled && cfg.LANProxy.SOCKSPort == port {
			return fmt.Errorf("port %d is reserved for enabled SmartDNS integration", port)
		}
	}
	return nil
}
