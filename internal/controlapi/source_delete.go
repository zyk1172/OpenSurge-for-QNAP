package controlapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
)

func (s *Server) handleSourceDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || filepath.Base(id) != id || id == "." || id == ".." {
		writeError(w, http.StatusBadRequest, "source_invalid", "invalid source id")
		return
	}

	sources, err := s.store.Sources()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "source_delete_failed", err.Error())
		return
	}
	decorated := s.decorateSourceStates(sources)
	index := -1
	for i := range decorated {
		if decorated[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		writeError(w, http.StatusNotFound, "source_not_found", "source not found")
		return
	}
	if decorated[index].Applied || decorated[index].Desired {
		writeError(w, http.StatusConflict, "source_in_use", "current or next-start source cannot be deleted")
		return
	}

	next := make([]Source, 0, len(sources)-1)
	for _, source := range sources {
		if source.ID != id {
			next = append(next, source)
		}
	}
	if err := s.store.SaveSources(next); err != nil {
		writeError(w, http.StatusInternalServerError, "source_delete_failed", err.Error())
		return
	}

	cleanupWarning := ""
	if deleter, ok := s.credentials.(sourceCredentialDeleter); ok {
		if err := deleter.Delete(r.Context(), id); err != nil {
			cleanupWarning = err.Error()
		}
	}
	if err := removeSourceDirectory(r.Context(), s.store.Dir(), id); err != nil && cleanupWarning == "" {
		cleanupWarning = err.Error()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":         true,
		"id":              id,
		"cleanup_warning": cleanupWarning,
	})
}

func removeSourceDirectory(ctx context.Context, storeDir, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	base := filepath.Join(storeDir, "sources")
	target := filepath.Join(base, id)
	rel, err := filepath.Rel(base, target)
	if err != nil || rel != id || rel == "." || rel == ".." {
		return os.ErrPermission
	}
	return os.RemoveAll(target)
}
