//go:build linux

package linux

import (
	"bufio"
	"context"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"open-mihomo-gateway/internal/platform"
)

// Backend is the Linux network backend: nftables for marking, iproute2 for
// policy routing, /dev/net/tun for the proxy core data plane, and /proc/sys for
// forwarding and rp_filter.
//
// It owns exactly one nftables table, one routing table id, one fwmark and one
// policy rule. It never flushes the ruleset and never edits a rule it cannot
// prove is its own.
type Backend struct {
	runner *runner

	tableName  string
	tempDir    string
	tunTimeout time.Duration

	// active records what the last successful setup applied, so Snapshot and
	// Restore can operate without the caller repeating the configuration.
	active *activeConfig
}

type activeConfig struct {
	routing platform.RoutingConfig
	nat     platform.NATConfig
}

// Option customises the backend. It exists mainly so the network-namespace lab
// and the unit tests can point the backend at a temp directory and a short TUN
// timeout without touching a real host.
type Option func(*Backend)

// WithTableName overrides the nftables table name.
func WithTableName(name string) Option {
	return func(b *Backend) { b.tableName = name }
}

// WithTempDir overrides where transient ruleset files are written.
func WithTempDir(dir string) Option {
	return func(b *Backend) { b.tempDir = dir }
}

// WithTUNTimeout overrides how long WaitForTUN waits for the device.
func WithTUNTimeout(timeout time.Duration) Option {
	return func(b *Backend) { b.tunTimeout = timeout }
}

// New constructs the Linux backend.
func New(options ...Option) (*Backend, error) {
	b := &Backend{
		runner:    newRunner(),
		tableName: DefaultTableName,
		tunTimeout: 15 * time.Second,
	}
	for _, option := range options {
		option(b)
	}
	if err := validateTableName(b.tableName); err != nil {
		return nil, err
	}
	return b, nil
}

// Name implements platform.NetworkBackend.
func (b *Backend) Name() platform.BackendName { return platform.BackendLinuxNFTables }

// DetectInterfaces implements platform.NetworkBackend.
func (b *Backend) DetectInterfaces(ctx context.Context) ([]platform.NetworkInterface, error) {
	return b.detectInterfaces(ctx)
}

// InterfaceByName implements platform.NetworkBackend.
func (b *Backend) InterfaceByName(ctx context.Context, name string) (platform.NetworkInterface, error) {
	return b.interfaceByName(ctx, name)
}

// ValidateTopology implements platform.NetworkBackend.
func (b *Backend) ValidateTopology(ctx context.Context, cfg platform.NetworkConfig) error {
	return b.validateTopology(ctx, cfg)
}

// EnableIPv4Forwarding implements platform.NetworkBackend.
func (b *Backend) EnableIPv4Forwarding(ctx context.Context) (func(context.Context) error, error) {
	restore, _, err := b.enableIPv4Forwarding(ctx)
	return restore, err
}

// SetupNAT implements platform.NetworkBackend.
func (b *Backend) SetupNAT(ctx context.Context, cfg platform.NATConfig) error {
	if cfg.TableName == "" {
		cfg.TableName = b.tableName
	}
	if err := b.applyNAT(ctx, cfg); err != nil {
		return err
	}
	b.ensureActive().nat = cfg
	b.active.nat.TableName = cfg.TableName
	return nil
}

// RemoveNAT implements platform.NetworkBackend.
func (b *Backend) RemoveNAT(ctx context.Context) error {
	return b.removeNAT(ctx)
}

// SetupPolicyRouting implements platform.NetworkBackend.
func (b *Backend) SetupPolicyRouting(ctx context.Context, cfg platform.RoutingConfig) error {
	if err := b.applyPolicyRouting(ctx, cfg); err != nil {
		return err
	}
	b.ensureActive().routing = cfg
	return nil
}

// RemovePolicyRouting implements platform.NetworkBackend.
func (b *Backend) RemovePolicyRouting(ctx context.Context) error {
	if b.active == nil {
		return nil
	}
	return b.removePolicyRouting(ctx, b.active.routing)
}

// SwitchToDirectFallback re-points the routing table at the real upstream
// gateway and enables masquerade. It is only ever called because the operator
// opted in; it is never an automatic response to a proxy core failure.
func (b *Backend) SwitchToDirectFallback(ctx context.Context, cfg platform.RoutingConfig) error {
	cfg.DirectFallback = true
	if err := b.applyPolicyRouting(ctx, cfg); err != nil {
		return err
	}
	nat := b.active.nat
	if nat.TableName == "" {
		nat.TableName = b.tableName
	}
	nat.Masquerade = true
	nat.UpstreamGateway = cfg.UpstreamGateway
	if err := b.applyNAT(ctx, nat); err != nil {
		return err
	}
	b.ensureActive().routing = cfg
	b.active.nat = nat
	return nil
}

