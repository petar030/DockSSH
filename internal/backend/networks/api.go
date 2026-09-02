package networks

import (
	"context"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// API is the Networks page facade used by TUI sessions. It owns no lifecycle
// or Docker resource state.
type API struct {
	docker    networkClient
	commands  backend.CommandRunner
	refreshes backend.RefreshRequester
}

func NewAPI(docker networkClient, commands backend.CommandRunner, refreshes backend.RefreshRequester) (*API, error) {
	if docker == nil || commands == nil || refreshes == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Networks API"}
	}
	return &API{docker: docker, commands: commands, refreshes: refreshes}, nil
}

func (api *API) RequestDetails(id string) error {
	id, err := requireNetworkID("request network details", id)
	if err != nil {
		return err
	}
	return api.refreshes.Request(backend.RefreshKey{Kind: RefreshKindDetails, ID: id}, backend.RefreshManual)
}

func (api *API) RequestConnections(id string) error {
	id, err := requireNetworkID("request network connections", id)
	if err != nil {
		return err
	}
	return api.refreshes.Request(backend.RefreshKey{Kind: RefreshKindConnections, ID: id}, backend.RefreshManual)
}

func (api *API) Create(ctx context.Context, options CreateOptions) (backend.CommandResult, error) {
	request, err := api.createRequest(options)
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

func (api *API) Connect(ctx context.Context, networkID string, options ConnectOptions) (backend.CommandResult, error) {
	request, err := api.connectRequest(networkID, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

func (api *API) Disconnect(ctx context.Context, networkID string, options DisconnectOptions) (backend.CommandResult, error) {
	request, err := api.disconnectRequest(networkID, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}
