package controlapi

import (
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

	"open-mihomo-gateway/internal/mihomo"
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
		if value.Enabled {
			value, _ = s.syncHostHosts(value, false)
		}
		writeJSON(w, http.StatusOK, hostHostsSyncStatus(value))

	case http.MethodPost:
		value, err = s.syncHostHosts(value, true)
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

func (s *Server) syncHostHosts(value hostHostsSyncSettings, force bool) (hostHostsSyncSettings, error) {
	if !value.Enabled && !force {
		return value, nil
	}

	data, err := os.ReadFile(value.Path)
	if err != nil {
		value.LastError = fmt.Sprintf("read mapped QNAP hosts: %v", err)
		_ = s.store.SaveHostHostsSyncSettings(value)
		return value, errors.New(value.LastError)
	}

	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if !force && digest == value.LastDigest {
		return value, nil
	}
	if _, err := mihomo.ParseTraditionalHostsFile(string(data)); err != nil {
		value.LastError = err.Error()
		_ = s.store.SaveHostHostsSyncSettings(value)
		return value, err
	}

	_, document, _, err := s.loadProfileOverlay()
	if err != nil {
		return value, err
	}
	raw, _ := document.DNS.Merge["hosts-file"].(string)
	standard, native, err := mihomo.SplitProfileHostsInputs(raw)
	if err != nil {
		return value, err
	}
	standard = replaceManagedHostHosts(standard, strings.TrimSpace(string(data)))
	combined := mihomo.JoinProfileHostsInputs(standard, native)
	if strings.TrimSpace(combined) == "" {
		delete(document.DNS.Merge, "hosts-file")
	} else {
		document.DNS.Merge["hosts-file"] = combined
	}
	document.DNS.Merge["use-hosts"] = true

	rendered, err := mihomo.RenderProfileOverlay(document)
	if err != nil {
		return value, err
	}
	if err := s.store.SaveProfileOverlay(rendered); err != nil {
		return value, err
	}

	value.LastDigest = digest
	value.LastSyncAt = time.Now().UTC().Format(time.RFC3339)
	value.LastEntries = countHostEntries(string(data))
	value.LastError = ""
	if err := s.store.SaveHostHostsSyncSettings(value); err != nil {
		return value, err
	}
	return value, nil
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
		_, _ = s.syncHostHosts(value, false)
	}
}
