// Package platform is the boundary between OpenSurge's core business logic and
// the operating system's network data plane.
package platform

import (
	"context"
	"errors"
)

type BackendName string

const (
	BackendLinuxNFTables BackendName = "linux-nftables"
)

// SnapshotSchemaVersion is bumped whenever persisted cleanup semantics change.
// Schema v2 records network-namespace identity so a restarted isolated
// container never replays an old namespace's cleanup against a fresh namespace.
const SnapshotSchemaVersion = 2

type NetworkBackend interface {
	Name() BackendName
	Capabilities(ctx context.Context) (Capabilities, error)
	DetectInterfaces(ctx context.Context) ([]NetworkInterface, error)
	InterfaceByName(ctx context.Context, name string) (NetworkInterface, error)
	ValidateTopology(ctx context.Context, cfg NetworkConfig) error
	EnableIPv4Forwarding(ctx context.Context) (restore func(context.Context) error, err error)
	SetupNAT(ctx context.Context, cfg NATConfig) error
	RemoveNAT(ctx context.Context) error
	SetupPolicyRouting(ctx context.Context, cfg RoutingConfig) error
	// PolicyRoutingPresent verifies the exact selector and routes owned by
	// OpenSurge. A running TUN process alone is not sufficient proof that the
	// downstream data plane is still attached to it.
	PolicyRoutingPresent(ctx context.Context, cfg RoutingConfig) (bool, error)
	RemovePolicyRouting(ctx context.Context) error
	SwitchToDirectFallback(ctx context.Context, cfg RoutingConfig) error
	EnsureTUN(ctx context.Context) error
	WaitForTUN(ctx context.Context, device string) (NetworkInterface, error)
	Snapshot(ctx context.Context) (*NetworkSnapshot, error)
	Restore(ctx context.Context, snapshot *NetworkSnapshot) error
	ObservedState(ctx context.Context) (*ObservedState, error)
}

var ErrUnsupported = errors.New("no network backend for this platform")

func Validate(b NetworkBackend) error {
	if b == nil {
		return errors.New("nil network backend")
	}
	if b.Name() == "" {
		return errors.New("network backend has no name")
	}
	return nil
}
