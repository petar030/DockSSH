package backend

import (
	"context"
	"errors"
	"sync"
)

// RefreshLoader reads authoritative state and returns one complete update.
type RefreshLoader interface {
	Load(context.Context, RefreshKey) (EventPayload, error)
}

type RefreshLoaderFunc func(context.Context, RefreshKey) (EventPayload, error)

func (loader RefreshLoaderFunc) Load(ctx context.Context, key RefreshKey) (EventPayload, error) {
	return loader(ctx, key)
}

type registeredLoader struct {
	page   Page
	loader RefreshLoader
}

// LoaderRegistry maps each operation kind to its page and loader. The key ID
// is passed to that loader, so dynamic resources need no registrations.
type LoaderRegistry struct {
	mu            sync.RWMutex
	loaders       map[RefreshKind]registeredLoader
	pageRefreshes map[Page]RefreshKey
}

func NewLoaderRegistry() *LoaderRegistry {
	return &LoaderRegistry{
		loaders:       make(map[RefreshKind]registeredLoader),
		pageRefreshes: make(map[Page]RefreshKey),
	}
}

func (registry *LoaderRegistry) Register(page Page, kind RefreshKind, loader RefreshLoader) error {
	if !page.Valid() || !((RefreshKey{Kind: kind}).Valid()) || loader == nil {
		return &AppError{Code: ErrorInvalidInput, Operation: "register refresh loader", Resource: string(kind)}
	}
	registry.mu.Lock()
	registry.loaders[kind] = registeredLoader{page: page, loader: loader}
	registry.mu.Unlock()
	return nil
}

// RegisterPageRefresh registers the complete loader used when a TUI session
// requests a general refresh of a page. Internal targeted loaders use Register.
func (registry *LoaderRegistry) RegisterPageRefresh(page Page, kind RefreshKind, loader RefreshLoader) error {
	if err := registry.Register(page, kind, loader); err != nil {
		return err
	}
	registry.mu.Lock()
	registry.pageRefreshes[page] = RefreshKey{Kind: kind}
	registry.mu.Unlock()
	return nil
}

func (registry *LoaderRegistry) Resolve(key RefreshKey) (Page, RefreshLoader, bool) {
	registry.mu.RLock()
	registered, ok := registry.loaders[key.Kind]
	registry.mu.RUnlock()
	return registered.page, registered.loader, ok
}

func (registry *LoaderRegistry) ResolvePage(page Page) (RefreshKey, bool) {
	registry.mu.RLock()
	key, ok := registry.pageRefreshes[page]
	registry.mu.RUnlock()
	return key, ok
}

type RefreshCoordinatorConfig struct {
	Buses   map[Page]*EventBus
	Loaders *LoaderRegistry
}

// RefreshCoordinator performs every requested authoritative read and publishes
// its typed result to the Event Bus belonging to that operation's page.
type RefreshCoordinator struct {
	buses   map[Page]*EventBus
	loaders *LoaderRegistry

	mu               sync.Mutex
	closed           bool
	activeWG         sync.WaitGroup
	lifecycleContext context.Context
	cancelLifecycle  context.CancelFunc
}

func NewRefreshCoordinator(config RefreshCoordinatorConfig) (*RefreshCoordinator, error) {
	if config.Loaders == nil || len(config.Buses) == 0 {
		return nil, &AppError{Code: ErrorInvalidInput, Operation: "create refresh coordinator"}
	}
	buses := make(map[Page]*EventBus, len(config.Buses))
	for page, bus := range config.Buses {
		if !page.Valid() || bus == nil {
			return nil, &AppError{Code: ErrorInvalidInput, Operation: "create refresh coordinator", Resource: string(page)}
		}
		buses[page] = bus
	}
	lifecycleContext, cancelLifecycle := context.WithCancel(context.Background())
	return &RefreshCoordinator{
		buses:            buses,
		loaders:          config.Loaders,
		lifecycleContext: lifecycleContext,
		cancelLifecycle:  cancelLifecycle,
	}, nil
}

