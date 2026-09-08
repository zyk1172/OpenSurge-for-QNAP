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
// volume so a container restart or NAS reboot can reconcile observed state
// against the exact cleanup recipe journaled before host mutation.
type State struct {
	PIDDNSMasq                int       `json:"pid_dnsmasq,omitempty"`
	DNSMasqProcessFingerprint string    `json:"dnsmasq_process_fingerprint,omitempty"`
	PIDMihomo                 int       `json:"pid_mihomo,omitempty"`
	MihomoProcessFingerprint  string    `json:"mihomo_process_fingerprint,omitempty"`
	BootSessionID             string    `json:"boot_session_id,omitempty"`
	DevicePolicyDigest        string    `json:"device_policy_digest,omitempty"`
	ProfileDigest             string    `json:"profile_digest,omitempty"`
	DNSIPv6                   bool      `json:"dns_ipv6"`
	TUNDevice                 string    `json:"tun_device,omitempty"`
	StartedAt                 time.Time `json:"started_at"`

	// These booleans describe lifecycle progress for status/UI. NetworkSnapshot's
	// Applied fields are a stricter write-ahead cleanup journal and are persisted
	// before the corresponding mutation, so a crash between syscall success and
	// the next state write cannot strand kernel state.
	ForwardingApplied bool `json:"forwarding_applied"`
	NATApplied        bool `json:"nat_applied"`
	RoutingApplied    bool `json:"routing_applied"`

	NetworkSnapshot *platform.NetworkSnapshot `json:"network_snapshot,omitempty"`
}

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

// prepareWriteAheadJournal marks cleanup intents whenever the manager has
// already attached complete NAT/routing recipes. Cleanup operations are
// deliberately idempotent and ownership-checked, so journaling an intent before
// a mutation is safer than discovering after a crash that the mutation happened
// but the state file still said it had not.
func prepareWriteAheadJournal(state *State) {
	if state == nil || state.NetworkSnapshot == nil {
		return
	}
	snapshot := state.NetworkSnapshot
	if snapshot.NAT != nil && snapshot.NFTablesTable != "" {
		snapshot.Applied.NAT = true
	}
	if snapshot.Routing != nil && snapshot.Routing.TableID != 0 && snapshot.Routing.FwMark != 0 {
		snapshot.Applied.PolicyRouting = true
	}
	// Presence of a captured pre-change value means the lifecycle has enough
	// information to restore forwarding. The journal may be written before the
	// actual EnableIPv4Forwarding call; restoring the same current value is a
	// harmless no-op if the process dies first.
	if snapshot.IPv4Forwarding != "" {
		snapshot.Applied.IPv4Forwarding = true
	}
}

// SaveState is a durable atomic write: write+fsync temp, rename, then fsync the
// parent directory. The parent fsync matters on NAS filesystems during sudden
// power loss; rename alone does not guarantee the new directory entry is on
// stable storage.
func SaveState(path string, state State) error {
	prepareWriteAheadJournal(&state)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
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
	if err := tmp.Chmod(0o640); err != nil {
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
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return syncDirectory(dir)
}

func syncDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func RemoveState(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
