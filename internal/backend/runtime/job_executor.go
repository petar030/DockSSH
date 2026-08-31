package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const defaultActiveJobs = 4

type JobExecutorConfig struct {
	Refreshes backend.RefreshRequester
	Publisher jobPublisher
	Clock     backend.Clock
	Capacity  int
}

type jobPublisher interface {
	Publish(backend.Page, backend.EventEnvelope) (backend.EventEnvelope, error)
}

// JobExecutor owns accepted long-running operations. It deliberately has no
// queue: jobs start immediately when capacity is available, otherwise the API
// returns busy/conflict and the user can retry.
type JobExecutor struct {
	refreshes backend.RefreshRequester
	publisher jobPublisher
	clock     backend.Clock
	capacity  int
	ctx       context.Context
	cancel    context.CancelFunc

	mu       sync.Mutex
	closed   bool
	nextID   uint64
	active   map[string]*runningJob
	conflict map[string]string
	workers  sync.WaitGroup
	closeOne sync.Once
	done     chan struct{}
}

type jobOutcome struct {
	result backend.JobResult
	err    error
}

type runningJob struct {
	id          string
	executor    *JobExecutor
	page        backend.Page
	operationID string
	cancel      context.CancelFunc
	progress    chan backend.ProgressEvent
	done        chan struct{}

	mu         sync.Mutex
	outcome    jobOutcome
	sequence   uint64
	cancelOnce sync.Once
}

func NewJobExecutor(config JobExecutorConfig) (*JobExecutor, error) {
	if config.Refreshes == nil || config.Publisher == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Job Executor"}
	}
	if config.Clock == nil {
		config.Clock = backend.NewRealClock()
	}
	if config.Capacity == 0 {
		config.Capacity = defaultActiveJobs
	}
	if config.Capacity < 0 {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Job Executor"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &JobExecutor{
		refreshes: config.Refreshes, publisher: config.Publisher, clock: config.Clock, capacity: config.Capacity,
		ctx: ctx, cancel: cancel, active: make(map[string]*runningJob),
		conflict: make(map[string]string), done: make(chan struct{}),
	}, nil
}

func (executor *JobExecutor) Start(submitCtx context.Context, request backend.JobRequest) (backend.Job, error) {
	if submitCtx == nil || request.Run == nil || !request.Page.Valid() || request.OperationID == "" || request.Operation == "" || len(request.Affected) == 0 {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "submit job"}
	}
	if err := submitCtx.Err(); err != nil {
		return nil, commandWaitError(request.Operation, err)
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.closed {
		return nil, &backend.AppError{Code: backend.ErrorStreamClosed, Operation: request.Operation}
	}
	if len(executor.active) >= executor.capacity {
		return nil, &backend.AppError{Code: backend.ErrorConflict, Operation: "start job", Resource: request.OperationID}
	}
	if request.ConflictKey != "" {
		if activeID := executor.conflict[request.ConflictKey]; activeID != "" {
			return nil, &backend.AppError{Code: backend.ErrorConflict, Operation: request.Operation, Resource: request.ConflictKey, ID: activeID}
		}
	}
	executor.nextID++
	id := fmt.Sprintf("%s-%d", request.OperationID, executor.nextID)
	jobCtx, cancel := context.WithCancel(executor.ctx)
	job := &runningJob{
		id: id, executor: executor, page: request.Page, operationID: request.OperationID, cancel: cancel,
		progress: make(chan backend.ProgressEvent, 32), done: make(chan struct{}),
	}
	executor.active[id] = job
	if request.ConflictKey != "" {
		executor.conflict[request.ConflictKey] = id
	}
	executor.workers.Add(1)
	go executor.execute(jobCtx, job, cloneJobRequest(request))
	return job, nil
}

