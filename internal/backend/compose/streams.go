package compose

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"

	composeapi "github.com/docker/compose/v5/pkg/api"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func (api *API) logs(ctx context.Context, projectName string, options LogsOptions) (backend.Stream[LogEntry], error) {
	projectName, err := requireProjectName("open Compose logs", projectName)
	if err != nil {
		return nil, err
	}
	if ctx == nil || options.Tail < 0 {
		return nil, &backend.AppError{
			Code: backend.ErrorInvalidInput, Operation: "open Compose logs", Resource: "compose project", ID: projectName,
		}
	}
	dockerOptions := composeapi.LogOptions{
		Services: cleanServices(options.Services), Follow: options.Follow, Timestamps: options.Timestamps,
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
	return newLogStream(ctx, func(streamCtx context.Context, values chan<- LogEntry) error {
		consumer := &logConsumer{ctx: streamCtx, values: values}
		return classifyComposeError(
			"stream Compose logs", projectName,
			api.compose.Logs(streamCtx, projectName, consumer, dockerOptions),
		)
	}), nil
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

type logConsumer struct {
	ctx    context.Context
	values chan<- LogEntry
}

func (consumer *logConsumer) Log(containerName, message string) {
	consumer.send(LogEntry{Container: containerName, Source: LogStdout, Data: composeLogLine(message)})
}

func (consumer *logConsumer) Err(containerName, message string) {
	consumer.send(LogEntry{Container: containerName, Source: LogStderr, Data: composeLogLine(message)})
}

func (consumer *logConsumer) Status(containerName, message string) {
	consumer.send(LogEntry{Container: containerName, Source: LogStatus, Data: composeLogLine(message)})
}

// Compose's LogConsumer receives complete lines with the terminating newline
// removed. Restore it so downstream stream consumers can use the same
// chunk/line handling as container logs and display followed output promptly.
func composeLogLine(message string) string {
	if strings.HasSuffix(message, "\n") {
		return message
	}
	return message + "\n"
}

func (consumer *logConsumer) send(entry LogEntry) {
	select {
	case consumer.values <- entry:
	case <-consumer.ctx.Done():
	}
}

type logStream struct {
	values    chan LogEntry
	done      chan error
	cancel    context.CancelFunc
	closeOnce sync.Once
}

func newLogStream(ctx context.Context, run func(context.Context, chan<- LogEntry) error) *logStream {
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &logStream{
		values: make(chan LogEntry, 32), done: make(chan error, 1), cancel: cancel,
	}
	go func() {
		err := run(streamCtx, stream.values)
		if errors.Is(err, context.Canceled) || streamCtx.Err() != nil {
			err = nil
		}
		close(stream.values)
		stream.done <- err
		close(stream.done)
	}()
	return stream
}

func (stream *logStream) Values() <-chan LogEntry { return stream.values }

func (stream *logStream) Done() <-chan error { return stream.done }

func (stream *logStream) Close() error {
	stream.closeOnce.Do(stream.cancel)
	return nil
}

var _ composeapi.LogConsumer = (*logConsumer)(nil)
var _ backend.Stream[LogEntry] = (*logStream)(nil)
