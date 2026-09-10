package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGatewayDesiredStateRoundTrip(t *testing.T) {
	path := GatewayDesiredStatePath(t.TempDir())
	if err := SaveGatewayDesiredState(path, true); err != nil {
		t.Fatal(err)
	}
	state, exists, err := LoadGatewayDesiredState(path)
	if err != nil || !exists {
		t.Fatalf("load desired state: exists=%v err=%v", exists, err)
	}
	if !state.Running || state.SchemaVersion != gatewayDesiredStateSchemaVersion || state.UpdatedAt.IsZero() {
		t.Fatalf("desired state = %#v", state)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("desired-state mode = %o, want 640", info.Mode().Perm())
	}

	if err := SaveGatewayDesiredState(path, false); err != nil {
		t.Fatal(err)
	}
	state, exists, err = LoadGatewayDesiredState(path)
	if err != nil || !exists || state.Running {
		t.Fatalf("updated desired state = %#v exists=%v err=%v", state, exists, err)
	}
}

func TestLoadGatewayDesiredStateMissingAndInvalidSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway-desired.json")
	if _, exists, err := LoadGatewayDesiredState(path); err != nil || exists {
		t.Fatalf("missing desired state: exists=%v err=%v", exists, err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":99,"running":true}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadGatewayDesiredState(path); err == nil {
		t.Fatal("unsupported schema unexpectedly accepted")
	}
}
