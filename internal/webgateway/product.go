package webgateway

import (
	"fmt"
	"net/http"
)

// NASPlatform is deployment metadata, not host auto-detection: a container
// sees its own Debian userspace rather than the NAS distribution.
type NASPlatform string

func ParseNASPlatform(value string) (NASPlatform, error) {
	if value == "" {
		value = "qnap" // Existing QNAP deployments keep their product behavior.
	}
	switch value {
	case "qnap", "synology", "fnos", "generic":
		return NASPlatform(value), nil
	default:
		return "", fmt.Errorf("unsupported OPENSURGE_NAS_PLATFORM %q", value)
	}
}

func (p NASPlatform) productName() string {
	switch p {
	case "qnap":
		return "OpenSurge for QNAP"
	case "synology":
		return "OpenSurge for Synology"
	case "fnos":
		return "OpenSurge for fnOS"
	default:
		return "OpenSurge for NAS"
	}
}

func (s *Server) handleProduct(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	network := "macvlan"
	if s.nasPlatform == "qnap" {
		network = "qnet"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"platform":       s.nasPlatform,
		"name":           s.nasPlatform.productName(),
		"network_driver": network,
		"host_takeover":  s.nasPlatform == "qnap",
		"experimental":   s.nasPlatform != "qnap",
	})
}

func (s *Server) platformPathBlocked(path string) bool {
	return s.nasPlatform != "qnap" && path == "/api/v1/qnap-host-routing"
}
