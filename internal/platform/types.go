package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// NetworkInterface is OpenSurge's platform-neutral view of a host interface.
// It deliberately carries only what the control plane and diagnostics need, so
// it can be produced from netlink, ioctl or a test fixture with equal ease.
type NetworkInterface struct {
	Name         string   `json:"name"`
	Index        int      `json:"index"`
	MTU          int      `json:"mtu"`
	HardwareAddr string   `json:"hardware_addr,omitempty"`
	Flags        []string `json:"flags,omitempty"`
	IPv4         []string `json:"ipv4,omitempty"`
	IPv6         []string `json:"ipv6,omitempty"`
}

// IsUp reports whether the interface carries the "up" flag.
func (i NetworkInterface) IsUp() bool {
	for _, flag := range i.Flags {
		if flag == "up" {
			return true
		}
	}
	return false
}

// HasIPv4 reports whether addr is configured on this interface.
func (i NetworkInterface) HasIPv4(addr string) bool {
	target := net.ParseIP(addr)
	if target == nil {
		return false
	}
	for _, cidr := range i.IPv4 {
		ip := cidr
		if idx := indexOfByte(cidr, '/'); idx >= 0 {
			ip = cidr[:idx]
		}
		if parsed := net.ParseIP(ip); parsed != nil && parsed.Equal(target) {
			return true
		}
	}
	return false
}

func indexOfByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// NetworkConfig describes the gateway topology the operator asked for.
type NetworkConfig struct {
	LANInterface    string `json:"lan_interface"`
	LANIP           string `json:"lan_ip"`
	LANCIDR         string `json:"lan_cidr"`
	UpstreamGateway string `json:"upstream_gateway"`
	// SameLAN is true when the upstream gateway is reached over the same
	// interface that serves LAN clients, which is the manual same-LAN gateway
	// mode shipped in v1.
	SameLAN bool `json:"same_lan"`
}

// RoutingConfig drives SetupPolicyRouting. TableID, RulePriority and FwMark are
// chosen by the caller and persisted, so a restart recognises OpenSurge's own
// rules instead of re-deriving them and colliding with something else.
type RoutingConfig struct {
	LANInterface    string `json:"lan_interface"`
	LANCIDR         string `json:"lan_cidr"`
	TUNDevice       string `json:"tun_device"`
	UpstreamGateway string `json:"upstream_gateway,omitempty"`
	TableID         uint32 `json:"table_id"`
	RulePriority    uint32 `json:"rule_priority"`
	FwMark          uint32 `json:"fw_mark"`
	// DirectFallback requests the direct-egress variant of the routing table
	// instead of the TUN variant.
	DirectFallback bool `json:"direct_fallback"`
}

// NATConfig drives SetupNAT. On Linux the rendered ruleset marks forwarded LAN
// traffic with FwMark and, only when Masquerade is set, source-NATs it.
type NATConfig struct {
	LANInterface string `json:"lan_interface"`
	LANCIDR      string `json:"lan_cidr"`
	TUNDevice    string `json:"tun_device,omitempty"`
	UpstreamGateway string `json:"upstream_gateway,omitempty"`
	FwMark       uint32 `json:"fw_mark"`
	TableName    string `json:"table_name"`
	Masquerade   bool   `json:"masquerade"`
}

// Capabilities reports host features preflight needs to check before any
// mutation happens. A false value must always be accompanied by a reason the UI
// can show.
type Capabilities struct {
	Kernel             string            `json:"kernel,omitempty"`
	Architecture       string            `json:"architecture,omitempty"`
	TUNDeviceNode      bool              `json:"tun_device_node"`
	CapNetAdmin        bool              `json:"cap_net_admin"`
	CapNetRaw          bool              `json:"cap_net_raw"`
	NFTables           bool              `json:"nftables"`
	NFTablesJSON       bool              `json:"nftables_json"`
	IProute2           bool              `json:"iproute2"`
	// IPv4ForwardSysctl reports whether the forwarding knob can be read.
	// IPv4ForwardWritable reports whether it can be *written*. They differ in a
	// container: Docker mounts /proc/sys read-only by default even with
	// CAP_NET_ADMIN, so the value may be readable but immutable. Preflight must
	// check the writable one before promising it can enable forwarding.
	IPv4ForwardSysctl  bool              `json:"ipv4_forward_sysctl"`
	IPv4ForwardWritable bool             `json:"ipv4_forward_writable"`
	NetworkNamespace   string            `json:"network_namespace,omitempty"`
	HostNetworkMode    bool              `json:"host_network_mode"`
	Missing            map[string]string `json:"missing,omitempty"`
}

