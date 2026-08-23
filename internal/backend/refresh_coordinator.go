package backend

import (
	"context"
	"errors"
	"sync"
	"time"
)

// SnapshotLoader reads one authoritative projection from Docker or Compose.
type SnapshotLoader interface {
	Load(context.Context, RefreshScope) (any, error)
}

type SnapshotLoaderFunc func(context.Context, RefreshScope) (any, error)

func (loader SnapshotLoaderFunc) Load(ctx context.Context, scope RefreshScope) (any, error) {
	return loader(ctx, scope)
}

type loaderKey struct {
	resource ResourceType
	view     SnapshotView
}

// LoaderRegistry resolves exact scopes first and resource/view loaders second.
// Resource/view loaders support detail scopes with dynamic IDs.
type LoaderRegistry struct {
	mu     sync.RWMutex
	exact  map[RefreshScope]SnapshotLoader
	byView map[loaderKey]SnapshotLoader
}

func NewLoaderRegistry() *LoaderRegistry {
	return &LoaderRegistry{
		exact:  make(map[RefreshScope]SnapshotLoader),
		byView: make(map[loaderKey]SnapshotLoader),
	}
}

func (registry *LoaderRegistry) Register(scope RefreshScope, loader SnapshotLoader) error {
	if err := validateScope(scope); err != nil {
		return err
	}
	if loader == nil {
		return &AppError{Code: ErrorInvalidInput, Operation: "register snapshot loader"}
	}
	registry.mu.Lock()
	registry.exact[scope] = loader
	registry.mu.Unlock()
	return nil
}

func (registry *LoaderRegistry) RegisterView(resource ResourceType, view SnapshotView, loader SnapshotLoader) error {
	if resource == "" || view == "" || loader == nil {
		return &AppError{Code: ErrorInvalidInput, Operation: "register snapshot view loader", Resource: resource}
	}
	registry.mu.Lock()
	registry.byView[loaderKey{resource: resource, view: view}] = loader
	registry.mu.Unlock()
	return nil
}

func (registry *LoaderRegistry) Resolve(scope RefreshScope) (SnapshotLoader, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if loader, ok := registry.exact[scope]; ok {
		return loader, true
	}
	loader, ok := registry.byView[loaderKey{resource: scope.Resource, view: scope.View}]
	return loader, ok
}

type refreshCall struct {
	done    chan struct{}
	result  RefreshResult
	err     error
	waiters int
}

type recentRefresh struct {
	reason RefreshReason
	at     time.Time
	result RefreshResult
}

// RefreshCoordinatorConfig configures the only writer to shared snapshots.
type RefreshCoordinatorConfig struct {
	Store          *StateStore
	Events         *EventBus
	Loaders        *LoaderRegistry
	Clock          Clock
	DebounceWindow time.Duration
}

// RefreshCoordinator coalesces identical work and publishes state changes.
type RefreshCoordinator struct {
	store          *StateStore
	events         *EventBus
	loaders        *LoaderRegistry
	clock          Clock
	debounceWindow time.Duration

	mu               sync.Mutex
	active           map[RefreshScope]*refreshCall
	recent           map[RefreshScope]recentRefresh
	closed           bool
	activeWG         sync.WaitGroup
	lifecycleContext context.Context
	cancelLifecycle  context.CancelFunc
}

func NewRefreshCoordinator(config RefreshCoordinatorConfig) (*RefreshCoordinator, error) {
	if config.Store == nil || config.Events == nil || config.Loaders == nil {
		return nil, &AppError{Code: ErrorInvalidInput, Operation: "create refresh coordinator"}
	}
	if config.Clock == nil {
		config.Clock = realClock{}
	}
	if config.DebounceWindow < 0 {
		return nil, &AppError{Code: ErrorInvalidInput, Operation: "create refresh coordinator"}
	}
	lifecycleContext, cancelLifecycle := context.WithCancel(context.Background())
	return &RefreshCoordinator{
		store:            config.Store,
		events:           config.Events,
		loaders:          config.Loaders,
		clock:            config.Clock,
		debounceWindow:   config.DebounceWindow,
		active:           make(map[RefreshScope]*refreshCall),
		recent:           make(map[RefreshScope]recentRefresh),
		lifecycleContext: lifecycleContext,
		cancelLifecycle:  cancelLifecycle,
	}, nil
}

