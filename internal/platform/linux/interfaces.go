//go:build linux

package linux

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"open-mihomo-gateway/internal/platform"
)

type ipAddr struct {
	IfIndex  int      `json:"ifindex"`
	IfName   string   `json:"ifname"`
	MTU      int      `json:"mtu"`
	Flags    []string `json:"flags"`
	Address  string   `json:"address"`
	AddrInfo []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
	} `json:"addr_info"`
}

func (b *Backend) detectInterfaces(ctx context.Context) ([]platform.NetworkInterface, error) {
	if b.runner.ipPath == "" {
		return nil, platform.NewError(platform.CodeIPRoute2Unavailable, "iproute2 (ip) not found in PATH")
	}
	out, err := b.runner.output(ctx, b.runner.ipPath, "-j", "addr", "show")
	if err != nil {
		return nil, err
	}
	var parsed []ipAddr
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, platform.NewError(platform.CodeCommandFailed, "parse ip addr output").Wrap(err)
	}
	interfaces := make([]platform.NetworkInterface, 0, len(parsed))
	for _, entry := range parsed {
		iface := platform.NetworkInterface{
			Name:         entry.IfName,
			Index:        entry.IfIndex,
			MTU:          entry.MTU,
			HardwareAddr: entry.Address,
			Flags:        normaliseFlags(entry.Flags),
		}
		for _, info := range entry.AddrInfo {
			address := info.Local
			if info.PrefixLen > 0 {
				address = fmt.Sprintf("%s/%d", info.Local, info.PrefixLen)
			}
			switch info.Family {
			case "inet":
				iface.IPv4 = append(iface.IPv4, address)
			case "inet6":
				iface.IPv6 = append(iface.IPv6, address)
			}
		}
		interfaces = append(interfaces, iface)
	}
	return interfaces, nil
}

func normaliseFlags(flags []string) []string {
	normalised := make([]string, 0, len(flags))
	for _, flag := range flags {
		normalised = append(normalised, strings.ToLower(flag))
	}
	return normalised
}

func (b *Backend) interfaceByName(ctx context.Context, name string) (platform.NetworkInterface, error) {
	if err := validateInterfaceName(name); err != nil {
		return platform.NetworkInterface{}, err
	}
	interfaces, err := b.detectInterfaces(ctx)
	if err != nil {
		return platform.NetworkInterface{}, err
	}
	for _, iface := range interfaces {
		if iface.Name == name {
			return iface, nil
		}
	}
	return platform.NetworkInterface{}, platform.NewError(platform.CodeInterfaceNotFound, "network interface not found").
		WithDetail("interface", name)
}

