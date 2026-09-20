package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	defaultSourceRefreshIntervalMinutes = 1440
	minSourceRefreshIntervalMinutes     = 60
	maxSourceRefreshIntervalMinutes     = 10080
)

func normalizedSourceRefreshInterval(value int) int {
	if value < minSourceRefreshIntervalMinutes || value > maxSourceRefreshIntervalMinutes {
		return defaultSourceRefreshIntervalMinutes
	}
	return value
}

func sourceRefreshDue(source Source, now time.Time) bool {
	if !source.AutoUpdate || !strings.HasPrefix(source.Origin, "https://") {
		return false
	}
	interval := time.Duration(normalizedSourceRefreshInterval(source.UpdateIntervalMinutes)) * time.Minute
	anchor := source.ImportedAt
	if source.LastRefreshAttemptAt != nil && source.LastRefreshAttemptAt.After(anchor) {
		anchor = *source.LastRefreshAttemptAt
	}
	if anchor.IsZero() {
		return true
	}
	return !now.Before(anchor.Add(interval))
}

func (s *Server) handleSourceRefreshSettings(w http.ResponseWriter, r *http.Request) {
	source, err := s.sourceByID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "source_not_found", err.Error())
		return
	}
	if !strings.HasPrefix(source.Origin, "https://") {
		writeError(w, http.StatusConflict, "source_not_refreshable", "only HTTPS sources can use scheduled refresh")
		return
	}
	var request struct {
		AutoUpdate      *bool `json:"auto_update"`
		IntervalMinutes *int  `json:"interval_minutes"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil {
		writeError(w, http.StatusBadRequest, "source_refresh_settings_invalid", err.Error())
		return
	}
	if request.AutoUpdate != nil {
		source.AutoUpdate = *request.AutoUpdate
	}
	if request.IntervalMinutes != nil {
		if *request.IntervalMinutes < minSourceRefreshIntervalMinutes || *request.IntervalMinutes > maxSourceRefreshIntervalMinutes {
			writeError(w, http.StatusUnprocessableEntity, "source_refresh_settings_invalid", fmt.Sprintf("interval_minutes must be between %d and %d", minSourceRefreshIntervalMinutes, maxSourceRefreshIntervalMinutes))
			return
		}
		source.UpdateIntervalMinutes = *request.IntervalMinutes
	} else {
		source.UpdateIntervalMinutes = normalizedSourceRefreshInterval(source.UpdateIntervalMinutes)
	}
	if err := s.saveSourceRecord(source); err != nil {
		writeError(w, http.StatusInternalServerError, "source_refresh_settings_failed", err.Error())
		return
	}
	source.SnapshotPath = ""
	source.FetchURL = ""
	writeJSON(w, http.StatusOK, source)
}

func (s *Server) refreshSource(ctx context.Context, id string) (Source, error) {
	s.sourceRefreshMu.Lock()
	defer s.sourceRefreshMu.Unlock()

	source, err := s.sourceByID(id)
	if err != nil {
		return Source{}, err
	}
	fetchURL, credentialErr := s.credentials.Get(ctx, source.ID)
	if credentialErr != nil || !strings.HasPrefix(fetchURL, "https://") {
		return Source{}, fmt.Errorf("only HTTPS sources can be refreshed")
	}
	attemptedAt := time.Now().UTC()
	refreshed, err := s.importURL(ctx, SourceImportRequest{Name: source.Name, Kind: source.Kind, URL: fetchURL})
	if err != nil {
		source.LastRefreshAttemptAt = &attemptedAt
		source.LastRefreshError = err.Error()
		source.UpdateIntervalMinutes = normalizedSourceRefreshInterval(source.UpdateIntervalMinutes)
		_ = s.saveSourceRecord(source)
		return Source{}, err
	}
	refreshed.LastRefreshAttemptAt = &attemptedAt
	refreshed.LastRefreshError = ""
	refreshed.UpdateIntervalMinutes = normalizedSourceRefreshInterval(refreshed.UpdateIntervalMinutes)
	if err := s.saveSourceRecord(refreshed); err != nil {
		return Source{}, err
	}
	return refreshed, nil
}

func (s *Server) saveSourceRecord(source Source) error {
	sources, err := s.store.Sources()
	if err != nil {
		return err
	}
	for index := range sources {
		if sources[index].ID == source.ID {
			sources[index] = source
			return s.store.SaveSources(sources)
		}
	}
	return fmt.Errorf("source %q not found", source.ID)
}

func (s *Server) runSourceRefreshLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for now := range ticker.C {
		s.refreshDueSources(now.UTC())
	}
}

func (s *Server) refreshDueSources(now time.Time) {
	sources, err := s.store.Sources()
	if err != nil {
		return
	}
	for _, source := range sources {
		if !sourceRefreshDue(source, now) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, _ = s.refreshSource(ctx, source.ID)
		cancel()
	}
}
