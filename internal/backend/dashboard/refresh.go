package dashboard

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const (
	RefreshKindSummary  backend.RefreshKind = "dashboard.summary"
	EventSummaryUpdated backend.EventType   = "dashboard_summary_updated"
	recentEventLimit                        = 8
)

type dockerReader interface {
	Ping(context.Context, client.PingOptions) (client.PingResult, error)
	Info(context.Context, client.InfoOptions) (client.SystemInfoResult, error)
	VolumeList(context.Context, client.VolumeListOptions) (client.VolumeListResult, error)
	NetworkList(context.Context, client.NetworkListOptions) (client.NetworkListResult, error)
	DiskUsage(context.Context, client.DiskUsageOptions) (client.DiskUsageResult, error)
}

type eventHistory interface {
	Recent(backend.EventFilter, int) []backend.EventEnvelope
}

// RefreshHandler is the Dashboard's runtime-only refresh implementation.
// Dashboard has no domain-specific TUI API: sessions subscribe to
// PageDashboard and call Backend.RequestRefresh(PageDashboard).
type RefreshHandler struct {
	docker dockerReader
	events eventHistory
}

func NewRefreshHandler(docker dockerReader, events eventHistory) (*RefreshHandler, error) {
	if docker == nil || events == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Dashboard refresh handler"}
	}
	return &RefreshHandler{docker: docker, events: events}, nil
}

// ReadRefresh is registered by runtime bootstrap and called only by the
// backend-owned RefreshManager. Dashboard sessions receive its typed result
// from the Dashboard Event Bus.
func (handler *RefreshHandler) ReadRefresh(ctx context.Context, key backend.RefreshKey) (backend.EventPayload, error) {
	if key.Kind != RefreshKindSummary || key.ID != "" {
		return nil, &backend.AppError{
			Code: backend.ErrorUnsupported, Operation: "read Dashboard refresh", Resource: string(key.Kind), ID: key.ID,
		}
	}

	ping, err := handler.docker.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
	if err != nil {
		return nil, fmt.Errorf("load Dashboard Engine availability: %w", err)
	}
	infoResult, err := handler.docker.Info(ctx, client.InfoOptions{})
	if err != nil {
		return nil, fmt.Errorf("load Dashboard Engine information: %w", err)
	}
	volumes, err := handler.docker.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return nil, fmt.Errorf("load Dashboard volume count: %w", err)
	}
	networks, err := handler.docker.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, fmt.Errorf("load Dashboard network count: %w", err)
	}
	disk, err := handler.docker.DiskUsage(ctx, client.DiskUsageOptions{
		Containers: true, Images: true, Volumes: true, BuildCache: true,
	})
	if err != nil {
		return nil, fmt.Errorf("load Dashboard disk usage: %w", err)
	}

	info := infoResult.Info
	return SummaryUpdated{
		Engine: EngineSummary{
			Available: true, Name: info.Name, ServerVersion: info.ServerVersion,
			APIVersion: ping.APIVersion, OperatingSystem: info.OperatingSystem,
			Architecture: info.Architecture, CPUs: info.NCPU, MemoryBytes: info.MemTotal,
			SystemTime: info.SystemTime,
		},
		Resources: ResourceCounts{
			Containers: info.Containers, ContainersRunning: info.ContainersRunning,
			ContainersPaused: info.ContainersPaused, ContainersStopped: info.ContainersStopped,
			Images: info.Images, Volumes: len(volumes.Items), Networks: len(networks.Items),
		},
		DiskUsage: DiskUsage{
			Containers: diskUsage(disk.Containers.TotalCount, disk.Containers.ActiveCount, disk.Containers.TotalSize, disk.Containers.Reclaimable),
			Images:     diskUsage(disk.Images.TotalCount, disk.Images.ActiveCount, disk.Images.TotalSize, disk.Images.Reclaimable),
			Volumes:    diskUsage(disk.Volumes.TotalCount, disk.Volumes.ActiveCount, disk.Volumes.TotalSize, disk.Volumes.Reclaimable),
			BuildCache: diskUsage(disk.BuildCache.TotalCount, disk.BuildCache.ActiveCount, disk.BuildCache.TotalSize, disk.BuildCache.Reclaimable),
		},
		RecentEvents: recentEvents(handler.events.Recent(backend.EventFilter{
			Types: []backend.EventType{backend.EventDockerObserved},
		}, recentEventLimit)),
	}, nil
}

func diskUsage(count, active, total, reclaimable int64) ResourceDiskUsage {
	return ResourceDiskUsage{
		Count: count, Active: active, TotalBytes: total, ReclaimableBytes: reclaimable,
	}
}

func recentEvents(events []backend.EventEnvelope) []RecentEvent {
	result := make([]RecentEvent, 0, len(events))
	for _, event := range events {
		observed, ok := event.Payload.(backend.DockerEventObserved)
		if !ok {
			continue
		}
		result = append(result, RecentEvent{
			Time: event.Time, Resource: observed.Resource, ResourceID: observed.ResourceID,
			Project: observed.Project, Action: observed.Action,
		})
	}
	return result
}
