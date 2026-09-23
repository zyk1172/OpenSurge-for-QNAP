package trafficscope

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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/gateway"
)

const (
	Endpoint              = "/api/v1/qnap-traffic-scopes"
	stateFileName         = "qnap-traffic-scopes.json"
	defaultHostNetNS      = "/run/opensurge/host-netns"
	routeTableID          = "20244"
	routeProtocol         = "244"
	stateSchemaVersion    = 1
	statusSchemaVersion   = 1
	selectorTypeSourceIIF = "source_ingress"
	publicRouteProbeIPv4  = "1.1.1.1"
	preferredPriorityMax  = 19995
	minimumPriority       = 1000
	maxSelectors          = 64
)

var (
	selectorIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	interfacePattern  = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,32}$`)
)

type Selector struct {
	ID               string `json:"id"`
	Label            string `json:"label,omitempty"`
	Enabled          bool   `json:"enabled"`
	Type             string `json:"type"`
	SourceIPv4       string `json:"source_ipv4"`
	IngressInterface string `json:"ingress_interface"`
	MainPriority     int    `json:"main_priority,omitempty"`
	ProxyPriority    int    `json:"proxy_priority,omitempty"`
}

type SelectorStatus struct {
	Selector
	Active bool   `json:"active"`
	Error  string `json:"error,omitempty"`
}

type Status struct {
	SchemaVersion    int              `json:"schema_version"`
	Supported        bool             `json:"supported"`
	GatewayReady     bool             `json:"gateway_ready"`
	GatewayIPv4      string           `json:"gateway_ipv4,omitempty"`
	GatewayInterface string           `json:"gateway_interface,omitempty"`
	Selectors        []SelectorStatus `json:"selectors"`
	Error            string           `json:"error,omitempty"`
	CheckedAt        time.Time        `json:"checked_at"`
}

type persistedState struct {
	SchemaVersion int        `json:"schema_version"`
	Selectors     []Selector `json:"selectors"`
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
	state, err := m.readStateLocked()
	if err != nil {
		return err
	}
	return m.disableLocked(ctx, state.Selectors)
}

func (m *Manager) Status(ctx context.Context) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(ctx)
}

func (m *Manager) SetSelectors(ctx context.Context, selectors []Selector) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	normalized, err := normalizeSelectors(selectors)
	if err != nil {
		status := m.statusLocked(ctx)
		status.Error = err.Error()
		return status, err
	}
	current, err := m.readStateLocked()
	if err != nil {
		return Status{}, err
	}
	if err := m.disableLocked(ctx, current.Selectors); err != nil {
		return m.statusLocked(ctx), err
	}
	for index := range normalized {
		normalized[index].MainPriority = 0
		normalized[index].ProxyPriority = 0
	}
	if err := m.writeStateLocked(persistedState{Selectors: normalized}); err != nil {
		return Status{}, err
	}
	if !hasEnabledSelector(normalized) {
		return m.statusLocked(ctx), nil
	}

	readiness, readinessErr := gateway.ReadinessConfig(ctx, m.configPath)
	if readinessErr != nil || !readiness.Ready || !readiness.DesiredRunning {
		// Persist the intent while failing open. The reconciler activates it
		// automatically after the gateway becomes healthy again.
		return m.statusLocked(ctx), nil
	}
	if err := m.enableLocked(ctx); err != nil {
		state, _ := m.readStateLocked()
		_ = m.disableLocked(ctx, state.Selectors)
		status := m.statusLocked(ctx)
		status.Error = err.Error()
		return status, err
	}
	return m.statusLocked(ctx), nil
}

func (m *Manager) reconcile(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readStateLocked()
	if err != nil || !hasEnabledSelector(state.Selectors) {
		_ = m.disableLocked(ctx, state.Selectors)
		return
	}
	readiness, err := gateway.ReadinessConfig(ctx, m.configPath)
	if err != nil || !readiness.Ready || !readiness.DesiredRunning {
		_ = m.disableLocked(ctx, state.Selectors)
		return
	}
	status := m.statusLocked(ctx)
	if !allDesiredSelectorsActive(status.Selectors) {
		_ = m.enableLocked(ctx)
	}
}

func allDesiredSelectorsActive(selectors []SelectorStatus) bool {
	for _, selector := range selectors {
		if selector.Enabled && !selector.Active {
			return false
		}
	}
	return true
}

func hasEnabledSelector(selectors []Selector) bool {
	for _, selector := range selectors {
		if selector.Enabled {
			return true
		}
	}
	return false
}

func normalizeSelectors(selectors []Selector) ([]Selector, error) {
	if len(selectors) > maxSelectors {
		return nil, fmt.Errorf("at most %d traffic selectors are supported", maxSelectors)
	}
	out := make([]Selector, 0, len(selectors))
	ids := map[string]bool{}
	tuples := map[string]bool{}
	for _, selector := range selectors {
		selector.ID = strings.TrimSpace(selector.ID)
		selector.Label = strings.TrimSpace(selector.Label)
		selector.Type = strings.TrimSpace(selector.Type)
		selector.SourceIPv4 = strings.TrimSpace(selector.SourceIPv4)
		selector.IngressInterface = strings.TrimSpace(selector.IngressInterface)
		if selector.Type == "" {
			selector.Type = selectorTypeSourceIIF
		}
		if !selectorIDPattern.MatchString(selector.ID) {
			return nil, fmt.Errorf("traffic selector id %q must contain only letters, numbers, dot, underscore or dash", selector.ID)
		}
		if ids[selector.ID] {
			return nil, fmt.Errorf("duplicate traffic selector id %q", selector.ID)
		}
		ids[selector.ID] = true
		if selector.Type != selectorTypeSourceIIF {
			return nil, fmt.Errorf("traffic selector %q uses unsupported type %q", selector.ID, selector.Type)
		}
		ip := net.ParseIP(selector.SourceIPv4)
		if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
			return nil, fmt.Errorf("traffic selector %q source_ipv4 must be a usable IPv4 address", selector.ID)
		}
		selector.SourceIPv4 = ip.To4().String()
		if !interfacePattern.MatchString(selector.IngressInterface) || selector.IngressInterface == "lo" {
			return nil, fmt.Errorf("traffic selector %q ingress_interface is invalid", selector.ID)
		}
		key := selector.SourceIPv4 + "|" + selector.IngressInterface
		if tuples[key] {
			return nil, fmt.Errorf("duplicate traffic selector for %s on %s", selector.SourceIPv4, selector.IngressInterface)
		}
		tuples[key] = true
		if selector.MainPriority < 0 || selector.ProxyPriority < 0 {
			return nil, fmt.Errorf("traffic selector %q has invalid persisted priorities", selector.ID)
		}
		if (selector.MainPriority == 0) != (selector.ProxyPriority == 0) {
			return nil, fmt.Errorf("traffic selector %q has incomplete persisted priorities", selector.ID)
		}
		if selector.MainPriority != 0 && selector.MainPriority >= selector.ProxyPriority {
			return nil, fmt.Errorf("traffic selector %q priority order is invalid", selector.ID)
		}
		out = append(out, selector)
	}
	return out, nil
}

func allocatePriorityPair(used map[int]bool) (int, int, error) {
	for proxy := preferredPriorityMax; proxy-1 >= minimumPriority; proxy -= 2 {
		main := proxy - 1
		if !used[main] && !used[proxy] {
			used[main] = true
			used[proxy] = true
			return main, proxy, nil
		}
	}
	return 0, 0, fmt.Errorf("cannot allocate free policy-rule priorities for Docker bridge traffic")
}

func usedPriorities(output []byte) map[int]bool {
	used := map[int]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		index := strings.IndexByte(line, ':')
		if index <= 0 {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(line[:index]))
		if err == nil {
			used[value] = true
		}
	}
	return used
}

func (m *Manager) enableLocked(ctx context.Context) error {
	cfg, err := config.Load(m.configPath)
	if err != nil {
		return err
	}
	if cfg.Gateway.Mode != config.GatewayModeSameLAN {
		return fmt.Errorf("Docker bridge traffic takeover requires QNAP same_lan mode")
	}
	if ip := net.ParseIP(cfg.Gateway.LANIP); ip == nil || ip.To4() == nil {
		return fmt.Errorf("OpenSurge LAN IPv4 is invalid: %q", cfg.Gateway.LANIP)
	}
	if _, err := os.Stat(m.netNSPath); err != nil {
		return fmt.Errorf("host network namespace is unavailable at %s; enable the QNAP host-takeover Compose override", m.netNSPath)
	}

	state, err := m.readStateLocked()
	if err != nil {
		return err
	}
	normalized, err := normalizeSelectors(state.Selectors)
	if err != nil {
		return err
	}
	if err := m.disableLocked(ctx, normalized); err != nil {
		return err
	}
	gatewayInterface, _, err := m.detectHostPathLocked(ctx, cfg.Gateway.LANIP)
	if err != nil {
		return err
	}

	rules, err := m.runHost(ctx, nil, "ip", "-4", "rule", "show")
	if err != nil {
		return fmt.Errorf("inspect QNAP host policy rules: %w", err)
	}
	used := usedPriorities(rules)

	for index := range normalized {
		selector := &normalized[index]
		selector.MainPriority = 0
		selector.ProxyPriority = 0
		if !selector.Enabled {
			continue
		}
		if _, err := m.runHost(ctx, nil, "ip", "link", "show", "dev", selector.IngressInterface); err != nil {
			return fmt.Errorf("traffic selector %q ingress interface %s is unavailable: %w", selector.ID, selector.IngressInterface, err)
		}
		reverse, err := m.runHost(ctx, nil, "ip", "-4", "route", "get", selector.SourceIPv4)
		if err != nil {
			return fmt.Errorf("verify return route for traffic selector %q: %w", selector.ID, err)
		}
		if !strings.Contains(string(reverse), "dev "+selector.IngressInterface) {
			return fmt.Errorf("traffic selector %q source %s does not return through %s: %s", selector.ID, selector.SourceIPv4, selector.IngressInterface, strings.TrimSpace(string(reverse)))
		}
		mainPriority, proxyPriority, err := allocatePriorityPair(used)
		if err != nil {
			return err
		}
		selector.MainPriority = mainPriority
		selector.ProxyPriority = proxyPriority
	}

	if err := m.ensureRouteTableFreeLocked(ctx); err != nil {
		return err
	}
	// Persist cleanup coordinates before installing host rules. If the process is
	// interrupted midway, the next reconciliation can still remove only the
	// exact priorities and tuple selectors owned by OpenSurge.
	if err := m.writeStateLocked(persistedState{Selectors: normalized}); err != nil {
		return err
	}
	if _, err := m.runHost(ctx, nil, "ip", "-4", "route", "replace", "default", "via", cfg.Gateway.LANIP, "dev", gatewayInterface, "table", routeTableID, "proto", routeProtocol); err != nil {
		_ = m.disableLocked(ctx, normalized)
		return fmt.Errorf("install Docker bridge OpenSurge route: %w", err)
	}
	for _, selector := range normalized {
		if !selector.Enabled {
			continue
		}
		source := selector.SourceIPv4 + "/32"
		mainPriority := strconv.Itoa(selector.MainPriority)
		proxyPriority := strconv.Itoa(selector.ProxyPriority)
		if _, err := m.runHost(ctx, nil, "ip", "-4", "rule", "add", "pref", mainPriority, "from", source, "iif", selector.IngressInterface, "table", "main", "suppress_prefixlength", "0"); err != nil {
			_ = m.disableLocked(ctx, normalized)
			return fmt.Errorf("preserve non-default routes for traffic selector %q: %w", selector.ID, err)
		}
		if _, err := m.runHost(ctx, nil, "ip", "-4", "rule", "add", "pref", proxyPriority, "from", source, "iif", selector.IngressInterface, "table", routeTableID); err != nil {
			_ = m.disableLocked(ctx, normalized)
			return fmt.Errorf("install OpenSurge route for traffic selector %q: %w", selector.ID, err)
		}
		probe, err := m.runHost(ctx, nil, "ip", "-4", "route", "get", publicRouteProbeIPv4, "from", selector.SourceIPv4, "iif", selector.IngressInterface)
		if err != nil {
			_ = m.disableLocked(ctx, normalized)
			return fmt.Errorf("verify traffic selector %q public route: %w", selector.ID, err)
		}
		if !routeUsesGatewayTable(probe, cfg.Gateway.LANIP, routeTableID) {
			_ = m.disableLocked(ctx, normalized)
			return fmt.Errorf("traffic selector %q is shadowed by an earlier host policy rule: %s", selector.ID, strings.TrimSpace(string(probe)))
		}
	}
	return nil
}

func (m *Manager) ensureRouteTableFreeLocked(ctx context.Context) error {
	output, err := m.runHost(ctx, nil, "ip", "-4", "route", "show", "table", routeTableID)
	if err != nil {
		if isMissingRouteTableError(output) {
			return nil
		}
		return fmt.Errorf("inspect QNAP traffic-scope route table %s: %w", routeTableID, err)
	}
	if strings.TrimSpace(string(output)) != "" {
		return fmt.Errorf("QNAP route table %s is already in use; OpenSurge will not overwrite it", routeTableID)
	}
	return nil
}

func (m *Manager) disableLocked(ctx context.Context, selectors []Selector) error {
	if _, err := os.Stat(m.netNSPath); err != nil {
		return nil
	}
	for _, selector := range selectors {
		if selector.MainPriority > 0 {
			_, _ = m.runHost(ctx, nil, "ip", "-4", "rule", "del", "pref", strconv.Itoa(selector.MainPriority), "from", selector.SourceIPv4+"/32", "iif", selector.IngressInterface, "table", "main", "suppress_prefixlength", "0")
		}
		if selector.ProxyPriority > 0 {
			_, _ = m.runHost(ctx, nil, "ip", "-4", "rule", "del", "pref", strconv.Itoa(selector.ProxyPriority), "from", selector.SourceIPv4+"/32", "iif", selector.IngressInterface, "table", routeTableID)
		}
	}
	_, _ = m.runHost(ctx, nil, "ip", "-4", "route", "flush", "table", routeTableID, "proto", routeProtocol)
	return nil
}

func (m *Manager) statusLocked(ctx context.Context) Status {
	status := Status{
		SchemaVersion: statusSchemaVersion,
		Selectors:     []SelectorStatus{},
		CheckedAt:     time.Now().UTC(),
	}
	state, err := m.readStateLocked()
	if err != nil {
		status.Error = err.Error()
		return status
	}
	for _, selector := range state.Selectors {
		status.Selectors = append(status.Selectors, SelectorStatus{Selector: selector})
	}
	cfg, err := config.Load(m.configPath)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.GatewayIPv4 = cfg.Gateway.LANIP
	if _, err := os.Stat(m.netNSPath); err != nil {
		status.Error = "host network namespace is not mounted; Docker bridge traffic takeover is unavailable"
		return status
	}
	status.Supported = cfg.Gateway.Mode == config.GatewayModeSameLAN
	if readiness, err := gateway.ReadinessConfig(ctx, m.configPath); err == nil {
		status.GatewayReady = readiness.Ready && readiness.DesiredRunning
	}
	gatewayInterface, _, err := m.detectHostPathLocked(ctx, cfg.Gateway.LANIP)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.GatewayInterface = gatewayInterface

	rules, rulesErr := m.runHost(ctx, nil, "ip", "-4", "rule", "show")
	routes, routesErr := m.runHost(ctx, nil, "ip", "-4", "route", "show", "table", routeTableID)
	tableReady := routesErr == nil &&
		strings.Contains(string(routes), "default via "+cfg.Gateway.LANIP) &&
		strings.Contains(string(routes), "proto "+routeProtocol)
	for index := range status.Selectors {
		selector := &status.Selectors[index]
		if !selector.Enabled || selector.MainPriority == 0 || selector.ProxyPriority == 0 {
			continue
		}
		if rulesErr != nil || !tableReady {
			continue
		}
		mainPriority := strconv.Itoa(selector.MainPriority)
		proxyPriority := strconv.Itoa(selector.ProxyPriority)
		source := "from " + selector.SourceIPv4
		if !ruleLineContains(rules, mainPriority, source, "iif "+selector.IngressInterface, "lookup main", "suppress_prefixlength 0") ||
			!ruleLineContains(rules, proxyPriority, source, "iif "+selector.IngressInterface, "lookup "+routeTableID) {
			continue
		}
		probe, err := m.runHost(ctx, nil, "ip", "-4", "route", "get", publicRouteProbeIPv4, "from", selector.SourceIPv4, "iif", selector.IngressInterface)
		if err != nil {
			selector.Error = err.Error()
			continue
		}
		selector.Active = routeUsesGatewayTable(probe, cfg.Gateway.LANIP, routeTableID)
		if !selector.Active {
			selector.Error = "policy rules are installed but a different route still wins"
		}
	}
	return status
}

func routeUsesGatewayTable(output []byte, gateway, table string) bool {
	text := string(output)
	return strings.Contains(text, "via "+gateway) && strings.Contains(text, "table "+table)
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

func isMissingRouteTableError(output []byte) bool {
	return strings.Contains(strings.ToLower(string(output)), "fib table does not exist")
}

func (m *Manager) detectHostPathLocked(ctx context.Context, gatewayIP string) (string, string, error) {
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
		return "", "", fmt.Errorf("cannot determine QNAP host path to OpenSurge from: %s", strings.TrimSpace(string(output)))
	}
	return iface, source, nil
}

func (m *Manager) runHost(ctx context.Context, input []byte, command string, args ...string) ([]byte, error) {
	nsArgs := []string{"--net=" + m.netNSPath, "--", command}
	nsArgs = append(nsArgs, args...)
	return m.runner.Run(ctx, input, "nsenter", nsArgs...)
}

func defaultState() persistedState {
	return persistedState{SchemaVersion: stateSchemaVersion, Selectors: []Selector{}}
}

func (m *Manager) readStateLocked() (persistedState, error) {
	data, err := os.ReadFile(m.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return defaultState(), nil
	}
	if err != nil {
		return persistedState{}, err
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return persistedState{}, err
	}
	if state.SchemaVersion != stateSchemaVersion {
		return persistedState{}, fmt.Errorf("unsupported QNAP traffic-scope state version %d", state.SchemaVersion)
	}
	if state.Selectors == nil {
		state.Selectors = []Selector{}
	}
	normalized, err := normalizeSelectors(state.Selectors)
	if err != nil {
		return persistedState{}, err
	}
	state.Selectors = normalized
	return state, nil
}

func (m *Manager) writeStateLocked(state persistedState) error {
	normalized, err := normalizeSelectors(state.Selectors)
	if err != nil {
		return err
	}
	state.SchemaVersion = stateSchemaVersion
	state.Selectors = normalized
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
				Selectors []Selector `json:"selectors"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_request", "message": err.Error()}})
				return
			}
			status, err := m.SetSelectors(r.Context(), input.Selectors)
			if err != nil {
				writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "qnap_traffic_scope_failed", "message": err.Error()}, "status": status})
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
