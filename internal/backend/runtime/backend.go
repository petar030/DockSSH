package runtime

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/eventhub"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/refresh"
)

type Config struct {
	Clock             backend.Clock
	EventHub          *eventhub.Hub
	Refreshes         *refresh.Coordinator
	RefreshPolicies   []backend.RefreshPolicy
	DockerEvents      DockerEventSource
	DockerEventRetry  time.Duration
	OwnedDockerClient io.Closer
}

// Backend is the process-wide implementation used by every TUI session. It
// routes direct calls and Docker events without retaining Docker resource data.
type Backend struct {
	events    *eventhub.Hub
	refreshes *refresh.Coordinator
	scheduler *refresh.Scheduler
	owner     io.Closer

	runCancel context.CancelFunc
	runDone   chan error
	eventDone chan error
	eventWG   sync.WaitGroup
	closeOnce sync.Once
	closeDone chan struct{}
	closeMu   sync.Mutex
	closeErr  error
}

// New constructs the Backend runtime and starts its scheduler and optional
// process-wide Docker event listener.
func New(ctx context.Context, config Config) (*Backend, error) {
	if ctx == nil || config.EventHub == nil || config.Refreshes == nil {
		closeRuntimeDependencies(config.EventHub, config.Refreshes)
		closeOwned(config.OwnedDockerClient)
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create backend runtime"}
	}
	if err := ctx.Err(); err != nil {
		closeRuntimeDependencies(config.EventHub, config.Refreshes)
		closeOwned(config.OwnedDockerClient)
		return nil, canceledError("create backend runtime", err)
	}
	if config.Clock == nil {
		config.Clock = backend.NewRealClock()
	}
	scheduler, err := refresh.NewScheduler(config.Clock, config.Refreshes, config.RefreshPolicies)
	if err != nil {
		_ = config.Refreshes.Shutdown(context.Background())
		_ = config.EventHub.Close()
		closeOwned(config.OwnedDockerClient)
		return nil, err
	}

	application := &Backend{
		events: config.EventHub, refreshes: config.Refreshes, scheduler: scheduler,
		owner: config.OwnedDockerClient, runDone: make(chan error, 1),
		closeDone: make(chan struct{}),
	}
	runContext, runCancel := context.WithCancel(context.Background())
	application.runCancel = runCancel
	go func() { application.runDone <- scheduler.Run(runContext) }()

	if config.DockerEvents != nil {
		listener, listenerErr := newDockerEventListener(
			config.DockerEvents, config.Clock, config.DockerEventRetry, application.handleDockerEvent,
		)
		if listenerErr != nil {
			runCancel()
			<-application.runDone
			_ = config.Refreshes.Shutdown(context.Background())
			_ = config.EventHub.Close()
			closeOwned(config.OwnedDockerClient)
			return nil, listenerErr
		}
		application.eventDone = make(chan error, 1)
		go func() { application.eventDone <- listener.Run(runContext) }()
	}
	return application, nil
}

func (application *Backend) Refresh(ctx context.Context, page backend.Page) error {
	return application.refreshes.RefreshPage(ctx, page, backend.RefreshManual)
}

func (application *Backend) Subscribe(
	ctx context.Context,
	page backend.Page,
	filter backend.EventFilter,
) (backend.Subscription, error) {
	return application.events.Subscribe(ctx, page, filter)
}

func (application *Backend) handleDockerEvent(ctx context.Context, event backend.DockerEventObserved) {
	if _, err := application.events.RecordDockerEvent(event); err != nil {
		return
	}
	for _, page := range PagesAffectedByDockerEvent(event) {
		page := page
		application.eventWG.Add(1)
		go func() {
			defer application.eventWG.Done()
			_ = application.refreshes.RefreshPage(ctx, page, backend.RefreshDockerEvent)
		}()
	}
}

// Close begins shutdown once and lets every caller wait with its own context.
func (application *Backend) Close(ctx context.Context) error {
	if ctx == nil {
		return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "close backend"}
	}
	application.closeOnce.Do(func() { go application.shutdown() })
	select {
	case <-application.closeDone:
		application.closeMu.Lock()
		defer application.closeMu.Unlock()
		return application.closeErr
	case <-ctx.Done():
		return canceledError("close backend", ctx.Err())
	}
}

func (application *Backend) shutdown() {
	application.runCancel()
	schedulerErr := <-application.runDone
	var listenerErr error
	if application.eventDone != nil {
		listenerErr = <-application.eventDone
	}
	application.eventWG.Wait()
	refreshErr := application.refreshes.Shutdown(context.Background())
	eventErr := application.events.Close()
	var ownerErr error
	if application.owner != nil {
		ownerErr = application.owner.Close()
	}
	application.closeMu.Lock()
	application.closeErr = errors.Join(schedulerErr, listenerErr, refreshErr, eventErr, ownerErr)
	application.closeMu.Unlock()
	close(application.closeDone)
}

func canceledError(operation string, err error) error {
	return &backend.AppError{Code: backend.ErrorCanceled, Operation: operation, Err: err}
}

func closeOwned(owner io.Closer) {
	if owner != nil {
		_ = owner.Close()
	}
}

func closeRuntimeDependencies(events *eventhub.Hub, refreshes *refresh.Coordinator) {
	if refreshes != nil {
		_ = refreshes.Shutdown(context.Background())
	}
	if events != nil {
		_ = events.Close()
	}
}

var _ backend.Backend = (*Backend)(nil)
