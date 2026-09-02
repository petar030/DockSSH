package runtime

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	composepage "github.com/petar030/ssh-native-docker-tui/internal/backend/compose"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/eventhub"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/images"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/networks"
	systempage "github.com/petar030/ssh-native-docker-tui/internal/backend/system"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/volumes"
)

type Config struct {
	Clock             backend.Clock
	EventHub          *eventhub.Hub
	Refreshes         *RefreshManager
	Commands          *CommandExecutor
	Jobs              *JobExecutor
	RefreshPolicies   []backend.RefreshPolicy
	DockerEvents      DockerEventSource
	DockerEventRetry  time.Duration
	OwnedDockerClient io.Closer
	Containers        *containers.API
	Compose           *composepage.API
	Images            *images.API
	Volumes           *volumes.API
	Networks          *networks.API
	System            *systempage.API
}

// Backend is the process-wide facade shared by every TUI session. It routes
// requests into backend-owned workers and stores no Docker resource snapshots.
type Backend struct {
	events     *eventhub.Hub
	refreshes  *RefreshManager
	commands   *CommandExecutor
	jobs       *JobExecutor
	scheduler  *Scheduler
	owner      io.Closer
	containers *containers.API
	compose    *composepage.API
	images     *images.API
	volumes    *volumes.API
	networks   *networks.API
	system     *systempage.API

	runCancel context.CancelFunc
	runDone   chan error
	eventDone chan error
	closeOnce sync.Once
	closeDone chan struct{}
	closeMu   sync.Mutex
	closeErr  error
}

// New constructs the runtime and starts the scheduler and optional process-wide
// Docker event listener. Accepted work is independent of the construction
// caller's context and ends only during Backend.Close.
func New(ctx context.Context, config Config) (*Backend, error) {
	if ctx == nil || config.EventHub == nil || config.Refreshes == nil || config.Commands == nil {
		closeRuntimeDependencies(config.Commands, config.Jobs, config.Refreshes, config.EventHub)
		closeOwned(config.OwnedDockerClient)
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create backend runtime"}
	}
	if err := ctx.Err(); err != nil {
		closeRuntimeDependencies(config.Commands, config.Jobs, config.Refreshes, config.EventHub)
		closeOwned(config.OwnedDockerClient)
		return nil, canceledError("create backend runtime", err)
	}
	if config.Clock == nil {
		config.Clock = backend.NewRealClock()
	}
	scheduler, err := NewScheduler(config.Clock, config.Refreshes, config.RefreshPolicies)
	if err != nil {
		closeRuntimeDependencies(config.Commands, config.Jobs, config.Refreshes, config.EventHub)
		closeOwned(config.OwnedDockerClient)
		return nil, err
	}

	application := &Backend{
		events: config.EventHub, refreshes: config.Refreshes, commands: config.Commands, jobs: config.Jobs,
		scheduler: scheduler, owner: config.OwnedDockerClient, containers: config.Containers, compose: config.Compose,
		images: config.Images, volumes: config.Volumes,
		networks: config.Networks,
		system:   config.System,
		runDone:  make(chan error, 1), closeDone: make(chan struct{}),
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
			closeRuntimeDependencies(config.Commands, config.Jobs, config.Refreshes, config.EventHub)
			closeOwned(config.OwnedDockerClient)
			return nil, listenerErr
		}
		application.eventDone = make(chan error, 1)
		go func() { application.eventDone <- listener.Run(runContext) }()
	}
	return application, nil
}

// RequestRefresh accepts an asynchronous complete refresh for one page. The
// typed result or failure is delivered through that page's Event Bus.
func (application *Backend) RequestRefresh(page backend.Page) error {
	return application.refreshes.RequestPage(page, backend.RefreshManual)
}

func (application *Backend) Subscribe(
	ctx context.Context,
	page backend.Page,
	filter backend.EventFilter,
) (backend.Subscription, error) {
	return application.events.Subscribe(ctx, page, filter)
}

func (application *Backend) Containers() *containers.API {
	return application.containers
}

func (application *Backend) Compose() *composepage.API {
	return application.compose
}

func (application *Backend) Images() *images.API {
	return application.images
}

func (application *Backend) Volumes() *volumes.API {
	return application.volumes
}

func (application *Backend) Networks() *networks.API {
	return application.networks
}

func (application *Backend) System() *systempage.API {
	return application.system
}

func (application *Backend) handleDockerEvent(_ context.Context, event backend.DockerEventObserved) {
	if _, err := application.events.RecordDockerEvent(event); err != nil {
		return
	}
	for _, page := range PagesAffectedByDockerEvent(event) {
		_ = application.refreshes.RequestPage(page, backend.RefreshDockerEvent)
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
	commandErr := application.commands.Close(context.Background())
	var jobErr error
	if application.jobs != nil {
		jobErr = application.jobs.Close(context.Background())
	}
	refreshErr := application.refreshes.Close(context.Background())
	eventErr := application.events.Close()
	var ownerErr error
	if application.owner != nil {
		ownerErr = application.owner.Close()
	}
	application.closeMu.Lock()
	application.closeErr = errors.Join(schedulerErr, listenerErr, commandErr, jobErr, refreshErr, eventErr, ownerErr)
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

func closeRuntimeDependencies(commands *CommandExecutor, jobs *JobExecutor, refreshes *RefreshManager, events *eventhub.Hub) {
	if commands != nil {
		_ = commands.Close(context.Background())
	}
	if jobs != nil {
		_ = jobs.Close(context.Background())
	}
	if refreshes != nil {
		_ = refreshes.Close(context.Background())
	}
	if events != nil {
		_ = events.Close()
	}
}

var _ backend.Backend = (*Backend)(nil)
