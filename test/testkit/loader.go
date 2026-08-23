package testkit

import (
	"context"
	"sync"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// RecordingLoader is a concurrency-safe programmable refresh loader.
type RecordingLoader struct {
	mu     sync.Mutex
	Calls  chan backend.RefreshKey
	LoadFn func(context.Context, backend.RefreshKey) (backend.EventPayload, error)
	count  int
}

func NewRecordingLoader(buffer int, load func(context.Context, backend.RefreshKey) (backend.EventPayload, error)) *RecordingLoader {
	return &RecordingLoader{Calls: make(chan backend.RefreshKey, buffer), LoadFn: load}
}

func (loader *RecordingLoader) Load(ctx context.Context, key backend.RefreshKey) (backend.EventPayload, error) {
	loader.mu.Lock()
	loader.count++
	loader.mu.Unlock()
	select {
	case loader.Calls <- key:
	default:
	}
	return loader.LoadFn(ctx, key)
}

func (loader *RecordingLoader) Count() int {
	loader.mu.Lock()
	defer loader.mu.Unlock()
	return loader.count
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
