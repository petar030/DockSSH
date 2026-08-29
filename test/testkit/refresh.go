package testkit

import (
	"context"
	"sync"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// RecordingRefreshHandler is a concurrency-safe programmable refresh reader.
type RecordingRefreshHandler struct {
	mu     sync.Mutex
	Calls  chan backend.RefreshKey
	ReadFn func(context.Context, backend.RefreshKey) (backend.EventPayload, error)
	count  int
}

func NewRecordingRefreshHandler(buffer int, read func(context.Context, backend.RefreshKey) (backend.EventPayload, error)) *RecordingRefreshHandler {
	return &RecordingRefreshHandler{Calls: make(chan backend.RefreshKey, buffer), ReadFn: read}
}

func (handler *RecordingRefreshHandler) ReadRefresh(ctx context.Context, key backend.RefreshKey) (backend.EventPayload, error) {
	handler.mu.Lock()
	handler.count++
	handler.mu.Unlock()
	select {
	case handler.Calls <- key:
	default:
	}
	return handler.ReadFn(ctx, key)
}

func (handler *RecordingRefreshHandler) Count() int {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.count
}

// Gate provides explicit synchronization without wall-clock sleeps.
type Gate struct {
	Started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

func NewGate() *Gate {
	return &Gate{Started: make(chan struct{}), release: make(chan struct{})}
}

func (gate *Gate) Block(ctx context.Context) error {
	gate.startedOnce.Do(func() { close(gate.Started) })
	select {
	case <-gate.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (gate *Gate) Release() {
	gate.releaseOnce.Do(func() { close(gate.release) })
}
