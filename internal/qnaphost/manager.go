package qnaphost

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/gateway"
)

const (
	Endpoint                     = "/api/v1/qnap-host-routing"
	stateFileName                = "qnap-host-routing.json"
	defaultHostNetNS             = "/run/opensurge/host-netns"
	routeTableID                 = "20242"
	routeProtocol                = "242"
	hostDNSUDPRulePriority       = "19996"
	hostDNSTCPRulePriority       = "19997"
	mainRulePriority             = "19998"
	proxyRulePriority            = "19999"
	preferredHostPriorityMax     = 19999
	minimumHostPriority          = 1000
	publicRouteProbeIPv4         = "1.1.1.1"
	containerDNSRouteTableID     = "20243"
	containerDNSRouteProtocol    = "243"
	containerDNSUDPRulePriority  = "20239"
	containerDNSTCPRulePriority  = "20240"
	policyCapabilityProbeHost    = "42990"
	policyCapabilityProbeGateway = "42991"
	hostRoutingStateSchema       = 3
	tailscaleInterface           = "tailscale0"
	tailscaleDNSIPv4             = "100.100.100.100"
	DNSModeAuto                  = "auto"
	DNSModeOpenSurge             = "opensurge"
	DNSModeHost                  = "host"
)

type Status struct {
	SchemaVersion            int                   `json:"schema_version"`
	Supported                bool                  `json:"supported"`
	Desired                  bool                  `json:"desired"`
	Enabled                  bool                  `json:"enabled"`
	GatewayReady             bool                  `json:"gateway_ready"`
	HostIPv4                 string                `json:"host_ipv4,omitempty"`
	HostInterface            string                `json:"host_interface,omitempty"`
	GatewayIPv4              string                `json:"gateway_ipv4,omitempty"`
	FallbackGateway          string                `json:"fallback_gateway,omitempty"`
	DNSRedirect              bool                  `json:"dns_redirect"`
	DNSMode                  string                `json:"dns_mode"`
	ProtectTailscale         bool                  `json:"protect_tailscale"`
	TailscaleDetected        bool                  `json:"tailscale_detected"`
	TailscaleInterface       string                `json:"tailscale_interface,omitempty"`
	TailscaleDNSProtected    bool                  `json:"tailscale_dns_protected"`
	TailscaleRoutesProtected bool                  `json:"tailscale_routes_protected"`
	HostRulePriorities       *hostPolicyPriorities `json:"host_rule_priorities,omitempty"`
	Error                    string                `json:"error,omitempty"`
	CheckedAt                time.Time             `json:"checked_at"`
}

type hostPolicyPriorities struct {
	DNSUDP int `json:"dns_udp"`
	DNSTCP int `json:"dns_tcp"`
	Main   int `json:"main"`
	Proxy  int `json:"proxy"`
}

func (p hostPolicyPriorities) valid() bool {
	return p.DNSUDP >= minimumHostPriority && p.DNSUDP < p.DNSTCP && p.DNSTCP < p.Main && p.Main < p.Proxy
}

func (p hostPolicyPriorities) strings() (string, string, string, string) {
	return strconv.Itoa(p.DNSUDP), strconv.Itoa(p.DNSTCP), strconv.Itoa(p.Main), strconv.Itoa(p.Proxy)
}

func defaultHostPriorities() hostPolicyPriorities {
	return hostPolicyPriorities{DNSUDP: 19996, DNSTCP: 19997, Main: 19998, Proxy: 19999}
}

func legacyHostPriorities() hostPolicyPriorities {
	return hostPolicyPriorities{DNSUDP: 24090, DNSTCP: 24091, Main: 24100, Proxy: 24110}
}

type intent struct {
	SchemaVersion    int                   `json:"schema_version"`
	Enabled          bool                  `json:"enabled"`
	HostPriorities   *hostPolicyPriorities `json:"host_priorities,omitempty"`
	DNSMode          string                `json:"dns_mode"`
	ProtectTailscale bool                  `json:"protect_tailscale"`
}

type commandRunner interface {
	Run(context.Context, []byte, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(input) > 0 {
		cmd.Stdin = bytes.NewReader(input)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return output, fmt.Errorf("%s: %s", strings.Join(append([]string{name}, args...), " "), message)
	}
	return output, nil
}

type Manager struct {
	configPath string
	statePath  string
	netNSPath  string
	runner     commandRunner
	mu         sync.Mutex
}

func New(configPath, storeDir string) *Manager {
	netNSPath := strings.TrimSpace(os.Getenv("OPENSURGE_HOST_NETNS_PATH"))
	if netNSPath == "" {
		netNSPath = defaultHostNetNS
	}
	return &Manager{
		configPath: configPath,
		statePath:  filepath.Join(storeDir, stateFileName),
		netNSPath:  netNSPath,
		runner:     execRunner{},
	}
}

func (m *Manager) Run(ctx context.Context) {
	m.reconcile(ctx)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = m.Release(releaseCtx)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reconcile(ctx)
		}
	}
}

