package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/device"
	"open-mihomo-gateway/internal/dhcp"
	"open-mihomo-gateway/internal/mihomo"
	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/platform/factory"
	"open-mihomo-gateway/internal/process"
	"open-mihomo-gateway/internal/runtime"
)

// Manager owns the gateway lifecycle: start, stop, reload and rollback.
type Manager struct {
	cfg   config.Config
	paths runtime.Paths
	deps  gatewayDeps
}

func New(cfg config.Config) Manager {
	return Manager{cfg: cfg, paths: runtime.NewPaths(cfg), deps: defaultGatewayDeps()}
}

type dhcpService interface {
	Check() error
	WriteConfig() error
	Start() (int, error)
	Stop(int) error
	Running(int) bool
}

type mihomoService interface {
	Check() error
	WriteConfig() error
	ValidateWrittenConfig() error
	ValidateWrittenConfigContext(context.Context) error
	Start() (int, error)
	Stop(int) error
	Running(int) bool
}

type gatewayDeps struct {
	geteuid            func() int
	loadState          func(string) (runtime.State, bool, error)
	saveState          func(string, runtime.State) error
	removeState        func(string) error
	ensure             func(runtime.Paths) error
	newDHCP            func(config.Config, runtime.Paths) dhcpService
	newMihomo          func(config.Config, runtime.Paths) mihomoService
	newBackend         func() (platform.NetworkBackend, error)
	interfaces         func() ([]net.Interface, error)
	interfaceByName    func(string) (*net.Interface, error)
	interfaceAddrs     func(*net.Interface) ([]net.Addr, error)
	probeReservationIP func(ip string, expectedMAC string) error
	currentBoot        func() (runtime.BootSession, error)
	processFingerprint func(int) (string, error)
	processMatches     func(int, string) (bool, error)
	warmTailscale      func(context.Context, config.Config) error
	stopPrepared       func(config.Config) error
	now                func() time.Time
}

func defaultGatewayDeps() gatewayDeps {
	return gatewayDeps{
		geteuid:     os.Geteuid,
		loadState:   runtime.LoadState,
		saveState:   runtime.SaveState,
		removeState: runtime.RemoveState,
		ensure:      runtime.Ensure,
		newDHCP: func(cfg config.Config, paths runtime.Paths) dhcpService {
			return dhcp.New(cfg, paths)
		},
		newMihomo: func(cfg config.Config, paths runtime.Paths) mihomoService {
			return mihomo.New(cfg, paths)
		},
		newBackend: func() (platform.NetworkBackend, error) {
			return factory.New()
		},
		interfaces:      net.Interfaces,
		interfaceByName: net.InterfaceByName,
		interfaceAddrs: func(iface *net.Interface) ([]net.Addr, error) {
			return iface.Addrs()
		},
		probeReservationIP: probeReservationIPConflict,
		currentBoot:        runtime.CurrentBootSession,
		processFingerprint: process.Fingerprint,
		processMatches:     process.MatchesFingerprint,
		warmTailscale:      mihomo.InitiateTailscaleWarmup,
		stopPrepared:       mihomo.StopPreparedLocked,
		now:                time.Now,
	}
}

func (m Manager) gatewayDeps() gatewayDeps {
	if m.deps.geteuid == nil {
		return defaultGatewayDeps()
	}
	return m.deps
}

func (m Manager) backend() (platform.NetworkBackend, error) {
	deps := m.gatewayDeps()
	if deps.newBackend == nil {
		return factory.New()
	}
	return deps.newBackend()
}

func currentBoot(deps gatewayDeps) (runtime.BootSession, error) {
	if deps.currentBoot != nil {
		return deps.currentBoot()
	}
	return runtime.CurrentBootSession()
}

func processFingerprint(deps gatewayDeps, pid int) (string, error) {
	if deps.processFingerprint != nil {
		return deps.processFingerprint(pid)
	}
	return process.Fingerprint(pid)
}

