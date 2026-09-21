package controlapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/mihomo"
	"open-mihomo-gateway/internal/runtime"
)

const (
	defaultHostHostsPath = "/run/opensurge/host-hosts"
	hostHostsBegin        = "# >>> OPENSURGE QNAP HOST HOSTS >>>"
	hostHostsEnd          = "# <<< OPENSURGE QNAP HOST HOSTS <<<"
)

type hostHostsSyncSettings struct {
	SchemaVersion    int    `json:"schema_version"`
	Enabled          bool   `json:"enabled"`
	AutoUpdate       bool   `json:"auto_update"`
	IntervalMinutes  int    `json:"interval_minutes"`
	Path             string `json:"path"`
	LastSyncAt       string `json:"last_sync_at,omitempty"`
	LastDigest       string `json:"last_digest,omitempty"`
	LastEntries      int    `json:"last_entries"`
	LastError        string `json:"last_error,omitempty"`
}

type hostHostsSyncView struct {
	hostHostsSyncSettings
	Mapped     bool   `json:"mapped"`
	MountError string `json:"mount_error,omitempty"`
}

func defaultHostHostsSyncSettings() hostHostsSyncSettings {
	return hostHostsSyncSettings{
		SchemaVersion:   SchemaVersion,
		IntervalMinutes: 30,
		Path:            defaultHostHostsPath,
	}
}

func (s *Store) HostHostsSyncSettings() (hostHostsSyncSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	value := defaultHostHostsSyncSettings()
	err := readJSON(filepath.Join(s.dir, "host-hosts-sync.json"), &value)
	if errors.Is(err, os.ErrNotExist) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	value.SchemaVersion = SchemaVersion
	if value.Path == "" {
		value.Path = defaultHostHostsPath
	}
	return value, nil
}

func (s *Store) SaveHostHostsSyncSettings(value hostHostsSyncSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	value.SchemaVersion = SchemaVersion
	if value.Path == "" {
		value.Path = defaultHostHostsPath
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, "host-hosts-sync.json"), append(data, '\n'), 0600)
}

func hostHostsSyncStatus(value hostHostsSyncSettings) hostHostsSyncView {
	view := hostHostsSyncView{hostHostsSyncSettings: value}
	info, err := os.Stat(value.Path)
	if err != nil {
		view.MountError = err.Error()
		return view
	}
	if !info.Mode().IsRegular() {
		view.MountError = "mapped path is not a regular file"
		return view
	}
	view.Mapped = true
	return view
}

