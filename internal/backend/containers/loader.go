package containers

import (
	"context"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

type loaderClient interface {
	ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerTop(context.Context, string, client.ContainerTopOptions) (client.ContainerTopResult, error)
}

// Loader reads complete Containers-page observations from Docker.
type Loader struct {
	docker loaderClient
}

func NewLoader(docker loaderClient) (*Loader, error) {
	if docker == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Containers loader"}
	}
	return &Loader{docker: docker}, nil
}

func (loader *Loader) Load(ctx context.Context, key backend.RefreshKey) (backend.EventPayload, error) {
	switch key.Kind {
	case RefreshKindList:
		if key.ID != "" {
			return nil, unsupportedRefresh(key)
		}
		return loader.loadList(ctx)
	case RefreshKindDetails:
		id, err := requireContainerID("refresh container details", key.ID)
		if err != nil {
			return nil, err
		}
		return loader.loadDetails(ctx, id)
	case RefreshKindProcesses:
		id, err := requireContainerID("refresh container processes", key.ID)
		if err != nil {
			return nil, err
		}
		return loader.loadProcesses(ctx, id)
	default:
		return nil, unsupportedRefresh(key)
	}
}

func (loader *Loader) loadList(ctx context.Context) (backend.EventPayload, error) {
	result, err := loader.docker.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, classifyDockerError("list containers", "", err)
	}
	containers := make([]Summary, 0, len(result.Items))
	for _, item := range result.Items {
		names := append([]string(nil), item.Names...)
		for index := range names {
			names[index] = strings.TrimPrefix(names[index], "/")
		}
		ports := make([]Port, 0, len(item.Ports))
		for _, port := range item.Ports {
			ports = append(ports, Port{
				IP: addressString(port.IP), PrivatePort: port.PrivatePort,
				PublicPort: port.PublicPort, Protocol: port.Type,
			})
		}
		health := ""
		if item.Health != nil {
			health = string(item.Health.Status)
		}
		containers = append(containers, Summary{
			ID: item.ID, Names: names, Image: item.Image, ImageID: item.ImageID,
			Command: item.Command, Created: time.Unix(item.Created, 0), State: string(item.State),
			Status: item.Status, Health: health, Ports: ports, Labels: cloneLabels(item.Labels),
		})
	}
	return ListUpdated{Containers: containers}, nil
}

func (loader *Loader) loadDetails(ctx context.Context, id string) (backend.EventPayload, error) {
	result, err := loader.docker.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, classifyDockerError("inspect container", id, err)
	}
	value := result.Container
	details := Details{
		ID: value.ID, Name: strings.TrimPrefix(value.Name, "/"), Created: parseDockerTime(value.Created),
		Image: value.Image, Path: value.Path, Args: append([]string(nil), value.Args...),
		RestartCount: value.RestartCount, Driver: value.Driver, Platform: value.Platform,
	}
	if value.Config != nil {
		details.Hostname = value.Config.Hostname
		details.User = value.Config.User
		details.WorkingDir = value.Config.WorkingDir
		details.Environment = append([]string(nil), value.Config.Env...)
		details.Labels = cloneLabels(value.Config.Labels)
	}
	if value.State != nil {
		details.State = State{
			Status: string(value.State.Status), Running: value.State.Running,
			Paused: value.State.Paused, Restarting: value.State.Restarting,
			OOMKilled: value.State.OOMKilled, Dead: value.State.Dead,
			PID: value.State.Pid, ExitCode: value.State.ExitCode, Error: value.State.Error,
			StartedAt: parseDockerTime(value.State.StartedAt), FinishedAt: parseDockerTime(value.State.FinishedAt),
		}
		if value.State.Health != nil {
			details.State.Health = string(value.State.Health.Status)
		}
	}
	for _, mount := range value.Mounts {
		details.Mounts = append(details.Mounts, Mount{
			Type: string(mount.Type), Name: mount.Name, Source: mount.Source,
			Destination: mount.Destination, Driver: mount.Driver, Mode: mount.Mode,
			ReadWrite: mount.RW, Propagation: string(mount.Propagation),
		})
	}
	if value.NetworkSettings != nil {
		for name, network := range value.NetworkSettings.Networks {
			if network == nil {
				continue
			}
			details.Networks = append(details.Networks, Network{
				Name: name, NetworkID: network.NetworkID, EndpointID: network.EndpointID,
				IPAddress: addressString(network.IPAddress), Gateway: addressString(network.Gateway),
				MACAddress: network.MacAddress.String(), IPv6Address: addressString(network.GlobalIPv6Address),
			})
		}
		sort.Slice(details.Networks, func(i, j int) bool { return details.Networks[i].Name < details.Networks[j].Name })
	}
	return DetailsUpdated{Container: details}, nil
}

func (loader *Loader) loadProcesses(ctx context.Context, id string) (backend.EventPayload, error) {
	result, err := loader.docker.ContainerTop(ctx, id, client.ContainerTopOptions{})
	if err != nil {
		return nil, classifyDockerError("list container processes", id, err)
	}
	return ProcessesUpdated{
		ContainerID: id,
		Titles:      append([]string(nil), result.Titles...),
		Rows:        cloneRows(result.Processes),
	}, nil
}

func unsupportedRefresh(key backend.RefreshKey) error {
	return &backend.AppError{
		Code: backend.ErrorUnsupported, Operation: "load Containers",
		Resource: string(key.Kind), ID: key.ID,
	}
}

func parseDockerTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func addressString(address netip.Addr) string {
	if !address.IsValid() {
		return ""
	}
	return address.String()
}

var _ backend.RefreshLoader = (*Loader)(nil)
