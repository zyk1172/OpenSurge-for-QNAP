package mihomo

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/process"
)

// Proxy node DNS must work before SmartDNS starts and must never depend on the
// same proxy it is trying to establish. Only the user's encrypted upstreams
// bootstrap it, directly, with fixed transport IPs and authenticated TLS names.
func renderSmartDNSIntegration(cfg config.Config, imported *importedProfile, fieldsYAML, hostsYAML string) (string, string, error) {
	if err := config.ValidateResolverConfig(cfg); err != nil {
		return "", "", err
	}
	if cfg.DNS.ResolverProxyEnabled {
		target := cfg.DNS.ResolverProxyTarget
		available := false
		if imported != nil {
			_, available = imported.inventory.targets[target]
		} else {
			available = cfg.UpstreamProxy.Enabled && (target == cfg.UpstreamProxy.Name || target == "open-surge-egress")
		}
		if !available {
			return "", "", fmt.Errorf("SmartDNS proxy target %q is not a proxy or group in the active profile", target)
		}
	}
	fields := map[string]any{}
	if err := yaml.Unmarshal([]byte(fieldsYAML), &fields); err != nil {
		return "", "", fmt.Errorf("parse DNS integration fields: %w", err)
	}
	hosts := map[string]any{}
	if err := yaml.Unmarshal([]byte(hostsYAML), &hosts); err != nil {
		return "", "", fmt.Errorf("parse DNS integration hosts: %w", err)
	}
	if hosts == nil {
		hosts = map[string]any{}
	}
	if cfg.DNS.MihomoUseSmartDNS {
		// Preserve fake-IP filters and hosts, but remove every subscription path
		// that could resolve ordinary domains outside the selected SmartDNS pool.
		for _, key := range []string{"nameserver", "fallback", "fallback-filter", "fallback-lazy-query", "nameserver-policy", "proxy-server-nameserver", "proxy-server-nameserver-policy", "direct-nameserver", "direct-nameserver-follow-policy", "default-nameserver", "respect-rules", "prefer-h3"} {
			delete(fields, key)
		}
		fields["nameserver"] = []string{"udp://" + config.SmartDNSResolverAddr + "#DIRECT"}
		fields["direct-nameserver"] = []string{"udp://" + config.SmartDNSResolverAddr + "#DIRECT"}
		fields["direct-nameserver-follow-policy"] = false
		fields["respect-rules"] = false
	}
	// An imported proxy DNS policy may itself use SmartDNS or a proxy group.
	// Always replace it when enabling either integration option.
	delete(fields, "proxy-server-nameserver-policy")
	var bootstrap, proxyServers []string
	fixedHosts := map[string][]string{}
	for _, upstream := range cfg.DNS.ResolverUpstreams {
		endpoint, _ := config.ParseResolverUpstream(upstream, cfg)
		if !endpoint.Encrypted {
			continue // plaintext never participates in node bootstrap
		}
		u, _ := url.Parse(strings.TrimSpace(upstream.Address))
		proxyServers = append(proxyServers, u.String()+"#DIRECT")
		host := strings.ToLower(endpoint.Hostname)
		if !slices.Contains(fixedHosts[host], endpoint.BootstrapIPv4) {
			fixedHosts[host] = append(fixedHosts[host], endpoint.BootstrapIPv4)
		}
		port := u.Port()
		if port == "" {
			if endpoint.Scheme == "https" {
				port = "443"
			} else {
				port = "853"
			}
		}
		u.Host = net.JoinHostPort(endpoint.BootstrapIPv4, port)
		// Pinned Mihomo's name-cert-verify checks both chain trust and DNSName;
		// it is an identity override, never skip-cert-verify.
		u.Fragment = "DIRECT&name-cert-verify=" + endpoint.Hostname
		bootstrap = append(bootstrap, u.String())
	}
	for host, ips := range fixedHosts {
		hosts[host] = ips
	}
	fields["use-hosts"] = true
	fields["use-system-hosts"] = false
	fields["default-nameserver"] = bootstrap
	fields["proxy-server-nameserver"] = proxyServers
	encodedFields, err := yaml.Marshal(fields)
	if err != nil {
		return "", "", err
	}
	encodedHosts, err := yaml.Marshal(hosts)
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(string(encodedFields)), strings.TrimSpace(string(encodedHosts)), nil
}

// The API starts before inbound listeners. Confirm this specific listener in
// the current process's rotated startup log rather than accepting a connection
// to a port that could belong to another process.
func (m Manager) waitForSmartDNSProxyListener(pid int, timeout time.Duration) error {
	ready := fmt.Sprintf("SOCKS[open-surge/smartdns] proxy listening at: 127.0.0.1:%d", config.SmartDNSProxyPort)
	const failure = "Listener open-surge/smartdns listen err:"
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !process.IsAlive(pid) {
			return fmt.Errorf("mihomo pid %d exited while the SmartDNS proxy listener was starting", pid)
		}
		data, _ := os.ReadFile(m.paths.MihomoLog)
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, failure) {
				return fmt.Errorf("SmartDNS proxy listener failed to start: %s", strings.TrimSpace(line))
			}
		}
		if strings.Contains(string(data), ready) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("SmartDNS proxy listener not ready after %s", timeout)
}
