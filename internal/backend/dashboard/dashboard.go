// Package dashboard implements the complete read model for the Dashboard tab.
package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const (
	RefreshKindSummary  backend.RefreshKind = "dashboard.summary"
	EventSummaryUpdated backend.EventType   = "dashboard_summary_updated"
	recentEventLimit                        = 8
)

// EngineSummary contains the small set of Engine facts shown on Dashboard.
type EngineSummary struct {
	Available       bool
	Name            string
	ServerVersion   string
	APIVersion      string
	OperatingSystem string
	Architecture    string
	CPUs            int
	MemoryBytes     int64
	SystemTime      string
}

// ResourceCounts contains the process-wide counts shown on Dashboard.
type ResourceCounts struct {
	Containers        int
	ContainersRunning int
	ContainersPaused  int
	ContainersStopped int
	Images            int
	Volumes           int
	Networks          int
}

// ResourceDiskUsage summarizes one Docker resource category in bytes.
type ResourceDiskUsage struct {
	Count            int64
	Active           int64
	TotalBytes       int64
	ReclaimableBytes int64
}

// DiskUsage contains all categories returned by Docker's system disk-usage API.
type DiskUsage struct {
	Containers ResourceDiskUsage
	Images     ResourceDiskUsage
	Volumes    ResourceDiskUsage
	BuildCache ResourceDiskUsage
}

// RecentEvent is the compact event row rendered on Dashboard.
type RecentEvent struct {
	Time       time.Time
	Resource   string
	ResourceID string
	Project    string
	Action     string
}

// SummaryUpdated replaces all data displayed by one Dashboard session.
type SummaryUpdated struct {
	Engine       EngineSummary
	Resources    ResourceCounts
	DiskUsage    DiskUsage
	RecentEvents []RecentEvent
}

func (SummaryUpdated) EventType() backend.EventType { return EventSummaryUpdated }

func (summary SummaryUpdated) CloneEventPayload() backend.EventPayload {
	summary.RecentEvents = append([]RecentEvent(nil), summary.RecentEvents...)
	return summary
}

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

// Loader reads every Dashboard window from Docker and recent event history.
type Loader struct {
	docker dockerReader
	events eventHistory
}

func NewLoader(docker dockerReader, events eventHistory) (*Loader, error) {
	if docker == nil || events == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Dashboard loader"}
	}
	return &Loader{docker: docker, events: events}, nil
}

func (loader *Loader) Load(ctx context.Context, key backend.RefreshKey) (backend.EventPayload, error) {
	if key.Kind != RefreshKindSummary || key.ID != "" {
		return nil, &backend.AppError{
			Code: backend.ErrorUnsupported, Operation: "load Dashboard", Resource: string(key.Kind), ID: key.ID,
		}
	}

	ping, err := loader.docker.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
	if err != nil {
		return nil, fmt.Errorf("load Dashboard Engine availability: %w", err)
	}
	infoResult, err := loader.docker.Info(ctx, client.InfoOptions{})
	if err != nil {
		return nil, fmt.Errorf("load Dashboard Engine information: %w", err)
	}
	volumes, err := loader.docker.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return nil, fmt.Errorf("load Dashboard volume count: %w", err)
	}
	networks, err := loader.docker.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, fmt.Errorf("load Dashboard network count: %w", err)
	}
	disk, err := loader.docker.DiskUsage(ctx, client.DiskUsageOptions{
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
		RecentEvents: recentEvents(loader.events.Recent(backend.EventFilter{
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

var _ backend.RefreshLoader = (*Loader)(nil)
