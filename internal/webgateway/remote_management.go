package webgateway

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	remoteTokenFileName = "remote-management-token.json"
	remoteTokenPrefix   = "osr_"
	remoteAPIPrefix     = "/api/remote/v1"
	remoteSchemaVersion = 1
)

type remoteTokenFile struct {
	SchemaVersion int       `json:"schema_version"`
	Hash          string    `json:"hash"`
	TokenPrefix   string    `json:"token_prefix"`
	CreatedAt     time.Time `json:"created_at"`
}

type RemoteTokenStatus struct {
	SchemaVersion int        `json:"schema_version"`
	Enabled       bool       `json:"enabled"`
	TokenPrefix   string     `json:"token_prefix,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	APIBase       string     `json:"api_base"`
	Capabilities  string     `json:"capabilities"`
}

type RemoteTokenStore struct {
	path string
	mu   sync.Mutex
}

func NewRemoteTokenStore(dir string) *RemoteTokenStore {
	return &RemoteTokenStore{path: filepath.Join(dir, remoteTokenFileName)}
}

func (s *RemoteTokenStore) Status() (RemoteTokenStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *RemoteTokenStore) Rotate() (string, RemoteTokenStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	token := remoteTokenPrefix + randomToken(32)
	digest := sha256.Sum256([]byte(token))
	created := time.Now().UTC()
	prefix := token
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	record := remoteTokenFile{
		SchemaVersion: remoteSchemaVersion,
		Hash:          hex.EncodeToString(digest[:]),
		TokenPrefix:   prefix,
		CreatedAt:     created,
	}
	payload, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", RemoteTokenStatus{}, err
	}
	if err := writeDurableAtomic(s.path, append(payload, '\n'), 0o600); err != nil {
		return "", RemoteTokenStatus{}, err
	}
	status := RemoteTokenStatus{
		SchemaVersion: remoteSchemaVersion,
		Enabled:       true,
		TokenPrefix:   prefix,
		CreatedAt:     &created,
		APIBase:       remoteAPIPrefix,
		Capabilities:  remoteAPIPrefix + "/capabilities",
	}
	return token, status, nil
}

func (s *RemoteTokenStore) Revoke() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *RemoteTokenStore) Verify(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, remoteTokenPrefix) || len(value) < len(remoteTokenPrefix)+32 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.loadLocked()
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(record.Hash)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	got := sha256.Sum256([]byte(value))
	return subtle.ConstantTimeCompare(got[:], want) == 1
}

func (s *RemoteTokenStore) statusLocked() (RemoteTokenStatus, error) {
	record, err := s.loadLocked()
	if errors.Is(err, os.ErrNotExist) {
		return RemoteTokenStatus{SchemaVersion: remoteSchemaVersion, APIBase: remoteAPIPrefix, Capabilities: remoteAPIPrefix + "/capabilities"}, nil
	}
	if err != nil {
		return RemoteTokenStatus{}, err
	}
	created := record.CreatedAt
	return RemoteTokenStatus{
		SchemaVersion: remoteSchemaVersion,
		Enabled:       true,
		TokenPrefix:   record.TokenPrefix,
		CreatedAt:     &created,
		APIBase:       remoteAPIPrefix,
		Capabilities:  remoteAPIPrefix + "/capabilities",
	}, nil
}

func (s *RemoteTokenStore) loadLocked() (remoteTokenFile, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return remoteTokenFile{}, err
	}
	var record remoteTokenFile
	if err := json.Unmarshal(data, &record); err != nil {
		return remoteTokenFile{}, err
	}
	if record.SchemaVersion != remoteSchemaVersion || record.Hash == "" || record.TokenPrefix == "" || record.CreatedAt.IsZero() {
		return remoteTokenFile{}, errors.New("remote management token file is invalid or unsupported")
	}
	return record, nil
}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) < 8 || !strings.EqualFold(header[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}

func (s *Server) requireAdminSession(w http.ResponseWriter, r *http.Request, mutation bool) bool {
	if !s.authenticated(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "authentication_required", "message": "sign in to OpenSurge"}})
		return false
	}
	if mutation && !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "origin_rejected", "message": "mutation origin is not allowed"}})
		return false
	}
	return true
}

func (s *Server) handleRemoteTokenStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminSession(w, r, false) {
		return
	}
	status, err := s.remoteTokens.Status()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "remote_token_state_failed", "message": err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleRemoteTokenRotate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminSession(w, r, true) {
		return
	}
	token, status, err := s.remoteTokens.Rotate()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "remote_token_rotate_failed", "message": err.Error()}})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"schema_version": status.SchemaVersion,
		"enabled":        status.Enabled,
		"token":          token,
		"token_prefix":   status.TokenPrefix,
		"created_at":     status.CreatedAt,
		"api_base":       status.APIBase,
		"capabilities":   status.Capabilities,
	})
}

func (s *Server) handleRemoteTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminSession(w, r, true) {
		return
	}
	if err := s.remoteTokens.Revoke(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "remote_token_revoke_failed", "message": err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, RemoteTokenStatus{SchemaVersion: remoteSchemaVersion, APIBase: remoteAPIPrefix, Capabilities: remoteAPIPrefix + "/capabilities"})
}

type remoteEndpoint struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Summary string `json:"summary"`
	Notes   string `json:"notes,omitempty"`
}

func remoteCapabilities() map[string]any {
	return map[string]any{
		"schema_version": remoteSchemaVersion,
		"product":        "OpenSurge for QNAP",
		"api_base":       remoteAPIPrefix,
		"authentication": map[string]string{
			"type":   "bearer",
			"header": "Authorization: Bearer <remote-management-token>",
		},
		"notes": []string{
			"Remote management exposes the existing QNAP Web management surface through a dedicated prefix.",
			"Browser session cookies are not accepted on the remote prefix; a remote management token is required.",
			"Configuration writes use optimistic concurrency: GET the current resource first and send its ETag/revision with If-Match when required.",
			"Gateway lifecycle operations can be asynchronous; follow the returned operation id through /operations/{id}.",
			"QNAP-blocked desktop/macOS operations remain unavailable remotely.",
		},
		"endpoints": []remoteEndpoint{
			{Method: "GET", Path: "/overview", Summary: "Read gateway, policy, provider, device and recovery overview."},
			{Method: "GET", Path: "/config", Summary: "Read runtime configuration and current revision."},
			{Method: "PUT", Path: "/config", Summary: "Validate and persist runtime configuration.", Notes: "Requires If-Match with the current config revision."},
			{Method: "GET|POST", Path: "/gateway/plan", Summary: "Inspect the gateway start plan and blockers."},
			{Method: "POST", Path: "/gateway/start", Summary: "Start the gateway."},
			{Method: "POST", Path: "/gateway/stop", Summary: "Stop the gateway or clean interrupted runtime state."},
			{Method: "POST", Path: "/gateway/reload", Summary: "Reload the active gateway configuration."},
			{Method: "POST", Path: "/gateway/restart-mihomo", Summary: "Restart only the mihomo data plane."},
			{Method: "GET|POST", Path: "/sources", Summary: "List or import a URL/file configuration source."},
			{Method: "POST", Path: "/sources/{id}/refresh", Summary: "Refresh a source from its origin."},
			{Method: "GET", Path: "/sources/{id}/preview", Summary: "Preview a source and effective inventory."},
			{Method: "POST", Path: "/sources/{id}/apply", Summary: "Apply a source version.", Notes: "Use If-Match when the resource supplies a revision."},
			{Method: "POST", Path: "/sources/{id}/export", Summary: "Export the stored source snapshot."},
			{Method: "GET|PUT", Path: "/profile-overlay", Summary: "Read or update the profile overlay."},
			{Method: "GET", Path: "/devices", Summary: "List observed and registered LAN devices."},
			{Method: "GET", Path: "/device-traffic", Summary: "Read per-device traffic."},
			{Method: "GET|PUT", Path: "/device-policy", Summary: "Read or persist device policy configuration."},
			{Method: "POST", Path: "/devices/{device}/selectors/{slot}", Summary: "Change a device policy selector."},
			{Method: "POST", Path: "/devices/{device}/connections/refresh", Summary: "Close/refresh active connections for one device."},
			{Method: "GET", Path: "/policies", Summary: "List proxy policy groups and selections."},
			{Method: "POST", Path: "/policies/{group}/selection", Summary: "Change a proxy group selection."},
			{Method: "POST", Path: "/policy-workspace", Summary: "Read/select/test policies through the workspace API."},
			{Method: "GET|POST", Path: "/local-routing", Summary: "Read or change gateway-local rule/global/direct routing."},
			{Method: "POST", Path: "/local-routing/connections/refresh", Summary: "Refresh local gateway connections after route changes."},
			{Method: "GET", Path: "/proxy-health", Summary: "Read proxy health status."},
			{Method: "POST", Path: "/proxy-health/tests", Summary: "Run proxy latency/health tests."},
			{Method: "GET|POST", Path: "/connectivity", Summary: "Read connectivity status."},
			{Method: "POST", Path: "/connectivity/tests", Summary: "Run connectivity tests."},
			{Method: "GET", Path: "/providers", Summary: "List proxy and rule providers."},
			{Method: "POST", Path: "/providers/{name}/refresh", Summary: "Refresh a provider."},
			{Method: "GET", Path: "/network/interfaces", Summary: "Read container-visible network interfaces."},
			{Method: "GET", Path: "/network/defaults", Summary: "Read detected network defaults."},
			{Method: "GET", Path: "/network/discovery", Summary: "Read network discovery data."},
			{Method: "POST", Path: "/network/dhcp-probe", Summary: "Probe DHCP state without mutating QNAP host networking."},
			{Method: "GET|PUT", Path: "/tailscale", Summary: "Read or update Tailscale/Headscale integration."},
			{Method: "GET", Path: "/tailscale/discovery", Summary: "Discover Tailscale nodes and routes."},
			{Method: "POST", Path: "/tailscale/forget-identity", Summary: "Forget persisted Tailscale identity."},
			{Method: "GET|POST", Path: "/doctor", Summary: "Read or run the QNAP environment Doctor."},
			{Method: "GET", Path: "/diagnostics", Summary: "Read detailed diagnostics and process logs."},
			{Method: "GET", Path: "/operations", Summary: "List recent operations."},
			{Method: "GET", Path: "/operations/{id}", Summary: "Read one asynchronous operation."},
			{Method: "GET", Path: "/events", Summary: "Stream/read operation events when supported by the existing control API."},
		},
	}
}

func (s *Server) handleRemoteManagement(w http.ResponseWriter, r *http.Request) {
	if !s.remoteTokens.Verify(bearerToken(r)) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "remote_token_required", "message": "a valid remote management bearer token is required"}})
		return
	}

	if r.URL.Path == remoteAPIPrefix || r.URL.Path == remoteAPIPrefix+"/" || r.URL.Path == remoteAPIPrefix+"/capabilities" {
		writeJSON(w, http.StatusOK, remoteCapabilities())
		return
	}

	suffix := strings.TrimPrefix(r.URL.Path, remoteAPIPrefix)
	if suffix == r.URL.Path || !strings.HasPrefix(suffix, "/") {
		http.NotFound(w, r)
		return
	}
	targetPath := "/api/v1" + suffix
	if s.qnapOnly && qnapPathBlocked(targetPath) {
		http.NotFound(w, r)
		return
	}

	// The external token is consumed at the Web boundary. The reverse proxy
	// replaces Authorization with the loopback-only control token before the
	// request reaches the privileged control process.
	r.URL.Path = targetPath
	r.URL.RawPath = ""
	r.Header.Set("X-OpenSurge-Remote-Management", "token")
	s.proxy.ServeHTTP(w, r)
}
