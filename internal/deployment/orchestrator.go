package deployment

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Orchestrator struct {
	Docker *DockerClient
	Image  string
	mu     sync.Mutex
}

func (o *Orchestrator) image() string {
	if o.Image == "" {
		return DefaultGatewayImage
	}
	return o.Image
}

func (o *Orchestrator) Status(ctx context.Context) (Status, error) {
	if o.Docker == nil {
		return Status{}, fmt.Errorf("docker client is required")
	}
	info, exists, err := o.Docker.InspectContainer(ctx, ManagedGatewayContainer)
	if err != nil {
		return Status{}, err
	}
	if !exists {
		return Status{}, nil
	}
	spec, managed := SpecFromLabels(info.Config.Labels)
	if !managed {
		return Status{Error: "a container named opensurge-gateway exists but is not owned by OpenSurge"}, nil
	}
	return Status{
		Configured: true,
		Running:    info.State.Running,
		Spec:       spec,
		GatewayURL: "http://" + spec.ContainerIP + ":8080",
	}, nil
}

func (o *Orchestrator) Apply(ctx context.Context, spec Spec) (Status, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	spec.Normalize()
	if err := spec.Validate(); err != nil {
		return Status{}, err
	}
	if o.Docker == nil {
		return Status{}, fmt.Errorf("docker client is required")
	}
	if err := o.Docker.Ping(ctx); err != nil {
		return Status{}, err
	}
	if err := o.Docker.ImageExists(ctx, o.image()); err != nil {
		return Status{}, err
	}

	oldInfo, oldExists, err := o.Docker.InspectContainer(ctx, ManagedGatewayContainer)
	if err != nil {
		return Status{}, err
	}
	var oldSpec Spec
	var oldManaged bool
	var oldImage string
	var oldRunning bool
	if oldExists {
		oldSpec, oldManaged = SpecFromLabels(oldInfo.Config.Labels)
		if !oldManaged {
			return Status{}, fmt.Errorf("refusing to replace container %q because it is not OpenSurge-managed", ManagedGatewayContainer)
		}
		oldImage = oldInfo.Config.Image
		oldRunning = oldInfo.State.Running
		if oldSpec == spec && oldRunning {
			return Status{Configured: true, Running: true, Spec: spec, GatewayURL: "http://" + spec.ContainerIP + ":8080"}, nil
		}
	}

	if err := o.replace(ctx, spec, o.image()); err != nil {
		rollbackErr := error(nil)
		if oldExists && oldManaged {
			rollbackErr = o.replace(ctx, oldSpec, oldImage)
			if rollbackErr == nil && !oldRunning {
				_ = o.Docker.StopContainer(context.Background(), ManagedGatewayContainer)
			}
		}
		if rollbackErr != nil {
			return Status{}, fmt.Errorf("apply deployment: %w; rollback also failed: %v", err, rollbackErr)
		}
		return Status{}, fmt.Errorf("apply deployment: %w", err)
	}
	return Status{Configured: true, Running: true, Spec: spec, GatewayURL: "http://" + spec.ContainerIP + ":8080"}, nil
}

func (o *Orchestrator) replace(ctx context.Context, spec Spec, image string) error {
	if err := o.Docker.StopContainer(ctx, ManagedGatewayContainer); err != nil {
		return fmt.Errorf("stop previous gateway: %w", err)
	}
	if err := o.Docker.RemoveContainer(ctx, ManagedGatewayContainer); err != nil {
		return fmt.Errorf("remove previous gateway: %w", err)
	}
	if err := o.Docker.RemoveManagedNetwork(ctx); err != nil {
		return err
	}
	if err := o.Docker.CreateManagedNetwork(ctx, spec); err != nil {
		return err
	}
	created := false
	defer func() {
		if !created {
			_ = o.Docker.RemoveContainer(context.Background(), ManagedGatewayContainer)
			_ = o.Docker.RemoveManagedNetwork(context.Background())
		}
	}()
	if err := o.Docker.CreateGatewayContainer(ctx, spec, image); err != nil {
		return err
	}
	if err := o.Docker.StartContainer(ctx, ManagedGatewayContainer); err != nil {
		return fmt.Errorf("start managed gateway: %w", err)
	}
	if err := o.Docker.WaitHealthy(ctx, ManagedGatewayContainer, 75*time.Second); err != nil {
		return err
	}
	created = true
	return nil
}

func (d *DockerClient) RemoveManagedNetwork(ctx context.Context) error {
	var info struct {
		Name   string            `json:"Name"`
		Labels map[string]string `json:"Labels"`
	}
	err := d.do(ctx, http.MethodGet, "/networks/"+url.PathEscape(ManagedGatewayNetwork), nil, &info)
	if dockerNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Labels["org.opensurge.managed"] != "true" {
		return fmt.Errorf("refusing to remove network %q because it is not OpenSurge-managed", ManagedGatewayNetwork)
	}
	if err := d.RemoveNetwork(ctx, ManagedGatewayNetwork); err != nil {
		return fmt.Errorf("remove managed qnet network: %w", err)
	}
	return nil
}