func (s *Server) handleHostHostsSync(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.HostHostsSyncSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "host_hosts_sync_failed", err.Error())
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, hostHostsSyncStatus(value))

	case http.MethodPut:
		previous := value
		var request struct {
			Enabled         *bool `json:"enabled"`
			AutoUpdate      *bool `json:"auto_update"`
			IntervalMinutes *int  `json:"interval_minutes"`
		}
		if err := decodeJSON(r, &request, 32<<10); err != nil {
			writeError(w, http.StatusBadRequest, "host_hosts_sync_invalid", err.Error())
			return
		}
		if request.Enabled != nil {
			value.Enabled = *request.Enabled
		}
		if request.AutoUpdate != nil {
			value.AutoUpdate = *request.AutoUpdate
		}
		if request.IntervalMinutes != nil {
			value.IntervalMinutes = *request.IntervalMinutes
		}
		if value.IntervalMinutes < 5 || value.IntervalMinutes > 10080 {
			writeError(w, http.StatusUnprocessableEntity, "host_hosts_sync_invalid", "interval_minutes must be between 5 and 10080")
			return
		}
		if err := s.store.SaveHostHostsSyncSettings(value); err != nil {
			writeError(w, http.StatusInternalServerError, "host_hosts_sync_failed", err.Error())
			return
		}
		if value.Enabled != previous.Enabled {
			if value.Enabled {
				value, err = s.syncHostHosts(r.Context(), value, true)
			} else {
				var migration hostHostsOverlayMigration
				migration, err = s.migrateLegacyHostHostsOverlay()
				if err == nil {
					err = s.reconcileHostHostsProfile(r.Context())
				}
				if err != nil {
					_ = s.restoreLegacyHostHostsOverlay(migration)
				} else {
					value.LastError = ""
					err = s.store.SaveHostHostsSyncSettings(value)
				}
			}
			if err != nil {
				previous.LastError = err.Error()
				_ = s.store.SaveHostHostsSyncSettings(previous)
				writeError(w, http.StatusConflict, "host_hosts_sync_apply_failed", err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, hostHostsSyncStatus(value))

	case http.MethodPost:
		value, err = s.syncHostHosts(r.Context(), value, true)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "host_hosts_sync_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, hostHostsSyncStatus(value))

	default:
		w.Header().Set("Allow", "GET, PUT, POST")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) syncHostHosts(ctx context.Context, value hostHostsSyncSettings, force bool) (hostHostsSyncSettings, error) {
	if !value.Enabled {
		if force {
			err := errors.New("host Hosts sync is disabled")
			value.LastError = err.Error()
			_ = s.store.SaveHostHostsSyncSettings(value)
			return value, err
		}
		return value, nil
	}

	data, err := os.ReadFile(value.Path)
	if err != nil {
		value.LastError = fmt.Sprintf("read mapped QNAP hosts: %v", err)
		_ = s.store.SaveHostHostsSyncSettings(value)
		return value, errors.New(value.LastError)
	}
	if _, err := mihomo.ParseTraditionalHostsFile(string(data)); err != nil {
		value.LastError = err.Error()
		_ = s.store.SaveHostHostsSyncSettings(value)
		return value, err
	}

	normalizedHosts := normalizeManagedHostHosts(string(data))
	sum := sha256.Sum256([]byte(normalizedHosts))
	digest := hex.EncodeToString(sum[:])

	migration, err := s.migrateLegacyHostHostsOverlay()
	if err != nil {
		value.LastError = err.Error()
		_ = s.store.SaveHostHostsSyncSettings(value)
		return value, err
	}

	previous, previousErr := s.store.HostHostsManaged()
	hadPrevious := previousErr == nil
	if previousErr != nil && !errors.Is(previousErr, os.ErrNotExist) {
		_ = s.restoreLegacyHostHostsOverlay(migration)
		value.LastError = previousErr.Error()
		_ = s.store.SaveHostHostsSyncSettings(value)
		return value, previousErr
	}
	if !hadPrevious || string(previous) != normalizedHosts {
		if err := s.store.SaveHostHostsManaged([]byte(normalizedHosts)); err != nil {
			_ = s.restoreLegacyHostHostsOverlay(migration)
			value.LastError = err.Error()
			_ = s.store.SaveHostHostsSyncSettings(value)
			return value, err
		}
	}

	// Do not trust LastDigest alone. Older builds recorded a successful sync
	// before the effective Mihomo profile was actually reloaded. Reconcile the
	// desired profile on every due check; when it already matches, this is a
	// cheap no-op and does not restart the gateway.
	if err := s.reconcileHostHostsProfile(ctx); err != nil {
		if hadPrevious {
			_ = s.store.SaveHostHostsManaged(previous)
		} else {
			_ = s.store.RemoveHostHostsManaged()
		}
		_ = s.restoreLegacyHostHostsOverlay(migration)
		value.LastError = err.Error()
		_ = s.store.SaveHostHostsSyncSettings(value)
		return value, err
	}

	value.LastDigest = digest
	value.LastSyncAt = time.Now().UTC().Format(time.RFC3339)
	value.LastEntries = countHostEntries(normalizedHosts)
	value.LastError = ""
	if err := s.store.SaveHostHostsSyncSettings(value); err != nil {
		return value, err
	}
	return value, nil
}

type hostHostsOverlayMigration struct {
	Previous []byte
	Changed  bool
}

func (s *Server) migrateLegacyHostHostsOverlay() (hostHostsOverlayMigration, error) {
	data, err := s.store.ProfileOverlay()
	if errors.Is(err, os.ErrNotExist) {
		return hostHostsOverlayMigration{}, nil
	}
	if err != nil {
		return hostHostsOverlayMigration{}, err
	}
	document, err := mihomo.ParseProfileOverlay(data)
	if err != nil {
		return hostHostsOverlayMigration{}, err
	}
	raw, _ := document.DNS.Merge["hosts-file"].(string)
	if !strings.Contains(raw, hostHostsBegin) && !strings.Contains(raw, hostHostsEnd) {
		return hostHostsOverlayMigration{}, nil
	}
	if _, err := detachLegacyHostHostsFromOverlay(&document); err != nil {
		return hostHostsOverlayMigration{}, err
	}
	rendered, err := mihomo.RenderProfileOverlay(document)
	if err != nil {
		return hostHostsOverlayMigration{}, err
	}
	if err := s.store.SaveProfileOverlay(rendered); err != nil {
		return hostHostsOverlayMigration{}, err
	}
	return hostHostsOverlayMigration{Previous: append([]byte(nil), data...), Changed: true}, nil
}

func (s *Server) restoreLegacyHostHostsOverlay(migration hostHostsOverlayMigration) error {
	if !migration.Changed {
		return nil
	}
	return s.store.SaveProfileOverlay(migration.Previous)
}

func (s *Server) reconcileHostHostsProfile(ctx context.Context) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	candidate, err := prepareContainerProfileReconcile(s.configPath, s.store.Dir())
	if err != nil {
		return err
	}
	if candidate == nil {
		return nil
	}
	if s.configRunner == nil {
		return fmt.Errorf("configuration runner is unavailable")
	}
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return err
	}
	_, running, err := runtime.LoadState(runtime.NewPaths(cfg).StateFile)
	if err != nil {
		return err
	}
	result, err := s.configRunner.ApplyProfile(ctx, s.configPath, candidate.Revision, candidate.Payload, candidate.SourceDigest, candidate.OverlayDigest)
	if err != nil {
		return err
	}
	if running && !result.Reloaded {
		return fmt.Errorf("QNAP host Hosts changed but the running gateway did not reload the effective profile")
	}
	return nil
}