func processMatches(deps gatewayDeps, pid int, fingerprint string) (bool, error) {
	if deps.processMatches != nil {
		return deps.processMatches(pid, fingerprint)
	}
	return process.MatchesFingerprint(pid, fingerprint)
}

func stopTrackedProcess(deps gatewayDeps, name string, pid int, fingerprint string, stop func(int) error) error {
	if pid <= 0 {
		return nil
	}
	if strings.TrimSpace(fingerprint) == "" {
		return stop(pid)
	}
	matches, err := processMatches(deps, pid, fingerprint)
	if err != nil {
		return fmt.Errorf("verify %s pid %d before stop: %w", name, pid, err)
	}
	if !matches {
		return nil
	}
	return stop(pid)
}

func trackedProcessRunning(deps gatewayDeps, pid int, fingerprint string, running func(int) bool) bool {
	if pid <= 0 {
		return false
	}
	if strings.TrimSpace(fingerprint) == "" {
		return running(pid)
	}
	matches, err := processMatches(deps, pid, fingerprint)
	return err == nil && matches && running(pid)
}

func (m Manager) Start(ctx context.Context) error {
	if m.gatewayDeps().geteuid() != 0 {
		return fmt.Errorf("start requires root privileges; OpenSurge runs as root inside its container")
	}
	return m.withLifecycleLock(func() error { return m.start(ctx) })
}

func (m Manager) StartLocked(ctx context.Context) error {
	return m.start(ctx)
}

func (m Manager) start(ctx context.Context) error {
	return m.startWithCommit(ctx, nil)
}

func (m Manager) StartCandidateLocked(ctx context.Context, commit func() error) error {
	if commit == nil {
		return fmt.Errorf("candidate commit is required")
	}
	return m.startWithCommit(ctx, commit)
}

