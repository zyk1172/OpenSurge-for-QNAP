package gateway

import (
	"context"
	"strings"

	"open-mihomo-gateway/internal/platform"
	"open-mihomo-gateway/internal/runtime"
)

// runtimeInterrupted distinguishes an active runtime from persistent state left
// behind by a host reboot or an isolated-container restart. Linux boot_id is a
// host identity and normally survives a container restart, so boot continuity
// alone is insufficient: the persisted network namespace is the second half of
// the runtime identity.
func (m Manager) runtimeInterrupted(ctx context.Context, state runtime.State, deps gatewayDeps) (bool, error) {
	bootSession, err := currentBoot(deps)
	if err != nil {
		return false, err
	}
	if !state.BelongsToBoot(bootSession) {
		return true, nil
	}

	snapshot := state.NetworkSnapshot
	if snapshot == nil || snapshot.Backend != platform.BackendLinuxNFTables {
		return false, nil
	}
	persistedNamespace := strings.TrimSpace(snapshot.NetworkNamespace)
	if persistedNamespace == "" {
		// Schema-v2 Linux snapshots are expected to carry namespace identity. If
		// it is missing, fail closed rather than risk signalling a reused PID.
		return true, nil
	}
	backend, err := m.backend()
	if err != nil {
		return false, err
	}
	caps, err := backend.Capabilities(ctx)
	if err != nil {
		return false, err
	}
	currentNamespace := strings.TrimSpace(caps.NetworkNamespace)
	if currentNamespace == "" {
		// An ownership check that cannot identify the current namespace cannot
		// prove process/runtime continuity. Treat it as interrupted.
		return true, nil
	}
	return currentNamespace != persistedNamespace, nil
}
