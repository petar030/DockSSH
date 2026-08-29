// Package containers implements the TUI-facing Containers page API.
package containers

import (
	"context"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// API is the Containers page facade used by TUI sessions. It owns no lifecycle
// and stores no Docker resource state. Its methods either submit short commands,
// request a typed page update, or open a session-owned stream.
type API struct {
	docker    dockerClient
	commands  backend.CommandRunner
	refreshes backend.RefreshRequester
}

func NewAPI(docker dockerClient, commands backend.CommandRunner, refreshes backend.RefreshRequester) (*API, error) {
	if docker == nil || commands == nil || refreshes == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Containers API"}
	}
	return &API{docker: docker, commands: commands, refreshes: refreshes}, nil
}

// RequestDetails requests a typed details update on the Containers page bus.
func (api *API) RequestDetails(id string) error {
	id, err := requireContainerID("request container details", id)
	if err != nil {
		return err
	}
	return api.refreshes.Request(backend.RefreshKey{Kind: RefreshKindDetails, ID: id}, backend.RefreshManual)
}

// RequestProcesses requests a typed processes update on the Containers page bus.
func (api *API) RequestProcesses(id string) error {
	id, err := requireContainerID("request container processes", id)
	if err != nil {
		return err
	}
	return api.refreshes.Request(backend.RefreshKey{Kind: RefreshKindProcesses, ID: id}, backend.RefreshManual)
}

// Start submits a short container-start command to the backend command executor.
func (api *API) Start(ctx context.Context, id string) (backend.CommandResult, error) {
	request, err := api.startRequest(id)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// Stop submits a short container-stop command to the backend command executor.
func (api *API) Stop(ctx context.Context, id string, options StopOptions) (backend.CommandResult, error) {
	request, err := api.stopRequest(id, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// Restart submits a short container-restart command to the backend command executor.
func (api *API) Restart(ctx context.Context, id string, options RestartOptions) (backend.CommandResult, error) {
	request, err := api.restartRequest(id, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// Pause submits a short container-pause command to the backend command executor.
func (api *API) Pause(ctx context.Context, id string) (backend.CommandResult, error) {
	request, err := api.pauseRequest(id)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// Unpause submits a short container-unpause command to the backend command executor.
func (api *API) Unpause(ctx context.Context, id string) (backend.CommandResult, error) {
	request, err := api.unpauseRequest(id)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// Kill submits a short container-kill command to the backend command executor.
func (api *API) Kill(ctx context.Context, id string, options KillOptions) (backend.CommandResult, error) {
	request, err := api.killRequest(id, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// Rename submits a short container-rename command to the backend command executor.
func (api *API) Rename(ctx context.Context, id string, options RenameOptions) (backend.CommandResult, error) {
	request, err := api.renameRequest(id, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// Remove submits a short container-remove command to the backend command executor.
func (api *API) Remove(ctx context.Context, id string, options RemoveOptions) (backend.CommandResult, error) {
	request, err := api.removeRequest(id, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// Logs opens a session-owned container-log stream.
func (api *API) Logs(ctx context.Context, id string, options LogsOptions) (backend.Stream[LogEntry], error) {
	return api.logs(ctx, id, options)
}

// Stats opens a session-owned container-statistics stream.
func (api *API) Stats(ctx context.Context, id string, options StatsOptions) (backend.Stream[StatsSample], error) {
	return api.stats(ctx, id, options)
}
