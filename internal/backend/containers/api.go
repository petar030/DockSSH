package containers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

type dockerClient interface {
	ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerTop(context.Context, string, client.ContainerTopOptions) (client.ContainerTopResult, error)
	ContainerStart(context.Context, string, client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerStop(context.Context, string, client.ContainerStopOptions) (client.ContainerStopResult, error)
	ContainerRestart(context.Context, string, client.ContainerRestartOptions) (client.ContainerRestartResult, error)
	ContainerPause(context.Context, string, client.ContainerPauseOptions) (client.ContainerPauseResult, error)
	ContainerUnpause(context.Context, string, client.ContainerUnpauseOptions) (client.ContainerUnpauseResult, error)
	ContainerKill(context.Context, string, client.ContainerKillOptions) (client.ContainerKillResult, error)
	ContainerRename(context.Context, string, client.ContainerRenameOptions) (client.ContainerRenameResult, error)
	ContainerRemove(context.Context, string, client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
	ContainerLogs(context.Context, string, client.ContainerLogsOptions) (client.ContainerLogsResult, error)
	ContainerStats(context.Context, string, client.ContainerStatsOptions) (client.ContainerStatsResult, error)
}

// API groups all Containers-page behavior around injected process-wide
// dependencies. It owns no lifecycle and stores no Docker resource state.
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

// Refresh requests

func (api *API) RequestDetails(id string) error {
	id, err := requireContainerID("request container details", id)
	if err != nil {
		return err
	}
	return api.refreshes.Request(backend.RefreshKey{Kind: RefreshKindDetails, ID: id}, backend.RefreshManual)
}

func (api *API) RequestProcesses(id string) error {
	id, err := requireContainerID("request container processes", id)
	if err != nil {
		return err
	}
	return api.refreshes.Request(backend.RefreshKey{Kind: RefreshKindProcesses, ID: id}, backend.RefreshManual)
}

// ReadRefresh performs the authoritative Docker read selected by key. It is
// called only by the backend-owned Refresh Manager.
func (api *API) ReadRefresh(ctx context.Context, key backend.RefreshKey) (backend.EventPayload, error) {
	switch key.Kind {
	case RefreshKindList:
		if key.ID != "" {
			return nil, unsupportedRefresh(key)
		}
		return api.readList(ctx)
	case RefreshKindDetails:
		id, err := requireContainerID("read container details", key.ID)
		if err != nil {
			return nil, err
		}
		return api.readDetails(ctx, id)
	case RefreshKindProcesses:
		id, err := requireContainerID("read container processes", key.ID)
		if err != nil {
			return nil, err
		}
		return api.readProcesses(ctx, id)
	default:
		return nil, unsupportedRefresh(key)
	}
}

func (api *API) readList(ctx context.Context) (backend.EventPayload, error) {
	result, err := api.docker.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, classifyDockerError("list containers", "", err)
	}
	values := make([]Summary, 0, len(result.Items))
	for _, item := range result.Items {
		names := append([]string(nil), item.Names...)
		for index := range names {
			names[index] = strings.TrimPrefix(names[index], "/")
		}
		ports := make([]Port, 0, len(item.Ports))
		for _, port := range item.Ports {
			ports = append(ports, Port{
				IP: addressString(port.IP), PrivatePort: port.PrivatePort,
				PublicPort: port.PublicPort, Protocol: port.Type,
			})
		}
		values = append(values, Summary{
			ID: item.ID, Names: names, Image: item.Image, ImageID: item.ImageID,
			Command: item.Command, Created: time.Unix(item.Created, 0), State: string(item.State),
			Status: item.Status, Ports: ports, Labels: cloneLabels(item.Labels),
		})
	}
	return ListUpdated{Containers: values}, nil
}

func (api *API) readDetails(ctx context.Context, id string) (backend.EventPayload, error) {
	result, err := api.docker.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, classifyDockerError("inspect container", id, err)
	}
	value := result.Container
	details := Details{
		ID: value.ID, Name: strings.TrimPrefix(value.Name, "/"), Created: parseDockerTime(value.Created),
		Image: value.Image, Path: value.Path, Args: append([]string(nil), value.Args...),
		RestartCount: value.RestartCount, Driver: value.Driver, Platform: value.Platform,
	}
	if value.Config != nil {
		details.Hostname = value.Config.Hostname
		details.User = value.Config.User
		details.WorkingDir = value.Config.WorkingDir
		details.Environment = append([]string(nil), value.Config.Env...)
		details.Labels = cloneLabels(value.Config.Labels)
	}
	if value.State != nil {
		details.State = State{
			Status: string(value.State.Status), Running: value.State.Running,
			Paused: value.State.Paused, Restarting: value.State.Restarting,
			OOMKilled: value.State.OOMKilled, Dead: value.State.Dead,
			PID: value.State.Pid, ExitCode: value.State.ExitCode, Error: value.State.Error,
			StartedAt: parseDockerTime(value.State.StartedAt), FinishedAt: parseDockerTime(value.State.FinishedAt),
		}
		if value.State.Health != nil {
			details.State.Health = string(value.State.Health.Status)
		}
	}
	for _, mount := range value.Mounts {
		details.Mounts = append(details.Mounts, Mount{
			Type: string(mount.Type), Name: mount.Name, Source: mount.Source,
			Destination: mount.Destination, Driver: mount.Driver, Mode: mount.Mode,
			ReadWrite: mount.RW, Propagation: string(mount.Propagation),
		})
	}
	if value.NetworkSettings != nil {
		for name, network := range value.NetworkSettings.Networks {
			if network == nil {
				continue
			}
			details.Networks = append(details.Networks, Network{
				Name: name, NetworkID: network.NetworkID, EndpointID: network.EndpointID,
				IPAddress: addressString(network.IPAddress), Gateway: addressString(network.Gateway),
				MACAddress: network.MacAddress.String(), IPv6Address: addressString(network.GlobalIPv6Address),
			})
		}
		sort.Slice(details.Networks, func(i, j int) bool { return details.Networks[i].Name < details.Networks[j].Name })
	}
	return DetailsUpdated{Container: details}, nil
}