func (m *Manager) Release(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.disableLocked(ctx)
}

func (m *Manager) SetDesired(ctx context.Context, enabled bool) (Status, error) {
	return m.SetDesiredWithPolicy(ctx, enabled, "", nil)
}

func (m *Manager) SetDesiredWithPolicy(ctx context.Context, enabled bool, dnsMode string, protectTailscale *bool) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := m.readStateLocked()
	if err != nil {
		return Status{}, err
	}
	if dnsMode != "" {
		if !validDNSMode(dnsMode) {
			return m.statusLocked(ctx), fmt.Errorf("unsupported NAS DNS mode %q", dnsMode)
		}
		state.DNSMode = dnsMode
	}
	if protectTailscale != nil {
		state.ProtectTailscale = *protectTailscale
	}
	if err := m.writeStateLocked(state); err != nil {
		return Status{}, err
	}

	if !enabled {
		state.Enabled = false
		if err := m.writeStateLocked(state); err != nil {
			return Status{}, err
		}
		if err := m.disableLocked(ctx); err != nil {
			status := m.statusLocked(ctx)
			status.Error = err.Error()
			return status, err
		}
		return m.statusLocked(ctx), nil
	}

	readiness, err := gateway.ReadinessConfig(ctx, m.configPath)
	if err != nil {
		status := m.statusLocked(ctx)
		status.Error = err.Error()
		return status, fmt.Errorf("OpenSurge gateway readiness check failed: %w", err)
	}
	if !readiness.Ready || !readiness.DesiredRunning {
		return m.statusLocked(ctx), fmt.Errorf("OpenSurge gateway must be running and ready before NAS host takeover can be enabled")
	}
	if err := m.enableLocked(ctx); err != nil {
		_ = m.disableLocked(ctx)
		state, _ = m.readStateLocked()
		state.Enabled = false
		_ = m.writeStateLocked(state)
		status := m.statusLocked(ctx)
		status.Error = err.Error()
		return status, err
	}
	state, err = m.readStateLocked()
	if err != nil {
		_ = m.disableLocked(ctx)
		return m.statusLocked(ctx), err
	}
	state.Enabled = true
	if err := m.writeStateLocked(state); err != nil {
		_ = m.disableLocked(ctx)
		return m.statusLocked(ctx), fmt.Errorf("persist NAS host takeover intent: %w", err)
	}
	return m.statusLocked(ctx), nil
}

func (m *Manager) Status(ctx context.Context) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(ctx)
}

func (m *Manager) reconcile(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readStateLocked()
	if err != nil || !state.Enabled {
		_ = m.disableLocked(ctx)
		return
	}
	readiness, err := gateway.ReadinessConfig(ctx, m.configPath)
	if err != nil || !readiness.Ready || !readiness.DesiredRunning {
		_ = m.disableLocked(ctx)
		return
	}
	status := m.statusLocked(ctx)
	needsDNS := state.DNSMode != DNSModeHost
	if !status.Enabled || (needsDNS && !status.DNSRedirect) {
		_ = m.enableLocked(ctx)
	}
}

