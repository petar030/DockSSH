package backend

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestRefreshCoordinatorStoresChangesAndPublishesOnlyChangedSnapshots(t *testing.T) {
	clock := newCoordinatorClock(time.Unix(1_000, 0))
	scope := RefreshScope{Resource: ResourceContainer, View: ViewSummary}
	loader := newCoordinatorLoader(func(context.Context, RefreshScope) (any, error) {
		return map[string]int{"containers": 1}, nil
	})
	coordinator, store, bus := newTestCoordinator(t, clock, scope, loader, 0)
	subscription, err := bus.Subscribe(context.Background(), EventFilter{Types: []EventType{EventSnapshotUpdated}})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	first, err := coordinator.Refresh(context.Background(), scope, RefreshStartup)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if first.Version != 1 || !first.Changed {
		t.Fatalf("first refresh result = %#v", first)
	}
	event := <-subscription.Events()
	if event.Type != EventSnapshotUpdated || event.SnapshotVersion != 1 || event.RefreshReason != RefreshStartup {
		t.Fatalf("snapshot event = %#v", event)
	}

	clock.Advance(time.Second)
	second, err := coordinator.Refresh(context.Background(), scope, RefreshManual)
	if err != nil {
		t.Fatalf("unchanged refresh: %v", err)
	}
	if second.Version != 1 || second.Changed {
		t.Fatalf("unchanged result = %#v", second)
	}
	select {
	case unexpected := <-subscription.Events():
		t.Fatalf("unchanged refresh published %#v", unexpected)
	default:
	}
	meta, err := store.Meta(scope)
	if err != nil || meta.RefreshedAt != clock.Now() || meta.Reason != RefreshManual {
		t.Fatalf("updated metadata = %#v, %v", meta, err)
	}
}

func TestRefreshCoordinatorFailurePreservesSnapshotAndPublishesFailure(t *testing.T) {
	clock := newCoordinatorClock(time.Unix(2_000, 0))
	scope := RefreshScope{Resource: ResourceImage, View: ViewSummary}
	var mu sync.Mutex
	fail := false
	loader := newCoordinatorLoader(func(context.Context, RefreshScope) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return nil, errors.New("daemon disappeared")
		}
		return []string{"image"}, nil
	})
	coordinator, store, bus := newTestCoordinator(t, clock, scope, loader, 0)
	if _, err := coordinator.Refresh(context.Background(), scope, RefreshStartup); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	subscription, err := bus.Subscribe(context.Background(), EventFilter{Types: []EventType{EventRefreshFailed}})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	mu.Lock()
	fail = true
	mu.Unlock()

	if _, err := coordinator.Refresh(context.Background(), scope, RefreshManual); !HasErrorCode(err, ErrorInternal) {
		t.Fatalf("refresh failure = %v", err)
	}
	meta, err := store.Meta(scope)
	if err != nil {
		t.Fatalf("metadata after failure: %v", err)
	}
	if !meta.Stale || meta.Version != 1 {
		t.Fatalf("metadata after failure = %#v", meta)
	}
	if _, err := store.Read(scope); err != nil {
		t.Fatalf("last valid snapshot was lost: %v", err)
	}
	event := <-subscription.Events()
	if event.Type != EventRefreshFailed || event.Attributes["error"] == "" {
		t.Fatalf("failure event = %#v", event)
	}
}

func TestRefreshCoordinatorCoalescesConcurrentIdenticalScopes(t *testing.T) {
	clock := newCoordinatorClock(time.Unix(3_000, 0))
	scope := RefreshScope{Resource: ResourceNetwork, View: ViewSummary}
	gate := newCoordinatorGate()
	loader := newCoordinatorLoader(func(ctx context.Context, _ RefreshScope) (any, error) {
		if err := gate.Block(ctx); err != nil {
			return nil, err
		}
		return []string{"network"}, nil
	})
	coordinator, _, _ := newTestCoordinator(t, clock, scope, loader, 0)

	type outcome struct {
		result RefreshResult
		err    error
	}
	firstDone := make(chan outcome, 1)
	go func() {
		result, err := coordinator.Refresh(context.Background(), scope, RefreshManual)
		firstDone <- outcome{result: result, err: err}
	}()
	<-gate.Started
	secondDone := make(chan outcome, 1)
	go func() {
		result, err := coordinator.Refresh(context.Background(), scope, RefreshManual)
		secondDone <- outcome{result: result, err: err}
	}()
	waitForRefreshWaiters(t, coordinator, scope, 1)
	gate.Release()

	first := <-firstDone
	second := <-secondDone
	if first.err != nil || second.err != nil {
		t.Fatalf("coalesced outcomes: first=%v second=%v", first.err, second.err)
	}
	if first.result.Coalesced {
		t.Fatal("owner refresh was marked coalesced")
	}
	if !second.result.Coalesced {
		t.Fatal("waiting refresh was not marked coalesced")
	}
	if loader.Count() != 1 {
		t.Fatalf("loader calls = %d, want 1", loader.Count())
	}
}

