package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const (
	defaultCommandWorkers       = 4
	defaultCommandQueueCapacity = 32
	defaultCommandTimeout       = 30 * time.Second
)

type CommandExecutorConfig struct {
	Refreshes     backend.RefreshRequester
	Workers       int
	QueueCapacity int
	Timeout       time.Duration
}

type commandOutcome struct {
	result backend.CommandResult
	err    error
}

type queuedCommand struct {
	request backend.CommandRequest
	result  chan commandOutcome
}

// CommandExecutor owns a bounded FIFO queue and a fixed worker pool. Once a
// command is accepted, its Docker call uses the executor's backend lifetime;
// the initiating context controls only submission and waiting.
type CommandExecutor struct {
	refreshes backend.RefreshRequester
	timeout   time.Duration
	queue     chan queuedCommand
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}

	mu       sync.Mutex
	closed   bool
	workers  sync.WaitGroup
	closeOne sync.Once
}

func NewCommandExecutor(config CommandExecutorConfig) (*CommandExecutor, error) {
	if config.Refreshes == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Command Executor"}
	}
	if config.Workers == 0 {
		config.Workers = defaultCommandWorkers
	}
	if config.QueueCapacity == 0 {
		config.QueueCapacity = defaultCommandQueueCapacity
	}
	if config.Timeout == 0 {
		config.Timeout = defaultCommandTimeout
	}
	if config.Workers < 0 || config.QueueCapacity < 0 || config.Timeout < 0 {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Command Executor"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	executor := &CommandExecutor{
		refreshes: config.Refreshes,
		timeout:   config.Timeout,
		queue:     make(chan queuedCommand, config.QueueCapacity),
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	executor.workers.Add(config.Workers)
	for range config.Workers {
		go executor.worker()
	}
	return executor, nil
}

func (executor *CommandExecutor) Run(waitCtx context.Context, request backend.CommandRequest) (backend.CommandResult, error) {
	if waitCtx == nil || request.Run == nil || request.OperationID == "" || request.Operation == "" || len(request.Affected) == 0 {
		return backend.CommandResult{}, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "submit command"}
	}
	if err := waitCtx.Err(); err != nil {
		return backend.CommandResult{}, commandWaitError(request.Operation, err)
	}
	queued := queuedCommand{request: cloneCommandRequest(request), result: make(chan commandOutcome, 1)}
	executor.mu.Lock()
	if executor.closed {
		executor.mu.Unlock()
		return backend.CommandResult{}, &backend.AppError{Code: backend.ErrorStreamClosed, Operation: request.Operation}
	}
	select {
	case executor.queue <- queued:
		executor.mu.Unlock()
	case <-waitCtx.Done():
		executor.mu.Unlock()
		return backend.CommandResult{}, commandWaitError(request.Operation, waitCtx.Err())
	default:
		executor.mu.Unlock()
		return backend.CommandResult{}, &backend.AppError{Code: backend.ErrorConflict, Operation: "queue command", Resource: request.OperationID}
	}

	select {
	case outcome := <-queued.result:
		return outcome.result, outcome.err
	case <-waitCtx.Done():
		return backend.CommandResult{}, commandWaitError(request.Operation, waitCtx.Err())
	}
}

func (executor *CommandExecutor) worker() {
	defer executor.workers.Done()
	for {
		select {
		case <-executor.ctx.Done():
			return
		case queued := <-executor.queue:
			if executor.ctx.Err() != nil {
				queued.result <- commandOutcome{err: &backend.AppError{
					Code: backend.ErrorCanceled, Operation: queued.request.Operation, Err: context.Canceled,
				}}
				continue
			}
			executor.execute(queued)
		}
	}
}

func (executor *CommandExecutor) execute(queued queuedCommand) {
	operationCtx, cancel := context.WithTimeout(executor.ctx, executor.timeout)
	err := queued.request.Run(operationCtx)
	if err == nil && operationCtx.Err() != nil {
		err = commandWaitError(queued.request.Operation, operationCtx.Err())
	}
	cancel()
	var refreshErr error
	for _, key := range queued.request.RefreshKeys {
		refreshErr = errors.Join(refreshErr, executor.refreshes.Request(key, backend.RefreshCommand))
	}
	result := backend.CommandResult{
		OperationID: queued.request.OperationID,
		Affected:    append([]backend.AffectedResource(nil), queued.request.Affected...),
		RefreshKeys: append([]backend.RefreshKey(nil), queued.request.RefreshKeys...),
	}
	queued.result <- commandOutcome{result: result, err: errors.Join(err, refreshErr)}
}

// Close rejects new commands, cancels active workers, resolves queued callers
// with cancellation and waits for the fixed worker pool to stop.
func (executor *CommandExecutor) Close(ctx context.Context) error {
	if ctx == nil {
		return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "close Command Executor"}
	}
	executor.closeOne.Do(func() {
		executor.mu.Lock()
		executor.closed = true
		executor.cancel()
		executor.mu.Unlock()
		for {
			select {
			case queued := <-executor.queue:
				queued.result <- commandOutcome{err: &backend.AppError{
					Code: backend.ErrorCanceled, Operation: queued.request.Operation, Err: context.Canceled,
				}}
			default:
				go func() {
					executor.workers.Wait()
					close(executor.done)
				}()
				return
			}
		}
	})
	select {
	case <-executor.done:
		return nil
	case <-ctx.Done():
		return commandWaitError("close Command Executor", ctx.Err())
	}
}

func cloneCommandRequest(request backend.CommandRequest) backend.CommandRequest {
	request.Affected = append([]backend.AffectedResource(nil), request.Affected...)
	request.RefreshKeys = append([]backend.RefreshKey(nil), request.RefreshKeys...)
	return request
}

func commandWaitError(operation string, err error) error {
	code := backend.ErrorCanceled
	if errors.Is(err, context.DeadlineExceeded) {
		code = backend.ErrorTimeout
	}
	return &backend.AppError{Code: code, Operation: operation, Err: err}
}

var _ backend.CommandRunner = (*CommandExecutor)(nil)
