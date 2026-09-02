package system

import (
	"context"
	"sort"
	"strings"

	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
	imagepage "github.com/petar030/ssh-native-docker-tui/internal/backend/images"
	networkpage "github.com/petar030/ssh-native-docker-tui/internal/backend/networks"
	volumepage "github.com/petar030/ssh-native-docker-tui/internal/backend/volumes"
)

func (api *API) containerPruneRequest(options ContainerPruneOptions) (backend.CommandRequest, <-chan PruneReport, error) {
	filters, err := filteredPruneOptions("prune containers", options.Until, options.Labels)
	if err != nil {
		return backend.CommandRequest{}, nil, err
	}
	reports := make(chan PruneReport, 1)
	request := backend.CommandRequest{
		OperationID: "system.prune.containers", Operation: "prune containers",
		Affected: []backend.AffectedResource{{Kind: "containers", ID: "filtered"}}, RefreshKeys: containerPruneRefreshKeys(),
		Run: func(ctx context.Context) error {
			result, err := api.docker.ContainerPrune(ctx, client.ContainerPruneOptions{Filters: filters.Clone()})
			if err == nil {
				reports <- PruneReport{ContainersDeleted: append([]string(nil), result.Report.ContainersDeleted...), SpaceReclaimed: result.Report.SpaceReclaimed}
			}
			return classifyDockerError("prune containers", err)
		},
	}
	return request, reports, nil
}

func (api *API) imagePruneRequest(options ImagePruneOptions) (backend.CommandRequest, <-chan PruneReport, error) {
	filters, err := imagePruneFilters(options)
	if err != nil {
		return backend.CommandRequest{}, nil, err
	}
	reports := make(chan PruneReport, 1)
	request := backend.CommandRequest{
		OperationID: "system.prune.images", Operation: "prune images",
		Affected: []backend.AffectedResource{{Kind: "images", ID: "filtered"}}, RefreshKeys: imagePruneRefreshKeys(),
		Run: func(ctx context.Context) error {
			result, err := api.docker.ImagePrune(ctx, client.ImagePruneOptions{Filters: filters.Clone()})
			if err == nil {
				report := PruneReport{SpaceReclaimed: result.Report.SpaceReclaimed}
				for _, value := range result.Report.ImagesDeleted {
					report.ImagesDeleted = append(report.ImagesDeleted, ImageDeletion{Deleted: value.Deleted, Untagged: value.Untagged})
				}
				reports <- report
			}
			return classifyDockerError("prune images", err)
		},
	}
	return request, reports, nil
}

func (api *API) volumePruneRequest(options VolumePruneOptions) (backend.CommandRequest, <-chan PruneReport, error) {
	if len(options.Labels) == 0 {
		return backend.CommandRequest{}, nil, invalidPruneInput("prune volumes", "label filters")
	}
	filters, err := filteredPruneOptions("prune volumes", "", options.Labels)
	if err != nil {
		return backend.CommandRequest{}, nil, err
	}
	reports := make(chan PruneReport, 1)
	request := backend.CommandRequest{
		OperationID: "system.prune.volumes", Operation: "prune volumes",
		Affected: []backend.AffectedResource{{Kind: "volumes", ID: "filtered"}}, RefreshKeys: volumePruneRefreshKeys(),
		Run: func(ctx context.Context) error {
			result, err := api.docker.VolumePrune(ctx, client.VolumePruneOptions{All: options.All, Filters: filters.Clone()})
			if err == nil {
				reports <- PruneReport{VolumesDeleted: append([]string(nil), result.Report.VolumesDeleted...), SpaceReclaimed: result.Report.SpaceReclaimed}
			}
			return classifyDockerError("prune volumes", err)
		},
	}
	return request, reports, nil
}

func (api *API) networkPruneRequest(options NetworkPruneOptions) (backend.CommandRequest, <-chan PruneReport, error) {
	filters, err := filteredPruneOptions("prune networks", options.Until, options.Labels)
	if err != nil {
		return backend.CommandRequest{}, nil, err
	}
	reports := make(chan PruneReport, 1)
	request := backend.CommandRequest{
		OperationID: "system.prune.networks", Operation: "prune networks",
		Affected: []backend.AffectedResource{{Kind: "networks", ID: "filtered"}}, RefreshKeys: networkPruneRefreshKeys(),
		Run: func(ctx context.Context) error {
			result, err := api.docker.NetworkPrune(ctx, client.NetworkPruneOptions{Filters: filters.Clone()})
			if err == nil {
				reports <- PruneReport{NetworksDeleted: append([]string(nil), result.Report.NetworksDeleted...)}
			}
			return classifyDockerError("prune networks", err)
		},
	}
	return request, reports, nil
}

func (api *API) systemPruneRequest(options SystemPruneOptions) (backend.CommandRequest, <-chan PruneReport, error) {
	if !api.allowSystemPrune {
		return backend.CommandRequest{}, nil, &backend.AppError{Code: backend.ErrorPermissionDenied, Operation: "prune system", Resource: "bootstrap opt-in"}
	}
	if options.Confirmation != SystemPruneConfirmation {
		return backend.CommandRequest{}, nil, invalidPruneInput("prune system", "confirmation")
	}
	reports := make(chan PruneReport, 1)
	request := backend.CommandRequest{
		OperationID: "system.prune", Operation: "prune system",
		Affected: []backend.AffectedResource{{Kind: "system", ID: "all-unused"}}, RefreshKeys: systemPruneRefreshKeys(options.IncludeVolumes),
		Run: func(ctx context.Context) error {
			report, err := api.runSystemPrune(ctx, options.IncludeVolumes)
			if err == nil {
				reports <- report
			}
			return err
		},
	}
	return request, reports, nil
}

