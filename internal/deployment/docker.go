package deployment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type DockerClient struct {
	socket string
	client *http.Client
	mu sync.Mutex
	apiVersion string
}

func NewDockerClient(socket string) *DockerClient {
	if strings.TrimSpace(socket) == "" {
		socket = "/var/run/docker.sock"
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	return &DockerClient{
		socket: socket,
		client: &http.Client{Transport: transport, Timeout: 20 * time.Second},
	}
}

func (d *DockerClient) Ping(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/_ping", nil)
	if err != nil {
		return err
	}
	response, err := d.client.Do(request)
	if err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("docker ping returned %s", response.Status)
	}
	return nil
}

func (d *DockerClient) api(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.apiVersion != "" {
		return d.apiVersion, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/version", nil)
	if err != nil {
		return "", err
	}
	response, err := d.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("docker version: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("docker version returned %s", response.Status)
	}
	var payload struct{ APIVersion string `json:"ApiVersion"` }
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode docker version: %w", err)
	}
	if strings.TrimSpace(payload.APIVersion) == "" {
		return "", fmt.Errorf("docker daemon did not report an API version")
	}
	d.apiVersion = payload.APIVersion
	return d.apiVersion, nil
}

func (d *DockerClient) do(ctx context.Context, method, path string, input, output any, okStatuses ...int) error {
	version, err := d.api(ctx)
	if err != nil {
		return err
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://docker/v"+version+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	accepted := response.StatusCode >= 200 && response.StatusCode < 300
	if len(okStatuses) > 0 {
		accepted = false
		for _, status := range okStatuses {
			if response.StatusCode == status {
				accepted = true
				break
			}
		}
	}
	if !accepted {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 32<<10))
		return &DockerAPIError{StatusCode: response.StatusCode, Message: strings.TrimSpace(string(payload))}
	}
	if output != nil && response.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}
	return nil
}

type DockerAPIError struct {
	StatusCode int
	Message string
}

