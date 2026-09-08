package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"open-mihomo-gateway/internal/platform"
)

// State is the persisted runtime state of the gateway. It lives on the /data
// volume so a container restart or a NAS reboot can still reconcile what the
// host is doing against what OpenSurge intends it to do.
type State struct {
	PIDDNSMasq                int    `json:"pid_dnsmasq,omitempty"`
	DNSMasqProcessFingerprint string `json:"dnsmasq_process_fingerprint,omitempty"`
	PIDMihomo                 int    `json:"pid_mihomo,omitempty"`
	MihomoProcessFingerprint  string `json:"mihomo_process_fingerprint,omitempty"`
	BootSessionID             string `json:"boot_session_id,omitempty"`
	DevicePolicyDigest        string `json:"device_policy_digest,omitempty"`
	ProfileDigest             string `json:"profile_digest,omitempty"`
	DNSIPv6                   bool   `json:"dns_ipv6"`
	TUNDevice                 string `json:"tun_device,omitempty"`
	StartedAt                 time.Time `json:"started_at"`

	// Applied records which host-mutating steps completed. Rollback only undoes
	// what actually happened, so a failure before NAT was applied never tries to
	// remove a table that was never created.
	ForwardingApplied bool `json:"forwarding_applied"`
	NATApplied        bool `json:"nat_applied"`
	RoutingApplied    bool `json:"routing_applied"`

	// NetworkSnapshot is the host state captured before the first mutation.
	// Restoring it is the only supported way to undo a start, which is why it is
	// persisted alongside the process identity rather than kept in memory.
	NetworkSnapshot *platform.NetworkSnapshot `json:"network_snapshot,omitempty"`
}

// LoadState reads persisted runtime state.
func LoadState(path string) (State, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, false, err
	}
	return state, true, nil
}

// SaveState persists runtime state atomically: write a temp file, chmod, then
// rename. A crash mid-write leaves the previous state intact instead of a
// truncated file that would be indistinguishable from a clean stop.
func SaveState(path string, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// RemoveState deletes persisted runtime state.
func RemoveState(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
