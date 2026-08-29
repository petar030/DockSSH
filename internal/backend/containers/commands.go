package containers

import (
	"context"
	"strings"

	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

// The functions in this file build Containers-specific command requests. The
// CommandExecutor owns their queueing, context, timeout, and execution.

func (api *API) startRequest(id string) (backend.CommandRequest, error) {
	return api.commandRequest(id, "start container", "container.start", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerStart(operationCtx, id, client.ContainerStartOptions{})
		return err
	})
}

func (api *API) stopRequest(id string, options StopOptions) (backend.CommandRequest, error) {
	return api.commandRequest(id, "stop container", "container.stop", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerStop(operationCtx, id, client.ContainerStopOptions{
			Signal: options.Signal, Timeout: options.TimeoutSeconds,
		})
		return err
	})
}

func (api *API) restartRequest(id string, options RestartOptions) (backend.CommandRequest, error) {
	return api.commandRequest(id, "restart container", "container.restart", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerRestart(operationCtx, id, client.ContainerRestartOptions{
			Signal: options.Signal, Timeout: options.TimeoutSeconds,
		})
		return err
	})
}

func (api *API) pauseRequest(id string) (backend.CommandRequest, error) {
	return api.commandRequest(id, "pause container", "container.pause", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerPause(operationCtx, id, client.ContainerPauseOptions{})
		return err
	})
}

func (api *API) unpauseRequest(id string) (backend.CommandRequest, error) {
	return api.commandRequest(id, "unpause container", "container.unpause", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerUnpause(operationCtx, id, client.ContainerUnpauseOptions{})
		return err
	})
}

func (api *API) killRequest(id string, options KillOptions) (backend.CommandRequest, error) {
	return api.commandRequest(id, "kill container", "container.kill", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerKill(operationCtx, id, client.ContainerKillOptions{Signal: options.Signal})
		return err
	})
}

func (api *API) renameRequest(id string, options RenameOptions) (backend.CommandRequest, error) {
	options.Name = strings.TrimSpace(options.Name)
	if options.Name == "" || strings.TrimPrefix(options.Name, "/") == "" {
		return backend.CommandRequest{}, &backend.AppError{
			Code: backend.ErrorInvalidInput, Operation: "rename container", Resource: "container", ID: id,
		}
	}
	return api.commandRequest(id, "rename container", "container.rename", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerRename(operationCtx, id, client.ContainerRenameOptions{NewName: options.Name})
		return err
	})
}

func (api *API) removeRequest(id string, options RemoveOptions) (backend.CommandRequest, error) {
	return api.commandRequest(id, "remove container", "container.remove", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerRemove(operationCtx, id, client.ContainerRemoveOptions{
			Force: options.Force, RemoveVolumes: options.RemoveVolumes,
		})
		return err
	})
}

func (api *API) commandRequest(
	id, operation, operationID string,
	run func(context.Context, string) error,
) (backend.CommandRequest, error) {
	id, err := requireContainerID(operation, id)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	return backend.CommandRequest{
		OperationID: operationID,
		Operation:   operation,
		Affected:    []backend.AffectedResource{{Kind: "container", ID: id}},
		RefreshKeys: commandRefreshKeys(),
		Run: func(operationCtx context.Context) error {
			return classifyDockerError(operation, id, run(operationCtx, id))
		},
	}, nil
}

func commandRefreshKeys() []backend.RefreshKey {
	return []backend.RefreshKey{
		{Kind: RefreshKindList},
		{Kind: dashboard.RefreshKindSummary},
	}
}