func normalizeManagedHostHosts(content string) string {
	content = strings.TrimPrefix(content, "\ufeff")
	lines := strings.Split(content, "\n")
	normalized := make([]string, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			normalized = append(normalized, line)
			continue
		}

		body := line
		comment := ""
		if index := strings.IndexByte(body, '#'); index >= 0 {
			comment = strings.TrimSpace(body[index+1:])
			body = strings.TrimSpace(body[:index])
		}
		fields := strings.Fields(body)
		if len(fields) == 0 {
			continue
		}
		line = strings.Join(fields, " ")
		if comment != "" {
			line += " # " + comment
		}
		normalized = append(normalized, line)
	}
	return strings.Join(normalized, "\n")
}

func replaceManagedHostHosts(standard, content string) string {
	start := strings.Index(standard, hostHostsBegin)
	end := strings.Index(standard, hostHostsEnd)
	if start >= 0 && end >= start {
		standard = strings.TrimSpace(standard[:start] + standard[end+len(hostHostsEnd):])
	}
	if content == "" {
		return standard
	}
	block := hostHostsBegin + "\n" + content + "\n" + hostHostsEnd
	if standard == "" {
		return block
	}
	return strings.TrimSpace(standard) + "\n\n" + block
}

func countHostEntries(content string) int {
	count := 0
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if fields := strings.Fields(line); len(fields) > 1 {
			count += len(fields) - 1
		}
	}
	return count
}

func (s *Server) runHostHostsSyncLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		value, err := s.store.HostHostsSyncSettings()
		if err != nil || !value.Enabled || !value.AutoUpdate {
			continue
		}
		last, _ := time.Parse(time.RFC3339, value.LastSyncAt)
		if !last.IsZero() && time.Since(last) < time.Duration(value.IntervalMinutes)*time.Minute {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), gatewayOperationTimeout)
		_, _ = s.syncHostHosts(ctx, value, false)
		cancel()
	}
}
