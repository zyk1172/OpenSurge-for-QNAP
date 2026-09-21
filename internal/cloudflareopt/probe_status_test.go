package cloudflareopt

import (
	"net/http"
	"testing"
)

func TestValidHTTPSValidationResponseStatusMatrix(t *testing.T) {
	cloudflare := http.Header{}
	cloudflare.Set("CF-Ray", "abc-SIN")
	tests := []struct {
		status int
		want   bool
	}{
		{199, false},
		{200, true},
		{204, true},
		{301, true},
		{308, true},
		{400, true},
		{401, true},
		{403, true},
		{404, true},
		{405, true},
		{429, true},
		{500, false},
		{503, false},
	}
	for _, tt := range tests {
		if got := validHTTPSValidationResponse(tt.status, cloudflare, nil); got != tt.want {
			t.Fatalf("status %d accepted=%v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestValidHTTPSValidationResponseRequiresCloudflareEdgeEvidence(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusFound, http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusTooManyRequests} {
		if validHTTPSValidationResponse(status, http.Header{}, nil) {
			t.Fatalf("status %d must not pass without Cloudflare edge evidence", status)
		}
	}
}
