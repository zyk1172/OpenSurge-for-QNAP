package qnaphost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const ManagedLabel = "io.opensurge.qnap.managed"

var interfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,64}$`)

type Interface struct {
	Name      string   `json:"name"`
	MTU       int      `json:"mtu"`
	Flags     []string `json:"flags"`
	Addresses []string `json:"addresses"`
}

type NetworkConfig struct {
	ParentInterface string `json:"parent_interface"`
	IPv4            string `json:"ipv4"`
	Subnet          string `json:"subnet"`
	Gateway         string `json:"gateway"`
}

type Status struct {
	SchemaVersion int            `json:"schema_version"`
	Interfaces    []Interface    `json:"interfaces"`
	Network       *NetworkConfig `json:"network,omitempty"`
	Connected     bool           `json:"connected"`
}

type Server struct {
	SocketPath    string
	DockerSocket  string
	ContainerName string
	NetworkName   string
	docker        *dockerClient
}

func NewServer(socketPath, dockerSocket, containerName, networkName string) *Server {
	if socketPath == "" {
		socketPath = "/run/opensurge-host/agent.sock"
	}
	if dockerSocket == "" {
		dockerSocket = "/var/run/docker.sock"
	}
	if containerName == "" {
		containerName = "opensurge"
	}
	if networkName == "" {
		networkName = "opensurge-qnet"
	}
	return &Server{
		SocketPath:    socketPath,
		DockerSocket:  dockerSocket,
		ContainerName: containerName,
		NetworkName:   networkName,
		docker:        newDockerClient(dockerSocket),
	}
}

func (s *Server) Serve(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.SocketPath), 0o700); err != nil {
		return err
	}
	_ = os.Remove(s.SocketPath)
	listener, err := net.Listen("unix", s.SocketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(s.SocketPath)
	if err := os.Chmod(s.SocketPath, 0o600); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", s.handleStatus)
	mux.HandleFunc("PUT /v1/network", s.handleNetwork)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.Serve(listener); errors.Is(err, http.ErrServerClosed) {
		return nil
	} else {
		return err
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleNetwork(w http.ResponseWriter, r *http.Request) {
	var cfg NetworkConfig
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateNetworkConfig(cfg); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	if err := s.ensureHostInterface(cfg.ParentInterface); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	if err := s.configureNetwork(r.Context(), cfg); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	status, err := s.status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) status(ctx context.Context) (Status, error) {
	interfaces, err := hostInterfaces()
	if err != nil {
		return Status{}, err
	}
	status := Status{SchemaVersion: 1, Interfaces: interfaces}
	network, exists, err := s.inspectManagedNetwork(ctx)
	if err != nil {
		return Status{}, err
	}
	if !exists {
		return status, nil
	}
	status.Network = networkConfigFromInspect(network)
	_, status.Connected = network.Containers[s.ContainerName]
	if !status.Connected {
		container, err := s.inspectTargetContainer(ctx)
		if err != nil {
			return Status{}, err
		}
		_, status.Connected = container.NetworkSettings.Networks[s.NetworkName]
	}
	return status, nil
}

func (s *Server) configureNetwork(ctx context.Context, cfg NetworkConfig) error {
	container, err := s.inspectTargetContainer(ctx)
	if err != nil {
		return err
	}
	if container.Config.Labels[ManagedLabel] != "true" {
		return fmt.Errorf("refusing to manage container %q without %s=true label", s.ContainerName, ManagedLabel)
	}

	if existing, exists, err := s.inspectManagedNetwork(ctx); err != nil {
		return err
	} else if exists {
		if existing.Labels[ManagedLabel] != "true" {
			return fmt.Errorf("network %q already exists but is not OpenSurge-managed", s.NetworkName)
		}
		if sameNetwork(existing, cfg) {
			if _, connected := container.NetworkSettings.Networks[s.NetworkName]; connected {
				return nil
			}
			return s.docker.do(ctx, http.MethodPost, "/networks/"+url.PathEscape(s.NetworkName)+"/connect", map[string]any{
				"Container": s.ContainerName,
				"EndpointConfig": map[string]any{"IPAMConfig": map[string]any{"IPv4Address": cfg.IPv4}},
			}, nil)
		}
		_ = s.docker.do(ctx, http.MethodPost, "/networks/"+url.PathEscape(s.NetworkName)+"/disconnect", map[string]any{
			"Container": s.ContainerName,
			"Force":     true,
		}, nil)
		if err := s.docker.do(ctx, http.MethodDelete, "/networks/"+url.PathEscape(s.NetworkName), nil, nil); err != nil {
			return fmt.Errorf("remove previous OpenSurge QNET: %w", err)
		}
	}

	create := dockerNetworkCreate{
		Name:   s.NetworkName,
		Driver: "qnet",
		Options: map[string]string{"iface": cfg.ParentInterface},
		Labels: map[string]string{ManagedLabel: "true"},
		IPAM: dockerIPAM{
			Driver:  "qnet",
			Options: map[string]string{"iface": cfg.ParentInterface},
			Config:  []dockerIPAMConfig{{Subnet: cfg.Subnet, Gateway: cfg.Gateway}},
		},
	}
	var created struct{ ID string `json:"Id"` }
	if err := s.docker.do(ctx, http.MethodPost, "/networks/create", create, &created); err != nil {
		return fmt.Errorf("create QNET: %w", err)
	}
	if err := s.docker.do(ctx, http.MethodPost, "/networks/"+url.PathEscape(s.NetworkName)+"/connect", map[string]any{
		"Container": s.ContainerName,
		"EndpointConfig": map[string]any{"IPAMConfig": map[string]any{"IPv4Address": cfg.IPv4}},
	}, nil); err != nil {
		_ = s.docker.do(ctx, http.MethodDelete, "/networks/"+url.PathEscape(s.NetworkName), nil, nil)
		return fmt.Errorf("connect OpenSurge container to QNET: %w", err)
	}
	return nil
}

func (s *Server) inspectTargetContainer(ctx context.Context) (dockerContainerInspect, error) {
	var container dockerContainerInspect
	if err := s.docker.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(s.ContainerName)+"/json", nil, &container); err != nil {
		return dockerContainerInspect{}, fmt.Errorf("inspect OpenSurge container: %w", err)
	}
	return container, nil
}

func (s *Server) inspectManagedNetwork(ctx context.Context) (dockerNetworkInspect, bool, error) {
	var network dockerNetworkInspect
	err := s.docker.do(ctx, http.MethodGet, "/networks/"+url.PathEscape(s.NetworkName), nil, &network)
	if errors.Is(err, errDockerNotFound) {
		return dockerNetworkInspect{}, false, nil
	}
	if err != nil {
		return dockerNetworkInspect{}, false, err
	}
	return network, true, nil
}

func (s *Server) ensureHostInterface(name string) error {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return fmt.Errorf("QNAP host interface %q does not exist", name)
	}
	if iface.Flags&net.FlagLoopback != 0 {
		return fmt.Errorf("loopback cannot be used as a QNET parent interface")
	}
	return nil
}

func hostInterfaces() ([]Interface, error) {
	list, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	result := make([]Interface, 0, len(list))
	for _, iface := range list {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		item := Interface{Name: iface.Name, MTU: iface.MTU}
		for _, addr := range addrs {
			item.Addresses = append(item.Addresses, addr.String())
		}
		if iface.Flags&net.FlagUp != 0 {
			item.Flags = append(item.Flags, "up")
		}
		if iface.Flags&net.FlagBroadcast != 0 {
			item.Flags = append(item.Flags, "broadcast")
		}
		if iface.Flags&net.FlagMulticast != 0 {
			item.Flags = append(item.Flags, "multicast")
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func validateNetworkConfig(cfg NetworkConfig) error {
	cfg.ParentInterface = strings.TrimSpace(cfg.ParentInterface)
	if !interfaceNamePattern.MatchString(cfg.ParentInterface) {
		return fmt.Errorf("invalid parent interface")
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(cfg.IPv4))
	if err != nil || !ip.Is4() {
		return fmt.Errorf("invalid IPv4 address")
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cfg.Subnet))
	if err != nil || !prefix.Addr().Is4() {
		return fmt.Errorf("invalid IPv4 subnet")
	}
	prefix = prefix.Masked()
	if !prefix.Contains(ip) {
		return fmt.Errorf("IPv4 address is outside the requested subnet")
	}
	gateway, err := netip.ParseAddr(strings.TrimSpace(cfg.Gateway))
	if err != nil || !gateway.Is4() || !prefix.Contains(gateway) {
		return fmt.Errorf("gateway must be an IPv4 address inside the requested subnet")
	}
	if gateway == ip {
		return fmt.Errorf("gateway and OpenSurge IPv4 address must differ")
	}
	return nil
}

func sameNetwork(network dockerNetworkInspect, cfg NetworkConfig) bool {
	if network.Driver != "qnet" || network.Options["iface"] != cfg.ParentInterface || network.IPAM.Driver != "qnet" || network.IPAM.Options["iface"] != cfg.ParentInterface {
		return false
	}
	for _, candidate := range network.IPAM.Config {
		if candidate.Subnet == cfg.Subnet && candidate.Gateway == cfg.Gateway {
			return true
		}
	}
	return false
}

func networkConfigFromInspect(network dockerNetworkInspect) *NetworkConfig {
	if network.Driver != "qnet" {
		return nil
	}
	cfg := &NetworkConfig{ParentInterface: network.Options["iface"]}
	if len(network.IPAM.Config) > 0 {
		cfg.Subnet = network.IPAM.Config[0].Subnet
		cfg.Gateway = network.IPAM.Config[0].Gateway
	}
	for _, endpoint := range network.Containers {
		if endpoint.Name == "opensurge" {
			cfg.IPv4 = strings.TrimSuffix(endpoint.IPv4Address, "/")
			if slash := strings.IndexByte(cfg.IPv4, '/'); slash >= 0 {
				cfg.IPv4 = cfg.IPv4[:slash]
			}
			break
		}
	}
	return cfg
}

var errDockerNotFound = errors.New("docker object not found")

type dockerClient struct{ client *http.Client }

func newDockerClient(socketPath string) *dockerClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &dockerClient{client: &http.Client{Transport: transport, Timeout: 20 * time.Second}}
}

func (d *dockerClient) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		io.Copy(io.Discard, resp.Body)
		return errDockerNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
		return fmt.Errorf("Docker API %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(payload)))
	}
	if output == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(output)
}

type dockerNetworkCreate struct {
	Name    string            `json:"Name"`
	Driver  string            `json:"Driver"`
	Options map[string]string `json:"Options"`
	Labels  map[string]string `json:"Labels"`
	IPAM    dockerIPAM        `json:"IPAM"`
}

type dockerIPAM struct {
	Driver  string             `json:"Driver"`
	Options map[string]string  `json:"Options"`
	Config  []dockerIPAMConfig `json:"Config"`
}

type dockerIPAMConfig struct {
	Subnet  string `json:"Subnet"`
	Gateway string `json:"Gateway"`
}

type dockerNetworkInspect struct {
	Name       string                    `json:"Name"`
	Driver     string                    `json:"Driver"`
	Options    map[string]string         `json:"Options"`
	Labels     map[string]string         `json:"Labels"`
	IPAM       dockerIPAM                `json:"IPAM"`
	Containers map[string]dockerEndpoint `json:"Containers"`
}

type dockerEndpoint struct {
	Name        string `json:"Name"`
	IPv4Address string `json:"IPv4Address"`
}

type dockerContainerInspect struct {
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	NetworkSettings struct {
		Networks map[string]json.RawMessage `json:"Networks"`
	} `json:"NetworkSettings"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": err.Error()}})
}
