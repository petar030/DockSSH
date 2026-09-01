package images

import (
	"context"
	"sort"
	"time"

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
		id, err := requireImageID("read image details", key.ID)
		if err != nil {
			return nil, err
		}
		return api.readDetails(ctx, id)
	case RefreshKindHistory:
		id, err := requireImageID("read image history", key.ID)
		if err != nil {
			return nil, err
		}
		return api.readHistory(ctx, id)
	default:
		return nil, unsupportedRefresh(key)
	}
}

func (api *API) readList(ctx context.Context) (backend.EventPayload, error) {
	result, err := api.docker.ImageList(ctx, client.ImageListOptions{All: true, SharedSize: true})
	if err != nil {
		return nil, classifyDockerError("list images", "", err)
	}
	values := make([]Summary, 0, len(result.Items))
	for _, item := range result.Items {
		tags := append([]string(nil), item.RepoTags...)
		digests := append([]string(nil), item.RepoDigests...)
		sort.Strings(tags)
		sort.Strings(digests)
		values = append(values, Summary{
			ID: item.ID, ParentID: item.ParentID, RepoTags: tags, RepoDigests: digests,
			Created: time.Unix(item.Created, 0), Size: item.Size, SharedSize: item.SharedSize,
			Containers: item.Containers, Labels: cloneStrings(item.Labels),
		})
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Created.Equal(values[j].Created) {
			return values[i].ID < values[j].ID
		}
		return values[i].Created.After(values[j].Created)
	})
	return ListUpdated{Images: values}, nil
}

func (api *API) readDetails(ctx context.Context, id string) (backend.EventPayload, error) {
	result, err := api.docker.ImageInspect(ctx, id)
	if err != nil {
		return nil, classifyDockerError("inspect image", id, err)
	}
	value := result.InspectResponse
	details := Details{
		ID: value.ID, RepoTags: sortedCopy(value.RepoTags), RepoDigests: sortedCopy(value.RepoDigests),
		Comment: value.Comment, Created: parseDockerTime(value.Created), Author: value.Author,
		Architecture: value.Architecture, Variant: value.Variant, OS: value.Os,
		OSVersion: value.OsVersion, Size: value.Size, RootFSType: value.RootFS.Type,
		Layers: append([]string(nil), value.RootFS.Layers...),
	}
	if value.Config != nil {
		details.User = value.Config.User
		details.Entrypoint = append([]string(nil), value.Config.Entrypoint...)
		details.Command = append([]string(nil), value.Config.Cmd...)
		details.WorkingDir = value.Config.WorkingDir
		details.Environment = append([]string(nil), value.Config.Env...)
		details.ExposedPorts = sortedSet(value.Config.ExposedPorts)
		details.Volumes = sortedSet(value.Config.Volumes)
		details.Labels = cloneStrings(value.Config.Labels)
	}
	if value.GraphDriver != nil {
		details.GraphDriver = value.GraphDriver.Name
		details.GraphDriverData = cloneStrings(value.GraphDriver.Data)
	}
	return DetailsUpdated{Image: details}, nil
}

func (api *API) readHistory(ctx context.Context, id string) (backend.EventPayload, error) {
	result, err := api.docker.ImageHistory(ctx, id)
	if err != nil {
		return nil, classifyDockerError("read image history", id, err)
	}
	entries := make([]HistoryEntry, 0, len(result.Items))
	for _, item := range result.Items {
		entries = append(entries, HistoryEntry{
			ID: item.ID, Created: time.Unix(item.Created, 0), CreatedBy: item.CreatedBy,
			Comment: item.Comment, Tags: sortedCopy(item.Tags), Size: item.Size,
		})
	}
	return HistoryUpdated{ImageID: id, Entries: entries}, nil
}

func unsupportedRefresh(key backend.RefreshKey) error {
	return &backend.AppError{Code: backend.ErrorUnsupported, Operation: "read Images refresh", Resource: string(key.Kind), ID: key.ID}
}

func parseDockerTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func sortedCopy(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