func (api *API) readProcesses(ctx context.Context, id string) (backend.EventPayload, error) {
	result, err := api.docker.ContainerTop(ctx, id, client.ContainerTopOptions{})
	if err != nil {
		return nil, classifyDockerError("list container processes", id, err)
	}
	return ProcessesUpdated{
		ContainerID: id,
		Titles:      append([]string(nil), result.Titles...),
		Rows:        cloneRows(result.Processes),
	}, nil
}

// Commands

func (api *API) Start(ctx context.Context, id string) (backend.CommandResult, error) {
	return api.command(ctx, id, "start container", "container.start", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerStart(operationCtx, id, client.ContainerStartOptions{})
		return err
	})
}

func (api *API) Stop(ctx context.Context, id string, options StopOptions) (backend.CommandResult, error) {
	return api.command(ctx, id, "stop container", "container.stop", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerStop(operationCtx, id, client.ContainerStopOptions{
			Signal: options.Signal, Timeout: options.TimeoutSeconds,
		})
		return err
	})
}

func (api *API) Restart(ctx context.Context, id string, options RestartOptions) (backend.CommandResult, error) {
	return api.command(ctx, id, "restart container", "container.restart", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerRestart(operationCtx, id, client.ContainerRestartOptions{
			Signal: options.Signal, Timeout: options.TimeoutSeconds,
		})
		return err
	})
}

func (api *API) Pause(ctx context.Context, id string) (backend.CommandResult, error) {
	return api.command(ctx, id, "pause container", "container.pause", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerPause(operationCtx, id, client.ContainerPauseOptions{})
		return err
	})
}

func (api *API) Unpause(ctx context.Context, id string) (backend.CommandResult, error) {
	return api.command(ctx, id, "unpause container", "container.unpause", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerUnpause(operationCtx, id, client.ContainerUnpauseOptions{})
		return err
	})
}

func (api *API) Kill(ctx context.Context, id string, options KillOptions) (backend.CommandResult, error) {
	return api.command(ctx, id, "kill container", "container.kill", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerKill(operationCtx, id, client.ContainerKillOptions{Signal: options.Signal})
		return err
	})
}

func (api *API) Rename(ctx context.Context, id string, options RenameOptions) (backend.CommandResult, error) {
	options.Name = strings.TrimSpace(options.Name)
	if options.Name == "" || strings.TrimPrefix(options.Name, "/") == "" {
		return backend.CommandResult{}, &backend.AppError{
			Code: backend.ErrorInvalidInput, Operation: "rename container", Resource: "container", ID: id,
		}
	}
	return api.command(ctx, id, "rename container", "container.rename", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerRename(operationCtx, id, client.ContainerRenameOptions{NewName: options.Name})
		return err
	})
}

