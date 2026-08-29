package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const (
	defaultRefreshPendingCapacity = 128
	defaultRefreshTimeout         = 30 * time.Second
)

// RefreshHandler performs one authoritative read for a registered refresh key.
// It returns data only; RefreshManager publishes the resulting page event.
type RefreshHandler func(context.Context, backend.RefreshKey) (backend.EventPayload, error)

type refreshPublisher interface {
	Publish(backend.Page, backend.EventEnvelope) (backend.EventEnvelope, error)
}

type refreshRoute struct {
	page    backend.Page
	handler RefreshHandler
}

type pendingRefresh struct {
	key    backend.RefreshKey
	reason backend.RefreshReason
}

type RefreshManagerConfig struct {
	Publisher       refreshPublisher
	PendingCapacity int
	Timeout         time.Duration
}

// RefreshManager owns refresh routing, deduplicated pending work, Docker-read
// execution and typed Event Hub publication. It stores no Docker resource data.
type RefreshManager struct {
	publisher refreshPublisher
	capacity  int
	timeout   time.Duration
	ctx       context.Context
	cancel    context.CancelFunc
	wake      chan struct{}
	done      chan struct{}

	mu       sync.Mutex
	routes   map[backend.RefreshKind]refreshRoute
	pages    map[backend.Page]backend.RefreshKey
	pending  map[backend.RefreshKey]backend.RefreshReason
	order    []backend.RefreshKey
	closed   bool
	closeOne sync.Once
}

func NewRefreshManager(config RefreshManagerConfig) (*RefreshManager, error) {
	if config.Publisher == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Refresh Manager"}
	}
	if config.PendingCapacity == 0 {
		config.PendingCapacity = defaultRefreshPendingCapacity
	}
	if config.PendingCapacity < 0 {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Refresh Manager"}
	}
	if config.Timeout == 0 {
		config.Timeout = defaultRefreshTimeout
	}
	if config.Timeout < 0 {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Refresh Manager"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &RefreshManager{
		publisher: config.Publisher,
		capacity:  config.PendingCapacity,
		timeout:   config.Timeout,
		ctx:       ctx,
		cancel:    cancel,
		wake:      make(chan struct{}, 1),
		done:      make(chan struct{}),
		routes:    make(map[backend.RefreshKind]refreshRoute),
		pages:     make(map[backend.Page]backend.RefreshKey),
		pending:   make(map[backend.RefreshKey]backend.RefreshReason),
	}
	go manager.run()
	return manager, nil
}

// Register adds one refresh kind and its destination page. A kind may be
// registered only once.
func (manager *RefreshManager) Register(page backend.Page, kind backend.RefreshKind, handler RefreshHandler) error {
	if !page.Valid() || !(backend.RefreshKey{Kind: kind}).Valid() || handler == nil {
		return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "register refresh handler", Resource: string(kind)}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return refreshClosedError("register refresh handler", backend.RefreshKey{Kind: kind})
	}
	if _, exists := manager.routes[kind]; exists {
		return &backend.AppError{Code: backend.ErrorConflict, Operation: "register refresh handler", Resource: string(kind)}
	}
	manager.routes[kind] = refreshRoute{page: page, handler: handler}
	return nil
}

// RegisterPage also makes kind the complete refresh requested for page.
func (manager *RefreshManager) RegisterPage(page backend.Page, kind backend.RefreshKind, handler RefreshHandler) error {
	if !page.Valid() || !(backend.RefreshKey{Kind: kind}).Valid() || handler == nil {
		return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "register page refresh", Resource: string(page)}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return refreshClosedError("register page refresh", backend.RefreshKey{Kind: kind})
	}
	if _, exists := manager.routes[kind]; exists {
		return &backend.AppError{Code: backend.ErrorConflict, Operation: "register refresh handler", Resource: string(kind)}
	}
	if _, exists := manager.pages[page]; exists {
		return &backend.AppError{Code: backend.ErrorConflict, Operation: "register page refresh", Resource: string(page)}
	}
	manager.routes[kind] = refreshRoute{page: page, handler: handler}
	manager.pages[page] = backend.RefreshKey{Kind: kind}
	return nil
}

// RequestPage records the complete refresh registered for a page.
func (manager *RefreshManager) RequestPage(page backend.Page, reason backend.RefreshReason) error {
	if !page.Valid() {
		return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "request page refresh", Resource: string(page)}
	}
	manager.mu.Lock()
	key, ok := manager.pages[page]
	manager.mu.Unlock()
	if !ok {
		return &backend.AppError{Code: backend.ErrorUnsupported, Operation: "request page refresh", Resource: string(page)}
	}
	return manager.Request(key, reason)
}

