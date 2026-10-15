//go:build linux

package process

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOwnsTCPListenerRequiresExactAddressStateAndFD(t *testing.T) {
	for _, tc := range []struct {
		name, address, state, inode, fd string
		want                            bool
	}{
		{"owned loopback", "0100007F:1EAE", "0A", "42", "42", true},
		{"foreign socket", "0100007F:1EAE", "0A", "42", "43", false},
		{"wildcard", "00000000:1EAE", "0A", "42", "42", false},
		{"wrong port", "0100007F:1EAF", "0A", "42", "42", false},
		{"established", "0100007F:1EAE", "01", "42", "42", false},
		{"mapped IPv4", "0000000000000000FFFF00000100007F:1EAE", "0A", "42", "42", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			p := filepath.Join(root, "123")
			for _, d := range []string{"fd", "net"} {
				if err := os.MkdirAll(filepath.Join(p, d), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink("socket:["+tc.fd+"]", filepath.Join(p, "fd", "7")); err != nil {
				t.Fatal(err)
			}
			line := fmt.Sprintf("0: %s 00000000:0000 %s 0:0 0:0 0 0 0 %s\n", tc.address, tc.state, tc.inode)
			if err := os.WriteFile(filepath.Join(p, "net", "tcp"), []byte(line), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := ownsTCPListenerAt(root, 123, "127.0.0.1:7854")
			if err != nil || got != tc.want {
				t.Fatalf("got %v %v want %v", got, err, tc.want)
			}
		})
	}
}
