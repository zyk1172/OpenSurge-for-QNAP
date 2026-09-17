package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
)

type sourceCredentialDeleter interface {
	Delete(context.Context, string) error
}

func (f *FileCredentialStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" {
		return fmt.Errorf("source credential id is required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	document, err := f.load()
	if err != nil {
		return err
	}
	if _, exists := document.Sources[id]; !exists {
		return nil
	}
	delete(document.Sources, id)
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(f.path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("save source credential file: %w", err)
	}
	return nil
}

func (m *memoryCredentialStore) Delete(_ context.Context, id string) error {
	delete(m.values, id)
	return nil
}
