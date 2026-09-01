package volumes

import (
	"context"
	"sort"
	"strings"

	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

func (api *API) createRequest(options CreateOptions) (backend.CommandRequest, error) {
	name, err := requireVolumeName("create volume", options.Name)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	driver := strings.TrimSpace(options.Driver)
	driverOptions, err := cleanMap("create volume", "driver option", options.DriverOptions, false)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	labels, err := cleanMap("create volume", "label", options.Labels, true)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	return backend.CommandRequest{
		OperationID: "volume.create", Operation: "create volume",
		Affected:    []backend.AffectedResource{{Kind: "volume", ID: name}},
		RefreshKeys: []backend.RefreshKey{{Kind: RefreshKindList}, {Kind: RefreshKindDetails, ID: name}, {Kind: dashboard.RefreshKindSummary}},
		Run: func(ctx context.Context) error {
			_, err := api.docker.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Driver: driver, DriverOpts: cloneStrings(driverOptions), Labels: cloneStrings(labels)})
			return classifyDockerError("create volume", name, err)
		},
	}, nil
}

func (api *API) removeRequest(name string, options RemoveOptions) (backend.CommandRequest, error) {
	name, err := requireVolumeName("remove volume", name)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	return backend.CommandRequest{
		OperationID: "volume.remove", Operation: "remove volume",
		Affected:    []backend.AffectedResource{{Kind: "volume", ID: name}},
		RefreshKeys: volumeMutationRefreshKeys(),
		Run: func(ctx context.Context) error {
			_, err := api.docker.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: options.Force})
			return classifyDockerError("remove volume", name, err)
		},
	}, nil
}

func (api *API) pruneRequest(options PruneOptions) (backend.CommandRequest, error) {
	labels, err := cleanMap("prune volumes", "label filter", options.Labels, true)
	if err != nil || len(labels) == 0 {
		if err != nil {
			return backend.CommandRequest{}, err
		}
		return backend.CommandRequest{}, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "prune volumes", Resource: "label filters"}
	}
	filters := make(client.Filters)
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := labels[key]
		if value == "" {
			filters.Add("label", key)
		} else {
			filters.Add("label", key+"="+value)
		}
	}
	return backend.CommandRequest{
		OperationID: "volume.prune", Operation: "prune volumes",
		Affected:    []backend.AffectedResource{{Kind: "volumes", ID: "filtered"}},
		RefreshKeys: volumeMutationRefreshKeys(),
		Run: func(ctx context.Context) error {
			_, err := api.docker.VolumePrune(ctx, client.VolumePruneOptions{All: options.All, Filters: filters.Clone()})
			return classifyDockerError("prune volumes", "filtered", err)
		},
	}, nil
}

func volumeMutationRefreshKeys() []backend.RefreshKey {
	return []backend.RefreshKey{{Kind: RefreshKindList}, {Kind: containers.RefreshKindList}, {Kind: dashboard.RefreshKindSummary}}
}

func cleanMap(operation, resource string, values map[string]string, allowEmptyValue bool) (map[string]string, error) {
	if values == nil {
		return nil, nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || (!allowEmptyValue && value == "") {
			return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: operation, Resource: resource}
		}
		result[key] = value
	}
	return result, nil
}