func (api *API) Remove(ctx context.Context, id string, options RemoveOptions) (backend.CommandResult, error) {
	return api.command(ctx, id, "remove container", "container.remove", func(operationCtx context.Context, id string) error {
		_, err := api.docker.ContainerRemove(operationCtx, id, client.ContainerRemoveOptions{
			Force: options.Force, RemoveVolumes: options.RemoveVolumes,
		})
		return err
	})
}

func (api *API) command(
	waitCtx context.Context,
	id, operation, operationID string,
	run func(context.Context, string) error,
) (backend.CommandResult, error) {
	id, err := requireContainerID(operation, id)
	if err != nil {
		return backend.CommandResult{}, err
	}
	keys := commandRefreshKeys()
	return api.commands.Run(waitCtx, backend.CommandRequest{
		OperationID: operationID,
		Operation:   operation,
		Affected:    []backend.AffectedResource{{Kind: "container", ID: id}},
		RefreshKeys: keys,
		Run: func(operationCtx context.Context) error {
			return classifyDockerError(operation, id, run(operationCtx, id))
		},
	})
}

func commandRefreshKeys() []backend.RefreshKey {
	return []backend.RefreshKey{
		{Kind: RefreshKindList},
		{Kind: dashboard.RefreshKindSummary},
	}
}

// Streams

func (api *API) Logs(ctx context.Context, id string, options LogsOptions) (backend.Stream[LogEntry], error) {
	id, err := requireContainerID("open container logs", id)
	if err != nil {
		return nil, err
	}
	if ctx == nil || options.Tail < 0 {
		return nil, &backend.AppError{
			Code: backend.ErrorInvalidInput, Operation: "open container logs", Resource: "container", ID: id,
		}
	}
	inspect, err := api.docker.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, classifyDockerError("inspect container log stream", id, err)
	}
	showStdout, showStderr := options.ShowStdout, options.ShowStderr
	if !showStdout && !showStderr {
		showStdout, showStderr = true, true
	}
	dockerOptions := client.ContainerLogsOptions{
		ShowStdout: showStdout, ShowStderr: showStderr, Follow: options.Follow,
		Timestamps: options.Timestamps,
	}
	if options.Tail > 0 {
		dockerOptions.Tail = strconv.Itoa(options.Tail)
	}
	if !options.Since.IsZero() {
		dockerOptions.Since = options.Since.Format(timeFormat)
	}
	if !options.Until.IsZero() {
		dockerOptions.Until = options.Until.Format(timeFormat)
	}
	reader, err := api.docker.ContainerLogs(ctx, id, dockerOptions)
	if err != nil {
		return nil, classifyDockerError("open container logs", id, err)
	}
	tty := inspect.Container.Config != nil && inspect.Container.Config.Tty
	return newManagedStream(ctx, reader, func(ctx context.Context, values chan<- LogEntry) error {
		stdout := logWriter{ctx: ctx, values: values, source: LogStdout}
		if tty {
			_, err := io.Copy(stdout, reader)
			return err
		}
		stderr := logWriter{ctx: ctx, values: values, source: LogStderr}
		_, err := stdcopy.StdCopy(stdout, stderr, reader)
		return err
	}), nil
}

func (api *API) Stats(ctx context.Context, id string, options StatsOptions) (backend.Stream[StatsSample], error) {
	id, err := requireContainerID("open container stats", id)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, &backend.AppError{
			Code: backend.ErrorInvalidInput, Operation: "open container stats", Resource: "container", ID: id,
		}
	}
	result, err := api.docker.ContainerStats(ctx, id, client.ContainerStatsOptions{
		Stream: !options.OneShot, IncludePreviousSample: options.OneShot,
	})
	if err != nil {
		return nil, classifyDockerError("open container stats", id, err)
	}
	return newManagedStream(ctx, result.Body, func(ctx context.Context, values chan<- StatsSample) error {
		decoder := json.NewDecoder(result.Body)
		var previous containertypes.StatsResponse
		for {
			var current containertypes.StatsResponse
			if err := decoder.Decode(&current); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
			sample := statsSample(current, previous)
			select {
			case values <- sample:
			case <-ctx.Done():
				return ctx.Err()
			}
			previous = current
			if options.OneShot {
				return nil
			}
		}
	}), nil
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

