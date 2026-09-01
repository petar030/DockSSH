package volumes

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// ReadRefresh is registered by bootstrap and called only by RefreshManager.
func (api *API) ReadRefresh(ctx context.Context, key backend.RefreshKey) (backend.EventPayload, error) {
	switch key.Kind {
	case RefreshKindList:
		if key.ID != "" {
			return nil, unsupportedRefresh(key)
		}
		return api.readList(ctx)
	case RefreshKindDetails:
		name, err := requireVolumeName("read volume details", key.ID)
		if err != nil {
			return nil, err
		}
		return api.readDetails(ctx, name)
	case RefreshKindAttachments:
		name, err := requireVolumeName("read volume attachments", key.ID)
		if err != nil {
			return nil, err
		}
		return api.readAttachments(ctx, name)
	default:
		return nil, unsupportedRefresh(key)
	}
}

func (api *API) readList(ctx context.Context) (backend.EventPayload, error) {
	result, err := api.docker.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return nil, classifyDockerError("list volumes", "", err)
	}
	values := make([]Volume, 0, len(result.Items))
	for _, item := range result.Items {
		values = append(values, mapVolume(item))
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return ListUpdated{Volumes: values, Warnings: append([]string(nil), result.Warnings...)}, nil
}

func (api *API) readDetails(ctx context.Context, name string) (backend.EventPayload, error) {
	result, err := api.docker.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if err != nil {
		return nil, classifyDockerError("inspect volume", name, err)
	}
	return DetailsUpdated{Volume: mapVolume(result.Volume)}, nil
}

func (api *API) readAttachments(ctx context.Context, name string) (backend.EventPayload, error) {
	filters := make(client.Filters)
	filters.Add("volume", name)
	result, err := api.docker.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, classifyDockerError("list volume attachments", name, err)
	}
	attachments := make([]Attachment, 0)
	for _, container := range result.Items {
		containerName := container.ID
		if len(container.Names) > 0 {
			containerName = strings.TrimPrefix(container.Names[0], "/")
		}
		for _, mount := range container.Mounts {
			if mount.Name != name {
				continue
			}
			attachments = append(attachments, Attachment{
				ContainerID: container.ID, ContainerName: containerName,
				State: string(container.State), Status: container.Status,
				Destination: mount.Destination, Mode: mount.Mode, ReadWrite: mount.RW,
				Propagation: string(mount.Propagation), MountType: string(mount.Type),
			})
		}
	}
	sort.Slice(attachments, func(i, j int) bool {
		if attachments[i].ContainerName == attachments[j].ContainerName {
			return attachments[i].Destination < attachments[j].Destination
		}
		return attachments[i].ContainerName < attachments[j].ContainerName
	})
	return AttachmentsUpdated{VolumeName: name, Attachments: attachments}, nil
}

func mapVolume(value volume.Volume) Volume {
	result := Volume{
		Name: value.Name, Driver: value.Driver, Scope: value.Scope,
		Created: parseDockerTime(value.CreatedAt), Mountpoint: value.Mountpoint,
		Labels: cloneStrings(value.Labels), Options: cloneStrings(value.Options),
	}
	if value.Status != nil {
		result.Status = make(map[string]string, len(value.Status))
		for key, status := range value.Status {
			result.Status[key] = fmt.Sprint(status)
		}
	}
	if value.UsageData != nil {
		result.UsageKnown = true
		result.Size = value.UsageData.Size
		result.ReferenceCount = value.UsageData.RefCount
	}
	return result
}

func unsupportedRefresh(key backend.RefreshKey) error {
	return &backend.AppError{Code: backend.ErrorUnsupported, Operation: "read Volumes refresh", Resource: string(key.Kind), ID: key.ID}
}

func parseDockerTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
