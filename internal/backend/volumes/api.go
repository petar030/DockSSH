package volumes

import (
	"context"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// API is the Volumes page facade used by TUI sessions. It owns no lifecycle or
// Docker resource state.
type API struct {
	docker    volumeClient
	commands  backend.CommandRunner
	refreshes backend.RefreshRequester
}

func NewAPI(docker volumeClient, commands backend.CommandRunner, refreshes backend.RefreshRequester) (*API, error) {
	if docker == nil || commands == nil || refreshes == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Volumes API"}
	}
	return &API{docker: docker, commands: commands, refreshes: refreshes}, nil
}

func (api *API) RequestDetails(name string) error {
	name, err := requireVolumeName("request volume details", name)
	if err != nil {
		return err
	}
	return api.refreshes.Request(backend.RefreshKey{Kind: RefreshKindDetails, ID: name}, backend.RefreshManual)
}

func (api *API) RequestAttachments(name string) error {
	name, err := requireVolumeName("request volume attachments", name)
	if err != nil {
		return err
	}
	return api.refreshes.Request(backend.RefreshKey{Kind: RefreshKindAttachments, ID: name}, backend.RefreshManual)
}

func (api *API) Create(ctx context.Context, options CreateOptions) (backend.CommandResult, error) {
	request, err := api.createRequest(options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

func (api *API) Remove(ctx context.Context, name string, options RemoveOptions) (backend.CommandResult, error) {
	request, err := api.removeRequest(name, options)
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