func (e *DockerAPIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("docker API returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("docker API returned HTTP %d: %s", e.StatusCode, e.Message)
}

func dockerNotFound(err error) bool {
	var apiErr *DockerAPIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

type containerInspect struct {
	ID string `json:"Id"`
	Config struct {
		Image string `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool `json:"Running"`
		Health *struct { Status string `json:"Status"` } `json:"Health"`
	} `json:"State"`
}

func (d *DockerClient) InspectContainer(ctx context.Context, name string) (containerInspect, bool, error) {
	var info containerInspect
	err := d.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", nil, &info)
	if dockerNotFound(err) {
		return containerInspect{}, false, nil
	}
	return info, err == nil, err
}

func (d *DockerClient) ImageExists(ctx context.Context, image string) error {
	var ignored map[string]any
	err := d.do(ctx, http.MethodGet, "/images/"+url.PathEscape(image)+"/json", nil, &ignored)
	if dockerNotFound(err) {
		return fmt.Errorf("gateway image %q is not present locally; rebuild the bootstrap stack first", image)
	}
	return err
}

func (d *DockerClient) StopContainer(ctx context.Context, name string) error {
	err := d.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/stop?t=30", nil, nil, http.StatusNoContent, http.StatusNotModified, http.StatusNotFound)
	return err
}

func (d *DockerClient) RemoveContainer(ctx context.Context, name string) error {
	err := d.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(name)+"?force=true", nil, nil, http.StatusNoContent, http.StatusNotFound)
	return err
}

func (d *DockerClient) RemoveNetwork(ctx context.Context, name string) error {
	err := d.do(ctx, http.MethodDelete, "/networks/"+url.PathEscape(name), nil, nil, http.StatusNoContent, http.StatusNotFound)
	return err
}

func (d *DockerClient) CreateManagedNetwork(ctx context.Context, spec Spec) error {
	payload := map[string]any{
		"Name": ManagedGatewayNetwork,
		"CheckDuplicate": true,
		"Driver": "qnet",
		"Options": map[string]string{"iface": spec.ParentInterface},
		"IPAM": map[string]any{
			"Driver": "qnet",
			"Options": map[string]string{"iface": spec.ParentInterface},
			"Config": []map[string]string{{"Subnet": spec.Subnet, "Gateway": spec.Gateway}},
		},
		"Labels": map[string]string{"org.opensurge.managed": "true"},
	}
	var response struct{ ID string `json:"Id"`; Warning string `json:"Warning"` }
	if err := d.do(ctx, http.MethodPost, "/networks/create", payload, &response, http.StatusCreated); err != nil {
		return fmt.Errorf("create managed qnet network: %w", err)
	}
	return nil
}

func (d *DockerClient) CreateGatewayContainer(ctx context.Context, spec Spec, image string) error {
	labels := map[string]string{
		"org.opensurge.managed": "true",
		"org.opensurge.parent-interface": spec.ParentInterface,
		"org.opensurge.container-ip": spec.ContainerIP,
		"org.opensurge.subnet": spec.Subnet,
		"org.opensurge.gateway": spec.Gateway,
		"org.opensurge.data-path": spec.DataPath,
	}
	payload := map[string]any{
		"Image": image,
		"Env": []string{
			"OPENSURGE_ROLE=gateway",
			"OPENSURGE_SEED_LAN_IP="+spec.ContainerIP,
			"OPENSURGE_SEED_LAN_CIDR="+spec.Subnet,
			"OPENSURGE_SEED_UPSTREAM_GATEWAY="+spec.Gateway,
			"OPENSURGE_CONTAINER_INTERFACE=eth0",
		},
		"Labels": labels,
		"HostConfig": map[string]any{
			"Binds": []string{spec.DataPath+":/data"},
			"CapAdd": []string{"NET_ADMIN", "NET_RAW"},
			"Devices": []map[string]string{{
				"PathOnHost": "/dev/net/tun",
				"PathInContainer": "/dev/net/tun",
				"CgroupPermissions": "rwm",
			}},
			"Sysctls": map[string]string{
				"net.ipv4.ip_forward": "1",
				"net.ipv4.conf.all.rp_filter": "0",
				"net.ipv4.conf.default.rp_filter": "0",
			},
			"SecurityOpt": []string{"no-new-privileges:true"},
			"RestartPolicy": map[string]any{"Name": "unless-stopped", "MaximumRetryCount": 0},
			"NetworkMode": ManagedGatewayNetwork,
			"LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		"NetworkingConfig": map[string]any{
			"EndpointsConfig": map[string]any{
				ManagedGatewayNetwork: map[string]any{
					"IPAMConfig": map[string]string{"IPv4Address": spec.ContainerIP},
				},
			},
		},
	}
	var response struct{ ID string `json:"Id"`; Warnings []string `json:"Warnings"` }
	path := "/containers/create?name="+url.QueryEscape(ManagedGatewayContainer)
	if err := d.do(ctx, http.MethodPost, path, payload, &response, http.StatusCreated); err != nil {
		return fmt.Errorf("create managed gateway container: %w", err)
	}
	return nil
}

func (d *DockerClient) StartContainer(ctx context.Context, name string) error {
	return d.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/start", nil, nil, http.StatusNoContent, http.StatusNotModified)
}

func (d *DockerClient) WaitHealthy(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		info, exists, err := d.InspectContainer(ctx, name)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("managed gateway container disappeared during startup")
		}
		if !info.State.Running {
			return fmt.Errorf("managed gateway container exited during startup")
		}
		if info.State.Health != nil {
			switch info.State.Health.Status {
			case "healthy":
				return nil
			case "unhealthy":
				return fmt.Errorf("managed gateway container became unhealthy")
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for managed gateway health")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func SpecFromLabels(labels map[string]string) (Spec, bool) {
	if labels["org.opensurge.managed"] != "true" {
		return Spec{}, false
	}
	spec := Spec{
		ParentInterface: labels["org.opensurge.parent-interface"],
		ContainerIP: labels["org.opensurge.container-ip"],
		Subnet: labels["org.opensurge.subnet"],
		Gateway: labels["org.opensurge.gateway"],
		DataPath: labels["org.opensurge.data-path"],
	}
	spec.Normalize()
	if spec.Validate() != nil {
		return Spec{}, false
	}
	return spec, true
}
