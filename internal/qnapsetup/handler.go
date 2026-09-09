package qnapsetup

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/gateway"
	"open-mihomo-gateway/internal/qnaphost"
)

type Handler struct {
	ConfigPath string
	Agent      *qnaphost.Client
}

type Status struct {
	SchemaVersion      int                   `json:"schema_version"`
	Host               qnaphost.Status       `json:"host"`
	ContainerInterface string                 `json:"container_interface,omitempty"`
	Gateway            *CurrentGatewayConfig `json:"gateway,omitempty"`
}

type CurrentGatewayConfig struct {
	IPv4      string `json:"ipv4"`
	Subnet    string `json:"subnet"`
	Gateway   string `json:"gateway"`
	Interface string `json:"interface"`
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Agent == nil {
		writeError(w, http.StatusServiceUnavailable, "QNAP host agent is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleStatus(w, r)
	case http.MethodPut:
		h.handleConfigure(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	host, err := h.Agent.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	response := Status{SchemaVersion: 1, Host: host}
	if cfg, err := config.LoadRuntime(h.ConfigPath); err == nil {
		response.Gateway = &CurrentGatewayConfig{
			IPv4:      cfg.Gateway.LANIP,
			Subnet:    cfg.Gateway.LANCIDR,
			Gateway:   cfg.Gateway.UpstreamGateway,
			Interface: cfg.Gateway.Interface,
		}
	}
	if host.Network != nil && host.Network.IPv4 != "" {
		response.ContainerInterface, _ = interfaceForIPv4(host.Network.IPv4)
	}
	writeJSON(w, http.StatusOK, response)
}

func (h Handler) handleConfigure(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadRuntime(h.ConfigPath)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "load current configuration: "+err.Error())
		return
	}
	status, err := gateway.New(cfg).Status(r.Context())
	if err == nil && status.Gateway != "stopped" {
		writeError(w, http.StatusConflict, "stop the gateway before changing the QNAP host network")
		return
	}

	var input qnaphost.NetworkConfig
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(input.Subnet))
	if err != nil || !prefix.Addr().Is4() {
		writeError(w, http.StatusUnprocessableEntity, "subnet must be a valid IPv4 CIDR")
		return
	}
	prefix = prefix.Masked()

	host, err := h.Agent.Configure(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	containerInterface, err := interfaceForIPv4(input.IPv4)
	if err != nil {
		writeError(w, http.StatusConflict, "QNET was attached but the container interface could not be identified: "+err.Error())
		return
	}

	candidate := cfg
	candidate.Gateway.Mode = config.GatewayModeSameLAN
	candidate.Gateway.Interface = containerInterface
	candidate.Gateway.UpstreamInterface = containerInterface
	candidate.Gateway.LANIP = strings.TrimSpace(input.IPv4)
	candidate.Gateway.LANPrefixLen = prefix.Bits()
	candidate.Gateway.LANCIDR = prefix.String()
	candidate.Gateway.UpstreamGateway = strings.TrimSpace(input.Gateway)
	candidate.DHCP.Enabled = false
	candidate.DNS.Listen = candidate.Gateway.LANIP
	candidate.Transparent.Mode = config.TransparentModeTUN
	candidate.Transparent.TUNAutoRoute = false
	candidate.Transparent.TUNAutoDetectInterface = false
	candidate.Transparent.TUNIPv6 = config.TUNIPv6Off
	candidate.Transparent.IPv6SharedL2Ready = false

	if err := config.Validate(candidate); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "QNET attached but generated gateway configuration is invalid: "+err.Error())
		return
	}
	if err := writeAtomic(h.ConfigPath, []byte(config.Render(candidate)), 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, "save QNAP gateway configuration: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, Status{
		SchemaVersion:      1,
		Host:               host,
		ContainerInterface: containerInterface,
		Gateway: &CurrentGatewayConfig{
			IPv4:      candidate.Gateway.LANIP,
			Subnet:    candidate.Gateway.LANCIDR,
			Gateway:   candidate.Gateway.UpstreamGateway,
			Interface: candidate.Gateway.Interface,
		},
	})
}

func interfaceForIPv4(value string) (string, error) {
	want := net.ParseIP(strings.TrimSpace(value)).To4()
	if want == nil {
		return "", fmt.Errorf("invalid IPv4 address %q", value)
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ipText, _, err := net.ParseCIDR(address.String())
			if err == nil && ipText.To4() != nil && ipText.Equal(want) {
				return iface.Name, nil
			}
		}
	}
	return "", fmt.Errorf("no container interface owns %s", value)
}

func writeAtomic(path string, payload []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".qnap-setup-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(payload); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		defer dir.Close()
		_ = dir.Sync()
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": message}})
}
