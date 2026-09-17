package controlapi

import (
	"context"
	"crypto/subtle"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const qnapSourceDeletePrefix = "/api/v1/qnap/sources/"

// WrapQNAPSourceDelete exposes source deletion only on the QNAP container
// surface. The LAN-facing Web gateway replaces the administrator session with
// the internal bearer token before forwarding, and direct loopback callers must
// present the same token. Keeping this wrapper QNAP-specific avoids widening the
// desktop Control API while still making deletion a real persisted operation.
func WrapQNAPSourceDelete(next http.Handler, configPath, storeDir, token string) http.Handler {
	store := NewStore(storeDir)
	deleteAPI := &Server{
		configPath:  configPath,
		store:       store,
		credentials: NewFileCredentialStore(storeDir),
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || !strings.HasPrefix(r.URL.Path, qnapSourceDeletePrefix) {
			next.ServeHTTP(w, r)
			return
		}
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !sourceDeleteTokenEqual(strings.TrimSpace(bearer), strings.TrimSpace(token)) {
			writeError(w, http.StatusUnauthorized, "authentication_required", "internal control token is required")
			return
		}
		id := strings.TrimPrefix(r.URL.Path, qnapSourceDeletePrefix)
		if id == "" || strings.Contains(id, "/") {
			writeError(w, http.StatusBadRequest, "source_invalid", "invalid source id")
			return
		}
		r.SetPathValue("id", id)
		deleteAPI.handleSourceDelete(w, r)
	})
}

func sourceDeleteTokenEqual(got, want string) bool {
	if got == "" || want == "" || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

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
