package qnaphost

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupLegacyTrafficScopesNoStateIsNoop(t *testing.T) {
	if err := CleanupLegacyTrafficScopes(context.Background(), t.TempDir()); err != nil {
		t.Fatalf("CleanupLegacyTrafficScopes without state: %v", err)
	}
}

func TestCleanupLegacyTrafficScopesRejectsMalformedStateWithoutTouchingHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, legacyTrafficScopeStateFile)
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupLegacyTrafficScopes(context.Background(), dir); err == nil {
		t.Fatal("expected malformed legacy state to return an error")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("malformed state should be retained for diagnosis: %v", err)
	}
}
