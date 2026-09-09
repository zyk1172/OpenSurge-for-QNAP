package deployment

import (
	"fmt"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	ManagedGatewayContainer = "opensurge-gateway"
	ManagedGatewayNetwork   = "opensurge-managed-lan"
	DefaultGatewayDataPath  = "/share/Container/opensurge"
	DefaultGatewayImage     = "opensurge-for-qnap:dev"
)

var interfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

type Spec struct {
	ParentInterface string `json:"parent_interface"`
	ContainerIP     string `json:"container_ip"`
	Subnet          string `json:"subnet"`
	Gateway         string `json:"gateway"`
	DataPath        string `json:"data_path"`
}

type ApplyRequest struct {
	Spec  Spec   `json:"spec"`
	Image string `json:"image"`
}

type Status struct {
	Configured bool `json:"configured"`
	Running    bool `json:"running"`
	Spec       Spec `json:"spec"`
	GatewayURL string `json:"gateway_url,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (s *Spec) Normalize() {
	s.ParentInterface = strings.TrimSpace(s.ParentInterface)
	s.ContainerIP = strings.TrimSpace(s.ContainerIP)
	s.Subnet = strings.TrimSpace(s.Subnet)
	s.Gateway = strings.TrimSpace(s.Gateway)
	s.DataPath = filepath.Clean(strings.TrimSpace(s.DataPath))
	if s.DataPath == "." || s.DataPath == "" {
		s.DataPath = DefaultGatewayDataPath
	}
}

func (s Spec) Validate() error {
	if !interfaceNamePattern.MatchString(s.ParentInterface) {
		return fmt.Errorf("parent_interface must be a Linux/QNAP interface name")
	}
	ip, err := netip.ParseAddr(s.ContainerIP)
	if err != nil || !ip.Is4() {
		return fmt.Errorf("container_ip must be a valid IPv4 address")
	}
	prefix, err := netip.ParsePrefix(s.Subnet)
	if err != nil || !prefix.Addr().Is4() {
		return fmt.Errorf("subnet must be a valid IPv4 CIDR")
	}
	prefix = prefix.Masked()
	gateway, err := netip.ParseAddr(s.Gateway)
	if err != nil || !gateway.Is4() {
		return fmt.Errorf("gateway must be a valid IPv4 address")
	}
	if !prefix.Contains(ip) {
		return fmt.Errorf("container_ip must be inside subnet")
	}
	if !prefix.Contains(gateway) {
		return fmt.Errorf("gateway must be inside subnet")
	}
	if ip == gateway {
		return fmt.Errorf("container_ip must not equal gateway")
	}
	if prefix.Bits() <= 30 {
		if ip == prefix.Addr() {
			return fmt.Errorf("container_ip must not be the network address")
		}
		last := lastIPv4(prefix)
		if ip == last {
			return fmt.Errorf("container_ip must not be the broadcast address")
		}
	}
	clean := filepath.Clean(s.DataPath)
	if !filepath.IsAbs(clean) || clean == "/" || clean == "/share" || !strings.HasPrefix(clean, "/share/") {
		return fmt.Errorf("data_path must be a dedicated absolute QNAP path under /share")
	}
	if strings.ContainsRune(clean, '\x00') {
		return fmt.Errorf("data_path contains an invalid NUL byte")
	}
	return nil
}

func lastIPv4(prefix netip.Prefix) netip.Addr {
	addr := prefix.Masked().Addr().As4()
	hostBits := 32 - prefix.Bits()
	mask := uint32(0xffffffff)
	if hostBits == 32 {
		mask = 0
	} else if hostBits > 0 {
		mask <<= hostBits
	}
	value := uint32(addr[0])<<24 | uint32(addr[1])<<16 | uint32(addr[2])<<8 | uint32(addr[3])
	value |= ^mask
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)})
}
