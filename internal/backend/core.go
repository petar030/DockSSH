package backend

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

var allPages = [...]Page{
	PageDashboard,
	PageContainers,
	PageCompose,
	PageImages,
	PageVolumes,
	PageNetworks,
	PageEvents,
	PageSystem,
}

// CoreConfig wires shared backend infrastructure without depending on a
// concrete Docker client implementation.
type CoreConfig struct {
	Clock               Clock
	EventBufferCapacity int
	EventBuffer         *EventBuffer
	DockerEvents        DockerEventSource
	DockerEventRetry    time.Duration
	Loaders             *LoaderRegistry
	RefreshPolicies     []RefreshPolicy
	OwnedDockerClient   io.Closer
}

// Core coordinates refreshes and observations for all sessions. It does not
// retain Docker resource snapshots.
type Core struct {
	buses       map[Page]*EventBus
	eventBuffer *EventBuffer
	coordinator *RefreshCoordinator
	scheduler   *RefreshScheduler
	owner       io.Closer

	runCancel context.CancelFunc
	runDone   chan error
	eventDone chan error
	eventWG   sync.WaitGroup
	closeOnce sync.Once
	closeDone chan struct{}
	closeMu   sync.Mutex
	closeErr  error
}

// NewCore constructs the shared observer foundation and starts its one refresh
// scheduler. Sessions subscribe and request their initial data after creation.
func NewCore(ctx context.Context, config CoreConfig) (*Core, error) {
	if ctx == nil || config.Loaders == nil {
		closeOwned(config.OwnedDockerClient)
		return nil, &AppError{Code: ErrorInvalidInput, Operation: "create core backend"}
	}
	if err := ctx.Err(); err != nil {
		closeOwned(config.OwnedDockerClient)
		return nil, canceledError("create core backend", RefreshKey{}, err)
	}
	if config.Clock == nil {
		config.Clock = realClock{}
	}
	if config.EventBuffer == nil {
		config.EventBuffer = NewEventBuffer(config.EventBufferCapacity)
	}
	buses := make(map[Page]*EventBus, len(allPages))
	for _, page := range allPages {
		buses[page] = NewEventBus(EventBusConfig{Clock: config.Clock})
	}
	coordinator, err := NewRefreshCoordinator(RefreshCoordinatorConfig{
		Buses:   buses,
		Loaders: config.Loaders,
	})
	if err != nil {
		closePageBuses(buses)
		closeOwned(config.OwnedDockerClient)
		return nil, err
	}
	scheduler, err := NewRefreshScheduler(config.Clock, coordinator, config.RefreshPolicies)
	if err != nil {
		_ = coordinator.Shutdown(context.Background())
		closePageBuses(buses)
		closeOwned(config.OwnedDockerClient)
		return nil, err
	}

	core := &Core{
		buses:       buses,
		eventBuffer: config.EventBuffer,
		coordinator: coordinator,
		scheduler:   scheduler,
		owner:       config.OwnedDockerClient,
		runDone:     make(chan error, 1),
		closeDone:   make(chan struct{}),
	}
	runContext, runCancel := context.WithCancel(context.Background())
	core.runCancel = runCancel
	go func() { core.runDone <- scheduler.Run(runContext) }()
	if config.DockerEvents != nil {
		listener, listenerErr := NewDockerEventListener(
			config.DockerEvents, config.Clock, config.DockerEventRetry, core.handleDockerEvent,
		)
		if listenerErr != nil {
			runCancel()
			<-core.runDone
			_ = coordinator.Shutdown(context.Background())
			closePageBuses(buses)
			closeOwned(config.OwnedDockerClient)
			return nil, listenerErr
		}
		core.eventDone = make(chan error, 1)
		go func() { core.eventDone <- listener.Run(runContext) }()
	}
	return core, nil
}

// Refresh performs the complete authoritative refresh configured for one page.
// TUI sessions do not need to know internal refresh keys or trigger reasons.
func (core *Core) Refresh(ctx context.Context, page Page) error {
	return core.coordinator.RefreshPage(ctx, page)
}

func (core *Core) Subscribe(ctx context.Context, page Page, filter EventFilter) (Subscription, error) {
	bus, ok := core.buses[page]
	if !ok {
		return nil, &AppError{Code: ErrorInvalidInput, Operation: "subscribe to page", Resource: string(page)}
	}
	return bus.Subscribe(ctx, filter)
}

// ObserveDockerEvent publishes and buffers one already-normalized daemon event.
func (core *Core) ObserveDockerEvent(event DockerEventObserved) (EventEnvelope, error) {
	published, err := core.buses[PageEvents].Publish(EventEnvelope{
		Time: event.OccurredAt, Reason: RefreshDockerEvent, Payload: event,
	})
	if err != nil {
		return EventEnvelope{}, err
	}
	core.eventBuffer.Add(published)
	return published, nil
}

func (core *Core) handleDockerEvent(ctx context.Context, event DockerEventObserved) {
	if _, err := core.ObserveDockerEvent(event); err != nil {
		return
	}
	for _, page := range PagesAffectedByDockerEvent(event) {
		page := page
		core.eventWG.Add(1)
		go func() {
			defer core.eventWG.Done()
			_ = core.coordinator.refreshPage(ctx, page, RefreshDockerEvent)
		}()
	}
}

func (core *Core) RecentDockerEvents(filter EventFilter, limit int) []EventEnvelope {
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
		return canceledError("close core backend", RefreshKey{}, ctx.Err())
	}
}

func (core *Core) shutdown() {
	core.runCancel()
	schedulerErr := <-core.runDone
	var listenerErr error
	if core.eventDone != nil {
		listenerErr = <-core.eventDone
	}
	core.eventWG.Wait()
	coordinatorErr := core.coordinator.Shutdown(context.Background())
	var eventErr error
	for _, page := range allPages {
		eventErr = errors.Join(eventErr, core.buses[page].Close())
	}
	var ownerErr error
	if core.owner != nil {
		ownerErr = core.owner.Close()
	}
	core.closeMu.Lock()
	core.closeErr = errors.Join(schedulerErr, listenerErr, coordinatorErr, eventErr, ownerErr)
	core.closeMu.Unlock()
	close(core.closeDone)
}

func closeOwned(owner io.Closer) {
	if owner != nil {
		_ = owner.Close()
	}
}

func closePageBuses(buses map[Page]*EventBus) {
	for _, bus := range buses {
		_ = bus.Close()
	}
}

var _ Backend = (*Core)(nil)
