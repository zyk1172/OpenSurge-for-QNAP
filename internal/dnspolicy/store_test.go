package dnspolicy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathForGatewayConfig(t *testing.T) {
	got := PathForGatewayConfig("/data/config/opensurge.yaml")
	want := "/data/config/dns-policy.json"
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestStoreMissingFileReturnsBackwardCompatibleDefault(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "dns-policy.json"))
	snapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Document.Mode != ModeInheritProfile {
		t.Fatalf("mode = %q, want %q", snapshot.Document.Mode, ModeInheritProfile)
	}
	if snapshot.Revision == "" {
		t.Fatal("default snapshot must have a revision")
	}
	if _, err := os.Stat(store.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load must not seed a file implicitly; stat error = %v", err)
	}
}

func TestStoreSavePersistsNormalizedDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "dns-policy.json")
	store := NewStore(path)
	initial, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	candidate := initial.Document
	candidate.Mode = ModeManaged
	candidate.Managed.Nameservers = []string{" https://dns.example/dns-query "}
	candidate.Managed.FakeIPFilter = []string{" +.lan "}
	saved, err := store.Save(candidate, initial.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision == initial.Revision {
		t.Fatal("save must advance revision after a semantic change")
	}
	if saved.Document.Managed.Nameservers[0] != "https://dns.example/dns-query" {
		t.Fatalf("stored nameserver not normalized: %#v", saved.Document.Managed.Nameservers)
	}
	reloaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Revision != saved.Revision {
		t.Fatalf("reloaded revision = %s, want %s", reloaded.Revision, saved.Revision)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}

func TestStoreRejectsStaleRevisionWithoutOverwrite(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "dns-policy.json"))
	initial, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	first := initial.Document
	first.Mode = ModeManaged
	first.Managed.Nameservers = []string{"1.1.1.1"}
	saved, err := store.Save(first, initial.Revision)
	if err != nil {
		t.Fatal(err)
	}
	second := initial.Document
	second.Mode = ModeManaged
	second.Managed.Nameservers = []string{"8.8.8.8"}
	_, err = store.Save(second, initial.Revision)
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("error = %v, want revision conflict", err)
	}
	reloaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Revision != saved.Revision || reloaded.Document.Managed.Nameservers[0] != "1.1.1.1" {
		t.Fatalf("stale save changed persisted policy: %#v", reloaded)
	}
}

func TestStoreRequiresExpectedRevision(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "dns-policy.json"))
	_, err := store.Save(DefaultDocument(), "")
	if err == nil || !strings.Contains(err.Error(), "expected DNS policy revision") {
		t.Fatalf("error = %v, want expected revision requirement", err)
	}
}

func TestStoreFailsClosedOnMalformedOrUnknownFields(t *testing.T) {
	for name, content := range map[string]string{
		"malformed": `{`,
		"unknown":   `{"schema_version":1,"mode":"inherit_profile","managed":{},"surprise":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dns-policy.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := NewStore(path).Load()
			if err == nil {
				t.Fatal("invalid persisted DNS policy must not fall back silently")
			}
		})
	}
}

func TestStoreRejectsUnsupportedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dns-policy.json")
	content := `{
  "schema_version": 2,
  "mode": "inherit_profile",
  "managed": {
    "default_nameservers": [],
    "nameservers": [],
    "direct_nameservers": [],
    "direct_nameserver_follow_policy": false,
    "proxy_server_nameservers": [],
    "fallback": [],
    "nameserver_policy": [],
    "fallback_filter": {"geoip": false, "geoip_code": "", "geosite": [], "ipcidr": [], "domain": []},
    "fake_ip_filter": [],
    "fake_ip_filter_mode": "blacklist",
    "respect_rules": false,
    "cache_algorithm": "arc",
    "prefer_h3": false
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewStore(path).Load()
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("error = %v, want schema rejection", err)
	}
}