func (m *Manager) statusLocked(ctx context.Context) Status {
	state, stateErr := m.readStateLocked()
	status := Status{
		SchemaVersion:    2,
		Desired:          state.Enabled,
		DNSMode:          state.DNSMode,
		ProtectTailscale: state.ProtectTailscale,
		CheckedAt:        time.Now().UTC(),
	}
	if stateErr != nil {
		status.Error = stateErr.Error()
		return status
	}
	cfg, err := config.Load(m.configPath)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.GatewayIPv4 = cfg.Gateway.LANIP
	status.FallbackGateway = cfg.Gateway.UpstreamGateway
	if _, err := os.Stat(m.netNSPath); err != nil {
		status.Error = "host network namespace is not mounted; rebuild the QNAP container with the current Compose file"
		return status
	}
	if readiness, err := gateway.ReadinessConfig(ctx, m.configPath); err == nil {
		status.GatewayReady = readiness.Ready && readiness.DesiredRunning
	}
	iface, hostIP, err := m.detectHostLocked(ctx, cfg.Gateway.LANIP)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Supported = true
	status.HostInterface = iface
	status.HostIPv4 = hostIP

	if _, err := m.runHost(ctx, nil, "ip", "link", "show", "dev", tailscaleInterface); err == nil {
		status.TailscaleDetected = true
		status.TailscaleInterface = tailscaleInterface
	}

	priorities := m.hostPrioritiesLocked()
	status.HostRulePriorities = &priorities
	dnsUDP, dnsTCP, mainPriority, proxyPriority := priorities.strings()
	hostRules, hostRulesErr := m.runHost(ctx, nil, "ip", "-4", "rule", "show")
	hostRoutes, hostRoutesErr := m.runHost(ctx, nil, "ip", "-4", "route", "show", "table", routeTableID)
	rulesInstalled := hostRulesErr == nil && hostRoutesErr == nil &&
		ruleLineContains(hostRules, mainPriority, "lookup main", "suppress_prefixlength 0") &&
		ruleLineContains(hostRules, proxyPriority, "lookup "+routeTableID) &&
		strings.Contains(string(hostRoutes), "default via "+cfg.Gateway.LANIP) &&
		strings.Contains(string(hostRoutes), "proto "+routeProtocol)
	if rulesInstalled {
		publicRoute, routeErr := m.runHost(ctx, nil, "ip", "-4", "route", "get", publicRouteProbeIPv4, "from", hostIP)
		status.Enabled = routeErr == nil && routeUsesGatewayTable(publicRoute, cfg.Gateway.LANIP, routeTableID)
		if !status.Enabled && routeErr == nil {
			status.Error = "NAS host policy is installed but an earlier host policy rule still wins: " + strings.TrimSpace(string(publicRoute))
		}
	}

	if state.DNSMode != DNSModeHost {
		containerRules, containerRulesErr := m.runContainer(ctx, nil, "ip", "-4", "rule", "show")
		containerRoutes, containerRoutesErr := m.runContainer(ctx, nil, "ip", "-4", "route", "show", "table", containerDNSRouteTableID)
		hostDNSRules := hostRulesErr == nil &&
			ruleLineContains(hostRules, dnsUDP, "ipproto udp", "dport 53", "lookup "+routeTableID) &&
			ruleLineContains(hostRules, dnsTCP, "ipproto tcp", "dport 53", "lookup "+routeTableID)
		containerDNSRules := containerRulesErr == nil && containerRoutesErr == nil &&
			ruleLineContains(containerRules, containerDNSUDPRulePriority, hostIP, "ipproto udp", "dport 53", "lookup "+containerDNSRouteTableID) &&
			ruleLineContains(containerRules, containerDNSTCPRulePriority, hostIP, "ipproto tcp", "dport 53", "lookup "+containerDNSRouteTableID) &&
			strings.Contains(string(containerRoutes), "default dev "+cfg.Transparent.TUNDevice) &&
			strings.Contains(string(containerRoutes), "proto "+containerDNSRouteProtocol)
		if hostDNSRules && containerDNSRules {
			udpRoute, udpErr := m.runHost(ctx, nil, "ip", "-4", "route", "get", cfg.Gateway.UpstreamGateway, "from", hostIP, "ipproto", "udp", "dport", "53")
			tcpRoute, tcpErr := m.runHost(ctx, nil, "ip", "-4", "route", "get", cfg.Gateway.UpstreamGateway, "from", hostIP, "ipproto", "tcp", "dport", "53")
			status.DNSRedirect = udpErr == nil && tcpErr == nil &&
				routeUsesGatewayTable(udpRoute, cfg.Gateway.LANIP, routeTableID) &&
				routeUsesGatewayTable(tcpRoute, cfg.Gateway.LANIP, routeTableID)
			if !status.DNSRedirect && status.Enabled && status.Error == "" {
				status.Error = "NAS DNS policy is installed but is shadowed by an earlier host policy rule"
			}
		}
	}

	if status.TailscaleDetected {
		status.TailscaleRoutesProtected = state.ProtectTailscale && hostRulesErr == nil && protectedHostRulesPrecedeOpenSurge(hostRules, priorities)
		if route, err := m.runHost(ctx, nil, "ip", "-4", "route", "get", tailscaleDNSIPv4, "from", hostIP); err == nil {
			status.TailscaleDNSProtected = !routeUsesGatewayTable(route, cfg.Gateway.LANIP, routeTableID)
		}
	}
	return status
}

func routeUsesGatewayTable(output []byte, gateway, table string) bool {
	text := string(output)
	return strings.Contains(text, "via "+gateway) && strings.Contains(text, "table "+table)
}

func parseRulePriority(line string) (int, bool) {
	line = strings.TrimSpace(line)
	index := strings.IndexByte(line, ':')
	if index <= 0 {
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimSpace(line[:index]))
	return value, err == nil
}

func ruleSource(line string) string {
	fields := strings.Fields(line)
	for index := 0; index+1 < len(fields); index++ {
		if fields[index] == "from" {
			return strings.TrimSpace(fields[index+1])
		}
	}
	return ""
}

