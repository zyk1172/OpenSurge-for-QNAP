package smartdns

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/runtime"
)

func renderedCacheFile(t *testing.T, cfg config.Config) string {
	t.Helper()
	content, err := RenderConfigWithResolvers(cfg, runtime.NewPaths(cfg), []string{"192.168.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "cache-persist yes\n") {
		return ""
	}
	for _, line := range strings.Split(content, "\n") {
		if value, ok := strings.CutPrefix(line, "cache-file "); ok {
			path, err := strconv.Unquote(value)
			if err != nil {
				t.Fatal(err)
			}
			return path
		}
	}
	return ""
}

func TestResolverCacheFollowsTrustAndProxyPolicy(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	cfg.DNS.ResolverMode = "custom"
	cfg.DNS.ResolverUpstreams = []config.ResolverUpstream{{Address: "https://dns.alidns.com/dns-query", BootstrapIPv4: "223.5.5.5"}}
	cache := renderedCacheFile(t, cfg)
	if cache == "" || renderedCacheFile(t, cfg) != cache {
		t.Fatal("unchanged encrypted policy must reuse its cache")
	}
	cfg.DNS.ResolverUpstreams[0].BootstrapIPv4 = "223.6.6.6"
	directCache := renderedCacheFile(t, cfg)
	if directCache == cache {
		t.Fatal("changed upstream reused the old cache")
	}
	cfg.DNS.ResolverProxyEnabled = true
	cfg.DNS.ResolverProxyTarget = "open-surge-egress"
	cfg.UpstreamProxy.Enabled = true
	cfg.UpstreamProxy.Port = 8080
	proxyCache := renderedCacheFile(t, cfg)
	if proxyCache == directCache {
		t.Fatal("proxy route reused direct-path answers")
	}
	cfg.UpstreamProxy.Server = "192.0.2.20"
	if renderedCacheFile(t, cfg) == proxyCache {
		t.Fatal("changed proxy profile reused the old cache")
	}
	cfg.DNS.ResolverAllowPlaintext = true
	if renderedCacheFile(t, cfg) != "" {
		t.Fatal("plaintext-authorized answers must not persist")
	}
	cfg.DNS.ResolverProxyEnabled = false
	cfg.DNS.ResolverMode = "system"
	if renderedCacheFile(t, cfg) != "" {
		t.Fatal("legacy system answers must not persist")
	}
}

func TestPruneResolverCachesPreservesForeignFiles(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Dir = t.TempDir()
	manager := New(cfg, runtime.NewPaths(cfg))
	if err := os.MkdirAll(manager.cacheDir(), 0700); err != nil {
		t.Fatal(err)
	}
	current := "resolver-" + strings.Repeat("1", 64) + ".cache"
	old := "resolver-" + strings.Repeat("2", 64) + ".cache"
	foreign := "user.cache"
	for _, name := range []string{current, old, old + ".tmp", foreign} {
		if err := os.WriteFile(filepath.Join(manager.cacheDir(), name), []byte("cache"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := "resolver-" + strings.Repeat("3", 64) + ".cache"
	if err := os.Symlink(foreign, filepath.Join(manager.cacheDir(), link)); err != nil {
		t.Fatal(err)
	}
	content := "cache-persist yes\ncache-file " + strconv.Quote(filepath.Join(manager.cacheDir(), current)) + "\n"
	if err := manager.pruneResolverCaches(content); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{current, foreign, link} {
		if _, err := os.Lstat(filepath.Join(manager.cacheDir(), name)); err != nil {
			t.Fatalf("removed retained file %s: %v", name, err)
		}
	}
	for _, name := range []string{old, old + ".tmp"} {
		if _, err := os.Stat(filepath.Join(manager.cacheDir(), name)); !os.IsNotExist(err) {
			t.Fatalf("obsolete owned cache %s still exists", name)
		}
	}
}
