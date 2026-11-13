package mihomo

import "testing"

func TestHostsConfigurationEqual(t *testing.T) {
	for _, test := range []struct {
		name, left, right string
		equal             bool
	}{
		{"unrelated settings", "hosts: {nas.local: 192.0.2.1}\nrules: [MATCH,DIRECT]\n", "# changed\nhosts:\n  nas.local: '192.0.2.1'\nrules: [MATCH,REJECT]\n", true},
		{"YAML aliases and merges", "options: &options {use-hosts: true}\ndns: {<<: *options}\naddresses: &addresses {a.local: 192.0.2.1}\nhosts: *addresses\n", "hosts: {a.local: 192.0.2.1}\ndns: {use-hosts: true}\n", true},
		{"mapping order", "hosts: {a.local: 192.0.2.1, b.local: 192.0.2.2}\n", "hosts: {b.local: 192.0.2.2, a.local: 192.0.2.1}\n", true},
		{"conservative address order and duplicates", "hosts: {a.local: [192.0.2.1,192.0.2.2]}\n", "hosts: {a.local: [192.0.2.2,192.0.2.1,192.0.2.1]}\n", false},
		{"IPv6 spelling", "hosts: {a.local: '2001:db8::1'}\n", "hosts: {a.local: '2001:0db8:0:0:0:0:0:1'}\n", true},
		{"empty mapping", "rules: [MATCH,DIRECT]\n", "hosts: {}\n", true},
		{"changed address", "hosts: {a.local: 192.0.2.1}\n", "hosts: {a.local: 192.0.2.2}\n", false},
		{"removed mapping", "hosts: {a.local: 192.0.2.1}\n", "hosts: {}\n", false},
		{"DNS activation", "dns: {use-hosts: false}\n", "dns: {use-hosts: true}\n", false},
		{"conservative default", "hosts: {}\n", "hosts: {}\ndns: {use-hosts: true}\n", false},
		{"hostname alias", "hosts: {a.local: b.local}\n", "hosts: {a.local: c.local}\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			equal, err := HostsConfigurationEqual([]byte(test.left), []byte(test.right))
			if err != nil || equal != test.equal {
				t.Fatalf("equal=%v expected=%v error=%v", equal, test.equal, err)
			}
		})
	}
}

func TestHostsConfigurationEqualRejectsInvalidDocuments(t *testing.T) {
	for _, data := range []string{"hosts: []\n", "dns: []\n", "hosts: {a.local: 192.0.2.1}\n---\nhosts: {}\n", "hosts: &loop {a.local: *loop}\n"} {
		if _, err := HostsConfigurationEqual([]byte("hosts: {}\n"), []byte(data)); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
}