func (m Manager) startWithCommit(ctx context.Context, commit func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ReportProgress(ctx, "checking_runtime")
	deps := m.gatewayDeps()
	if deps.geteuid() != 0 {
		return fmt.Errorf("start requires root privileges; OpenSurge runs as root inside its container")
	}
	if _, exists, err := deps.loadState(m.paths.StateFile); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("gateway state already exists; run stop first")
	}
	if err := config.Normalize(&m.cfg); err != nil {
		return err
	}
	if err := config.PrepareDevicePolicy(&m.cfg); err != nil {
		return err
	}
	if err := config.Validate(m.cfg); err != nil {
		return err
	}
	if err := mihomo.PrepareDevicePolicy(&m.cfg); err != nil {
		return err
	}
	if err := m.stopPreparedEngine(deps); err != nil {
		return err
	}
	backend, err := m.backend()
	if err != nil {
		return err
	}
	ReportProgress(ctx, "validating_network")
	if err := deps.ensure(m.paths); err != nil {
		return err
	}
	bootSession, err := currentBoot(deps)
	if err != nil {
		return fmt.Errorf("determine current boot session: %w", err)
	}

	dhcpManager := deps.newDHCP(m.cfg, m.paths)
	mihomoManager := deps.newMihomo(m.cfg, m.paths)
	if err := m.preflight(ctx, backend, dhcpManager, mihomoManager, deps); err != nil {
		return err
	}
	ReportProgress(ctx, "checking_reservations")
	if err := m.checkReservationConflicts(deps); err != nil {
		return err
	}
	ReportProgress(ctx, "preparing_config")
	if err := mihomoManager.WriteConfig(); err != nil {
		return err
	}
	if err := dhcpManager.WriteConfig(); err != nil {
		return err
	}
	ReportProgress(ctx, "validating_config")
	if err := mihomoManager.ValidateWrittenConfigContext(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if commit != nil {
		ReportProgress(ctx, "saving_config")
		if err := commit(); err != nil {
			return err
		}
	}

	ReportProgress(ctx, "saving_runtime")
	profileDigest, err := config.MihomoProfileDigest(m.cfg)
	if err != nil {
		return fmt.Errorf("digest imported mihomo profile: %w", err)
	}
	if bundle := m.cfg.DevicePolicy.Bundle; bundle != nil {
		if err := dhcp.ReconcilePolicyLeases(m.paths.LeaseFile, bundle.Compiled.Reservations); err != nil {
			return err
		}
		if err := device.WritePolicyBundleSnapshot(m.paths.DevicePolicyApplied, *bundle); err != nil {
			return err
		}
	}

	// Build the complete cleanup recipe before the first host mutation. TUN has a
	// configured deterministic name, so it can be persisted before mihomo creates
	// the interface.
	natConfig := platform.NATConfig{
		LANInterface:      m.cfg.Gateway.Interface,
		UpstreamInterface: m.cfg.Gateway.UpstreamInterface,
		LANCIDR:           m.cfg.Gateway.LANCIDR,
		TUNDevice:         m.cfg.Transparent.TUNDevice,
		UpstreamGateway:   m.cfg.Gateway.UpstreamGateway,
		FwMark:            m.cfg.Transparent.FwMark,
		TableName:         m.cfg.Transparent.NFTTableName,
	}
	routingConfig := platform.RoutingConfig{
		LANInterface:      m.cfg.Gateway.Interface,
		UpstreamInterface: m.cfg.Gateway.UpstreamInterface,
		LANCIDR:           m.cfg.Gateway.LANCIDR,
		TUNDevice:         m.cfg.Transparent.TUNDevice,
		UpstreamGateway:   m.cfg.Gateway.UpstreamGateway,
		TableID:           m.cfg.Transparent.RouteTableID,
		RulePriority:      m.cfg.Transparent.RouteRulePriority,
		FwMark:            m.cfg.Transparent.FwMark,
	}

	snapshot, err := backend.Snapshot(ctx)
	if err != nil {
		return err
	}
	snapshot.NFTablesTable = natConfig.TableName
	snapshot.NAT = &natConfig
	snapshot.Routing = &routingConfig

	state := runtime.State{
		StartedAt:       deps.now(),
		BootSessionID:   bootSession.ID,
		ProfileDigest:   profileDigest,
		DNSIPv6:         m.cfg.DNS.IPv6,
		NetworkSnapshot: snapshot,
	}
	if bundle := m.cfg.DevicePolicy.Bundle; bundle != nil {
		state.DevicePolicyDigest = bundle.Digest
	}
	if err := deps.saveState(m.paths.StateFile, state); err != nil {
		_ = device.RemovePolicyBundleSnapshot(m.paths.DevicePolicyApplied)
		return err
	}

	ReportProgress(ctx, "enabling_forwarding")
	if err := ctx.Err(); err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}
	if _, err := backend.EnableIPv4Forwarding(ctx); err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}
	state.ForwardingApplied = true
	state.NetworkSnapshot.Applied.IPv4Forwarding = true
	if err := deps.saveState(m.paths.StateFile, state); err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}

	ReportProgress(ctx, "starting_mihomo")
	if err := ctx.Err(); err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}
	mihomoPID, err := mihomoManager.Start()
	if err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}
	state.PIDMihomo = mihomoPID
	state.MihomoProcessFingerprint, err = processFingerprint(deps, mihomoPID)
	if err != nil || (mihomoPID > 0 && state.MihomoProcessFingerprint == "") {
		if err == nil {
			err = fmt.Errorf("mihomo pid %d disappeared before its identity could be recorded", mihomoPID)
		}
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}
	if err := deps.saveState(m.paths.StateFile, state); err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}

	ReportProgress(ctx, "starting_dns")
	pid, err := dhcpManager.Start()
	if err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}
	state.PIDDNSMasq = pid
	state.DNSMasqProcessFingerprint, err = processFingerprint(deps, pid)
	if err != nil || (pid > 0 && state.DNSMasqProcessFingerprint == "") {
		if err == nil {
			err = fmt.Errorf("dnsmasq pid %d disappeared before its identity could be recorded", pid)
		}
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}
	if err := deps.saveState(m.paths.StateFile, state); err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}

	ReportProgress(ctx, "waiting_for_tun")
	tunDevice, err := backend.WaitForTUN(ctx, m.cfg.Transparent.TUNDevice)
	if err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}
	state.TUNDevice = tunDevice.Name
	natConfig.TUNDevice = tunDevice.Name
	routingConfig.TUNDevice = tunDevice.Name
	state.NetworkSnapshot.NAT = &natConfig
	state.NetworkSnapshot.Routing = &routingConfig
	if err := deps.saveState(m.paths.StateFile, state); err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}

	ReportProgress(ctx, "applying_firewall")
	if err := backend.SetupNAT(ctx, natConfig); err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}
	state.NATApplied = true
	state.NetworkSnapshot.Applied.NAT = true
	if err := deps.saveState(m.paths.StateFile, state); err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}

	ReportProgress(ctx, "applying_routes")
	if err := backend.SetupPolicyRouting(ctx, routingConfig); err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}
	state.RoutingApplied = true
	state.NetworkSnapshot.Applied.PolicyRouting = true
	if err := deps.saveState(m.paths.StateFile, state); err != nil {
		return m.rollback(ctx, err, state, backend, dhcpManager, mihomoManager)
	}

	fmt.Printf("Gateway runtime prepared in %s\n", m.paths.Dir)
	if mihomoPID > 0 {
		fmt.Printf("mihomo started with pid %d\n", mihomoPID)
	}
	if pid > 0 {
		fmt.Printf("dnsmasq started with pid %d\n", pid)
	}
	m.warmManagedTailscale(ctx, deps)
	fmt.Printf("TUN %s active; nftables table %s and routing table %d applied\n",
		tunDevice.Name, natConfig.TableName, routingConfig.TableID)
	return nil
}

