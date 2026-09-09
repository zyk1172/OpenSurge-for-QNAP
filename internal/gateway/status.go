package gateway

import (
	"context"
	"fmt"
	"strings"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/device"
	"open-mihomo-gateway/internal/dhcp"
	"open-mihomo-gateway/internal/mihomo"
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/runtime"
)

type Status struct {
	Gateway             string `json:"gateway"`
	RuntimeState        string `json:"runtime_state,omitempty"`
	Interface           string `json:"interface"`
	LANIP               string `json:"lan_ip"`
	DataPlane           string `json:"data_plane"`
	DHCP                string `json:"dhcp"`
	DHCPEnabled         bool   `json:"dhcp_enabled"`
	Mihomo              string `json:"mihomo"`
	MihomoError         string `json:"mihomo_error,omitempty"`
	TUN                 string `json:"tun"`
	TUNInterface        string `json:"tun_interface,omitempty"`
	TUNError            string `json:"tun_error,omitempty"`
	NFTables            string `json:"nftables"`
	Forwarding          string `json:"forwarding"`
	ClientCount         int    `json:"client_count"`
	DNSIPv6             bool   `json:"dns_ipv6"`
	TUNIPv6Requested    string `json:"tun_ipv6_requested"`
	// IPv6Takeover tells the UI that downstream IPv6 is intentionally not
	// supported in v1. Clients that still receive IPv6 from the main router can
	// bypass OpenSurge, so this must be surfaced rather than silently dropped.
	IPv6Takeover string `json:"ipv6_takeover"`
}

func (m Manager) Status(ctx context.Context) (Status, error) {
	state, exists, err := runtime.LoadState(m.paths.StateFile)
	if err != nil {
		return Status{}, err
	}
	clients, err := device.LoadLeases(m.paths.LeaseFile)
	if err != nil {
		return Status{}, err
	}

	gatewayStatus := "stopped"
	dhcpStatus := "stopped"
	mihomoStatus := "stopped"
	mihomoError := ""
	tunStatus := "disabled"
	tunInterface := ""
	tunError := ""
	if m.cfg.Transparent.TUNEnabled() {
		tunStatus = "stopped"
	}
	dataPlane := "tun-nft-fwmark"
	nftStatus := "not_applied"
	if m.cfg.Gateway.SameLAN() {
		dataPlane = "tun-iif-route"
		nftStatus = "not_required"
	}
	runtimeState := "none"
	dnsIPv6 := m.cfg.DNS.IPv6
	tunIPv6Requested := m.cfg.Transparent.TUNIPv6
	ipv6Takeover := "unsupported"
	if exists {
		dnsIPv6 = state.DNSIPv6
		if state.NetworkSnapshot != nil && state.NetworkSnapshot.Routing != nil {
			mode := state.NetworkSnapshot.Routing.RuleMode
			if mode == "" {
				mode = platform.RoutingRuleFWMark
			}
			if mode == platform.RoutingRuleIngressInterface {
				dataPlane = "tun-iif-route"
				nftStatus = "not_required"
			} else {
				dataPlane = "tun-nft-fwmark"
			}
		}
		bootSession, bootErr := runtime.CurrentBootSession()
		if bootErr != nil {
			return Status{}, fmt.Errorf("determine current boot session: %w", bootErr)
		}
		if !state.BelongsToBoot(bootSession) {
			gatewayStatus = "degraded"
			runtimeState = "interrupted"
		} else {
			runtimeState = "active"
			appliedCfg := m.cfg
			dhcpRunning := false
			mihomoRunning := false
			mihomoManager := mihomo.New(appliedCfg, m.paths)
			if trackedProcessRunning(m.gatewayDeps(), state.PIDMihomo, state.MihomoProcessFingerprint, mihomoManager.Running) {
				mihomoRunning = true
				mihomoStatus = "running"
				version, versionErr, runtimeTUN, tunErr := fetchMihomoRuntime(ctx, appliedCfg)
				if versionErr == nil && version.Version != "" {
					mihomoStatus = "running (" + version.Version + ")"
				} else if versionErr != nil {
					mihomoError = versionErr.Error()
				}
				if m.cfg.Transparent.TUNEnabled() {
					switch {
					case tunErr != nil:
						tunStatus = "unknown"
						tunError = tunErr.Error()
					case runtimeTUN.Enabled:
						tunStatus = "ready"
						tunInterface = runtimeTUN.Device
					default:
						tunStatus = "failed"
						tunInterface = runtimeTUN.Device
						tunError = "mihomo runtime config reports TUN disabled"
					}
				}
			}
			dhcpManager := dhcp.New(m.cfg, m.paths)
			if trackedProcessRunning(m.gatewayDeps(), state.PIDDNSMasq, state.DNSMasqProcessFingerprint, dhcpManager.Running) {
				dhcpRunning = true
				dhcpStatus = "running"
			}
			// A failed runtime read is an observability warning, not evidence that
			// the already-running TUN data plane stopped. An explicit disabled
			// response remains a real degraded condition.
			tunReady := !m.cfg.Transparent.TUNEnabled() || tunStatus == "ready" || tunStatus == "unknown"
			if dhcpRunning && mihomoRunning && tunReady {
				gatewayStatus = "running"
			} else {
				gatewayStatus = "degraded"
			}
			if dataPlane != "tun-iif-route" && state.NATApplied {
				nftStatus = "applied"
				if backend, err := m.backend(); err == nil {
					if observed, err := backend.ObservedState(ctx); err == nil {
						found := false
						for _, table := range observed.NFTables {
							if table == m.cfg.Transparent.NFTTableName {
								found = true
							}
						}
						if !found {
							nftStatus = "missing"
						}
					}
				}
			}
		}
	}
	forwarding := "unknown"
	if backend, err := m.backend(); err == nil {
		if observed, err := backend.ObservedState(ctx); err == nil {
			if observed.IPv4Forwarding {
				forwarding = "enabled"
			} else {
				forwarding = "disabled"
			}
		}
	}

	return Status{
		Gateway:             gatewayStatus,
		RuntimeState:        runtimeState,
		Interface:           m.cfg.Gateway.Interface,
		LANIP:               m.cfg.Gateway.LANIP,
		DataPlane:           dataPlane,
		DHCP:                dhcpStatus,
		DHCPEnabled:         m.cfg.DHCP.Enabled,
		Mihomo:              mihomoStatus,
		MihomoError:         mihomoError,
		TUN:                 tunStatus,
		TUNInterface:        tunInterface,
		TUNError:            tunError,
		NFTables:            nftStatus,
		Forwarding:          forwarding,
		ClientCount:         len(clients),
		DNSIPv6:             dnsIPv6,
		TUNIPv6Requested:    tunIPv6Requested,
		IPv6Takeover:        ipv6Takeover,
	}, nil
}

