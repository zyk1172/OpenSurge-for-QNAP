//go:build linux

package linux

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"open-mihomo-gateway/internal/platform"
)

type Backend struct {
	runner *runner

	tableName  string
	tempDir    string
	tunTimeout time.Duration

	// Process-local convenience only. Persisted NetworkSnapshot is authoritative
	// for Stop and crash recovery.
	active *activeConfig
}

type activeConfig struct {
	routing platform.RoutingConfig
	nat     platform.NATConfig
}

type Option func(*Backend)

func WithTableName(name string) Option { return func(b *Backend) { b.tableName = name } }
func WithTempDir(dir string) Option    { return func(b *Backend) { b.tempDir = dir } }
func WithTUNTimeout(timeout time.Duration) Option {
	return func(b *Backend) { b.tunTimeout = timeout }
}

func New(options ...Option) (*Backend, error) {
	b := &Backend{
		runner:     newRunner(),
		tableName:  DefaultTableName,
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

func (b *Backend) Name() platform.BackendName { return platform.BackendLinuxNFTables }

func (b *Backend) DetectInterfaces(ctx context.Context) ([]platform.NetworkInterface, error) {
	return b.detectInterfaces(ctx)
}

func (b *Backend) InterfaceByName(ctx context.Context, name string) (platform.NetworkInterface, error) {
	return b.interfaceByName(ctx, name)
}

func (b *Backend) ValidateTopology(ctx context.Context, cfg platform.NetworkConfig) error {
	return b.validateTopology(ctx, cfg)
}

func (b *Backend) EnableIPv4Forwarding(ctx context.Context) (func(context.Context) error, error) {
	restore, _, err := b.enableIPv4Forwarding(ctx)
	return restore, err
}

func (b *Backend) SetupNAT(ctx context.Context, cfg platform.NATConfig) error {
	if cfg.TableName == "" {
		cfg.TableName = b.tableName
	}
	if err := b.applyNAT(ctx, cfg); err != nil {
		return err
	}
	b.ensureActive().nat = cfg
	return nil
}

func (b *Backend) RemoveNAT(ctx context.Context) error {
	if b.active != nil && strings.TrimSpace(b.active.nat.TableName) != "" {
		return b.removeNATTable(ctx, b.active.nat.TableName)
	}
	return b.removeNAT(ctx)
}

func (b *Backend) SetupPolicyRouting(ctx context.Context, cfg platform.RoutingConfig) error {
	if err := b.applyPolicyRouting(ctx, cfg); err != nil {
		return err
	}
	b.ensureActive().routing = cfg
	return nil
}

func (b *Backend) PolicyRoutingPresent(ctx context.Context, cfg platform.RoutingConfig) (bool, error) {
	return b.policyRoutingPresent(ctx, cfg)
}

func (b *Backend) RemovePolicyRouting(ctx context.Context) error {
	if b.active == nil {
		return nil
	}
	return b.removePolicyRouting(ctx, b.active.routing)
}

func (b *Backend) SwitchToDirectFallback(ctx context.Context, cfg platform.RoutingConfig) error {
	if b.active == nil {
		return platform.NewError(platform.CodeInvalidArgument,
			"direct fallback requires an applied OpenSurge routing recipe")
	}
	cfg.DirectFallback = true
	if err := b.applyPolicyRouting(ctx, cfg); err != nil {
		return err
	}
	b.active.routing = cfg

	// Same-LAN iif mode preserves the client's source address and sends packets
	// directly back to the real router on the same L2 segment. It needs no
	// masquerade and therefore remains usable on QNAP kernels without nf_tables.
	if effectiveRuleMode(cfg) == platform.RoutingRuleIngressInterface {
		return nil
	}
	if strings.TrimSpace(b.active.nat.TableName) == "" {
		return platform.NewError(platform.CodeInvalidArgument,
			"fwmark direct fallback requires the persisted/applied NAT recipe")
	}
	nat := b.active.nat
	nat.Masquerade = true
	nat.UpstreamGateway = cfg.UpstreamGateway
	if err := b.applyNAT(ctx, nat); err != nil {
		return err
	}
	b.active.nat = nat
	return nil
}

func (b *Backend) EnsureTUN(ctx context.Context) error { return b.ensureTUN(ctx) }

func (b *Backend) WaitForTUN(ctx context.Context, device string) (platform.NetworkInterface, error) {
	return b.waitForTUN(ctx, device)
}

func (b *Backend) ensureActive() *activeConfig {
	if b.active == nil {
		b.active = &activeConfig{}
	}
	return b.active
}

func currentNetworkNamespace() (string, error) {
	value, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return "", platform.NewError(platform.CodeSnapshotInvalid,
			"cannot identify current Linux network namespace").Wrap(err)
	}
	if strings.TrimSpace(value) == "" {
		return "", platform.NewError(platform.CodeSnapshotInvalid,
			"current Linux network namespace identity is empty")
	}
	return value, nil
}

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
		caps.Report("tun_device_node", "/dev/net/tun is missing; pass the device into the container")
	}

	effective, err := readEffectiveCaps()
	if err != nil {
		caps.Report("capabilities", "cannot read /proc/self/status capabilities: "+err.Error())
	} else {
		caps.CapNetAdmin = hasCapability(effective, capNetAdmin)
		caps.CapNetRaw = hasCapability(effective, capNetRaw)
		if !caps.CapNetAdmin {
			caps.Report("cap_net_admin", "CAP_NET_ADMIN is missing; add NET_ADMIN to the container")
		}
		if !caps.CapNetRaw {
			caps.Report("cap_net_raw", "CAP_NET_RAW is missing; DHCP/ICMP features may be unavailable")
		}
	}

	caps.NFTables, caps.NFTablesJSON, _ = b.nftCapabilities(ctx)
	if !caps.NFTables {
		caps.Report("nftables", "nft was not found in PATH")
	}
	if caps.NFTables && !caps.NFTablesJSON {
		caps.Report("nftables_json", "installed nft lacks JSON output required for ownership-safe operations")
	}
	caps.IProute2 = b.runner.ipPath != ""
	if !caps.IProute2 {
		caps.Report("iproute2", "ip was not found in PATH")
	}

	currentForward := ""
	if value, err := readProcSys(procIPv4Forward); err == nil {
		currentForward = value
		caps.IPv4ForwardSysctl = true
	} else {
		caps.Report("ipv4_forward_sysctl", "cannot read /proc/sys/net/ipv4/ip_forward")
	}
	caps.IPv4ForwardWritable = procSysWritable(procIPv4Forward)
	caps.IPv4ForwardReady = currentForward == "1" || caps.IPv4ForwardWritable
	if !caps.IPv4ForwardReady {
		caps.Report("ipv4_forward_ready",
			"IPv4 forwarding is disabled and cannot be changed; set net.ipv4.ip_forward=1 in the container namespace")
	} else if !caps.IPv4ForwardWritable && currentForward == "1" {
		caps.Report("ipv4_forward_writable",
			"IPv4 forwarding is already enabled by the container runtime; /proc/sys is read-only")
	}

	if namespace, err := currentNetworkNamespace(); err == nil {
		caps.NetworkNamespace = namespace
	} else {
		caps.Report("network_namespace", err.Error())
	}
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