func (api *API) runSystemPrune(ctx context.Context, includeVolumes bool) (PruneReport, error) {
	var report PruneReport
	containersResult, err := api.docker.ContainerPrune(ctx, client.ContainerPruneOptions{})
	if err != nil {
		return report, classifyDockerError("prune system containers", err)
	}
	report.ContainersDeleted = append(report.ContainersDeleted, containersResult.Report.ContainersDeleted...)
	report.SpaceReclaimed += containersResult.Report.SpaceReclaimed

	networksResult, err := api.docker.NetworkPrune(ctx, client.NetworkPruneOptions{})
	if err != nil {
		return report, classifyDockerError("prune system networks", err)
	}
	report.NetworksDeleted = append(report.NetworksDeleted, networksResult.Report.NetworksDeleted...)

	imagesResult, err := api.docker.ImagePrune(ctx, client.ImagePruneOptions{Filters: make(client.Filters).Add("dangling", "false")})
	if err != nil {
		return report, classifyDockerError("prune system images", err)
	}
	for _, value := range imagesResult.Report.ImagesDeleted {
		report.ImagesDeleted = append(report.ImagesDeleted, ImageDeletion{Deleted: value.Deleted, Untagged: value.Untagged})
	}
	report.SpaceReclaimed += imagesResult.Report.SpaceReclaimed

	cacheResult, err := api.docker.BuildCachePrune(ctx, client.BuildCachePruneOptions{All: true})
	if err != nil {
		return report, classifyDockerError("prune system build cache", err)
	}
	report.BuildCacheDeleted = append(report.BuildCacheDeleted, cacheResult.Report.CachesDeleted...)
	report.SpaceReclaimed += cacheResult.Report.SpaceReclaimed

	if includeVolumes {
		volumesResult, err := api.docker.VolumePrune(ctx, client.VolumePruneOptions{All: true})
		if err != nil {
			return report, classifyDockerError("prune system volumes", err)
		}
		report.VolumesDeleted = append(report.VolumesDeleted, volumesResult.Report.VolumesDeleted...)
		report.SpaceReclaimed += volumesResult.Report.SpaceReclaimed
	}
	return report, nil
}

func filteredPruneOptions(operation, until string, labels map[string]string) (client.Filters, error) {
	filters := make(client.Filters)
	if until = strings.TrimSpace(until); until != "" {
		filters.Add("until", until)
	}
	keys := make([]string, 0, len(labels))
	cleaned := make(map[string]string, len(labels))
	for key, value := range labels {
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "" {
			return nil, invalidPruneInput(operation, "label filter")
		}
		keys = append(keys, key)
		cleaned[key] = value
	}
	sort.Strings(keys)
	for _, key := range keys {
		if cleaned[key] == "" {
			filters.Add("label", key)
		} else {
			filters.Add("label", key+"="+cleaned[key])
		}
	}
	if len(filters) == 0 {
		return nil, invalidPruneInput(operation, "filters")
	}
	return filters, nil
}

func imagePruneFilters(options ImagePruneOptions) (client.Filters, error) {
	filters, err := filteredPruneOptionsAllowEmpty("prune images", options.Until, options.Labels)
	if err != nil {
		return nil, err
	}
	if options.Dangling != nil {
		if !*options.Dangling {
			return nil, invalidPruneInput("prune images", "dangling filter")
		}
		filters.Add("dangling", "true")
	}
	if len(filters) == 0 {
		return nil, invalidPruneInput("prune images", "filters")
	}
	return filters, nil
}

func filteredPruneOptionsAllowEmpty(operation, until string, labels map[string]string) (client.Filters, error) {
	if strings.TrimSpace(until) == "" && len(labels) == 0 {
		return make(client.Filters), nil
	}
	return filteredPruneOptions(operation, until, labels)
}

func invalidPruneInput(operation, resource string) error {
	return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: operation, Resource: resource}
}

func systemRefreshKeys() []backend.RefreshKey {
	return []backend.RefreshKey{{Kind: RefreshKindInfo}, {Kind: RefreshKindDiskUsage}, {Kind: dashboard.RefreshKindSummary}}
}

func containerPruneRefreshKeys() []backend.RefreshKey {
	return append([]backend.RefreshKey{{Kind: containers.RefreshKindList}}, systemRefreshKeys()...)
}

func imagePruneRefreshKeys() []backend.RefreshKey {
	return append([]backend.RefreshKey{{Kind: imagepage.RefreshKindList}}, systemRefreshKeys()...)
}

func volumePruneRefreshKeys() []backend.RefreshKey {
	return append([]backend.RefreshKey{{Kind: volumepage.RefreshKindList}, {Kind: containers.RefreshKindList}}, systemRefreshKeys()...)
}

func networkPruneRefreshKeys() []backend.RefreshKey {
	return append([]backend.RefreshKey{{Kind: networkpage.RefreshKindList}}, systemRefreshKeys()...)
}

func systemPruneRefreshKeys(includeVolumes bool) []backend.RefreshKey {
	keys := []backend.RefreshKey{
		{Kind: containers.RefreshKindList}, {Kind: imagepage.RefreshKindList}, {Kind: networkpage.RefreshKindList},
	}
	if includeVolumes {
		keys = append(keys, backend.RefreshKey{Kind: volumepage.RefreshKindList})
	}
	return append(keys, systemRefreshKeys()...)
}
