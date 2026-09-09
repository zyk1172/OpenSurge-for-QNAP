package linuxnetwork

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"open-mihomo-gateway/internal/macosnetwork"
)

// This package is the Linux implementation of the control-plane discovery
// callbacks. The control API still carries legacy macosnetwork DTO names from
// upstream; only the DTOs are reused here. No macOS command is executed.

type ipAddress struct {
	IfName  string   `json:"ifname"`
	Address string   `json:"address"`
	Flags   []string `json:"flags"`
	AddrInfo []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
		Scope     string `json:"scope"`
	} `json:"addr_info"`
}

type ipRoute struct {
	Dst      string `json:"dst"`
	Gateway  string `json:"gateway"`
	Dev      string `json:"dev"`
	PrefSrc  string `json:"prefsrc"`
	Protocol string `json:"protocol"`
	Scope    string `json:"scope"`
}

type ipNeighbor struct {
	Dst    string   `json:"dst"`
	Dev    string   `json:"dev"`
	LLAddr string   `json:"lladdr"`
	State  []string `json:"state"`
}

func Discover(ctx context.Context, _ string, interfaceName string) (macosnetwork.Snapshot, error) {
	interfaceName = strings.TrimSpace(interfaceName)
	if interfaceName == "" {
		return macosnetwork.Snapshot{}, fmt.Errorf("interface is required")
	}
	addresses, err := interfaceAddresses(ctx, interfaceName)
	if err != nil {
		return macosnetwork.Snapshot{}, err
	}
	if len(addresses) != 1 {
		return macosnetwork.Snapshot{}, fmt.Errorf("interface %q was not found", interfaceName)
	}
	entry := addresses[0]
	snapshot := macosnetwork.Snapshot{
		NetworkService: interfaceName,
		Interface:      interfaceName,
		HardwareAddr:   strings.ToLower(entry.Address),
		IPv4Mode:       macosnetwork.IPv4ModeManual,
		DNS:            resolvConfDNS(),
	}
	for _, info := range entry.AddrInfo {
		if info.Family == "inet" && snapshot.IPv4 == "" {
			if net.ParseIP(info.Local).To4() == nil || info.PrefixLen <= 0 || info.PrefixLen >= 32 {
				continue
			}
			snapshot.IPv4 = info.Local
			snapshot.SubnetMask = net.IP(net.CIDRMask(info.PrefixLen, 32)).String()
		}
	}
	routes, err := routeList(ctx, false, "default", interfaceName)
	if err == nil {
		for _, route := range routes {
			if route.Dev == interfaceName && net.ParseIP(route.Gateway).To4() != nil {
				snapshot.Router = route.Gateway
				break
			}
		}
	}
	v6Routes, err := routeList(ctx, true, "default", interfaceName)
	if err == nil && len(v6Routes) > 0 {
		snapshot.IPv6Default = true
		local := map[string]struct{}{}
		for _, info := range entry.AddrInfo {
			if info.Family == "inet6" {
				if ip := net.ParseIP(info.Local); ip != nil && ip.To4() == nil {
					local[ip.String()] = struct{}{}
				}
			}
		}
		selfOnly := true
		for _, route := range v6Routes {
			gateway := net.ParseIP(strings.Split(route.Gateway, "%")[0])
			if gateway == nil {
				selfOnly = false
				break
			}
			if _, ok := local[gateway.String()]; !ok {
				selfOnly = false
				break
			}
		}
		snapshot.IPv6DefaultSelfOnly = selfOnly
	}
	if snapshot.IPv4 == "" || snapshot.SubnetMask == "" {
		return macosnetwork.Snapshot{}, fmt.Errorf("interface %q does not expose a usable IPv4 address", interfaceName)
	}
	if snapshot.Router == "" {
		return macosnetwork.Snapshot{}, fmt.Errorf("interface %q has no IPv4 default gateway", interfaceName)
	}
	return snapshot, nil
}

func DiscoverDefault(ctx context.Context) (macosnetwork.Snapshot, error) {
	route, err := LookupRoute(ctx, "default")
	if err != nil {
		return macosnetwork.Snapshot{}, err
	}
	return Discover(ctx, route.Interface, route.Interface)
}

func ListInterfaces(ctx context.Context) ([]macosnetwork.InterfaceOption, error) {
	entries, err := interfaceAddresses(ctx, "")
	if err != nil {
		return nil, err
	}
	options := make([]macosnetwork.InterfaceOption, 0, len(entries))
	for _, entry := range entries {
		if entry.IfName == "lo" || !flagPresent(entry.Flags, "UP") {
			continue
		}
		option := macosnetwork.InterfaceOption{Interface: entry.IfName, NetworkService: entry.IfName}
		for _, info := range entry.AddrInfo {
			if info.Family != "inet6" {
				continue
			}
			ip := net.ParseIP(info.Local)
			if ip != nil && ip.To4() == nil && ip.IsLinkLocalUnicast() {
				option.IPv6LinkLocal = ip.String()
				break
			}
		}
		options = append(options, option)
	}
	return options, nil
}

