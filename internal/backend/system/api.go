package system

import (
	"context"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// API is the System page facade used by TUI sessions. Broad system prune is
// available only when bootstrap explicitly opts this instance in.
type API struct {
	docker           systemClient
	commands         backend.CommandRunner
	refreshes        backend.RefreshRequester
	allowSystemPrune bool
}

func NewAPI(docker systemClient, commands backend.CommandRunner, refreshes backend.RefreshRequester, allowSystemPrune bool) (*API, error) {
	if docker == nil || commands == nil || refreshes == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create System API"}
	}
	return &API{docker: docker, commands: commands, refreshes: refreshes, allowSystemPrune: allowSystemPrune}, nil
}

func (api *API) RequestDiskUsage() error {
	return api.refreshes.Request(backend.RefreshKey{Kind: RefreshKindDiskUsage}, backend.RefreshManual)
}

func (api *API) PruneContainers(ctx context.Context, options ContainerPruneOptions) (PruneResult, error) {
	request, reports, err := api.containerPruneRequest(options)
	return api.runPrune(ctx, request, reports, err)
}

func (api *API) PruneImages(ctx context.Context, options ImagePruneOptions) (PruneResult, error) {
	request, reports, err := api.imagePruneRequest(options)
	return api.runPrune(ctx, request, reports, err)
}

func (api *API) PruneVolumes(ctx context.Context, options VolumePruneOptions) (PruneResult, error) {
	request, reports, err := api.volumePruneRequest(options)
	return api.runPrune(ctx, request, reports, err)
}

func (api *API) PruneNetworks(ctx context.Context, options NetworkPruneOptions) (PruneResult, error) {
	request, reports, err := api.networkPruneRequest(options)
	return api.runPrune(ctx, request, reports, err)
}

func (api *API) PruneSystem(ctx context.Context, options SystemPruneOptions) (PruneResult, error) {
	request, reports, err := api.systemPruneRequest(options)
	return api.runPrune(ctx, request, reports, err)
}

func (api *API) runPrune(ctx context.Context, request backend.CommandRequest, reports <-chan PruneReport, validationErr error) (PruneResult, error) {
	if validationErr != nil {
		return PruneResult{}, validationErr
	}
	result, err := api.commands.Run(ctx, request)
	if err != nil {
		return PruneResult{Command: result}, err
	}
	return PruneResult{Command: result, Report: <-reports}, nil
}