func (m Manager) Reload(ctx context.Context) error {
	if m.gatewayDeps().geteuid() != 0 {
		return fmt.Errorf("reload requires root privileges")
	}
	return m.withLifecycleLock(func() error { return m.reload(ctx) })
}

func (m Manager) ReloadLocked(ctx context.Context) error {
	return m.reload(ctx)
}

func (m Manager) reload(ctx context.Context) error {
	ReportProgress(ctx, "checking_runtime")
	deps := m.gatewayDeps()
	if deps.geteuid() != 0 {
		return fmt.Errorf("reload requires root privileges")
	}
	state, exists, err := deps.loadState(m.paths.StateFile)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("gateway is not running; run start instead")
	}
	bootSession, err := currentBoot(deps)
	if err != nil {
		return fmt.Errorf("determine current boot session: %w", err)
	}
	if !state.BelongsToBoot(bootSession) {
		return fmt.Errorf("gateway runtime was interrupted by a container or host restart; run stop to recover it before reload")
	}
	dhcpManager := deps.newDHCP(m.cfg, m.paths)
	mihomoManager := deps.newMihomo(m.cfg, m.paths)
	if !trackedProcessRunning(deps, state.PIDDNSMasq, state.DNSMasqProcessFingerprint, dhcpManager.Running) ||
		!trackedProcessRunning(deps, state.PIDMihomo, state.MihomoProcessFingerprint, mihomoManager.Running) {
		return fmt.Errorf("gateway is degraded; reload requires both DNS and mihomo to be running")
	}
	if err := m.validateReloadCandidate(ctx); err != nil {
		return fmt.Errorf("reload candidate validation failed: %w", err)
	}
	if err := m.stop(ctx); err != nil {
		return fmt.Errorf("reload stop failed: %w", err)
	}
	if err := m.start(ctx); err != nil {
		return fmt.Errorf("reload start failed after gateway stop: %w", err)
	}
	return nil
}

