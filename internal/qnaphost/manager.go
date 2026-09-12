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
	"strings"
	"sync"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/gateway"
)

const (
	Endpoint          = "/api/v1/qnap-host-routing"
	stateFileName     = "qnap-host-routing.json"
	defaultHostNetNS  = "/run/opensurge/host-netns"
	routeTableID      = "20242"
	mainRulePriority  = "24100"
	proxyRulePriority = "24110"
	nftTableName      = "opensurge_nas_host"
)

type Status struct {
	SchemaVersion   int       `json:"schema_version"`
	Supported       bool      `json:"supported"`
	Desired         bool      `json:"desired"`
	Enabled         bool      `json:"enabled"`
	GatewayReady    bool      `json:"gateway_ready"`
	HostIPv4        string    `json:"host_ipv4,omitempty"`
	HostInterface   string    `json:"host_interface,omitempty"`
	GatewayIPv4     string    `json:"gateway_ipv4,omitempty"`
	FallbackGateway string    `json:"fallback_gateway,omitempty"`
	DNSRedirect     bool      `json:"dns_redirect"`
	Error           string    `json:"error,omitempty"`
	CheckedAt       time.Time `json:"checked_at"`
}

type intent struct {
	SchemaVersion int  `json:"schema_version"`
	Enabled       bool `json:"enabled"`
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

// Run keeps the host routing state fail-open: when the OpenSurge data plane is
// not ready, the NAS host policy is removed so QTS falls back to its ordinary
// main-table default gateway. When the gateway becomes ready again, a persisted
// opt-in is restored automatically.
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

// Release removes only OpenSurge-owned host rules and leaves the persisted
// desired flag untouched. A clean container stop/rebuild therefore restores
// the NAS to QTS/main-table routing, while the next healthy container can
// re-enable the opt-in automatically.
func (m *Manager) Release(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.disableLocked(ctx)
}

func (m *Manager) SetDesired(ctx context.Context, enabled bool) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.writeIntentLocked(enabled); err != nil {
		return Status{}, err
	}
	if !enabled {
		if err := m.disableLocked(ctx); err != nil {
			status := m.statusLocked(ctx)
			status.Error = err.Error()
			return status, err
		}
		return m.statusLocked(ctx), nil
	}

	readiness, err := gateway.ReadinessConfig(ctx, m.configPath)
	if err != nil || !readiness.Ready || !readiness.DesiredRunning {
		_ = m.disableLocked(ctx)
		status := m.statusLocked(ctx)
		if err != nil {
			status.Error = err.Error()
			return status, fmt.Errorf("OpenSurge gateway readiness check failed: %w", err)
		}
		return status, fmt.Errorf("OpenSurge gateway must be running and ready before NAS host takeover can be enabled")
	}
	if err := m.enableLocked(ctx); err != nil {
		_ = m.disableLocked(ctx)
		status := m.statusLocked(ctx)
		status.Error = err.Error()
		return status, err
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
	desired, _ := m.readIntentLocked()
	if !desired {
		_ = m.disableLocked(ctx)
		return
	}
	readiness, err := gateway.ReadinessConfig(ctx, m.configPath)
	if err != nil || !readiness.Ready || !readiness.DesiredRunning {
		_ = m.disableLocked(ctx)
		return
	}
	status := m.statusLocked(ctx)
	if !status.Enabled || !status.DNSRedirect {
		_ = m.enableLocked(ctx)
	}
}

func (m *Manager) statusLocked(ctx context.Context) Status {
	status := Status{SchemaVersion: 1, CheckedAt: time.Now().UTC()}
	status.Desired, _ = m.readIntentLocked()
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
	status.Supported = true
	if readiness, err := gateway.ReadinessConfig(ctx, m.configPath); err == nil {
		status.GatewayReady = readiness.Ready && readiness.DesiredRunning
	}
	if iface, hostIP, err := m.detectHostLocked(ctx, cfg.Gateway.LANIP); err == nil {
		status.HostInterface = iface
		status.HostIPv4 = hostIP
	} else {
		status.Error = err.Error()
	}
	rules, rulesErr := m.runHost(ctx, nil, "ip", "-4", "rule", "show")
	routes, routesErr := m.runHost(ctx, nil, "ip", "-4", "route", "show", "table", routeTableID)
	if rulesErr == nil && routesErr == nil {
		status.Enabled = strings.Contains(string(rules), "lookup "+routeTableID) && strings.Contains(string(routes), "default via "+cfg.Gateway.LANIP)
	}
	if _, err := m.runHost(ctx, nil, "nft", "list", "table", "ip", nftTableName); err == nil {
		status.DNSRedirect = true
	}
	return status
}

func (m *Manager) enableLocked(ctx context.Context) error {
	cfg, err := config.Load(m.configPath)
	if err != nil {
		return err
	}
	if cfg.Gateway.Mode != config.GatewayModeSameLAN {
		return fmt.Errorf("NAS host takeover requires QNAP same_lan mode")
	}
	if net.ParseIP(cfg.Gateway.LANIP) == nil || net.ParseIP(cfg.Gateway.LANIP).To4() == nil {
		return fmt.Errorf("OpenSurge LAN IPv4 is invalid: %q", cfg.Gateway.LANIP)
	}
	if net.ParseIP(cfg.Gateway.UpstreamGateway) == nil || net.ParseIP(cfg.Gateway.UpstreamGateway).To4() == nil {
		return fmt.Errorf("QNAP fallback gateway is invalid: %q", cfg.Gateway.UpstreamGateway)
	}
	if _, err := os.Stat(m.netNSPath); err != nil {
		return fmt.Errorf("host network namespace is unavailable at %s; rebuild the container with the current QNAP Compose file", m.netNSPath)
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

	_ = m.disableLocked(ctx)
	if _, err := m.runHost(ctx, nil, "ip", "-4", "route", "replace", "default", "via", cfg.Gateway.LANIP, "dev", iface, "table", routeTableID); err != nil {
		return fmt.Errorf("install NAS host proxy route: %w", err)
	}
	if _, err := m.runHost(ctx, nil, "ip", "-4", "rule", "add", "pref", mainRulePriority, "iif", "lo", "lookup", "main", "suppress_prefixlength", "0"); err != nil {
		_ = m.disableLocked(ctx)
		return fmt.Errorf("preserve QNAP non-default routes: %w", err)
	}
	if _, err := m.runHost(ctx, nil, "ip", "-4", "rule", "add", "pref", proxyRulePriority, "iif", "lo", "lookup", routeTableID); err != nil {
		_ = m.disableLocked(ctx)
		return fmt.Errorf("install NAS local-origin policy rule: %w", err)
	}

	nftScript := fmt.Sprintf(`add table ip %s
add chain ip %s output { type nat hook output priority -100; policy accept; }
add rule ip %s output ip daddr != %s udp dport 53 dnat to %s
add rule ip %s output ip daddr != %s tcp dport 53 dnat to %s
`, nftTableName, nftTableName, nftTableName, cfg.Gateway.LANIP, cfg.Gateway.LANIP, nftTableName, cfg.Gateway.LANIP, cfg.Gateway.LANIP)
	if _, err := m.runHost(ctx, []byte(nftScript), "nft", "-f", "-"); err != nil {
		_ = m.disableLocked(ctx)
		return fmt.Errorf("redirect NAS IPv4 DNS to OpenSurge: %w", err)
	}

	probe, err := m.runHost(ctx, nil, "ip", "-4", "route", "get", "1.1.1.1", "from", hostIP, "iif", "lo")
	if err != nil || !strings.Contains(string(probe), "via "+cfg.Gateway.LANIP) {
		_ = m.disableLocked(ctx)
		if err != nil {
			return fmt.Errorf("verify NAS host proxy route: %w", err)
		}
		return fmt.Errorf("NAS host route verification did not select OpenSurge: %s", strings.TrimSpace(string(probe)))
	}
	return nil
}

func (m *Manager) disableLocked(ctx context.Context) error {
	if _, err := os.Stat(m.netNSPath); err != nil {
		return nil
	}
	// Every resource is uniquely OpenSurge-owned. Deletion is intentionally
	// idempotent because QTS, a reboot, or a previous failed attempt may already
	// have removed part of the state.
	_, _ = m.runHost(ctx, nil, "nft", "delete", "table", "ip", nftTableName)
	_, _ = m.runHost(ctx, nil, "ip", "-4", "rule", "del", "pref", proxyRulePriority)
	_, _ = m.runHost(ctx, nil, "ip", "-4", "rule", "del", "pref", mainRulePriority)
	_, _ = m.runHost(ctx, nil, "ip", "-4", "route", "flush", "table", routeTableID)
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

func (m *Manager) readIntentLocked() (bool, error) {
	data, err := os.ReadFile(m.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var state intent
	if err := json.Unmarshal(data, &state); err != nil {
		return false, err
	}
	if state.SchemaVersion != 1 {
		return false, fmt.Errorf("unsupported QNAP host routing state version %d", state.SchemaVersion)
	}
	return state.Enabled, nil
}

func (m *Manager) writeIntentLocked(enabled bool) error {
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(intent{SchemaVersion: 1, Enabled: enabled}, "", "  ")
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

// Handler exposes the host-routing toggle only through the authenticated QNAP
// Web proxy. The external AI management token is deliberately rejected because
// this endpoint changes the NAS host network namespace itself.
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
				Enabled bool `json:"enabled"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_request", "message": err.Error()}})
				return
			}
			status, err := m.SetDesired(r.Context(), input.Enabled)
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