// validateTopology performs all non-mutating host checks. Ownership collision
// is skipped only when validating a reload candidate while the live OpenSurge
// runtime legitimately still owns the configured ids.
func (b *Backend) validateTopology(ctx context.Context, cfg platform.NetworkConfig) error {
	if err := validateInterfaceName(cfg.LANInterface); err != nil {
		return err
	}
	iface, err := b.interfaceByName(ctx, cfg.LANInterface)
	if err != nil {
		return err
	}
	if !iface.IsUp() {
		return platform.NewError(platform.CodeInterfaceNotUp, "LAN interface is not up").
			WithDetail("interface", cfg.LANInterface)
	}
	if strings.TrimSpace(cfg.UpstreamInterface) != "" {
		if err := validateInterfaceName(cfg.UpstreamInterface); err != nil {
			return err
		}
		upstream, err := b.interfaceByName(ctx, cfg.UpstreamInterface)
		if err != nil {
			return err
		}
		if !upstream.IsUp() {
			return platform.NewError(platform.CodeInterfaceNotUp, "upstream interface is not up").
				WithDetail("interface", cfg.UpstreamInterface)
		}
	}

	lanIP, err := validateIPv4(cfg.LANIP)
	if err != nil {
		return err
	}
	if !iface.HasIPv4(lanIP.String()) {
		return platform.NewError(platform.CodeLANIPMissing, "gateway LAN IP is not configured on the LAN interface").
			WithDetails(map[string]string{
				"interface": cfg.LANInterface,
				"lan_ip":    lanIP.String(),
			})
	}
	network, err := validateCIDR(cfg.LANCIDR)
	if err != nil {
		return err
	}
	if !network.Contains(lanIP) {
		return platform.NewError(platform.CodeSubnetInvalid, "gateway LAN IP is outside the configured LAN subnet").
			WithDetails(map[string]string{
				"lan_ip":   lanIP.String(),
				"lan_cidr": network.String(),
			})
	}
	if gateway := strings.TrimSpace(cfg.UpstreamGateway); gateway != "" {
		gwIP, err := validateIPv4(gateway)
		if err != nil {
			return platform.NewError(platform.CodeInvalidArgument, "upstream gateway is not an IPv4 address").
				WithDetail("upstream_gateway", gateway)
		}
		if gwIP.Equal(lanIP) {
			return platform.NewError(platform.CodeUpstreamConflict, "upstream gateway is the OpenSurge address itself").
				WithDetail("upstream_gateway", gateway)
		}
		if cfg.SameLAN && !network.Contains(gwIP) {
			return platform.NewError(platform.CodeUpstreamUnreachable,
				"upstream gateway is not inside the LAN subnet, so it is not reachable on-link in same-LAN mode").
				WithDetails(map[string]string{
					"upstream_gateway": gwIP.String(),
					"lan_cidr":         network.String(),
				})
		}
	}

	interfaces, err := b.detectInterfaces(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range interfaces {
		if candidate.Name == cfg.LANInterface {
			continue
		}
		if candidate.HasIPv4(lanIP.String()) {
			return platform.NewError(platform.CodeLANIPConflict,
				"gateway LAN IP is also configured on another interface; remove the duplicate before starting").
				WithDetails(map[string]string{
					"lan_ip":    lanIP.String(),
					"interface": candidate.Name,
				})
		}
	}
	if cfg.SkipOwnershipCheck {
		return nil
	}
	return b.validateOwnership(ctx, cfg)
}

type neighbourEntry struct {
	Dev    string   `json:"dev"`
	To     string   `json:"to"`
	LLAddr string   `json:"lladdr"`
	State  []string `json:"state"`
}

func (b *Backend) Neighbours(ctx context.Context, iface string) ([]Neighbour, error) {
	if err := validateInterfaceName(iface); err != nil {
		return nil, err
	}
	if b.runner.ipPath == "" {
		return nil, platform.NewError(platform.CodeIPRoute2Unavailable, "iproute2 (ip) not found in PATH")
	}
	out, err := b.runner.output(ctx, b.runner.ipPath, "-j", "neigh", "show", "dev", iface)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var parsed []neighbourEntry
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, platform.NewError(platform.CodeCommandFailed, "parse ip neigh output").Wrap(err)
	}
	result := make([]Neighbour, 0, len(parsed))
	for _, entry := range parsed {
		reachable := false
		for _, state := range entry.State {
			if state == "REACHABLE" || state == "STALE" || state == "DELAY" || state == "PROBE" {
				reachable = true
				break
			}
		}
		result = append(result, Neighbour{
			Interface: entry.Dev,
			IPv4:      entry.To,
			MAC:       entry.LLAddr,
			Reachable: reachable,
		})
	}
	return result, nil
}

type Neighbour struct {
	Interface string
	IPv4      string
	MAC       string
	Reachable bool
}

func (b *Backend) DefaultRoute(ctx context.Context) (via string, dev string, err error) {
	if b.runner.ipPath == "" {
		return "", "", platform.NewError(platform.CodeIPRoute2Unavailable, "iproute2 (ip) not found in PATH")
	}
	out, err := b.runner.output(ctx, b.runner.ipPath, "-j", "route", "show", "default")
	if err != nil {
		return "", "", err
	}
	var routes []struct {
		Dst     string `json:"dst"`
		Gateway string `json:"gateway"`
		Dev     string `json:"dev"`
	}
	if err := json.Unmarshal(out, &routes); err != nil {
		return "", "", platform.NewError(platform.CodeCommandFailed, "parse ip route output").Wrap(err)
	}
	for _, route := range routes {
		if route.Dst == "" || route.Dst == "default" {
			return route.Gateway, route.Dev, nil
		}
	}
	return "", "", nil
}

func hasTUNNode() bool {
	_, err := os.Stat("/dev/net/tun")
	return err == nil
}

func parseIPv4List(values []string) []net.IP {
	result := make([]net.IP, 0, len(values))
	for _, value := range values {
		ip := value
		if idx := strings.IndexByte(value, '/'); idx >= 0 {
			ip = value[:idx]
		}
		if parsed := net.ParseIP(ip); parsed != nil && parsed.To4() != nil {
			result = append(result, parsed.To4())
		}
	}
	return result
}