func (m Manager) RestartMihomo(ctx context.Context) error {
	if m.gatewayDeps().geteuid() != 0 {
		return fmt.Errorf("restart-mihomo requires root privileges")
	}
	return m.withLifecycleLock(func() error { return m.restartMihomo(ctx) })
}

func (m Manager) restartMihomo(ctx context.Context) error {
	ReportProgress(ctx, "checking_runtime")
	deps := m.gatewayDeps()
	if deps.geteuid() != 0 {
		return fmt.Errorf("restart-mihomo requires root privileges")
	}
	state, exists, err := deps.loadState(m.paths.StateFile)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("gateway is not running; run start instead")
	}
	bootSession, err := currentBoot(deps)
	if err != nil {
		return fmt.Errorf("determine current boot session: %w", err)
	}
	if !state.BelongsToBoot(bootSession) {
		return fmt.Errorf("gateway runtime was interrupted by a restart; run stop to recover it first")
	}
	desiredProfileDigest, err := config.MihomoProfileDigest(m.cfg)
	if err != nil {
		return fmt.Errorf("digest current imported mihomo profile: %w", err)
	}
	if desiredProfileDigest != state.ProfileDigest {
		return fmt.Errorf("desired imported mihomo profile differs from the applied runtime; run reload instead")
	}

	mihomoManager := deps.newMihomo(m.cfg, m.paths)
	ReportProgress(ctx, "validating_config")
	if err := mihomoManager.ValidateWrittenConfig(); err != nil {
		return fmt.Errorf("prepared mihomo config validation failed: %w", err)
	}

	previousPID := state.PIDMihomo
	previousFingerprint := state.MihomoProcessFingerprint
	state.PIDMihomo = 0
	state.MihomoProcessFingerprint = ""
	if err := deps.saveState(m.paths.StateFile, state); err != nil {
		return fmt.Errorf("mark mihomo restart in runtime state: %w", err)
	}
	ReportProgress(ctx, "stopping_mihomo")
	if err := stopTrackedProcess(deps, "mihomo", previousPID, previousFingerprint, mihomoManager.Stop); err != nil {
		if trackedProcessRunning(deps, previousPID, previousFingerprint, mihomoManager.Running) {
			state.PIDMihomo = previousPID
			state.MihomoProcessFingerprint = previousFingerprint
		}
		return errors.Join(fmt.Errorf("stop mihomo pid %d: %w", previousPID, err), deps.saveState(m.paths.StateFile, state))
	}

	archivedLog, err := archiveMihomoLog(m.paths.MihomoLog, deps.now())
	if err != nil {
		return fmt.Errorf("archive mihomo log before restart: %w", err)
	}
	ReportProgress(ctx, "starting_mihomo")
	newPID, err := mihomoManager.Start()
	if err != nil {
		return fmt.Errorf("start replacement mihomo process: %w", err)
	}
	state.PIDMihomo = newPID
	state.MihomoProcessFingerprint, err = processFingerprint(deps, newPID)
	if err != nil || (newPID > 0 && state.MihomoProcessFingerprint == "") {
		if err == nil {
			err = fmt.Errorf("replacement mihomo pid %d disappeared before its identity could be recorded", newPID)
		}
		return errors.Join(err, mihomoManager.Stop(newPID))
	}
	if err := deps.saveState(m.paths.StateFile, state); err != nil {
		return errors.Join(fmt.Errorf("save replacement mihomo pid: %w", err), mihomoManager.Stop(newPID))
	}

	fmt.Printf("mihomo restarted with pid %d\n", newPID)
	if archivedLog != "" {
		fmt.Printf("previous mihomo log archived at %s\n", archivedLog)
	}
	return nil
}

