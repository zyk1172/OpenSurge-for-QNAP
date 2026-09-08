package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// NetworkInterface is OpenSurge's platform-neutral view of a host interface.
type NetworkInterface struct {
	Name         string   `json:"name"`
	Index        int      `json:"index"`
	MTU          int      `json:"mtu"`
	HardwareAddr string   `json:"hardware_addr,omitempty"`
	Flags        []string `json:"flags,omitempty"`
	IPv4         []string `json:"ipv4,omitempty"`
	IPv6         []string `json:"ipv6,omitempty"`
}

func (i NetworkInterface) IsUp() bool {
	for _, flag := range i.Flags {
		if flag == "up" {
			return true
		}
	}
	return false
}

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

// NetworkConfig describes topology plus kernel resources OpenSurge wants to
// own. SkipOwnershipCheck is used only for validating a reload candidate while
// the currently applied OpenSurge runtime legitimately still owns those ids.
type NetworkConfig struct {
	LANInterface       string `json:"lan_interface"`
	LANIP              string `json:"lan_ip"`
	LANCIDR            string `json:"lan_cidr"`
	UpstreamInterface  string `json:"upstream_interface,omitempty"`
	UpstreamGateway    string `json:"upstream_gateway"`
	NFTTableName       string `json:"nft_table_name,omitempty"`
	FwMark             uint32 `json:"fw_mark,omitempty"`
	RouteTableID       uint32 `json:"route_table_id,omitempty"`
	RouteRulePriority  uint32 `json:"route_rule_priority,omitempty"`
	SameLAN            bool   `json:"same_lan"`
	SkipOwnershipCheck bool   `json:"-"`
}

// RoutingConfig drives SetupPolicyRouting and is persisted as the cleanup
// recipe so a fresh process can remove the exact rule/routes it applied.
type RoutingConfig struct {
	LANInterface      string `json:"lan_interface"`
	UpstreamInterface string `json:"upstream_interface,omitempty"`
	LANCIDR           string `json:"lan_cidr"`
	TUNDevice         string `json:"tun_device"`
	UpstreamGateway   string `json:"upstream_gateway,omitempty"`
	TableID           uint32 `json:"table_id"`
	RulePriority      uint32 `json:"rule_priority"`
	FwMark            uint32 `json:"fw_mark"`
	DirectFallback    bool   `json:"direct_fallback"`
}

// NATConfig drives SetupNAT and is persisted for cross-process cleanup.
type NATConfig struct {
	LANInterface      string `json:"lan_interface"`
	UpstreamInterface string `json:"upstream_interface,omitempty"`
	LANCIDR           string `json:"lan_cidr"`
	TUNDevice         string `json:"tun_device,omitempty"`
	UpstreamGateway   string `json:"upstream_gateway,omitempty"`
	FwMark            uint32 `json:"fw_mark"`
	TableName         string `json:"table_name"`
	Masquerade        bool   `json:"masquerade"`
}

type Capabilities struct {
	Kernel               string            `json:"kernel,omitempty"`
	Architecture         string            `json:"architecture,omitempty"`
	TUNDeviceNode        bool              `json:"tun_device_node"`
	CapNetAdmin          bool              `json:"cap_net_admin"`
	CapNetRaw            bool              `json:"cap_net_raw"`
	NFTables             bool              `json:"nftables"`
	NFTablesJSON         bool              `json:"nftables_json"`
	IProute2             bool              `json:"iproute2"`
	IPv4ForwardSysctl    bool              `json:"ipv4_forward_sysctl"`
	IPv4ForwardWritable  bool              `json:"ipv4_forward_writable"`
	IPv4ForwardReady     bool              `json:"ipv4_forward_ready"`
	NetworkNamespace     string            `json:"network_namespace,omitempty"`
	HostNetworkMode      bool              `json:"host_network_mode"`
	Missing              map[string]string `json:"missing,omitempty"`
}

func (c *Capabilities) Report(key, reason string) {
	if c.Missing == nil {
		c.Missing = map[string]string{}
	}
	c.Missing[key] = reason
}

type ObservedState struct {
	IPv4Forwarding bool               `json:"ipv4_forwarding"`
	Interfaces     []NetworkInterface `json:"interfaces"`
	Rules          []string           `json:"rules,omitempty"`
	Routes         []string           `json:"routes,omitempty"`
	NFTables       []string           `json:"nftables,omitempty"`
	TUNPresent     map[string]bool    `json:"tun_present,omitempty"`
}

// NetworkSnapshot persists both pre-change host values and the intended
// OpenSurge-owned resource recipe. Stop/recovery must use this rather than
// process-local backend memory.
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
	Applied        AppliedSteps      `json:"applied"`
}

type AppliedSteps struct {
	NAT            bool `json:"nat"`
	PolicyRouting  bool `json:"policy_routing"`
	IPv4Forwarding bool `json:"ipv4_forwarding"`
	DirectFallback bool `json:"direct_fallback"`
}

func NewSnapshot(backend BackendName) *NetworkSnapshot {
	return &NetworkSnapshot{
		SchemaVersion: SnapshotSchemaVersion,
		Backend:       backend,
		CapturedAt:    time.Now().UTC(),
	}
}

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

func (s *NetworkSnapshot) Encode() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

func DecodeSnapshot(data []byte) (*NetworkSnapshot, error) {
	var snapshot NetworkSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode network snapshot: %w", err)
	}
	return &snapshot, nil
}
