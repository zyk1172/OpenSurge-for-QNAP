package webgateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// ServeWithPrivateRoutes keeps QNAP-only privileged routes behind the same
// administrator session/origin policy as the normal Web gateway without
// exposing the Docker-capable host agent directly to the LAN.
func (s *Server) ServeWithPrivateRoutes(ctx context.Context, routes map[string]http.Handler) error {
	server := &http.Server{
		Addr:              s.addr,
		Handler:           s.HandlerWithPrivateRoutes(routes),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.ListenAndServe(); errors.Is(err, http.ErrServerClosed) {
		return nil
	} else {
		return err
	}
}

func (s *Server) HandlerWithPrivateRoutes(routes map[string]http.Handler) http.Handler {
	base := s.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for prefix, handler := range routes {
			if !strings.HasPrefix(r.URL.Path, prefix) {
				continue
			}
			if !s.authenticated(r) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "authentication_required", "message": "sign in to OpenSurge"}})
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !sameOrigin(r) {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "origin_rejected", "message": "mutation origin is not allowed"}})
				return
			}
			handler.ServeHTTP(w, r)
			return
		}
		base.ServeHTTP(w, r)
	})
}