// Request records one refresh key without waiting for the authoritative read.
// Matching pending work is collapsed and keeps the most recent reason.
func (manager *RefreshManager) Request(key backend.RefreshKey, reason backend.RefreshReason) error {
	if !key.Valid() || reason == "" {
		return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "request refresh", Resource: string(key.Kind), ID: key.ID}
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return refreshClosedError("request refresh", key)
	}
	if _, exists := manager.routes[key.Kind]; !exists {
		manager.mu.Unlock()
		return &backend.AppError{Code: backend.ErrorUnsupported, Operation: "request refresh", Resource: string(key.Kind), ID: key.ID}
	}
	if _, exists := manager.pending[key]; exists {
		manager.pending[key] = reason
		manager.mu.Unlock()
		return nil
	}
	if len(manager.pending) >= manager.capacity {
		manager.mu.Unlock()
		return &backend.AppError{Code: backend.ErrorConflict, Operation: "queue refresh", Resource: string(key.Kind), ID: key.ID}
	}
	manager.pending[key] = reason
	manager.order = append(manager.order, key)
	manager.mu.Unlock()
	select {
	case manager.wake <- struct{}{}:
	default:
	}
	return nil
}

func (manager *RefreshManager) run() {
	defer close(manager.done)
	for {
		select {
		case <-manager.ctx.Done():
			return
		case <-manager.wake:
		}
		for {
			pending, ok := manager.takePending()
			if !ok {
				break
			}
			manager.execute(pending)
			if manager.ctx.Err() != nil {
				return
			}
		}
	}
}

func (manager *RefreshManager) takePending() (pendingRefresh, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for len(manager.order) > 0 {
		key := manager.order[0]
		manager.order = manager.order[1:]
		reason, exists := manager.pending[key]
		if !exists {
			continue
		}
		delete(manager.pending, key)
		return pendingRefresh{key: key, reason: reason}, true
	}
	return pendingRefresh{}, false
}

func (manager *RefreshManager) execute(pending pendingRefresh) {
	manager.mu.Lock()
	route, ok := manager.routes[pending.key.Kind]
	manager.mu.Unlock()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(manager.ctx, manager.timeout)
	payload, err := route.handler(ctx, pending.key)
	contextErr := ctx.Err()
	cancel()
	if err == nil && contextErr != nil {
		err = contextErr
	}
	if err == nil && payload == nil {
		err = errors.New("refresh handler returned no payload")
	}
	if err != nil {
		if manager.ctx.Err() == nil {
			_, _ = manager.publisher.Publish(route.page, backend.EventEnvelope{
				Key: pending.key, Reason: pending.reason,
				Payload: backend.RefreshFailed{Err: classifyRefreshError(pending.key, err)},
			})
		}
		return
	}
	if manager.ctx.Err() != nil {
		return
	}
	_, _ = manager.publisher.Publish(route.page, backend.EventEnvelope{
		Key: pending.key, Reason: pending.reason, Payload: payload,
	})
}

// Close rejects new work, cancels the active read, discards pending work and
// waits for the worker to exit.
func (manager *RefreshManager) Close(ctx context.Context) error {
	if ctx == nil {
		return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "close Refresh Manager"}
	}
	manager.closeOne.Do(func() {
		manager.mu.Lock()
		manager.closed = true
		manager.pending = make(map[backend.RefreshKey]backend.RefreshReason)
		manager.order = nil
		manager.cancel()
		manager.mu.Unlock()
	})
	select {
	case <-manager.done:
		return nil
	case <-ctx.Done():
		return &backend.AppError{Code: backend.ErrorCanceled, Operation: "close Refresh Manager", Err: ctx.Err()}
	}
}

func classifyRefreshError(key backend.RefreshKey, err error) error {
	var appErr *backend.AppError
	if errors.As(err, &appErr) {
		return err
	}
	code := backend.ErrorInternal
	if errors.Is(err, context.Canceled) {
		code = backend.ErrorCanceled
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = backend.ErrorTimeout
	}
	return &backend.AppError{Code: code, Operation: "read refresh", Resource: string(key.Kind), ID: key.ID, Err: err}
}

func refreshClosedError(operation string, key backend.RefreshKey) error {
	return &backend.AppError{Code: backend.ErrorStreamClosed, Operation: operation, Resource: string(key.Kind), ID: key.ID}
}

var _ backend.RefreshRequester = (*RefreshManager)(nil)
