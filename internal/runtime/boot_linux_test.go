//go:build linux

package runtime

import "testing"

func TestCurrentBootSessionIncludesNetworkNamespace(t *testing.T) {
	boot, err := CurrentBootSession()
	if err != nil {
		t.Fatal(err)
	}
	if boot.ID == "" && boot.StartedAt.IsZero() {
		t.Fatal("Linux boot session has no host boot evidence")
	}
	if boot.NetworkNamespace == "" {
		t.Fatal("Linux boot session has no network namespace identity")
	}
}
