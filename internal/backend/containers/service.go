package containers

import (
	"bytes"
	"context"
	"strings"
	"sync"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

type serviceClient interface {
	ContainerStart(context.Context, string, client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerStop(context.Context, string, client.ContainerStopOptions) (client.ContainerStopResult, error)
	ContainerRestart(context.Context, string, client.ContainerRestartOptions) (client.ContainerRestartResult, error)
	ContainerPause(context.Context, string, client.ContainerPauseOptions) (client.ContainerPauseResult, error)
	ContainerUnpause(context.Context, string, client.ContainerUnpauseOptions) (client.ContainerUnpauseResult, error)
	ContainerKill(context.Context, string, client.ContainerKillOptions) (client.ContainerKillResult, error)
	ContainerRename(context.Context, string, client.ContainerRenameOptions) (client.ContainerRenameResult, error)
	ContainerRemove(context.Context, string, client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)

	ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerLogs(context.Context, string, client.ContainerLogsOptions) (client.ContainerLogsResult, error)
	ContainerStats(context.Context, string, client.ContainerStatsOptions) (client.ContainerStatsResult, error)

	ExecCreate(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttach(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error)
	ExecInspect(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error)
}

type refresher interface {
	Refresh(context.Context, backend.RefreshKey, backend.RefreshReason) (backend.RefreshResult, error)
}

// Service implements direct Container commands, targeted read requests, and
// session-owned streams using the shared Moby client.
type Service struct {
	docker          serviceClient
	refreshes       refresher
	refreshRequests backend.PageRefreshRequester

	lifecycle context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	closed    bool
	streams   sync.WaitGroup
}

func NewService(
	docker serviceClient,
	refreshes refresher,
	refreshRequests backend.PageRefreshRequester,
) (*Service, error) {
	if docker == nil || refreshes == nil || refreshRequests == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Containers service"}
	}
	lifecycle, cancel := context.WithCancel(context.Background())
	return &Service{
		docker: docker, refreshes: refreshes, refreshRequests: refreshRequests,
		lifecycle: lifecycle, cancel: cancel,
	}, nil
}

func (service *Service) RefreshDetails(ctx context.Context, id string) error {
	id, err := requireContainerID("refresh container details", id)
	if err != nil {
		return err
	}
	_, err = service.refreshes.Refresh(ctx, backend.RefreshKey{Kind: RefreshKindDetails, ID: id}, backend.RefreshManual)
	return err
}

func (service *Service) RefreshProcesses(ctx context.Context, id string) error {
	id, err := requireContainerID("refresh container processes", id)
	if err != nil {
		return err
	}
	_, err = service.refreshes.Refresh(ctx, backend.RefreshKey{Kind: RefreshKindProcesses, ID: id}, backend.RefreshManual)
	return err
}

func (service *Service) Start(ctx context.Context, id string) (backend.CommandResult, error) {
	return service.command(ctx, id, "start container", "container.start", func(id string) error {
		_, err := service.docker.ContainerStart(ctx, id, client.ContainerStartOptions{})
		return err
	})
}

func (service *Service) Stop(ctx context.Context, id string, options StopOptions) (backend.CommandResult, error) {
	return service.command(ctx, id, "stop container", "container.stop", func(id string) error {
		_, err := service.docker.ContainerStop(ctx, id, client.ContainerStopOptions{
			Signal: options.Signal, Timeout: options.TimeoutSeconds,
		})
		return err
	})
}

func (service *Service) Restart(ctx context.Context, id string, options RestartOptions) (backend.CommandResult, error) {
	return service.command(ctx, id, "restart container", "container.restart", func(id string) error {
		_, err := service.docker.ContainerRestart(ctx, id, client.ContainerRestartOptions{
			Signal: options.Signal, Timeout: options.TimeoutSeconds,
		})
		return err
	})
}

func (service *Service) Pause(ctx context.Context, id string) (backend.CommandResult, error) {
	return service.command(ctx, id, "pause container", "container.pause", func(id string) error {
		_, err := service.docker.ContainerPause(ctx, id, client.ContainerPauseOptions{})
		return err
	})
}

func (service *Service) Unpause(ctx context.Context, id string) (backend.CommandResult, error) {
	return service.command(ctx, id, "unpause container", "container.unpause", func(id string) error {
		_, err := service.docker.ContainerUnpause(ctx, id, client.ContainerUnpauseOptions{})
		return err
	})
}

func (service *Service) Kill(ctx context.Context, id string, options KillOptions) (backend.CommandResult, error) {
	return service.command(ctx, id, "kill container", "container.kill", func(id string) error {
		_, err := service.docker.ContainerKill(ctx, id, client.ContainerKillOptions{Signal: options.Signal})
		return err
	})
}

func (service *Service) Rename(ctx context.Context, id string, options RenameOptions) (backend.CommandResult, error) {
	options.Name = strings.TrimSpace(options.Name)
	if options.Name == "" || strings.TrimPrefix(options.Name, "/") == "" {
		return backend.CommandResult{}, &backend.AppError{
			Code: backend.ErrorInvalidInput, Operation: "rename container", Resource: "container", ID: id,
		}
	}
	return service.command(ctx, id, "rename container", "container.rename", func(id string) error {
		_, err := service.docker.ContainerRename(ctx, id, client.ContainerRenameOptions{NewName: options.Name})
		return err
	})
}

func (service *Service) Remove(ctx context.Context, id string, options RemoveOptions) (backend.CommandResult, error) {
	return service.command(ctx, id, "remove container", "container.remove", func(id string) error {
		_, err := service.docker.ContainerRemove(ctx, id, client.ContainerRemoveOptions{
			Force: options.Force, RemoveVolumes: options.RemoveVolumes,
		})
		return err
	})
}

func (service *Service) Exec(ctx context.Context, id string, options ExecOptions) (ExecResult, error) {
	id, err := requireContainerID("exec in container", id)
	if err != nil {
		return ExecResult{}, err
	}
	if len(options.Command) == 0 {
		return ExecResult{}, &backend.AppError{
			Code: backend.ErrorInvalidInput, Operation: "exec in container", Resource: "container", ID: id,
		}
	}
	if ctx == nil {
		return ExecResult{}, &backend.AppError{
			Code: backend.ErrorInvalidInput, Operation: "exec in container", Resource: "container", ID: id,
		}
	}
	if err := service.ensureOpen("exec in container", id); err != nil {
		return ExecResult{}, err
	}
	created, err := service.docker.ExecCreate(ctx, id, client.ExecCreateOptions{
		User: options.User, Privileged: options.Privileged, TTY: false,
		AttachStdout: true, AttachStderr: true, Env: append([]string(nil), options.Environment...),
		WorkingDir: options.WorkingDir, Cmd: append([]string(nil), options.Command...),
	})
	if err != nil {
		return ExecResult{}, classifyDockerError("create container exec", id, err)
	}
	attached, err := service.docker.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: false})
	if err != nil {
		return ExecResult{}, classifyDockerError("attach container exec", id, err)
	}
	defer attached.Close()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, attached.Reader); err != nil {
		return ExecResult{}, classifyDockerError("read container exec", id, err)
	}
	inspected, err := service.docker.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return ExecResult{}, classifyDockerError("inspect container exec", id, err)
	}
	return ExecResult{
		ExecID: created.ID, ExitCode: inspected.ExitCode,
		Stdout: stdout.String(), Stderr: stderr.String(),
	}, nil
}