// EnsureTUN implements platform.NetworkBackend.
func (b *Backend) EnsureTUN(ctx context.Context) error { return b.ensureTUN(ctx) }

// WaitForTUN implements platform.NetworkBackend.
func (b *Backend) WaitForTUN(ctx context.Context, device string) (platform.NetworkInterface, error) {
	return b.waitForTUN(ctx, device)
}

func (b *Backend) ensureActive() *activeConfig {
	if b.active == nil {
		b.active = &activeConfig{}
	}
	return b.active
}

// Capabilities reports what this host can actually do. Every false value is
// paired with a reason so preflight can show the user what to fix instead of a
// bare "network error".
func (b *Backend) Capabilities(ctx context.Context) (platform.Capabilities, error) {
	caps := platform.Capabilities{
		Architecture: runtime.GOARCH,
		Missing:      map[string]string{},
	}
	if release, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		caps.Kernel = strings.TrimSpace(string(release))
	} else {
		caps.Report("kernel", "cannot read /proc/sys/kernel/osrelease")
	}

	caps.TUNDeviceNode = hasTUNNode()
	if !caps.TUNDeviceNode {
		caps.Report("tun_device_node", "/dev/net/tun is missing; pass --device /dev/net/tun and enable TUN on the host")
	}

	effective, err := readEffectiveCaps()
	if err != nil {
		caps.Report("capabilities", "cannot read /proc/self/status capabilities: "+err.Error())
	} else {
		caps.CapNetAdmin = hasCapability(effective, capNetAdmin)
		caps.CapNetRaw = hasCapability(effective, capNetRaw)
		if !caps.CapNetAdmin {
			caps.Report("cap_net_admin", "CAP_NET_ADMIN is missing; add it with cap_add or run the documented fallback")
		}
		if !caps.CapNetRaw {
			caps.Report("cap_net_raw", "CAP_NET_RAW is missing; dnsmasq DHCP and ICMP probes need it")
		}
	}

	caps.NFTables, caps.NFTablesJSON, _ = b.nftCapabilities(ctx)
	if !caps.NFTables {
		caps.Report("nftables", "nft was not found in PATH")
	}
	caps.IProute2 = b.runner.ipPath != ""
	if !caps.IProute2 {
		caps.Report("iproute2", "ip was not found in PATH")
	}

	if _, err := readProcSys(procIPv4Forward); err == nil {
		caps.IPv4ForwardSysctl = true
	} else {
		caps.Report("ipv4_forward_sysctl",
			"cannot read /proc/sys/net/ipv4/ip_forward")
	}
	// Docker mounts /proc/sys read-only by default, even with CAP_NET_ADMIN.
	// Detect it here so preflight can name the exact fix instead of failing
	// halfway through applying the data plane.
	caps.IPv4ForwardWritable = procSysWritable(procIPv4Forward)
	if !caps.IPv4ForwardWritable {
		current := "unknown"
		if value, err := readProcSys(procIPv4Forward); err == nil {
			current = value
		}
		if current == "1" {
			caps.Report("ipv4_forward_writable",
				"net.ipv4.ip_forward is already 1 but the file is read-only, so OpenSurge cannot restore it on stop; add sysctls: net.ipv4.ip_forward=1 to the Compose file")
		} else {
			caps.Report("ipv4_forward_writable",
				"net.ipv4.ip_forward is "+current+" and not writable; add sysctls: net.ipv4.ip_forward=1 to the Compose file (privileged mode is not required)")
		}
	}

	if inode, err := os.Readlink("/proc/self/ns/net"); err == nil {
		caps.NetworkNamespace = inode
	} else {
		caps.Report("network_namespace", "cannot read /proc/self/ns/net")
	}
	// Heuristic: a container in host network mode sees the host's Docker
	// bridges. It is reported as a warning input, never as a hard failure.
	if interfaces, err := b.detectInterfaces(ctx); err == nil {
		for _, iface := range interfaces {
			if iface.Name == "docker0" || strings.HasPrefix(iface.Name, "br-") {
				caps.HostNetworkMode = true
				break
			}
		}
	}
	return caps, nil
}

