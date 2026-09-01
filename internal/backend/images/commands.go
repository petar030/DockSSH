package images

import (
	"context"
	"sort"
	"strings"

	"github.com/distribution/reference"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

func (api *API) tagRequest(id string, options TagOptions) (backend.CommandRequest, error) {
	id, err := requireImageID("tag image", id)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	target, err := normalizeTagReference(options.Reference)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	return backend.CommandRequest{
		OperationID: "image.tag", Operation: "tag image",
		Affected:    []backend.AffectedResource{{Kind: "image", ID: id}, {Kind: "image_reference", ID: target}},
		RefreshKeys: []backend.RefreshKey{{Kind: RefreshKindList}, {Kind: RefreshKindDetails, ID: id}, {Kind: dashboard.RefreshKindSummary}},
		Run: func(ctx context.Context) error {
			_, err := api.docker.ImageTag(ctx, client.ImageTagOptions{Source: id, Target: target})
			return classifyDockerError("tag image", id, err)
		},
	}, nil
}

func (api *API) removeRequest(id string, options RemoveOptions) (backend.CommandRequest, error) {
	id, err := requireImageID("remove image", id)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	return backend.CommandRequest{
		OperationID: "image.remove", Operation: "remove image",
		Affected:    []backend.AffectedResource{{Kind: "image", ID: id}},
		RefreshKeys: []backend.RefreshKey{{Kind: RefreshKindList}, {Kind: dashboard.RefreshKindSummary}},
		Run: func(ctx context.Context) error {
			_, err := api.docker.ImageRemove(ctx, id, client.ImageRemoveOptions{Force: options.Force, PruneChildren: options.PruneChildren})
			return classifyDockerError("remove image", id, err)
		},
	}, nil
}

func (api *API) pruneRequest(options PruneOptions) (backend.CommandRequest, error) {
	filters, err := imagePruneFilters(options)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	return backend.CommandRequest{
		OperationID: "image.prune", Operation: "prune images",
		Affected:    []backend.AffectedResource{{Kind: "images", ID: "filtered"}},
		RefreshKeys: []backend.RefreshKey{{Kind: RefreshKindList}, {Kind: dashboard.RefreshKindSummary}},
		Run: func(ctx context.Context) error {
			_, err := api.docker.ImagePrune(ctx, client.ImagePruneOptions{Filters: filters.Clone()})
			return classifyDockerError("prune images", "filtered", err)
		},
	}, nil
}

func normalizeTagReference(value string) (string, error) {
	value = strings.TrimSpace(value)
	named, err := reference.ParseNormalizedNamed(value)
	if err != nil {
		return "", &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "tag image", Resource: "image reference", ID: value, Err: err}
	}
	if _, digested := named.(reference.Digested); digested {
		return "", &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "tag image", Resource: "image reference", ID: value}
	}
	return reference.FamiliarString(reference.TagNameOnly(named)), nil
}

func imagePruneFilters(options PruneOptions) (client.Filters, error) {
	filters := make(client.Filters)
	if options.Dangling != nil {
		if !*options.Dangling {
			return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "prune images", Resource: "dangling filter"}
		}
		filters.Add("dangling", "true")
	}
	if until := strings.TrimSpace(options.Until); until != "" {
		filters.Add("until", until)
	}
	labels := make([]string, 0, len(options.Labels))
	for key, value := range options.Labels {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "prune images", Resource: "label filter"}
		}
		if value == "" {
			labels = append(labels, key)
		} else {
			labels = append(labels, key+"="+value)
		}
	}
	sort.Strings(labels)
	for _, label := range labels {
		filters.Add("label", label)
	}
	if len(filters) == 0 {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "prune images", Resource: "filters"}
	}
	return filters, nil
}