func (m Manager) warmManagedTailscale(ctx context.Context, deps gatewayDeps) {
	if !m.cfg.Tailscale.Enabled || deps.warmTailscale == nil {
		return
	}
	ReportProgress(ctx, "initiating_tailscale")
	if err := deps.warmTailscale(ctx, m.cfg); err != nil {
		reportNotice(ctx, "tailscale_warmup_unavailable")
		return
	}
	reportNotice(ctx, "tailscale_warmup_started")
}

func archiveMihomoLog(path string, now time.Time) (string, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)
	archive := filepath.Join(filepath.Dir(path), fmt.Sprintf("%s-before-restart-%s%s", base, now.UTC().Format("20060102T150405.000000000Z"), ext))
	if err := os.Rename(path, archive); err != nil {
		return "", err
	}
	return archive, nil
}

func (m Manager) validateReloadCandidate(ctx context.Context) error {
	ReportProgress(ctx, "validating_network")
	parent := filepath.Dir(m.paths.Dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(parent, ".opensurge-reload-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)

	candidateConfig := m.cfg
	if err := config.Normalize(&candidateConfig); err != nil {
		return err
	}
	if err := config.PrepareDevicePolicy(&candidateConfig); err != nil {
		return err
	}
	candidateConfig.Runtime.Dir = temp
	candidateConfig.Mihomo.Config = filepath.Join(temp, "mihomo.yaml")
	if err := config.Validate(candidateConfig); err != nil {
		return err
	}
	candidate := Manager{cfg: candidateConfig, paths: runtime.NewPaths(candidateConfig), deps: m.gatewayDeps()}
	deps := candidate.gatewayDeps()
	if err := deps.ensure(candidate.paths); err != nil {
		return err
	}
	backend, err := candidate.backend()
	if err != nil {
		return err
	}
	dhcpManager := deps.newDHCP(candidate.cfg, candidate.paths)
	mihomoManager := deps.newMihomo(candidate.cfg, candidate.paths)
	if err := candidate.preflightWithOwnership(ctx, backend, dhcpManager, mihomoManager, deps, false); err != nil {
		return err
	}
	ReportProgress(ctx, "checking_reservations")
	if err := candidate.checkReservationConflicts(deps); err != nil {
		return err
	}
	ReportProgress(ctx, "preparing_config")
	if err := mihomoManager.WriteConfig(); err != nil {
		return err
	}
	if err := dhcpManager.WriteConfig(); err != nil {
		return err
	}
	ReportProgress(ctx, "validating_config")
	return mihomoManager.ValidateWrittenConfig()
}

func (m Manager) Stop(ctx context.Context) error {
	if m.gatewayDeps().geteuid() != 0 {
		return fmt.Errorf("stop requires root privileges")
	}
	return m.withLifecycleLock(func() error { return m.stop(ctx) })
}

func (m Manager) stop(ctx context.Context) error {
	ReportProgress(ctx, "checking_runtime")
	deps := m.gatewayDeps()
	if deps.geteuid() != 0 {
		return fmt.Errorf("stop requires root privileges")
	}
	if err := m.stopPreparedEngine(deps); err != nil {
		return err
	}
	state, exists, err := deps.loadState(m.paths.StateFile)
	if err != nil {
		return err
	}
	if exists {
		bootSession, bootErr := currentBoot(deps)
		if bootErr != nil {
			return fmt.Errorf("determine current boot session: %w", bootErr)
		}
		if !state.BelongsToBoot(bootSession) {
			return m.cleanupInterruptedRuntime(ctx, deps, state)
		}
	}
	backend, err := m.backend()
	if err != nil {
		return err
	}
	var cleanupErr error
	if exists {
		if state.NetworkSnapshot == nil && (state.ForwardingApplied || state.NATApplied || state.RoutingApplied) {
			return fmt.Errorf("runtime state records applied network changes but has no network snapshot; refusing blind cleanup")
		}
		ReportProgress(ctx, "restoring_network")
		if state.NetworkSnapshot != nil {
			cleanupErr = errors.Join(cleanupErr, backend.Restore(ctx, state.NetworkSnapshot))
		}
		ReportProgress(ctx, "stopping_dns")
		dhcpManager := deps.newDHCP(m.cfg, m.paths)
		cleanupErr = errors.Join(cleanupErr, stopTrackedProcess(deps, "dnsmasq", state.PIDDNSMasq, state.DNSMasqProcessFingerprint, dhcpManager.Stop))
		ReportProgress(ctx, "stopping_mihomo")
		mihomoManager := deps.newMihomo(m.cfg, m.paths)
		cleanupErr = errors.Join(cleanupErr, stopTrackedProcess(deps, "mihomo", state.PIDMihomo, state.MihomoProcessFingerprint, mihomoManager.Stop))
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	ReportProgress(ctx, "clearing_runtime")
	cleanupErr = errors.Join(cleanupErr, deps.removeState(m.paths.StateFile))
	cleanupErr = errors.Join(cleanupErr, device.RemovePolicyBundleSnapshot(m.paths.DevicePolicyApplied))
	if cleanupErr != nil {
		return cleanupErr
	}

	fmt.Println("Gateway stopped and runtime state cleared.")
	return nil
}

func (m Manager) stopPreparedEngine(deps gatewayDeps) error {
	stop := deps.stopPrepared
	if stop == nil {
		stop = mihomo.StopPreparedLocked
	}
	if err := stop(m.cfg); err != nil {
		return fmt.Errorf("release prepared engine before gateway transition: %w", err)
	}
	return nil
}

// cleanupInterruptedRuntime never signals stale PIDs. It may, however, restore
// persisted network state: snapshot cleanup is content-scoped and is exactly the
// safe recovery path required after a container/process restart.
func (m Manager) cleanupInterruptedRuntime(ctx context.Context, deps gatewayDeps, state runtime.State) error {
	if state.NetworkSnapshot != nil {
		backend, err := m.backend()
		if err != nil {
			return err
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		ReportProgress(cleanupCtx, "restoring_network")
		if err := backend.Restore(cleanupCtx, state.NetworkSnapshot); err != nil {
			return fmt.Errorf("restore interrupted gateway network state: %w", err)
		}
	}
	ReportProgress(ctx, "clearing_runtime")
	cleanupErr := errors.Join(
		deps.removeState(m.paths.StateFile),
		device.RemovePolicyBundleSnapshot(m.paths.DevicePolicyApplied),
	)
	if cleanupErr != nil {
		return cleanupErr
	}
	fmt.Println("Interrupted gateway runtime was reconciled without signaling stale PIDs.")
	return nil
}

func (m Manager) preflight(ctx context.Context, backend platform.NetworkBackend, dhcpManager dhcpService, mihomoManager mihomoService, deps gatewayDeps) error {
	return m.preflightWithOwnership(ctx, backend, dhcpManager, mihomoManager, deps, true)
}

func (m Manager) preflightWithOwnership(ctx context.Context, backend platform.NetworkBackend, dhcpManager dhcpService, mihomoManager mihomoService, deps gatewayDeps, checkOwnership bool) error {
	if err := dhcpManager.Check(); err != nil {
		return err
	}
	if err := mihomoManager.Check(); err != nil {
		return err
	}
	caps, err := backend.Capabilities(ctx)
	if err != nil {
		return err
	}
	// Real platform backends return a non-nil Missing map. Test doubles may omit
	// it; in that case capability gating is intentionally left to the test.
	if caps.Missing != nil {
		if !caps.CapNetAdmin {
			return platform.NewError(platform.CodeCapabilityMissing, "CAP_NET_ADMIN is required").WithDetail("capability", "NET_ADMIN")
		}
		if m.cfg.DHCP.Enabled && !caps.CapNetRaw {
			return platform.NewError(platform.CodeCapabilityMissing, "CAP_NET_RAW is required while DHCP is enabled").WithDetail("capability", "NET_RAW")
		}
		if !caps.TUNDeviceNode {
			return platform.NewError(platform.CodeTUNUnavailable, "/dev/net/tun is required")
		}
		if !caps.NFTables || !caps.NFTablesJSON {
			return platform.NewError(platform.CodeNFTablesUnavailable, "nftables with JSON output is required")
		}
		if !caps.IProute2 {
			return platform.NewError(platform.CodeIPRoute2Unavailable, "iproute2 is required")
		}
		if !caps.IPv4ForwardSysctl || !caps.IPv4ForwardReady {
			return platform.NewError(platform.CodeForwardingUnavailable, "IPv4 forwarding must already be enabled or writable in this network namespace")
		}
	}

	sameInterface := strings.TrimSpace(m.cfg.Gateway.Interface) == strings.TrimSpace(m.cfg.Gateway.UpstreamInterface)
	if m.cfg.Gateway.SameLAN() {
		if !sameInterface {
			return fmt.Errorf("gateway.mode %s requires gateway and upstream interfaces to match", m.cfg.Gateway.Mode)
		}
	} else if sameInterface {
		return fmt.Errorf("gateway and upstream interfaces must differ")
	}
	if err := backend.EnsureTUN(ctx); err != nil {
		return err
	}
	topology := platform.NetworkConfig{
		LANInterface:       m.cfg.Gateway.Interface,
		LANIP:              m.cfg.Gateway.LANIP,
		LANCIDR:            m.cfg.Gateway.LANCIDR,
		UpstreamInterface:  m.cfg.Gateway.UpstreamInterface,
		UpstreamGateway:    m.cfg.Gateway.UpstreamGateway,
		NFTTableName:       m.cfg.Transparent.NFTTableName,
		FwMark:             m.cfg.Transparent.FwMark,
		RouteTableID:       m.cfg.Transparent.RouteTableID,
		RouteRulePriority:  m.cfg.Transparent.RouteRulePriority,
		SameLAN:            m.cfg.Gateway.SameLAN(),
		SkipOwnershipCheck: !checkOwnership,
	}
	if err := backend.ValidateTopology(ctx, topology); err != nil {
		return err
	}
	if _, err := deps.interfaceByName(m.cfg.Gateway.Interface); err != nil {
		return fmt.Errorf("interface %s: %w", m.cfg.Gateway.Interface, err)
	}
	return nil
}

func (m Manager) rollback(ctx context.Context, cause error, state runtime.State, backend platform.NetworkBackend, dhcpManager dhcpService, mihomoManager mihomoService) error {
	if ctx.Err() != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		ctx = cleanupCtx
	}
	ReportProgress(ctx, "rolling_back")
	deps := m.gatewayDeps()
	var cleanupErr error
	if state.NetworkSnapshot != nil {
		cleanupErr = errors.Join(cleanupErr, backend.Restore(ctx, state.NetworkSnapshot))
	}
	cleanupErr = errors.Join(cleanupErr, stopTrackedProcess(deps, "dnsmasq", state.PIDDNSMasq, state.DNSMasqProcessFingerprint, dhcpManager.Stop))
	cleanupErr = errors.Join(cleanupErr, stopTrackedProcess(deps, "mihomo", state.PIDMihomo, state.MihomoProcessFingerprint, mihomoManager.Stop))
	if cleanupErr != nil {
		return fmt.Errorf("%w; rollback failed and runtime state was retained for recovery: %v", cause, cleanupErr)
	}
	cleanupErr = errors.Join(cleanupErr, deps.removeState(m.paths.StateFile))
	cleanupErr = errors.Join(cleanupErr, device.RemovePolicyBundleSnapshot(m.paths.DevicePolicyApplied))
	if cleanupErr != nil {
		return fmt.Errorf("%w; rollback cleanup failed: %v", cause, cleanupErr)
	}
	return cause
}