// Snapshot captures the pre-change host state plus the active configuration.
func (b *Backend) Snapshot(ctx context.Context) (*platform.NetworkSnapshot, error) {
	snapshot := platform.NewSnapshot(platform.BackendLinuxNFTables)
	if value, err := b.currentIPv4Forwarding(); err == nil {
		snapshot.IPv4Forwarding = value
	} else {
		return nil, err
	}
	if b.active != nil {
		routing := b.active.routing
		snapshot.Routing = &routing
		nat := b.active.nat
		snapshot.NAT = &nat
		snapshot.NFTablesTable = nat.TableName
		if rules, err := b.listRules(ctx); err == nil && strings.TrimSpace(rules) != "" {
			snapshot.Rules = splitLines(rules)
		}
		if routes, err := b.listRoutes(ctx, routing.TableID); err == nil && strings.TrimSpace(routes) != "" {
			snapshot.Routes = splitLines(routes)
		}
	}
	if snapshot.NFTablesTable == "" {
		snapshot.NFTablesTable = b.tableName
	}
	return snapshot, nil
}

// Restore returns the host to the captured state. It is best effort: every step
// runs even if an earlier one failed, so a partially applied setup is still
// unwound as far as possible.
func (b *Backend) Restore(ctx context.Context, snapshot *platform.NetworkSnapshot) error {
	if err := snapshot.Validate(platform.BackendLinuxNFTables); err != nil {
		return err
	}
	var failures []string

	if snapshot.NFTablesTable != "" {
		if exists, err := b.tableExists(ctx, snapshot.NFTablesTable); err != nil {
			failures = append(failures, "check nftables table: "+err.Error())
		} else if exists {
			if err := b.runner.run(ctx, b.runner.nftPath, "delete", "table", nftFamily, snapshot.NFTablesTable); err != nil {
				failures = append(failures, "delete nftables table: "+err.Error())
			}
		}
	}
	if snapshot.Routing != nil && snapshot.Routing.TableID != 0 {
		if err := b.removePolicyRouting(ctx, *snapshot.Routing); err != nil {
			failures = append(failures, "remove policy routing: "+err.Error())
		}
	}
	if len(snapshot.RPFilter) > 0 {
		if err := b.restoreRPFilter(snapshot.RPFilter); err != nil {
			failures = append(failures, "restore rp_filter: "+err.Error())
		}
	}
	if snapshot.IPv4Forwarding != "" {
		if _, err := writeProcSys(procIPv4Forward, snapshot.IPv4Forwarding); err != nil {
			failures = append(failures, "restore ip_forward: "+err.Error())
		}
	}
	if len(failures) > 0 {
		joined := strings.Join(failures, "; ")
		return platform.NewError(platform.CodeCommandFailed, "restore network snapshot: "+joined).
			WithDetail("failures", joined)
	}
	return nil
}

// ObservedState reads the live host state. Crash recovery compares this against
// the persisted desired state; it is never served from a cache.
func (b *Backend) ObservedState(ctx context.Context) (*platform.ObservedState, error) {
	state := &platform.ObservedState{TUNPresent: map[string]bool{}}
	if value, err := b.currentIPv4Forwarding(); err == nil {
		state.IPv4Forwarding = value == "1"
	}
	if interfaces, err := b.detectInterfaces(ctx); err == nil {
		state.Interfaces = interfaces
		for _, iface := range interfaces {
			if strings.HasPrefix(iface.Name, "tun") || strings.HasPrefix(iface.Name, "utun") {
				state.TUNPresent[iface.Name] = true
			}
		}
	}
	if rules, err := b.listRules(ctx); err == nil && strings.TrimSpace(rules) != "" {
		state.Rules = splitLines(rules)
	}
	if b.active != nil && b.active.routing.TableID != 0 {
		if routes, err := b.listRoutes(ctx, b.active.routing.TableID); err == nil && strings.TrimSpace(routes) != "" {
			state.Routes = splitLines(routes)
		}
	}
	if tables, err := b.listTables(ctx); err == nil {
		state.NFTables = tables
	}
	return state, nil
}

func splitLines(text string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines
}

// Linux capability bits read from /proc/self/status.
const (
	capNetAdmin = 12
	capNetRaw   = 13
)

// readEffectiveCaps parses CapEff from /proc/self/status.
func readEffectiveCaps() (uint64, error) {
	file, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, errors.New("malformed CapEff line")
		}
		return strconv.ParseUint(fields[1], 16, 64)
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("CapEff not found in /proc/self/status")
}

func hasCapability(mask uint64, bit uint) bool {
	return mask&(1<<bit) != 0
}
