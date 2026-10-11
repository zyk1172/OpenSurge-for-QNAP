package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
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

func ValidateResolverConfig(cfg Config) error {
	mode := cfg.DNS.ResolverMode
	if mode == "" {
		mode = "system"
	}
	if mode != "system" && mode != "custom" {
		return fmt.Errorf("dns.resolver_mode must be system or custom")
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
		key := endpoint.Scheme + "|" + endpoint.Target + "|" + endpoint.BootstrapIPv4
		if seen[key] {
			return fmt.Errorf("dns.resolver_upstreams contains a duplicate endpoint")
		}
		seen[key] = true
	}
	return nil
}