func ruleMatchesHostSource(line, hostIP string, includeFromAll bool) bool {
	host := net.ParseIP(hostIP)
	if host == nil || host.To4() == nil {
		return false
	}
	value := ruleSource(line)
	if value == "" {
		return false
	}
	if value == "all" || value == "0.0.0.0/0" {
		return includeFromAll
	}
	if parsed := net.ParseIP(strings.TrimSuffix(value, "/32")); parsed != nil {
		return parsed.Equal(host)
	}
	_, network, err := net.ParseCIDR(value)
	return err == nil && network.Contains(host)
}

func protectedHostRulesPrecedeOpenSurge(hostRules []byte, priorities hostPolicyPriorities) bool {
	for _, line := range strings.Split(string(hostRules), "\n") {
		priority, ok := parseRulePriority(line)
		if !ok || priority < minimumHostPriority || priority >= priorities.DNSUDP {
			continue
		}
		source := ruleSource(line)
		if source == "all" || source == "0.0.0.0/0" {
			return true
		}
	}
	return false
}

func chooseHostPriorities(hostRules []byte, hostIP string, protectForeignFromAll bool) (hostPolicyPriorities, error) {
	used := make(map[int]bool)
	anchor := 0
	protectedFloor := minimumHostPriority - 1
	for _, line := range strings.Split(string(hostRules), "\n") {
		priority, ok := parseRulePriority(line)
		if !ok {
			continue
		}
		used[priority] = true
		source := ruleSource(line)
		if protectForeignFromAll && (source == "all" || source == "0.0.0.0/0") {
			if priority >= minimumHostPriority && priority < preferredHostPriorityMax && priority > protectedFloor {
				protectedFloor = priority
			}
			continue
		}
		if priority > 0 && ruleMatchesHostSource(line, hostIP, !protectForeignFromAll) && (anchor == 0 || priority < anchor) {
			anchor = priority
		}
	}

	maxPriority := preferredHostPriorityMax
	if anchor > 0 && anchor-1 < maxPriority {
		maxPriority = anchor - 1
	}
	for proxy := maxPriority; proxy-3 >= minimumHostPriority; proxy-- {
		candidate := hostPolicyPriorities{DNSUDP: proxy - 3, DNSTCP: proxy - 2, Main: proxy - 1, Proxy: proxy}
		if protectForeignFromAll && candidate.DNSUDP <= protectedFloor {
			continue
		}
		if used[candidate.DNSUDP] || used[candidate.DNSTCP] || used[candidate.Main] || used[candidate.Proxy] {
			continue
		}
		return candidate, nil
	}
	if protectForeignFromAll && protectedFloor >= minimumHostPriority {
		return hostPolicyPriorities{}, fmt.Errorf("cannot allocate OpenSurge host priorities after protected host/VPN rules (through %d) and before the QNAP host-source rule", protectedFloor)
	}
	if anchor > 0 {
		return hostPolicyPriorities{}, fmt.Errorf("cannot allocate OpenSurge policy priorities before QNAP host-source rule %d without entering protected low-priority space", anchor)
	}
	return hostPolicyPriorities{}, fmt.Errorf("cannot allocate a free OpenSurge host policy priority block")
}

