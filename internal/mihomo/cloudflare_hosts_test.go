package mihomo

import (
	"strings"
	"testing"
)

func TestJoinCloudflareManagedHostsPreservesUserNativeHosts(t *testing.T) {
	user := `"manual.example.com": "192.0.2.8"`
	joined, err := JoinCloudflareManagedHosts(user, map[string]string{"api.example.com": "104.18.1.2"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(joined, `"manual.example.com": "192.0.2.8"`) || !strings.Contains(joined, `"api.example.com": "104.18.1.2"`) {
		t.Fatalf("joined = %s", joined)
	}
	clean, managed, err := SplitCloudflareManagedHosts(joined)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(clean) != user {
		t.Fatalf("user block changed: %q", clean)
	}
	if managed["api.example.com"] != "104.18.1.2" {
		t.Fatalf("managed = %#v", managed)
	}
}

func TestJoinCloudflareManagedHostsReplacesPreviousBlock(t *testing.T) {
	first, err := JoinCloudflareManagedHosts("", map[string]string{"api.example.com": "104.18.1.2"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := JoinCloudflareManagedHosts(first, map[string]string{"api.example.com": "104.18.1.3"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(second, cloudflareHostsBegin) != 1 || strings.Contains(second, "104.18.1.2") || !strings.Contains(second, "104.18.1.3") {
		t.Fatalf("second = %s", second)
	}
}

func TestJoinCloudflareManagedHostsRejectsUserNativeConflict(t *testing.T) {
	_, err := JoinCloudflareManagedHosts(`"api.example.com": "192.0.2.9"`, map[string]string{"api.example.com": "104.18.1.2"})
	if err == nil {
		t.Fatal("expected conflict")
	}
}