func DiscoverNeighbors(ctx context.Context, interfaceName string) ([]macosnetwork.Neighbor, error) {
	interfaceName = strings.TrimSpace(interfaceName)
	if interfaceName == "" {
		return nil, fmt.Errorf("interface is required")
	}
	out, err := run(ctx, "ip", "-j", "neigh", "show", "dev", interfaceName)
	if err != nil {
		return nil, err
	}
	var entries []ipNeighbor
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("decode ip neighbor JSON: %w", err)
	}
	neighbors := make([]macosnetwork.Neighbor, 0, len(entries))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		ip := net.ParseIP(entry.Dst)
		mac, macErr := net.ParseMAC(entry.LLAddr)
		if ip == nil || ip.To4() == nil || macErr != nil || entry.Dev != interfaceName || unusableNeighbor(entry.State) {
			continue
		}
		key := ip.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		neighbors = append(neighbors, macosnetwork.Neighbor{IP: key, MAC: strings.ToLower(mac.String()), Interface: interfaceName})
	}
	return neighbors, nil
}

func LookupRoute(ctx context.Context, destination string) (macosnetwork.RouteSelection, error) {
	destination = strings.TrimSpace(destination)
	var out []byte
	var err error
	if destination == "" || destination == "default" {
		out, err = run(ctx, "ip", "-j", "route", "show", "default")
	} else {
		out, err = run(ctx, "ip", "-j", "route", "get", destination)
	}
	if err != nil {
		return macosnetwork.RouteSelection{}, err
	}
	var routes []ipRoute
	if err := json.Unmarshal(out, &routes); err != nil {
		return macosnetwork.RouteSelection{}, fmt.Errorf("decode ip route JSON: %w", err)
	}
	for _, route := range routes {
		if strings.TrimSpace(route.Dev) == "" {
			continue
		}
		prefix := route.Dst
		if prefix == "default" {
			prefix = "0.0.0.0/0"
		}
		return macosnetwork.RouteSelection{Interface: route.Dev, Gateway: route.Gateway, Prefix: prefix}, nil
	}
	return macosnetwork.RouteSelection{}, fmt.Errorf("route lookup for %q returned no usable route", destination)
}

func PingRouter(ctx context.Context, router string) error {
	if net.ParseIP(strings.TrimSpace(router)).To4() == nil {
		return fmt.Errorf("router must be IPv4")
	}
	_, err := run(ctx, "ping", "-c", "1", "-W", "1", router)
	return err
}

func interfaceAddresses(ctx context.Context, interfaceName string) ([]ipAddress, error) {
	args := []string{"-j", "addr", "show"}
	if strings.TrimSpace(interfaceName) != "" {
		args = append(args, "dev", interfaceName)
	}
	out, err := run(ctx, "ip", args...)
	if err != nil {
		return nil, err
	}
	var entries []ipAddress
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("decode ip address JSON: %w", err)
	}
	return entries, nil
}

func routeList(ctx context.Context, ipv6 bool, destination, interfaceName string) ([]ipRoute, error) {
	args := []string{"-j"}
	if ipv6 {
		args = append(args, "-6")
	}
	args = append(args, "route", "show", destination)
	if strings.TrimSpace(interfaceName) != "" {
		args = append(args, "dev", interfaceName)
	}
	out, err := run(ctx, "ip", args...)
	if err != nil {
		return nil, err
	}
	var routes []ipRoute
	if err := json.Unmarshal(out, &routes); err != nil {
		return nil, fmt.Errorf("decode ip route JSON: %w", err)
	}
	return routes, nil
}

func resolvConfDNS() []string {
	file, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return []string{}
	}
	defer file.Close()
	var result []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || fields[0] != "nameserver" || net.ParseIP(fields[1]) == nil {
			continue
		}
		result = append(result, fields[1])
	}
	return result
}

func flagPresent(flags []string, want string) bool {
	for _, flag := range flags {
		if strings.EqualFold(flag, want) {
			return true
		}
	}
	return false
}

func unusableNeighbor(states []string) bool {
	for _, state := range states {
		switch strings.ToUpper(state) {
		case "FAILED", "INCOMPLETE", "NOARP":
			return true
		}
	}
	return false
}

func run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("%s is required for Linux network discovery: %w", binary, err)
	}
	commandCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", binary, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
