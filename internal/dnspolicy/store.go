package dnspolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const MaxDocumentSize = 1 << 20

var ErrRevisionConflict = errors.New("DNS policy revision conflict")

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Path() string {
	return s.path
}

// PathForGatewayConfig keeps the DNS policy beside opensurge.yaml. For the
// QNAP container's default config path this resolves to
// /data/config/dns-policy.json, inside the existing persistent /data boundary.
func PathForGatewayConfig(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "dns-policy.json")
}

func (s *Store) Load() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) Save(candidate Document, expectedRevision string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedRevision == "" {
		return Snapshot{}, fmt.Errorf("expected DNS policy revision is required")
	}
	current, err := s.loadLocked()
	if err != nil {
		return Snapshot{}, err
	}
	if current.Revision != expectedRevision {
		return Snapshot{}, fmt.Errorf("%w: expected %s, current %s", ErrRevisionConflict, expectedRevision, current.Revision)
	}
	normalized, err := Normalize(candidate)
	if err != nil {
		return Snapshot{}, err
	}
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return Snapshot{}, fmt.Errorf("marshal DNS policy: %w", err)
	}
	data = append(data, '\n')
	if len(data) > MaxDocumentSize {
		return Snapshot{}, fmt.Errorf("DNS policy exceeds %d bytes", MaxDocumentSize)
	}
	if err := writeAtomic(s.path, data, 0o600); err != nil {
		return Snapshot{}, err
	}
	revision, err := Revision(normalized)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Document: normalized, Revision: revision}, nil
}

func (s *Store) loadLocked() (Snapshot, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		document := DefaultDocument()
		revision, revisionErr := Revision(document)
		if revisionErr != nil {
			return Snapshot{}, revisionErr
		}
		return Snapshot{Document: document, Revision: revision}, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("read DNS policy: %w", err)
	}
	if len(data) > MaxDocumentSize {
		return Snapshot{}, fmt.Errorf("stored DNS policy exceeds %d bytes", MaxDocumentSize)
	}
	var document Document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Snapshot{}, fmt.Errorf("decode stored DNS policy: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Snapshot{}, fmt.Errorf("decode stored DNS policy: multiple JSON documents are not supported")
		}
		return Snapshot{}, fmt.Errorf("decode stored DNS policy: %w", err)
	}
	normalized, err := Normalize(document)
	if err != nil {
		return Snapshot{}, fmt.Errorf("stored DNS policy is invalid: %w", err)
	}
	revision, err := Revision(normalized)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Document: normalized, Revision: revision}, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create DNS policy directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".dns-policy-*.tmp")
	if err != nil {
		return fmt.Errorf("create DNS policy temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	closeOnError := func(cause error) error {
		_ = tmp.Close()
		return cause
	}
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("chmod DNS policy temp file: %w", closeOnError(err))
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write DNS policy temp file: %w", closeOnError(err))
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync DNS policy temp file: %w", closeOnError(err))
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close DNS policy temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("commit DNS policy: %w", err)
	}
	// Best-effort directory fsync strengthens crash durability on filesystems
	// that support it. It intentionally cannot turn a completed rename into a
	// reported save failure on platforms that reject directory fsync.
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}