type versionResult struct {
	value mihomo.Version
	err   error
}

type tunResult struct {
	value mihomo.TUNRuntimeState
	err   error
}

func fetchMihomoRuntime(ctx context.Context, cfg config.Config) (mihomo.Version, error, mihomo.TUNRuntimeState, error) {
	if !cfg.Transparent.TUNEnabled() {
		version, err := mihomo.FetchVersion(ctx, cfg)
		return version, err, mihomo.TUNRuntimeState{}, nil
	}
	versionCh := make(chan versionResult, 1)
	tunCh := make(chan tunResult, 1)
	go func() {
		value, err := mihomo.FetchVersion(ctx, cfg)
		versionCh <- versionResult{value: value, err: err}
	}()
	go func() {
		value, err := mihomo.FetchTUNRuntimeState(ctx, cfg)
		tunCh <- tunResult{value: value, err: err}
	}()
	version := <-versionCh
	tun := <-tunCh
	return version.value, version.err, tun.value, tun.err
}

func (s Status) Format() string {
	dnsmasqLabel := "DHCP"
	if !s.DHCPEnabled {
		dnsmasqLabel = "DNS"
	}
	tunLabel := s.TUN
	if s.TUNInterface != "" {
		tunLabel += " (" + s.TUNInterface + ")"
	}
	if s.TUNError != "" {
		tunLabel += ": " + s.TUNError
	}
	lines := []string{
		fmt.Sprintf("Gateway: %s", s.Gateway),
		fmt.Sprintf("Runtime state: %s", s.RuntimeState),
		fmt.Sprintf("Interface: %s", s.Interface),
		fmt.Sprintf("LAN IP: %s", s.LANIP),
		fmt.Sprintf("Data plane: %s", s.DataPlane),
		fmt.Sprintf("%s: %s", dnsmasqLabel, s.DHCP),
		fmt.Sprintf("mihomo: %s", s.Mihomo),
		fmt.Sprintf("TUN: %s", tunLabel),
		fmt.Sprintf("IPv6 DNS queries: %t", s.DNSIPv6),
		fmt.Sprintf("Downstream IPv6 takeover: requested=%s state=%s", s.TUNIPv6Requested, s.IPv6Takeover),
		fmt.Sprintf("nftables: %s", s.NFTables),
		fmt.Sprintf("IP forwarding: %s", s.Forwarding),
		fmt.Sprintf("Clients: %d", s.ClientCount),
	}
	return strings.Join(lines, "\n") + "\n"
}
