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

// ipAddr is the subset of `ip -j addr show` this backend consumes. The command
// is invoked with -j so the result is machine-readable; no human-oriented output
// is parsed as the source of truth anywhere in this package.
type ipAddr struct {
	IfIndex   int    `json:"ifindex"`
	IfName    string `json:"ifname"`
	MTU       int    `json:"mtu"`
	Flags     []string `json:"flags"`
	Address   string `json:"address"`
	AddrInfo  []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
	} `json:"addr_info"`
}

// detectInterfaces lists interfaces using the JSON output of iproute2.
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

// normaliseFlags lowercases interface flags. `ip -j` emits them upper case;
// storing them lower case keeps NetworkInterface.IsUp's comparison valid on
// every iproute2 version instead of silently reporting every interface as down.
func normaliseFlags(flags []string) []string {
	normalised := make([]string, 0, len(flags))
	for _, flag := range flags {
		normalised = append(normalised, strings.ToLower(flag))
	}
	return normalised
}

// interfaceByName resolves one interface.
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

// validateTopology checks the requested topology without mutating anything.
// It is the part of preflight that needs real host knowledge.
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
				"lan_ip":  lanIP.String(),
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
		if !network.Contains(gwIP) {
			return platform.NewError(platform.CodeUpstreamUnreachable,
				"upstream gateway is not inside the LAN subnet, so it is not reachable on-link").
				WithDetails(map[string]string{
					"upstream_gateway": gwIP.String(),
					"lan_cidr":         network.String(),
				})
		}
	}
	// A duplicate address on another interface makes the forwarding decision
	// ambiguous, which shows up as intermittent black holes rather than a clean
	// failure. Reject it up front.
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
	return nil
}

// neighbourEntry is one row of `ip -j neigh show`, used for device discovery
// where upstream relied on macOS `arp -n`.
type neighbourEntry struct {
	Dev    string `json:"dev"`
	To     string `json:"to"`
	LLAddr string `json:"lladdr"`
	State  []string `json:"state"`
}

// Neighbours returns reachable L2 neighbours on an interface, the Linux
// replacement for the upstream macOS ARP cache reader.
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

// Neighbour is a discovered L2 neighbour.
type Neighbour struct {
	Interface string
	IPv4      string
	MAC       string
	Reachable bool
}

// DefaultRoute reports the IPv4 default route, if any.
func (b *Backend) DefaultRoute(ctx context.Context) (via string, dev string, err error) {
	if b.runner.ipPath == "" {
		return "", "", platform.NewError(platform.CodeIPRoute2Unavailable, "iproute2 (ip) not found in PATH")
	}
	out, err := b.runner.output(ctx, b.runner.ipPath, "-j", "route", "show", "default")
	if err != nil {
		return "", "", err
	}
	var routes []struct {
		Dst    string   `json:"dst"`
		Gateway string  `json:"gateway"`
		Dev    string   `json:"dev"`
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

// hasTUNNode reports whether /dev/net/tun exists, without requiring the device
// to be opened yet.
func hasTUNNode() bool {
	_, err := os.Stat("/dev/net/tun")
	return err == nil
}

// parseIPv4List is a small helper used by diagnostics.
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