// Refresh loads, compares, and atomically stores one scope.
func (coordinator *RefreshCoordinator) Refresh(
	ctx context.Context,
	scope RefreshScope,
	reason RefreshReason,
) (RefreshResult, error) {
	if err := validateScope(scope); err != nil {
		return RefreshResult{}, err
	}
	if ctx == nil {
		return RefreshResult{}, &AppError{Code: ErrorInvalidInput, Operation: "refresh", Resource: scope.Resource, ID: scope.ID}
	}
	if err := ctx.Err(); err != nil {
		return RefreshResult{}, canceledError("refresh", scope, err)
	}

	coordinator.mu.Lock()
	if coordinator.closed {
		coordinator.mu.Unlock()
		return RefreshResult{}, &AppError{Code: ErrorStreamClosed, Operation: "refresh coordinator", Resource: scope.Resource, ID: scope.ID}
	}
	if previous, ok := coordinator.recent[scope]; ok && coordinator.shouldDebounce(previous, reason) {
		result := previous.result
		result.Coalesced = true
		coordinator.mu.Unlock()
		return result, nil
	}
	if call, ok := coordinator.active[scope]; ok {
		call.waiters++
		coordinator.mu.Unlock()
		select {
		case <-call.done:
			result := call.result
			result.Coalesced = true
			return result, call.err
		case <-ctx.Done():
			return RefreshResult{}, canceledError("wait for coalesced refresh", scope, ctx.Err())
		}
	}
	call := &refreshCall{done: make(chan struct{})}
	coordinator.active[scope] = call
	coordinator.activeWG.Add(1)
	coordinator.mu.Unlock()

	result, err := coordinator.execute(ctx, scope, reason)

	coordinator.mu.Lock()
	call.result = result
	call.err = err
	delete(coordinator.active, scope)
	if err == nil {
		coordinator.recent[scope] = recentRefresh{reason: reason, at: coordinator.clock.Now(), result: result}
	}
	close(call.done)
	coordinator.activeWG.Done()
	coordinator.mu.Unlock()
	return result, err
}

func (coordinator *RefreshCoordinator) execute(
	ctx context.Context,
	scope RefreshScope,
	reason RefreshReason,
) (RefreshResult, error) {
	loader, ok := coordinator.loaders.Resolve(scope)
	if !ok {
		err := &AppError{Code: ErrorUnsupported, Operation: "resolve snapshot loader", Resource: scope.Resource, ID: scope.ID}
		coordinator.store.markFailed(scope, err)
		coordinator.publishFailure(scope, reason, err)
		return RefreshResult{}, err
	}

	loadContext, cancelLoad := context.WithCancel(ctx)
	stopLifecycleCancellation := context.AfterFunc(coordinator.lifecycleContext, cancelLoad)
	defer func() {
		stopLifecycleCancellation()
		cancelLoad()
	}()
	data, err := loader.Load(loadContext, scope)
	if err != nil {
		wrapped := classifyRefreshError(scope, err)
		coordinator.store.markFailed(scope, wrapped)
		coordinator.publishFailure(scope, reason, wrapped)
		return RefreshResult{}, wrapped
	}
	result, err := coordinator.store.store(scope, data, reason, coordinator.clock.Now())
	if err != nil {
		coordinator.store.markFailed(scope, err)
		coordinator.publishFailure(scope, reason, err)
		return RefreshResult{}, err
	}
	if result.Changed {
		_, _ = coordinator.events.Publish(AppEvent{
			Type:            EventSnapshotUpdated,
			ResourceType:    scope.Resource,
			ResourceID:      scope.ID,
			Scope:           scope,
			SnapshotVersion: result.Version,
			RefreshReason:   reason,
		})
	}
	return result, nil
}

func (coordinator *RefreshCoordinator) publishFailure(scope RefreshScope, reason RefreshReason, err error) {
	_, _ = coordinator.events.Publish(AppEvent{
		Type:          EventRefreshFailed,
		ResourceType:  scope.Resource,
		ResourceID:    scope.ID,
		Scope:         scope,
		RefreshReason: reason,
		Attributes:    map[string]string{"error": err.Error()},
	})
}

func (coordinator *RefreshCoordinator) shouldDebounce(previous recentRefresh, next RefreshReason) bool {
	if coordinator.debounceWindow == 0 || !pairedReasons(previous.reason, next) {
		return false
	}
	elapsed := coordinator.clock.Now().Sub(previous.at)
	return elapsed >= 0 && elapsed <= coordinator.debounceWindow
}

func pairedReasons(first, second RefreshReason) bool {
	return first == RefreshCommand && second == RefreshDockerEvent ||
		first == RefreshDockerEvent && second == RefreshCommand
}

// Shutdown rejects new refreshes and waits for active loaders.
func (coordinator *RefreshCoordinator) Shutdown(ctx context.Context) error {
	coordinator.mu.Lock()
	coordinator.closed = true
	coordinator.mu.Unlock()
	coordinator.cancelLifecycle()
	done := make(chan struct{})
	go func() {
		coordinator.activeWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return canceledError("shutdown refresh coordinator", RefreshScope{}, ctx.Err())
	}
}

func classifyRefreshError(scope RefreshScope, err error) error {
	if errors.Is(err, context.Canceled) {
		return canceledError("load snapshot", scope, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &AppError{Code: ErrorTimeout, Operation: "load snapshot", Resource: scope.Resource, ID: scope.ID, Err: err}
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return err
	}
	return &AppError{Code: ErrorInternal, Operation: "load snapshot", Resource: scope.Resource, ID: scope.ID, Err: err}
}

func canceledError(operation string, scope RefreshScope, err error) error {
	return &AppError{Code: ErrorCanceled, Operation: operation, Resource: scope.Resource, ID: scope.ID, Err: err}
}
