package refresh

import (
	"context"
	"errors"
	"sync"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// Publisher is the small Event Hub capability needed by refresh execution.
type Publisher interface {
	Publish(backend.Page, backend.EventEnvelope) (backend.EventEnvelope, error)
}

type CoordinatorConfig struct {
	Publisher Publisher
	Catalog   *Catalog
}

// Coordinator performs each requested authoritative read and publishes its
// typed result to the Event Hub page selected by the refresh catalog.
type Coordinator struct {
	publisher Publisher
	catalog   *Catalog

	mu               sync.Mutex
	closed           bool
	activeWG         sync.WaitGroup
	lifecycleContext context.Context
	cancelLifecycle  context.CancelFunc
}

func NewCoordinator(config CoordinatorConfig) (*Coordinator, error) {
	if config.Publisher == nil || config.Catalog == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create refresh coordinator"}
	}
	lifecycleContext, cancelLifecycle := context.WithCancel(context.Background())
	return &Coordinator{
		publisher:        config.Publisher,
		catalog:          config.Catalog,
		lifecycleContext: lifecycleContext,
		cancelLifecycle:  cancelLifecycle,
	}, nil
}

// RefreshPage runs the complete base refresh configured for one page.
func (coordinator *Coordinator) RefreshPage(
	ctx context.Context,
	page backend.Page,
	reason backend.RefreshReason,
) error {
	if !page.Valid() {
		return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "refresh page", Resource: string(page)}
	}
	key, ok := coordinator.catalog.ResolvePage(page)
	if !ok {
		return &backend.AppError{Code: backend.ErrorUnsupported, Operation: "refresh page", Resource: string(page)}
	}
	_, err := coordinator.Refresh(ctx, key, reason)
	return err
}

// Refresh performs a new load for every call. The caller's context and backend
// shutdown both cancel that load.
func (coordinator *Coordinator) Refresh(
	ctx context.Context,
	key backend.RefreshKey,
	reason backend.RefreshReason,
) (backend.RefreshResult, error) {
	if !key.Valid() {
		return backend.RefreshResult{}, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "validate refresh key", Resource: string(key.Kind), ID: key.ID}
	}
	if ctx == nil {
		return backend.RefreshResult{}, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "refresh", Resource: string(key.Kind), ID: key.ID}
	}
	if err := ctx.Err(); err != nil {
		return backend.RefreshResult{}, canceledError("refresh", key, err)
	}

	coordinator.mu.Lock()
	if coordinator.closed {
		coordinator.mu.Unlock()
		return backend.RefreshResult{}, &backend.AppError{Code: backend.ErrorStreamClosed, Operation: "refresh coordinator", Resource: string(key.Kind), ID: key.ID}
	}
	coordinator.activeWG.Add(1)
	coordinator.mu.Unlock()
	defer coordinator.activeWG.Done()

	page, loader, ok := coordinator.catalog.Resolve(key)
	if !ok {
		return backend.RefreshResult{}, &backend.AppError{Code: backend.ErrorUnsupported, Operation: "resolve refresh loader", Resource: string(key.Kind), ID: key.ID}
	}

	loadContext, cancelLoad := context.WithCancel(ctx)
	stopLifecycleCancellation := context.AfterFunc(coordinator.lifecycleContext, cancelLoad)
	defer func() {
		stopLifecycleCancellation()
		cancelLoad()
	}()

	payload, err := loader.Load(loadContext, key)
	if err != nil {
		wrapped := classifyError(key, err)
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			_, _ = coordinator.publisher.Publish(page, backend.EventEnvelope{
				Key: key, Reason: reason, Payload: backend.RefreshFailed{Err: wrapped},
			})
		}
		return backend.RefreshResult{}, wrapped
	}
	if err := loadContext.Err(); err != nil {
		return backend.RefreshResult{}, classifyError(key, err)
	}
	published, err := coordinator.publisher.Publish(page, backend.EventEnvelope{
		Key: key, Reason: reason, Payload: payload,
	})
	if err != nil {
		return backend.RefreshResult{}, err
	}
	return backend.RefreshResult{
		Key: key, Sequence: published.Sequence, PublishedAt: published.Time,
	}, nil
}

// Shutdown rejects new refreshes, cancels active loaders, and waits for them.
func (coordinator *Coordinator) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "shutdown refresh coordinator"}
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
		return canceledError("shutdown refresh coordinator", backend.RefreshKey{}, ctx.Err())
	}
}

func classifyError(key backend.RefreshKey, err error) error {
	if errors.Is(err, context.Canceled) {
		return canceledError("load refresh", key, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &backend.AppError{Code: backend.ErrorTimeout, Operation: "load refresh", Resource: string(key.Kind), ID: key.ID, Err: err}
	}
	var appErr *backend.AppError
	if errors.As(err, &appErr) {
		return err
	}
	return &backend.AppError{Code: backend.ErrorInternal, Operation: "load refresh", Resource: string(key.Kind), ID: key.ID, Err: err}
}

func canceledError(operation string, key backend.RefreshKey, err error) error {
	return &backend.AppError{Code: backend.ErrorCanceled, Operation: operation, Resource: string(key.Kind), ID: key.ID, Err: err}
}