// Report adds a human-readable reason for a missing capability.
func (c *Capabilities) Report(key, reason string) {
	if c.Missing == nil {
		c.Missing = map[string]string{}
	}
	c.Missing[key] = reason
}

// ObservedState is the truth read from the host, used by crash recovery to
// reconcile against persisted desired state. It never comes from a cache.
type ObservedState struct {
	IPv4Forwarding bool              `json:"ipv4_forwarding"`
	Interfaces     []NetworkInterface `json:"interfaces"`
	Rules          []string          `json:"rules,omitempty"`
	Routes         []string          `json:"routes,omitempty"`
	NFTables       []string          `json:"nftables,omitempty"`
	TUNPresent     map[string]bool   `json:"tun_present,omitempty"`
}

// NetworkSnapshot is what a backend needs in order to undo everything it did.
// It is persisted to /data/state so that a crash, a container restart or a NAS
// reboot can still restore the host even when the process that made the change
// is gone.
//
// The active configuration is stored alongside the observed values. Without it
// a snapshot restored by a later process could only guess which table id,
// fwmark or interface to undo, and guessing during rollback is how hosts get
// broken.
type NetworkSnapshot struct {
	SchemaVersion  int               `json:"schema_version"`
	Backend        BackendName       `json:"backend"`
	CapturedAt     time.Time         `json:"captured_at"`
	IPv4Forwarding string            `json:"ipv4_forwarding,omitempty"`
	RPFilter       map[string]string `json:"rp_filter,omitempty"`
	NFTablesTable  string            `json:"nftables_table,omitempty"`
	Rules          []string          `json:"rules,omitempty"`
	Routes         []string          `json:"routes,omitempty"`
	Routing        *RoutingConfig    `json:"routing,omitempty"`
	NAT            *NATConfig        `json:"nat,omitempty"`
	// Applied records which setup steps completed, so rollback knows which
	// teardowns are actually required.
	Applied AppliedSteps `json:"applied"`
}

// AppliedSteps tracks transaction progress for inverse-order rollback.
type AppliedSteps struct {
	NAT            bool `json:"nat"`
	PolicyRouting  bool `json:"policy_routing"`
	IPv4Forwarding bool `json:"ipv4_forwarding"`
	DirectFallback bool `json:"direct_fallback"`
}

// NewSnapshot builds a snapshot stamped with the current schema version.
func NewSnapshot(backend BackendName) *NetworkSnapshot {
	return &NetworkSnapshot{
		SchemaVersion: SnapshotSchemaVersion,
		Backend:       backend,
		CapturedAt:    time.Now().UTC(),
	}
}

// Validate rejects a snapshot this build cannot safely restore.
func (s *NetworkSnapshot) Validate(expected BackendName) error {
	if s == nil {
		return errors.New("network snapshot is nil")
	}
	if s.SchemaVersion != SnapshotSchemaVersion {
		return fmt.Errorf("network snapshot schema %d is not supported by this build (%d)", s.SchemaVersion, SnapshotSchemaVersion)
	}
	if s.Backend != expected {
		return fmt.Errorf("network snapshot was captured by backend %q, not %q", s.Backend, expected)
	}
	return nil
}

// Encode serialises the snapshot for persistence.
func (s *NetworkSnapshot) Encode() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// DecodeSnapshot parses a persisted snapshot.
func DecodeSnapshot(data []byte) (*NetworkSnapshot, error) {
	var snapshot NetworkSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode network snapshot: %w", err)
	}
	return &snapshot, nil
}
