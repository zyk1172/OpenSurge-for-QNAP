// Package platform is the boundary between OpenSurge's core business logic and
// the operating system's network data plane.
//
// The upstream project targeted macOS only, so its gateway manager called
// pfctl, networksetup and macOS sysctl keys directly. Porting to Linux/QNAP by
// globally replacing `pfctl` with `nft` would keep that coupling and make the
// result impossible to test, audit or extend to another platform.
//
// This package therefore defines a single NetworkBackend contract. Everything
// that touches the host network must go through it:
//
//	core business logic  !=  Linux/Darwin shell commands
//
// Every method is:
//
//   - context aware, so a cancelled start cannot leave work half done;
//   - expected to be idempotent where the name starts with Ensure/Setup/Remove;
//   - required to own only OpenSurge-managed state. A backend must never flush
//     or rewrite rules it did not create. On Linux that means it may only touch
//     `table inet opensurge` and must never run `nft flush ruleset`.
//
// Even though this fork no longer builds a Darwin backend, the boundary is kept
// so the same core can later target QNAP, Synology, Debian, Ubuntu or any
// generic Linux server, and so the Linux backend can be fully exercised by a
// network-namespace lab instead of a developer's real LAN.
package platform

import (
	"context"
	"errors"
)

// BackendName identifies a concrete NetworkBackend implementation. It is
// persisted inside NetworkSnapshot so a snapshot captured by one backend is
// never blindly restored by a different one.
type BackendName string

const (
	// BackendLinuxNFTables is the Linux backend: nftables + iproute2 policy
	// routing + /dev/net/tun.
	BackendLinuxNFTables BackendName = "linux-nftables"
)

// SnapshotSchemaVersion is persisted with every NetworkSnapshot. A backend must
// refuse to restore a snapshot whose schema it cannot interpret instead of
// guessing at its shape.
const SnapshotSchemaVersion = 1

// NetworkBackend is the complete set of host-network operations OpenSurge is
// allowed to perform. Implementations must be safe to call from a single
// process at a time; cross-process exclusion is owned by the gateway lifecycle
// lock, not by this interface.
type NetworkBackend interface {
	// Name reports which implementation is in use.
	Name() BackendName

	// Capabilities reports what this host can actually do. Preflight uses it to
	// fail fast with an actionable reason instead of mutating the network and
	// discovering a missing tool halfway through.
	Capabilities(ctx context.Context) (Capabilities, error)

	// DetectInterfaces lists interfaces visible to OpenSurge, including their
	// IPv4 addresses. Used for discovery, validation and diagnostics.
	DetectInterfaces(ctx context.Context) ([]NetworkInterface, error)

	// InterfaceByName resolves a single interface. Returns an error carrying
	// code CodeInterfaceNotFound when it does not exist.
	InterfaceByName(ctx context.Context, name string) (NetworkInterface, error)

	// ValidateTopology checks that the requested gateway topology is coherent:
	// the LAN interface exists, the LAN IP lives on it, the subnet is valid,
	// the upstream gateway is reachable on-link, and LAN/upstream do not
	// conflict. It must not modify anything.
	ValidateTopology(ctx context.Context, cfg NetworkConfig) error

	// EnableIPv4Forwarding turns on IPv4 forwarding and reports the value it
	// replaced so it can be restored verbatim.
	EnableIPv4Forwarding(ctx context.Context) (restore func(context.Context) error, err error)

	// SetupNAT installs OpenSurge's own NAT/marking rules. On Linux this creates
	// or replaces `table inet opensurge` and nothing else.
	SetupNAT(ctx context.Context, cfg NATConfig) error

	// RemoveNAT deletes only the rules SetupNAT created. It must succeed when
	// they are already absent.
	RemoveNAT(ctx context.Context) error

	// SetupPolicyRouting installs the fwmark rule and the dedicated routing
	// table that sends forwarded LAN traffic through the TUN device.
	SetupPolicyRouting(ctx context.Context, cfg RoutingConfig) error

	// RemovePolicyRouting deletes only what SetupPolicyRouting created.
	RemovePolicyRouting(ctx context.Context) error

	// SwitchToDirectFallback re-points the OpenSurge routing table at the real
	// upstream gateway and enables masquerade so traffic keeps flowing while the
	// proxy core is down. It is opt-in per product policy, never implicit.
	SwitchToDirectFallback(ctx context.Context, cfg RoutingConfig) error

	// EnsureTUN verifies the TUN facility is usable (device node present,
	// permission granted, kernel module available).
	EnsureTUN(ctx context.Context) error

	// WaitForTUN blocks until the TUN device named by the proxy core appears,
	// returning its interface view. It respects context cancellation so a start
	// that never brings the device up fails instead of hanging.
	WaitForTUN(ctx context.Context, device string) (NetworkInterface, error)

	// Snapshot captures everything this backend has changed, or would need in
	// order to undo a change, in a serializable form.
	Snapshot(ctx context.Context) (*NetworkSnapshot, error)

	// Restore returns the host to the captured state. It is best effort and
	// must continue past individual failures, reporting them joined together.
	Restore(ctx context.Context, snapshot *NetworkSnapshot) error

	// ObservedState reports what is actually in effect right now, without
	// trusting persisted state. Crash recovery compares this against the desired
	// state to decide between recover, restore, restart, or asking the user.
	ObservedState(ctx context.Context) (*ObservedState, error)
}

// ErrUnsupported is returned by New on a platform with no backend.
var ErrUnsupported = errors.New("no network backend for this platform")

// Validate confirms a backend implementation satisfies the contract. It is
// called by tests for every registered backend.
func Validate(b NetworkBackend) error {
	if b == nil {
		return errors.New("nil network backend")
	}
	if b.Name() == "" {
		return errors.New("network backend has no name")
	}
	return nil
}
