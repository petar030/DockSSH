package images

import (
	"context"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// API is the Images page facade used by TUI sessions. It owns no lifecycle or
// Docker resource state.
type API struct {
	docker    imageClient
	commands  backend.CommandRunner
	jobs      backend.JobRunner
	refreshes backend.RefreshRequester
}

func NewAPI(docker imageClient, commands backend.CommandRunner, jobs backend.JobRunner, refreshes backend.RefreshRequester) (*API, error) {
	if docker == nil || commands == nil || jobs == nil || refreshes == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Images API"}
	}
	return &API{docker: docker, commands: commands, jobs: jobs, refreshes: refreshes}, nil
}

func (api *API) RequestDetails(id string) error {
	id, err := requireImageID("request image details", id)
	if err != nil {
		return err
	}
	return api.refreshes.Request(backend.RefreshKey{Kind: RefreshKindDetails, ID: id}, backend.RefreshManual)
}

func (api *API) RequestHistory(id string) error {
	id, err := requireImageID("request image history", id)
	if err != nil {
		return err
	}
	return api.refreshes.Request(backend.RefreshKey{Kind: RefreshKindHistory, ID: id}, backend.RefreshManual)
}

func (api *API) Tag(ctx context.Context, id string, options TagOptions) (backend.CommandResult, error) {
	request, err := api.tagRequest(id, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

func (api *API) Remove(ctx context.Context, id string, options RemoveOptions) (backend.CommandResult, error) {
	request, err := api.removeRequest(id, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

func (api *API) Prune(ctx context.Context, options PruneOptions) (backend.CommandResult, error) {
	request, err := api.pruneRequest(options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

func (api *API) Pull(ctx context.Context, reference string, options PullOptions) (backend.Job, error) {
	request, err := api.pullJob(reference, options)
	if err != nil {
		return nil, err
	}
	return api.jobs.Start(ctx, request)
}