func (executor *JobExecutor) execute(ctx context.Context, job *runningJob, request backend.JobRequest) {
	defer executor.workers.Done()
	job.report(backend.ProgressEvent{Status: "started", Message: request.Operation})
	err := request.Run(ctx, job.report)
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	status := "completed"
	if err != nil {
		status = "failed"
		if errors.Is(err, context.Canceled) {
			status = "canceled"
		}
	}
	job.report(backend.ProgressEvent{Status: status, Message: request.Operation})
	var refreshErr error
	for _, key := range request.RefreshKeys {
		refreshErr = errors.Join(refreshErr, executor.refreshes.Request(key, backend.RefreshJobCompleted))
	}
	job.mu.Lock()
	job.outcome = jobOutcome{
		result: backend.JobResult{
			JobID: job.id, Affected: append([]backend.AffectedResource(nil), request.Affected...),
			RefreshKeys: append([]backend.RefreshKey(nil), request.RefreshKeys...), CompletedAt: executor.clock.Now(),
		},
		err: errors.Join(classifyJobError(request.Operation, err), refreshErr),
	}
	job.mu.Unlock()
	_, publishErr := executor.publisher.Publish(request.Page, backend.EventEnvelope{Payload: backend.JobFinished{
		OperationID: request.OperationID, Result: job.outcome.result, Err: job.outcome.err,
	}})
	if publishErr != nil {
		job.mu.Lock()
		job.outcome.err = errors.Join(job.outcome.err, publishErr)
		job.mu.Unlock()
	}
	executor.mu.Lock()
	delete(executor.active, job.id)
	if request.ConflictKey != "" && executor.conflict[request.ConflictKey] == job.id {
		delete(executor.conflict, request.ConflictKey)
	}
	executor.mu.Unlock()
	close(job.progress)
	close(job.done)
}

func (executor *JobExecutor) Close(ctx context.Context) error {
	if ctx == nil {
		return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "close Job Executor"}
	}
	executor.closeOne.Do(func() {
		executor.mu.Lock()
		executor.closed = true
		executor.cancel()
		executor.mu.Unlock()
		go func() {
			executor.workers.Wait()
			close(executor.done)
		}()
	})
	select {
	case <-executor.done:
		return nil
	case <-ctx.Done():
		return commandWaitError("close Job Executor", ctx.Err())
	}
}

func (job *runningJob) ID() string { return job.id }

func (job *runningJob) Progress() <-chan backend.ProgressEvent { return job.progress }

func (job *runningJob) Wait(ctx context.Context) (backend.JobResult, error) {
	if ctx == nil {
		return backend.JobResult{}, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "wait for job"}
	}
	select {
	case <-job.done:
		job.mu.Lock()
		defer job.mu.Unlock()
		return job.outcome.result, job.outcome.err
	case <-ctx.Done():
		return backend.JobResult{}, commandWaitError("wait for job", ctx.Err())
	}
}

func (job *runningJob) Cancel() error {
	job.cancelOnce.Do(job.cancel)
	return nil
}

func (job *runningJob) report(event backend.ProgressEvent) {
	job.mu.Lock()
	job.sequence++
	event.Sequence = job.sequence
	if event.Time.IsZero() {
		event.Time = job.executor.clock.Now()
	}
	job.mu.Unlock()
	select {
	case job.progress <- event:
	default:
	}
	_, _ = job.executor.publisher.Publish(job.page, backend.EventEnvelope{Payload: backend.JobProgressed{
		JobID: job.id, OperationID: job.operationID, Progress: event,
	}})
}

func cloneJobRequest(request backend.JobRequest) backend.JobRequest {
	request.Affected = append([]backend.AffectedResource(nil), request.Affected...)
	request.RefreshKeys = append([]backend.RefreshKey(nil), request.RefreshKeys...)
	return request
}

func classifyJobError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var applicationError *backend.AppError
	if errors.As(err, &applicationError) {
		return err
	}
	code := backend.ErrorInternal
	switch {
	case errors.Is(err, context.Canceled):
		code = backend.ErrorCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = backend.ErrorTimeout
	}
	return &backend.AppError{Code: code, Operation: operation, Err: err}
}

var _ backend.JobRunner = (*JobExecutor)(nil)
var _ backend.Job = (*runningJob)(nil)