func (service *Service) command(
	ctx context.Context,
	id, operation, operationID string,
	run func(string) error,
) (backend.CommandResult, error) {
	id, err := requireContainerID(operation, id)
	if err != nil {
		return backend.CommandResult{}, err
	}
	if ctx == nil {
		return backend.CommandResult{}, &backend.AppError{
			Code: backend.ErrorInvalidInput, Operation: operation, Resource: "container", ID: id,
		}
	}
	if err := service.ensureOpen(operation, id); err != nil {
		return backend.CommandResult{}, err
	}
	if err := run(id); err != nil {
		return backend.CommandResult{}, classifyDockerError(operation, id, err)
	}
	keys := commandRefreshKeys()
	result := backend.CommandResult{
		OperationID: operationID,
		Affected:    []backend.AffectedResource{{Kind: "container", ID: id}},
		RefreshKeys: keys,
	}
	//Crashes here
	service.refreshRequests.RequestPage(backend.PageContainers, backend.RefreshCommand)
	service.refreshRequests.RequestPage(backend.PageDashboard, backend.RefreshCommand)
	return result, nil
}

func (service *Service) ensureOpen(operation, id string) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	if !service.closed {
		return nil
	}
	return &backend.AppError{
		Code: backend.ErrorStreamClosed, Operation: operation, Resource: "container", ID: id,
	}
}

func commandRefreshKeys() []backend.RefreshKey {
	return []backend.RefreshKey{
		{Kind: RefreshKindList},
		{Kind: dashboard.RefreshKindSummary},
	}
}

// Close cancels all session-owned streams opened through this service and waits
// for their readers to exit before the shared Docker client is closed.
func (service *Service) Close() error {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return nil
	}
	service.closed = true
	service.cancel()
	service.mu.Unlock()
	service.streams.Wait()
	return nil
}

var _ API = (*Service)(nil)
