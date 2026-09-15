//go:build linux

package linux

import (
	"encoding/json"
	"testing"

	"open-mihomo-gateway/internal/platform"
)

func TestIsOwnedQNAPHostDNSRule(t *testing.T) {
	cfg := platform.NetworkConfig{
		LANInterface:      "eth0",
		RouteTableID:      20241,
		RouteRulePriority: 20241,
		SameLAN:           true,
	}

	tests := []struct {
		name string
		json string
		want bool
	}{
		{
			name: "udp NAS DNS rule",
			json: `{"priority":20239,"src":"192.168.2.240","iif":"eth0","ipproto":"udp","dport":53,"table":"20243"}`,
			want: true,
		},
		{
			name: "tcp NAS DNS rule",
			json: `{"priority":20240,"src":"192.168.2.240/32","iif":"eth0","ipproto":"tcp","dport":53,"table":20243}`,
			want: true,
		},
		{
			name: "broad ingress rule",
			json: `{"priority":20239,"src":"all","iif":"eth0","table":"20243"}`,
			want: false,
		},
		{
			name: "wrong table",
			json: `{"priority":20239,"src":"192.168.2.240","iif":"eth0","ipproto":"udp","dport":53,"table":"20241"}`,
			want: false,
		},
		{
			name: "wrong priority",
			json: `{"priority":20238,"src":"192.168.2.240","iif":"eth0","ipproto":"udp","dport":53,"table":"20243"}`,
			want: false,
		},
		{
			name: "subnet source is not an owned host selector",
			json: `{"priority":20239,"src":"192.168.2.0/24","iif":"eth0","ipproto":"udp","dport":53,"table":"20243"}`,
			want: false,
		},
		{
			name: "wrong ingress interface",
			json: `{"priority":20239,"src":"192.168.2.240","iif":"br0","ipproto":"udp","dport":53,"table":"20243"}`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rule ipRule
			if err := json.Unmarshal([]byte(tt.json), &rule); err != nil {
				t.Fatalf("decode rule: %v", err)
			}
			if got := isOwnedQNAPHostDNSRule(rule, cfg); got != tt.want {
				t.Fatalf("isOwnedQNAPHostDNSRule() = %t, want %t", got, tt.want)
			}
		})
	}
}
