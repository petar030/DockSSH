package containers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/moby/moby/api/pkg/stdcopy"
	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// Streams in this file are session-owned. They use the caller's view context
// and do not enter the CommandExecutor or RefreshManager.

func (api *API) logs(ctx context.Context, id string, options LogsOptions) (backend.Stream[LogEntry], error) {
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

func (api *API) stats(ctx context.Context, id string, options StatsOptions) (backend.Stream[StatsSample], error) {
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