type logWriter struct {
	ctx    context.Context
	values chan<- LogEntry
	source LogSource
}

func (writer logWriter) Write(value []byte) (int, error) {
	entry := LogEntry{Source: writer.source, Data: string(append([]byte(nil), value...))}
	select {
	case writer.values <- entry:
		return len(value), nil
	case <-writer.ctx.Done():
		return 0, writer.ctx.Err()
	}
}

type managedStream[T any] struct {
	values     chan T
	done       chan error
	cancel     context.CancelFunc
	reader     io.Closer
	readerOnce sync.Once
	closeOnce  sync.Once
}

func newManagedStream[T any](
	ctx context.Context,
	reader io.Closer,
	read func(context.Context, chan<- T) error,
) *managedStream[T] {
	streamContext, cancel := context.WithCancel(ctx)
	stream := &managedStream[T]{
		values: make(chan T, 32), done: make(chan error, 1), cancel: cancel, reader: reader,
	}
	stopReaderClose := context.AfterFunc(streamContext, stream.closeReader)
	go func() {
		err := read(streamContext, stream.values)
		stopReaderClose()
		if streamContext.Err() != nil {
			err = nil
		}
		stream.closeReader()
		cancel()
		if errors.Is(err, context.Canceled) || errors.Is(err, io.ErrClosedPipe) {
			err = nil
		}
		close(stream.values)
		stream.done <- err
		close(stream.done)
	}()
	return stream
}

func (stream *managedStream[T]) Values() <-chan T { return stream.values }

func (stream *managedStream[T]) Done() <-chan error { return stream.done }

func (stream *managedStream[T]) Close() error {
	stream.closeOnce.Do(func() {
		stream.cancel()
		stream.closeReader()
	})
	return nil
}

func (stream *managedStream[T]) closeReader() {
	stream.readerOnce.Do(func() {
		if stream.reader != nil {
			_ = stream.reader.Close()
		}
	})
}

func unsupportedRefresh(key backend.RefreshKey) error {
	return &backend.AppError{
		Code: backend.ErrorUnsupported, Operation: "read Containers refresh",
		Resource: string(key.Kind), ID: key.ID,
	}
}

func parseDockerTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func addressString(address netip.Addr) string {
	if !address.IsValid() {
		return ""
	}
	return address.String()
}

func statsSample(current, previous containertypes.StatsResponse) StatsSample {
	if previous.CPUStats.CPUUsage.TotalUsage == 0 {
		previous = containertypes.StatsResponse{CPUStats: current.PreCPUStats}
	}
	cpuDelta := difference(current.CPUStats.CPUUsage.TotalUsage, previous.CPUStats.CPUUsage.TotalUsage)
	systemDelta := difference(current.CPUStats.SystemUsage, previous.CPUStats.SystemUsage)
	processors := uint64(current.CPUStats.OnlineCPUs)
	if processors == 0 {
		processors = uint64(len(current.CPUStats.CPUUsage.PercpuUsage))
	}
	var cpuPercent float64
	if cpuDelta > 0 && systemDelta > 0 && processors > 0 {
		cpuPercent = float64(cpuDelta) / float64(systemDelta) * float64(processors) * 100
	}
	var memoryPercent float64
	if current.MemoryStats.Limit > 0 {
		memoryPercent = float64(current.MemoryStats.Usage) / float64(current.MemoryStats.Limit) * 100
	}
	var networkRx, networkTx uint64
	for _, network := range current.Networks {
		networkRx += network.RxBytes
		networkTx += network.TxBytes
	}
	var blockRead, blockWrite uint64
	for _, entry := range current.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(entry.Op) {
		case "read":
			blockRead += entry.Value
		case "write":
			blockWrite += entry.Value
		}
	}
	return StatsSample{
		ContainerID: current.ID, Name: strings.TrimPrefix(current.Name, "/"), ReadAt: current.Read,
		CPUPercent: cpuPercent, MemoryUsage: current.MemoryStats.Usage,
		MemoryLimit: current.MemoryStats.Limit, MemoryPercent: memoryPercent,
		NetworkRx: networkRx, NetworkTx: networkTx, BlockRead: blockRead,
		BlockWrite: blockWrite, PIDs: current.PidsStats.Current,
	}
}

func difference(current, previous uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}

var _ backend.Stream[LogEntry] = (*managedStream[LogEntry])(nil)
var _ backend.Stream[StatsSample] = (*managedStream[StatsSample])(nil)
