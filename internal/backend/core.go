package backend

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// CoreConfig wires shared backend infrastructure without depending on a
// concrete Docker client implementation.
type CoreConfig struct {
	Clock               Clock
	Store               *StateStore
	Events              *EventBus
	EventBufferCapacity int
	Loaders             *LoaderRegistry
	StartupScopes       []RefreshScope
	RefreshPolicies     []RefreshPolicy
	DebounceWindow      time.Duration
	OwnedDockerClient   io.Closer
}

// Core is the production shared-state backend foundation.
type Core struct {
	store       *StateStore
	events      *EventBus
	eventBuffer *EventBuffer
	coordinator *RefreshCoordinator
	scheduler   *RefreshScheduler
	owner       io.Closer

	runCancel context.CancelFunc
	runDone   chan error
	closeOnce sync.Once
	closeDone chan struct{}
	closeMu   sync.Mutex
	closeErr  error
}

// NewCore constructs the foundation, performs startup synchronization, and
// starts the process-wide refresh scheduler.
func NewCore(ctx context.Context, config CoreConfig) (*Core, error) {
	if ctx == nil || config.Loaders == nil {
		return nil, &AppError{Code: ErrorInvalidInput, Operation: "create core backend"}
	}
	if config.Clock == nil {
		config.Clock = realClock{}
	}
	if config.Store == nil {
		config.Store = NewStateStore()
	}
	if config.Events == nil {
		config.Events = NewEventBus(EventBusConfig{Clock: config.Clock})
	}
	coordinator, err := NewRefreshCoordinator(RefreshCoordinatorConfig{
		Store:          config.Store,
		Events:         config.Events,
		Loaders:        config.Loaders,
		Clock:          config.Clock,
		DebounceWindow: config.DebounceWindow,
	})
	if err != nil {
		closeOwned(config.OwnedDockerClient)
		return nil, err
	}
	scheduler, err := NewRefreshScheduler(config.Clock, coordinator, config.RefreshPolicies)
	if err != nil {
		_ = coordinator.Shutdown(context.Background())
		_ = config.Events.Close()
		closeOwned(config.OwnedDockerClient)
		return nil, err
	}

	core := &Core{
		store:       config.Store,
		events:      config.Events,
		eventBuffer: NewEventBuffer(config.EventBufferCapacity),
		coordinator: coordinator,
		scheduler:   scheduler,
		owner:       config.OwnedDockerClient,
		runDone:     make(chan error, 1),
		closeDone:   make(chan struct{}),
	}
	for _, scope := range config.StartupScopes {
		if _, err := coordinator.Refresh(ctx, scope, RefreshStartup); err != nil {
			_ = coordinator.Shutdown(context.Background())
			_ = config.Events.Close()
			closeOwned(config.OwnedDockerClient)
			return nil, err
		}
	}

	runContext, runCancel := context.WithCancel(context.Background())
	core.runCancel = runCancel
	go func() { core.runDone <- scheduler.Run(runContext) }()
	return core, nil
}

func (core *Core) Refresh(ctx context.Context, scope RefreshScope, reason RefreshReason) (RefreshResult, error) {
	return core.coordinator.Refresh(ctx, scope, reason)
}

func (core *Core) GetSnapshotVersion(ctx context.Context, scope RefreshScope) (SnapshotMeta, error) {
	if ctx == nil {
		return SnapshotMeta{}, &AppError{Code: ErrorInvalidInput, Operation: "get snapshot version", Resource: scope.Resource, ID: scope.ID}
	}
	if err := ctx.Err(); err != nil {
		return SnapshotMeta{}, canceledError("get snapshot version", scope, err)
	}
	return core.store.Meta(scope)
}

func (core *Core) SubscribeStateChanges(ctx context.Context, filter EventFilter) (Subscription, error) {
	return core.events.Subscribe(ctx, filter)
}

// ObserveDockerEvent publishes and buffers one already-normalized daemon event.
func (core *Core) ObserveDockerEvent(event AppEvent) (AppEvent, error) {
	event.Type = EventDockerObserved
	published, err := core.events.Publish(event)
	if err != nil {
		return AppEvent{}, err
	}
	core.eventBuffer.Add(published)
	return published, nil
}

func (core *Core) RecentDockerEvents(filter EventFilter, limit int) []AppEvent {
	return core.eventBuffer.Recent(filter, limit)
}

// Close begins shutdown once and lets every caller wait with its own context.
func (core *Core) Close(ctx context.Context) error {
	if ctx == nil {
		return &AppError{Code: ErrorInvalidInput, Operation: "close core backend"}
	}
	core.closeOnce.Do(func() { go core.shutdown() })
	select {
	case <-core.closeDone:
		core.closeMu.Lock()
		defer core.closeMu.Unlock()
		return core.closeErr
	case <-ctx.Done():
		return canceledError("close core backend", RefreshScope{}, ctx.Err())
	}
}

func (core *Core) shutdown() {
	core.runCancel()
	schedulerErr := <-core.runDone
	coordinatorErr := core.coordinator.Shutdown(context.Background())
	eventErr := core.events.Close()
	var ownerErr error
	if core.owner != nil {
		ownerErr = core.owner.Close()
	}
	core.closeMu.Lock()
	core.closeErr = errors.Join(schedulerErr, coordinatorErr, eventErr, ownerErr)
	core.closeMu.Unlock()
	close(core.closeDone)
}

func closeOwned(owner io.Closer) {
	if owner != nil {
		_ = owner.Close()
	}
}

var _ Backend = (*Core)(nil)
