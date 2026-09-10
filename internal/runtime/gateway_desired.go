package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const gatewayDesiredStateSchemaVersion = 1

type GatewayDesiredState struct {
	SchemaVersion int       `json:"schema_version"`
	Running       bool      `json:"running"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func GatewayDesiredStatePath(runtimeDir string) string {
	return filepath.Join(runtimeDir, "gateway-desired.json")
}

func LoadGatewayDesiredState(path string) (GatewayDesiredState, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return GatewayDesiredState{}, false, nil
	}
	if err != nil {
		return GatewayDesiredState{}, false, err
	}
	var state GatewayDesiredState
	if err := json.Unmarshal(data, &state); err != nil {
		return GatewayDesiredState{}, false, err
	}
	if state.SchemaVersion != gatewayDesiredStateSchemaVersion {
		return GatewayDesiredState{}, false, errors.New("unsupported gateway desired-state schema version")
	}
	return state, true, nil
}

func SaveGatewayDesiredState(path string, running bool) error {
	state := GatewayDesiredState{
		SchemaVersion: gatewayDesiredStateSchemaVersion,
		Running:       running,
		UpdatedAt:     time.Now().UTC(),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".gateway-desired-*.tmp")
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
