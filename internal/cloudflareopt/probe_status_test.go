package cloudflareopt

import "testing"

func TestValidHTTPSValidationStatus(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{199, false},
		{200, true},
		{204, true},
		{301, true},
		{308, true},
		{400, false},
		{401, false},
		{403, false},
		{404, false},
		{429, false},
		{500, false},
		{503, false},
	}
	for _, tt := range tests {
		if got := validHTTPSValidationStatus(tt.status); got != tt.want {
			t.Fatalf("status %d accepted=%v, want %v", tt.status, got, tt.want)
		}
	}
}