func ruleLineContains(output []byte, priority string, fragments ...string) bool {
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, priority+":") {
			continue
		}
		matched := true
		for _, fragment := range fragments {
			if !strings.Contains(line, fragment) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func (m *Manager) enableLocked(ctx context.Context) error {
	cfg, err := config.Load(m.configPath)
	if err != nil {
		return err
	}
	if cfg.Gateway.Mode != config.GatewayModeSameLAN {
		return fmt.Errorf("NAS host takeover requires QNAP same_lan mode")
	}
	if ip := net.ParseIP(cfg.Gateway.LANIP); ip == nil || ip.To4() == nil {
		return fmt.Errorf("OpenSurge LAN IPv4 is invalid: %q", cfg.Gateway.LANIP)
	}
	if ip := net.ParseIP(cfg.Gateway.UpstreamGateway); ip == nil || ip.To4() == nil {
		return fmt.Errorf("QNAP fallback gateway is invalid: %q", cfg.Gateway.UpstreamGateway)
	}
	if _, err := os.Stat(m.netNSPath); err != nil {
		return fmt.Errorf("host network namespace is unavailable at %s; rebuild the container with the QNAP host-takeover Compose override", m.netNSPath)
	}
	iface, hostIP, err := m.detectHostLocked(ctx, cfg.Gateway.LANIP)
	if err != nil {
		return err
	}
	if hostIP == cfg.Gateway.LANIP {
		return fmt.Errorf("refusing host takeover because NAS host IPv4 equals the OpenSurge container IPv4")
	}
	if scope, err := cfg.LANScope(); err == nil && !scope.Contains(net.ParseIP(hostIP)) {
		return fmt.Errorf("NAS host IPv4 %s is outside configured LAN %s", hostIP, scope.String())
	}
	if strings.TrimSpace(cfg.Gateway.Interface) == "" {
		return fmt.Errorf("OpenSurge LAN interface is empty")
	}
	if strings.TrimSpace(cfg.Transparent.TUNDevice) == "" {
		return fmt.Errorf("OpenSurge TUN device is empty")
	}

	state, err := m.readStateLocked()
	if err != nil {
		return err
	}
	_ = m.disableLocked(ctx)
	hostRules, err := m.runHost(ctx, nil, "ip", "-4", "rule", "show")
	if err != nil {
		return fmt.Errorf("inspect QNAP host policy rules: %w", err)
	}
	priorities, err := chooseHostPriorities(hostRules, hostIP, state.ProtectTailscale)
	if err != nil {
		return err
	}
	if err := m.writeIntentWithPrioritiesLocked(state.Enabled, priorities); err != nil {
		return fmt.Errorf("persist selected NAS host policy priorities: %w", err)
	}
	needsDNS := state.DNSMode != DNSModeHost
	if err := m.ensurePolicySlotsFreeLocked(ctx, needsDNS); err != nil {
		return err
	}
	if needsDNS {
		if err := m.preflightL4PolicyRoutingLocked(ctx); err != nil {
			return err
		}
	}
	if _, err := m.runContainer(ctx, nil, "ip", "link", "show", "dev", cfg.Transparent.TUNDevice); err != nil {
		return fmt.Errorf("OpenSurge TUN device %s is unavailable: %w", cfg.Transparent.TUNDevice, err)
	}

	if needsDNS {
		if err := m.installContainerDNSPolicyLocked(ctx, cfg, hostIP); err != nil {
			_ = m.disableLocked(ctx)
			return err
		}
	}

	if _, err := m.runHost(ctx, nil, "ip", "-4", "route", "replace", "default", "via", cfg.Gateway.LANIP, "dev", iface, "table", routeTableID, "proto", routeProtocol); err != nil {
		_ = m.disableLocked(ctx)
		return fmt.Errorf("install NAS host proxy route: %w", err)
	}
	_, _, mainPriority, proxyPriority := priorities.strings()
	if _, err := m.runHost(ctx, nil, "ip", "-4", "rule", "add", "pref", mainPriority, "iif", "lo", "table", "main", "suppress_prefixlength", "0"); err != nil {
		_ = m.disableLocked(ctx)
		return fmt.Errorf("preserve QNAP non-default routes: %w", err)
	}
	if _, err := m.runHost(ctx, nil, "ip", "-4", "rule", "add", "pref", proxyPriority, "iif", "lo", "table", routeTableID); err != nil {
		_ = m.disableLocked(ctx)
		return fmt.Errorf("install NAS local-origin policy rule: %w", err)
	}
	if needsDNS {
		if err := m.installHostDNSPolicyLocked(ctx); err != nil {
			_ = m.disableLocked(ctx)
			return err
		}
	}

	installed := m.statusLocked(ctx)
	if !installed.Enabled || (needsDNS && !installed.DNSRedirect) {
		_ = m.disableLocked(ctx)
		return fmt.Errorf("verify NAS host takeover state: routing=%t dns=%t", installed.Enabled, installed.DNSRedirect)
	}
	if state.ProtectTailscale && installed.TailscaleDetected && (!installed.TailscaleDNSProtected || !installed.TailscaleRoutesProtected) {
		_ = m.disableLocked(ctx)
		return fmt.Errorf("Tailscale coexistence verification failed: dns_protected=%t routes_protected=%t", installed.TailscaleDNSProtected, installed.TailscaleRoutesProtected)
	}

	if needsDNS {
		gatewayDNSProbe, err := m.runContainer(ctx, nil, "ip", "-4", "route", "get", cfg.Gateway.UpstreamGateway, "from", hostIP, "iif", cfg.Gateway.Interface, "ipproto", "udp", "dport", "53")
		if err != nil || !strings.Contains(string(gatewayDNSProbe), "dev "+cfg.Transparent.TUNDevice) {
			_ = m.disableLocked(ctx)
			if err != nil {
				return fmt.Errorf("verify OpenSurge DNS TUN route: %w", err)
			}
			return fmt.Errorf("OpenSurge DNS route verification did not select %s: %s", cfg.Transparent.TUNDevice, strings.TrimSpace(string(gatewayDNSProbe)))
		}
	}
	return nil
}

func (m *Manager) installHostDNSPolicyLocked(ctx context.Context) error {
	priorities := m.hostPrioritiesLocked()
	dnsUDP, dnsTCP, _, _ := priorities.strings()
	for _, rule := range []struct {
		priority string
		proto    string
	}{
		{priority: dnsUDP, proto: "udp"},
		{priority: dnsTCP, proto: "tcp"},
	} {
		if _, err := m.runHost(ctx, nil, "ip", "-4", "rule", "add", "pref", rule.priority, "iif", "lo", "ipproto", rule.proto, "dport", "53", "table", routeTableID); err != nil {
			return fmt.Errorf("install NAS %s DNS policy rule: %w", strings.ToUpper(rule.proto), err)
		}
	}
	return nil
}

func (m *Manager) installContainerDNSPolicyLocked(ctx context.Context, cfg config.Config, hostIP string) error {
	if _, err := m.runContainer(ctx, nil, "ip", "-4", "route", "replace", "default", "dev", cfg.Transparent.TUNDevice, "table", containerDNSRouteTableID, "proto", containerDNSRouteProtocol); err != nil {
		return fmt.Errorf("install OpenSurge NAS-DNS TUN route: %w", err)
	}
	for _, rule := range []struct {
		priority string
		proto    string
	}{
		{priority: containerDNSUDPRulePriority, proto: "udp"},
		{priority: containerDNSTCPRulePriority, proto: "tcp"},
	} {
		if _, err := m.runContainer(ctx, nil, "ip", "-4", "rule", "add", "pref", rule.priority, "from", hostIP+"/32", "iif", cfg.Gateway.Interface, "ipproto", rule.proto, "dport", "53", "table", containerDNSRouteTableID); err != nil {
			return fmt.Errorf("install OpenSurge NAS %s DNS TUN rule: %w", strings.ToUpper(rule.proto), err)
		}
	}
	return nil
}

func (m *Manager) preflightL4PolicyRoutingLocked(ctx context.Context) error {
	probe := []string{"-4", "rule", "add", "pref", policyCapabilityProbeHost, "from", "192.0.2.1/32", "iif", "lo", "ipproto", "udp", "dport", "65534", "table", "main"}
	if _, err := m.runHost(ctx, nil, "ip", probe...); err != nil {
		return fmt.Errorf("QNAP host kernel does not support L4 policy routing required for NAS DNS takeover: %w", err)
	}
	deleteProbe := []string{"-4", "rule", "del", "pref", policyCapabilityProbeHost, "from", "192.0.2.1/32", "iif", "lo", "ipproto", "udp", "dport", "65534", "table", "main"}
	if _, err := m.runHost(ctx, nil, "ip", deleteProbe...); err != nil {
		return fmt.Errorf("clean QNAP host L4 policy-routing probe: %w", err)
	}

	probe[4] = policyCapabilityProbeGateway
	if _, err := m.runContainer(ctx, nil, "ip", probe...); err != nil {
		return fmt.Errorf("OpenSurge container namespace does not support L4 policy routing required for NAS DNS takeover: %w", err)
	}
	deleteProbe[4] = policyCapabilityProbeGateway
	if _, err := m.runContainer(ctx, nil, "ip", deleteProbe...); err != nil {
		return fmt.Errorf("clean OpenSurge L4 policy-routing probe: %w", err)
	}
	return nil
}

func (m *Manager) ensurePolicySlotsFreeLocked(ctx context.Context, includeDNS bool) error {
	hostRules, err := m.runHost(ctx, nil, "ip", "-4", "rule", "show")
	if err != nil {
		return fmt.Errorf("inspect QNAP host policy rules: %w", err)
	}
	priorities := m.hostPrioritiesLocked()
	dnsUDP, dnsTCP, mainPriority, proxyPriority := priorities.strings()
	wanted := []string{mainPriority, proxyPriority}
	if includeDNS {
		wanted = append([]string{dnsUDP, dnsTCP}, wanted...)
	}
	for _, line := range strings.Split(string(hostRules), "\n") {
		line = strings.TrimSpace(line)
		for _, priority := range wanted {
			if strings.HasPrefix(line, priority+":") {
				return fmt.Errorf("QNAP host already uses policy-rule priority %s; OpenSurge will not overwrite it", priority)
			}
		}
	}
	hostRoutes, err := m.runHost(ctx, nil, "ip", "-4", "route", "show", "table", routeTableID)
	if err != nil {
		if !isMissingRouteTableError(hostRoutes) {
			return fmt.Errorf("inspect QNAP host route table %s: %w", routeTableID, err)
		}
		hostRoutes = nil
	}
	if strings.TrimSpace(string(hostRoutes)) != "" {
		return fmt.Errorf("QNAP host route table %s is already in use; OpenSurge will not overwrite it", routeTableID)
	}

	if !includeDNS {
		return nil
	}
	containerRules, err := m.runContainer(ctx, nil, "ip", "-4", "rule", "show")
	if err != nil {
		return fmt.Errorf("inspect OpenSurge container policy rules: %w", err)
	}
	for _, line := range strings.Split(string(containerRules), "\n") {
		line = strings.TrimSpace(line)
		for _, priority := range []string{containerDNSUDPRulePriority, containerDNSTCPRulePriority} {
			if strings.HasPrefix(line, priority+":") {
				return fmt.Errorf("OpenSurge container already uses policy-rule priority %s; NAS DNS takeover will not overwrite it", priority)
			}
		}
	}
	containerRoutes, err := m.runContainer(ctx, nil, "ip", "-4", "route", "show", "table", containerDNSRouteTableID)
	if err != nil {
		if !isMissingRouteTableError(containerRoutes) {
			return fmt.Errorf("inspect OpenSurge NAS-DNS route table %s: %w", containerDNSRouteTableID, err)
		}
		containerRoutes = nil
	}
	if strings.TrimSpace(string(containerRoutes)) != "" {
		return fmt.Errorf("OpenSurge route table %s is already in use; NAS DNS takeover will not overwrite it", containerDNSRouteTableID)
	}
	return nil
}

func isMissingRouteTableError(output []byte) bool {
	return strings.Contains(strings.ToLower(string(output)), "fib table does not exist")
}

func (m *Manager) disableLocked(ctx context.Context) error {
	if _, err := os.Stat(m.netNSPath); err != nil {
		return nil
	}
	sets := []hostPolicyPriorities{m.hostPrioritiesLocked(), legacyHostPriorities()}
	seen := make(map[hostPolicyPriorities]bool)
	for _, priorities := range sets {
		if seen[priorities] {
			continue
		}
		seen[priorities] = true
		dnsUDP, dnsTCP, mainPriority, proxyPriority := priorities.strings()
		_, _ = m.runHost(ctx, nil, "ip", "-4", "rule", "del", "pref", dnsUDP, "iif", "lo", "ipproto", "udp", "dport", "53", "table", routeTableID)
		_, _ = m.runHost(ctx, nil, "ip", "-4", "rule", "del", "pref", dnsTCP, "iif", "lo", "ipproto", "tcp", "dport", "53", "table", routeTableID)
		_, _ = m.runHost(ctx, nil, "ip", "-4", "rule", "del", "pref", proxyPriority, "iif", "lo", "table", routeTableID)
		_, _ = m.runHost(ctx, nil, "ip", "-4", "rule", "del", "pref", mainPriority, "iif", "lo", "table", "main", "suppress_prefixlength", "0")
	}
	_, _ = m.runHost(ctx, nil, "ip", "-4", "route", "flush", "table", routeTableID, "proto", routeProtocol)

	_, _ = m.runContainer(ctx, nil, "ip", "-4", "rule", "del", "pref", containerDNSUDPRulePriority, "ipproto", "udp", "dport", "53", "table", containerDNSRouteTableID)
	_, _ = m.runContainer(ctx, nil, "ip", "-4", "rule", "del", "pref", containerDNSTCPRulePriority, "ipproto", "tcp", "dport", "53", "table", containerDNSRouteTableID)
	_, _ = m.runContainer(ctx, nil, "ip", "-4", "route", "flush", "table", containerDNSRouteTableID, "proto", containerDNSRouteProtocol)
	return nil
}

func (m *Manager) detectHostLocked(ctx context.Context, gatewayIP string) (string, string, error) {
	output, err := m.runHost(ctx, nil, "ip", "-4", "route", "get", gatewayIP)
	if err != nil {
		return "", "", fmt.Errorf("NAS cannot route to OpenSurge %s: %w", gatewayIP, err)
	}
	fields := strings.Fields(string(output))
	var iface, source string
	for index := 0; index+1 < len(fields); index++ {
		switch fields[index] {
		case "dev":
			iface = fields[index+1]
		case "src":
			source = fields[index+1]
		}
	}
	if iface == "" || source == "" {
		return "", "", fmt.Errorf("cannot determine QNAP host interface/source from: %s", strings.TrimSpace(string(output)))
	}
	if ip := net.ParseIP(source); ip == nil || ip.To4() == nil {
		return "", "", fmt.Errorf("detected QNAP host source is not IPv4: %s", source)
	}
	return iface, source, nil
}

func (m *Manager) runHost(ctx context.Context, input []byte, command string, args ...string) ([]byte, error) {
	nsArgs := []string{"--net=" + m.netNSPath, "--", command}
	nsArgs = append(nsArgs, args...)
	return m.runner.Run(ctx, input, "nsenter", nsArgs...)
}

func (m *Manager) runContainer(ctx context.Context, input []byte, command string, args ...string) ([]byte, error) {
	return m.runner.Run(ctx, input, command, args...)
}

func validDNSMode(mode string) bool {
	return mode == DNSModeAuto || mode == DNSModeOpenSurge || mode == DNSModeHost
}

func defaultIntent() intent {
	return intent{SchemaVersion: hostRoutingStateSchema, DNSMode: DNSModeAuto, ProtectTailscale: true}
}

func (m *Manager) readStateLocked() (intent, error) {
	data, err := os.ReadFile(m.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return defaultIntent(), nil
	}
	if err != nil {
		return intent{}, err
	}
	var state intent
	if err := json.Unmarshal(data, &state); err != nil {
		return intent{}, err
	}
	switch state.SchemaVersion {
	case 1:
		legacy := legacyHostPriorities()
		state.SchemaVersion = hostRoutingStateSchema
		state.HostPriorities = &legacy
		state.DNSMode = DNSModeAuto
		state.ProtectTailscale = true
	case 2:
		state.SchemaVersion = hostRoutingStateSchema
		state.DNSMode = DNSModeAuto
		state.ProtectTailscale = true
	case hostRoutingStateSchema:
		if state.DNSMode == "" {
			state.DNSMode = DNSModeAuto
		}
	default:
		return intent{}, fmt.Errorf("unsupported QNAP host routing state version %d", state.SchemaVersion)
	}
	if state.HostPriorities != nil && !state.HostPriorities.valid() {
		return intent{}, fmt.Errorf("invalid persisted QNAP host policy priorities: %+v", *state.HostPriorities)
	}
	if !validDNSMode(state.DNSMode) {
		return intent{}, fmt.Errorf("invalid persisted NAS DNS mode %q", state.DNSMode)
	}
	return state, nil
}

func (m *Manager) readIntentLocked() (bool, error) {
	state, err := m.readStateLocked()
	return state.Enabled, err
}

func (m *Manager) hostPrioritiesLocked() hostPolicyPriorities {
	state, err := m.readStateLocked()
	if err == nil && state.HostPriorities != nil && state.HostPriorities.valid() {
		return *state.HostPriorities
	}
	return defaultHostPriorities()
}

func (m *Manager) writeIntentLocked(enabled bool) error {
	state, err := m.readStateLocked()
	if err != nil {
		return err
	}
	state.Enabled = enabled
	return m.writeStateLocked(state)
}

func (m *Manager) writeIntentWithPrioritiesLocked(enabled bool, priorities hostPolicyPriorities) error {
	if !priorities.valid() {
		return fmt.Errorf("invalid QNAP host policy priorities: %+v", priorities)
	}
	state, err := m.readStateLocked()
	if err != nil {
		return err
	}
	state.Enabled = enabled
	state.HostPriorities = &priorities
	return m.writeStateLocked(state)
}

func (m *Manager) writeStateLocked(state intent) error {
	if state.DNSMode == "" {
		state.DNSMode = DNSModeAuto
	}
	if !validDNSMode(state.DNSMode) {
		return fmt.Errorf("invalid NAS DNS mode %q", state.DNSMode)
	}
	if state.HostPriorities != nil && !state.HostPriorities.valid() {
		return fmt.Errorf("invalid QNAP host policy priorities: %+v", *state.HostPriorities)
	}
	state.SchemaVersion = hostRoutingStateSchema
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.statePath + ".tmp"
	if err := os.WriteFile(tmp, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.statePath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (m *Manager) Handler(controlToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != Endpoint {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("X-OpenSurge-Remote-Management") != "" {
			http.NotFound(w, r)
			return
		}
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(bearer), []byte(controlToken)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "authentication_required", "message": "authenticated QNAP Web session required"}})
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, m.Status(r.Context()))
		case http.MethodPut:
			var input struct {
				Enabled          bool   `json:"enabled"`
				DNSMode          string `json:"dns_mode,omitempty"`
				ProtectTailscale *bool  `json:"protect_tailscale,omitempty"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_request", "message": err.Error()}})
				return
			}
			status, err := m.SetDesiredWithPolicy(r.Context(), input.Enabled, input.DNSMode, input.ProtectTailscale)
			if err != nil {
				writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "qnap_host_routing_failed", "message": err.Error()}, "status": status})
				return
			}
			writeJSON(w, http.StatusOK, status)
		default:
			w.Header().Set("Allow", "GET, PUT")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": map[string]string{"code": "method_not_allowed", "message": "method not allowed"}})
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