// RefreshPage runs the complete base refresh configured for a TUI page.
func (coordinator *RefreshCoordinator) RefreshPage(ctx context.Context, page Page) error {
	if !page.Valid() {
		return &AppError{Code: ErrorInvalidInput, Operation: "refresh page", Resource: string(page)}
	}
	key, ok := coordinator.loaders.ResolvePage(page)
	if !ok {
		return &AppError{Code: ErrorUnsupported, Operation: "refresh page", Resource: string(page)}
	}
	_, err := coordinator.Refresh(ctx, key, RefreshManual)
	return err
}

// Refresh performs a new load for every call. The caller's context and backend
// shutdown both cancel that load.
func (coordinator *RefreshCoordinator) Refresh(
	ctx context.Context,
	key RefreshKey,
	reason RefreshReason,
) (RefreshResult, error) {
	if err := validateRefreshKey(key); err != nil {
		return RefreshResult{}, err
	}
	if ctx == nil {
		return RefreshResult{}, &AppError{Code: ErrorInvalidInput, Operation: "refresh", Resource: string(key.Kind), ID: key.ID}
	}
	if err := ctx.Err(); err != nil {
		return RefreshResult{}, canceledError("refresh", key, err)
	}

	coordinator.mu.Lock()
	if coordinator.closed {
		coordinator.mu.Unlock()
		return RefreshResult{}, &AppError{Code: ErrorStreamClosed, Operation: "refresh coordinator", Resource: string(key.Kind), ID: key.ID}
	}
	coordinator.activeWG.Add(1)
	coordinator.mu.Unlock()
	defer coordinator.activeWG.Done()

	page, loader, ok := coordinator.loaders.Resolve(key)
	if !ok {
		return RefreshResult{}, &AppError{Code: ErrorUnsupported, Operation: "resolve refresh loader", Resource: string(key.Kind), ID: key.ID}
	}
	bus, ok := coordinator.buses[page]
	if !ok {
		return RefreshResult{}, &AppError{Code: ErrorInternal, Operation: "resolve page event bus", Resource: string(page)}
	}

	loadContext, cancelLoad := context.WithCancel(ctx)
	stopLifecycleCancellation := context.AfterFunc(coordinator.lifecycleContext, cancelLoad)
	defer func() {
		stopLifecycleCancellation()
		cancelLoad()
	}()

	payload, err := loader.Load(loadContext, key)
	if err != nil {
		wrapped := classifyRefreshError(key, err)
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			_, _ = bus.Publish(EventEnvelope{Key: key, Reason: reason, Payload: RefreshFailed{Err: wrapped}})
		}
		return RefreshResult{}, wrapped
	}
	if err := loadContext.Err(); err != nil {
		return RefreshResult{}, classifyRefreshError(key, err)
	}
	published, err := bus.Publish(EventEnvelope{Key: key, Reason: reason, Payload: payload})
	if err != nil {
		return RefreshResult{}, err
	}
	return RefreshResult{Key: key, Sequence: published.Sequence, PublishedAt: published.Time}, nil
}

// Shutdown rejects new refreshes, cancels active loaders, and waits for them.
func (coordinator *RefreshCoordinator) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return &AppError{Code: ErrorInvalidInput, Operation: "shutdown refresh coordinator"}
	}
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
		return canceledError("shutdown refresh coordinator", RefreshKey{}, ctx.Err())
	}
}

func classifyRefreshError(key RefreshKey, err error) error {
	if errors.Is(err, context.Canceled) {
		return canceledError("load refresh", key, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &AppError{Code: ErrorTimeout, Operation: "load refresh", Resource: string(key.Kind), ID: key.ID, Err: err}
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return err
	}
	return &AppError{Code: ErrorInternal, Operation: "load refresh", Resource: string(key.Kind), ID: key.ID, Err: err}
}

func canceledError(operation string, key RefreshKey, err error) error {
	return &AppError{Code: ErrorCanceled, Operation: operation, Resource: string(key.Kind), ID: key.ID, Err: err}
}

func validateRefreshKey(key RefreshKey) error {
	if key.Valid() {
		return nil
	}
	return &AppError{Code: ErrorInvalidInput, Operation: "validate refresh key", Resource: string(key.Kind), ID: key.ID}
}
