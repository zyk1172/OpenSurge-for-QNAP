package controlapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQNAPSourceDeleteRequiresTokenAndCleansPersistentData(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}

	const sourceID = "source-delete-test"
	sourceDir := filepath.Join(root, "sources", sourceID)
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(sourceDir, "profile.yaml")
	if err := os.WriteFile(snapshot, []byte("proxies: []\nproxy-groups: []\nrules:\n  - MATCH,DIRECT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSources([]Source{{
		SchemaVersion: SchemaVersion,
		ID: sourceID,
		Name: "Disposable",
		Kind: "mihomo_profile",
		Origin: "https://example.com/profile",
		SnapshotPath: snapshot,
		Digest: "digest-delete-test",
		Valid: true,
		ImportedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	credentials := NewFileCredentialStore(root)
	if err := credentials.Put(t.Context(), sourceID, "https://example.com/profile?token=secret"); err != nil {
		t.Fatal(err)
	}

	handler := WrapQNAPSourceDelete(http.NotFoundHandler(), filepath.Join(root, "missing-config.yaml"), root, "internal-token")

	unauthorized := httptest.NewRequest(http.MethodDelete, qnapSourceDeletePrefix+sourceID, nil)
	unauthorizedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", unauthorizedRecorder.Code, unauthorizedRecorder.Body.String())
	}
	if sources, err := store.Sources(); err != nil || len(sources) != 1 {
		t.Fatalf("unauthorized delete changed sources: len=%d err=%v", len(sources), err)
	}

	request := httptest.NewRequest(http.MethodDelete, qnapSourceDeletePrefix+sourceID, nil)
	request.Header.Set("Authorization", "Bearer internal-token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if sources, err := store.Sources(); err != nil || len(sources) != 0 {
		t.Fatalf("source record remains: len=%d err=%v", len(sources), err)
	}
	if _, err := os.Stat(sourceDir); !os.IsNotExist(err) {
		t.Fatalf("source directory still exists: err=%v", err)
	}
	if _, err := credentials.Get(t.Context(), sourceID); err == nil {
		t.Fatal("source credential still exists after delete")
	}
}

func TestQNAPSourceDeleteRejectsNestedID(t *testing.T) {
	root := t.TempDir()
	if err := NewStore(root).Ensure(); err != nil {
		t.Fatal(err)
	}
	handler := WrapQNAPSourceDelete(http.NotFoundHandler(), filepath.Join(root, "missing-config.yaml"), root, "internal-token")
	request := httptest.NewRequest(http.MethodDelete, qnapSourceDeletePrefix+"a/b", nil)
	request.Header.Set("Authorization", "Bearer internal-token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("nested id status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