// Snapshot captures pre-change state and the namespace identity. The gateway
// manager attaches exact NAT/routing recipes before journaling the snapshot.
func (b *Backend) Snapshot(ctx context.Context) (*platform.NetworkSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot := platform.NewSnapshot(platform.BackendLinuxNFTables)
	namespace, err := currentNetworkNamespace()
	if err != nil {
		return nil, err
	}
	snapshot.NetworkNamespace = namespace
	value, err := b.currentIPv4Forwarding()
	if err != nil {
		return nil, err
	}
	snapshot.IPv4Forwarding = value
	return snapshot, nil
}

// Restore is the authoritative cross-process cleanup path. If an isolated
// container was restarted, its old network namespace no longer exists and all
// per-namespace nftables/routes/sysctls disappeared with it. Replaying the old
// snapshot into the new namespace would be harmful, so namespace mismatch is a
// safe no-op. In host-network mode the namespace identity remains the same and
// cleanup proceeds normally.
func (b *Backend) Restore(ctx context.Context, snapshot *platform.NetworkSnapshot) error {
	if err := snapshot.Validate(platform.BackendLinuxNFTables); err != nil {
		return err
	}
	currentNS, err := currentNetworkNamespace()
	if err != nil {
		return err
	}
	if snapshot.NetworkNamespace == "" {
		return platform.NewError(platform.CodeSnapshotInvalid,
			"network snapshot does not contain a namespace identity")
	}
	if currentNS != snapshot.NetworkNamespace {
		return nil
	}

	var failures []string
	if snapshot.Applied.PolicyRouting {
		if snapshot.Routing == nil {
			failures = append(failures, "policy routing journaled but routing recipe is missing")
		} else if err := b.removePolicyRouting(ctx, *snapshot.Routing); err != nil {
			failures = append(failures, "remove policy routing: "+err.Error())
		}
	}
	if snapshot.Applied.NAT {
		table := snapshot.NFTablesTable
		if table == "" && snapshot.NAT != nil {
			table = snapshot.NAT.TableName
		}
		if table == "" {
			failures = append(failures, "NAT journaled but nftables table name is missing")
		} else if err := b.removeNATTable(ctx, table); err != nil {
			failures = append(failures, "remove nftables table: "+err.Error())
		}
	}
	if len(snapshot.RPFilter) > 0 {
		if err := b.restoreRPFilter(snapshot.RPFilter); err != nil {
			failures = append(failures, "restore rp_filter: "+err.Error())
		}
	}
	if snapshot.Applied.IPv4Forwarding && snapshot.IPv4Forwarding != "" {
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

const (
	capNetAdmin = 12
	capNetRaw   = 13
)

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
	return 0, fmt.Errorf("CapEff not found in /proc/self/status")
}

func hasCapability(mask uint64, bit uint) bool {
	return mask&(1<<bit) != 0
}
