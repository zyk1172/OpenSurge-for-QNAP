package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"open-mihomo-gateway/internal/platform"
)

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

// prepareWriteAheadJournal marks idempotent cleanup intents before host
// mutation. The persisted recipe is authoritative after a crash.
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
	if snapshot.IPv4Forwarding != "" {
		snapshot.Applied.IPv4Forwarding = true
	}
}

// SaveState requires its parent directory to have been created by runtime.Ensure.
// Keeping that contract prevents a typo/wrong runtime root from silently
// creating new directories. Within an existing directory the write is durable:
// temp file -> fsync -> rename -> parent-directory fsync.
func SaveState(path string, state State) error {
	prepareWriteAheadJournal(&state)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err != nil {
		return err
	} else if !info.IsDir() {
		return errors.New("runtime state parent is not a directory")
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