func TestRefreshCoordinatorDebouncesCommandAndDockerEventPair(t *testing.T) {
	clock := newCoordinatorClock(time.Unix(4_000, 0))
	scope := RefreshScope{Resource: ResourceVolume, View: ViewSummary}
	loader := newCoordinatorLoader(func(context.Context, RefreshScope) (any, error) {
		return []string{"volume"}, nil
	})
	coordinator, _, _ := newTestCoordinator(t, clock, scope, loader, 2*time.Second)

	if _, err := coordinator.Refresh(context.Background(), scope, RefreshCommand); err != nil {
		t.Fatalf("command refresh: %v", err)
	}
	result, err := coordinator.Refresh(context.Background(), scope, RefreshDockerEvent)
	if err != nil {
		t.Fatalf("debounced event refresh: %v", err)
	}
	if !result.Coalesced || loader.Count() != 1 {
		t.Fatalf("debounced result=%#v loader calls=%d", result, loader.Count())
	}
	clock.Advance(3 * time.Second)
	if _, err := coordinator.Refresh(context.Background(), scope, RefreshDockerEvent); err != nil {
		t.Fatalf("event after debounce: %v", err)
	}
	if loader.Count() != 2 {
		t.Fatalf("loader calls after window = %d, want 2", loader.Count())
	}
}

func TestRefreshCoordinatorShutdownCancelsActiveLoaders(t *testing.T) {
	clock := newCoordinatorClock(time.Unix(4_500, 0))
	scope := RefreshScope{Resource: ResourceSystem, View: ViewEngine}
	gate := newCoordinatorGate()
	loader := newCoordinatorLoader(func(ctx context.Context, _ RefreshScope) (any, error) {
		if err := gate.Block(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	})
	coordinator, _, _ := newTestCoordinator(t, clock, scope, loader, 0)
	refreshDone := make(chan error, 1)
	go func() {
		_, err := coordinator.Refresh(context.Background(), scope, RefreshManual)
		refreshDone <- err
	}()
	<-gate.Started

	if err := coordinator.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := <-refreshDone; !HasErrorCode(err, ErrorCanceled) {
		t.Fatalf("active refresh error = %v", err)
	}
	if _, err := coordinator.Refresh(context.Background(), scope, RefreshManual); !HasErrorCode(err, ErrorStreamClosed) {
		t.Fatalf("refresh after shutdown error = %v", err)
	}
}

type coordinatorClock struct {
	mu  sync.Mutex
	now time.Time
}

func newCoordinatorClock(now time.Time) *coordinatorClock {
	return &coordinatorClock{now: now}
}

func (clock *coordinatorClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *coordinatorClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

func (*coordinatorClock) NewTicker(time.Duration) Ticker {
	panic("coordinator test clock does not create tickers")
}

type coordinatorLoader struct {
	mu     sync.Mutex
	count  int
	loadFn func(context.Context, RefreshScope) (any, error)
}

func newCoordinatorLoader(loadFn func(context.Context, RefreshScope) (any, error)) *coordinatorLoader {
	return &coordinatorLoader{loadFn: loadFn}
}

func (loader *coordinatorLoader) Load(ctx context.Context, scope RefreshScope) (any, error) {
	loader.mu.Lock()
	loader.count++
	loader.mu.Unlock()
	return loader.loadFn(ctx, scope)
}

func (loader *coordinatorLoader) Count() int {
	loader.mu.Lock()
	defer loader.mu.Unlock()
	return loader.count
}

type coordinatorGate struct {
	Started chan struct{}
	release chan struct{}
	start   sync.Once
	done    sync.Once
}

func newCoordinatorGate() *coordinatorGate {
	return &coordinatorGate{Started: make(chan struct{}), release: make(chan struct{})}
}

func (gate *coordinatorGate) Block(ctx context.Context) error {
	gate.start.Do(func() { close(gate.Started) })
	select {
	case <-gate.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (gate *coordinatorGate) Release() {
	gate.done.Do(func() { close(gate.release) })
}

func newTestCoordinator(
	t *testing.T,
	clock Clock,
	scope RefreshScope,
	loader SnapshotLoader,
	debounce time.Duration,
) (*RefreshCoordinator, *StateStore, *EventBus) {
	t.Helper()
	store := NewStateStore()
	bus := NewEventBus(EventBusConfig{Clock: clock, SubscriberBuffer: 8})
	registry := NewLoaderRegistry()
	if err := registry.Register(scope, loader); err != nil {
		t.Fatalf("register loader: %v", err)
	}
	coordinator, err := NewRefreshCoordinator(RefreshCoordinatorConfig{
		Store: store, Events: bus, Loaders: registry, Clock: clock, DebounceWindow: debounce,
	})
	if err != nil {
		t.Fatalf("new coordinator: %v", err)
	}
	t.Cleanup(func() {
		_ = coordinator.Shutdown(context.Background())
		_ = bus.Close()
	})
	return coordinator, store, bus
}

func waitForRefreshWaiters(t *testing.T, coordinator *RefreshCoordinator, scope RefreshScope, count int) {
	t.Helper()
	for attempts := 0; attempts < 100_000; attempts++ {
		coordinator.mu.Lock()
		call := coordinator.active[scope]
		waiters := 0
		if call != nil {
			waiters = call.waiters
		}
		coordinator.mu.Unlock()
		if waiters >= count {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("coalesced refresh did not register a waiter")
}
