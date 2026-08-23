package backend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRefreshCoordinatorLoadsEveryRequestAndPublishesOnlyToItsPage(t *testing.T) {
	key := RefreshKey{Kind: RefreshKindBackendStatus}
	loader := newCoordinatorLoader(func(context.Context, RefreshKey) (EventPayload, error) {
		return BackendStatusUpdated{APIVersion: "1.48"}, nil
	})
	coordinator, buses := newTestCoordinator(t, PageSystem, key.Kind, loader)
	systemSubscription, err := buses[PageSystem].Subscribe(context.Background(), EventFilter{})
	if err != nil {
		t.Fatalf("subscribe to System: %v", err)
	}
	otherSubscription, err := buses[PageContainers].Subscribe(context.Background(), EventFilter{})
	if err != nil {
		t.Fatalf("subscribe to Containers: %v", err)
	}

	for index := 0; index < 2; index++ {
		result, err := coordinator.Refresh(context.Background(), key, RefreshManual)
		if err != nil {
			t.Fatalf("refresh %d: %v", index+1, err)
		}
		event := <-systemSubscription.Events()
		if event.Sequence != result.Sequence || event.Key != key {
			t.Fatalf("refresh %d result/event = %#v / %#v", index+1, result, event)
		}
	}
	if loader.Count() != 2 {
		t.Fatalf("loader calls = %d, want 2", loader.Count())
	}
	select {
	case event := <-otherSubscription.Events():
		t.Fatalf("System update leaked to Containers bus: %#v", event)
	default:
	}
}

func TestRefreshCoordinatorPublishesFailureOnlyToItsPage(t *testing.T) {
	key := RefreshKey{Kind: "images.list"}
	loader := newCoordinatorLoader(func(context.Context, RefreshKey) (EventPayload, error) {
		return nil, errors.New("daemon disappeared")
	})
	coordinator, buses := newTestCoordinator(t, PageImages, key.Kind, loader)
	subscription, err := buses[PageImages].Subscribe(context.Background(), EventFilter{Types: []EventType{EventRefreshFailed}})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if _, err := coordinator.Refresh(context.Background(), key, RefreshManual); !HasErrorCode(err, ErrorInternal) {
		t.Fatalf("refresh failure = %v", err)
	}
	event := <-subscription.Events()
	failure, ok := event.Payload.(RefreshFailed)
	if !ok || failure.Err == nil || event.Key != key {
		t.Fatalf("failure event = %#v", event)
	}
}

func TestRefreshCoordinatorDoesNotCoalesceIdenticalConcurrentRequests(t *testing.T) {
	key := RefreshKey{Kind: "containers.list"}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	loader := newCoordinatorLoader(func(ctx context.Context, _ RefreshKey) (EventPayload, error) {
		started <- struct{}{}
		select {
		case <-release:
			return BackendStatusUpdated{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	coordinator, _ := newTestCoordinator(t, PageContainers, key.Kind, loader)

	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := coordinator.Refresh(context.Background(), key, RefreshManual)
			done <- err
		}()
	}
	waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for range 2 {
		select {
		case <-started:
		case <-waitContext.Done():
			t.Fatal("an identical refresh waited for another request")
		}
	}
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("refresh: %v", err)
		}
	}
	if loader.Count() != 2 {
		t.Fatalf("loader calls = %d, want 2", loader.Count())
	}
}

func TestRefreshCoordinatorCallerCancellationCancelsItsLoadWithoutFailureEvent(t *testing.T) {
	key := RefreshKey{Kind: "volumes.list"}
	started := make(chan struct{})
	loader := newCoordinatorLoader(func(ctx context.Context, _ RefreshKey) (EventPayload, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	coordinator, buses := newTestCoordinator(t, PageVolumes, key.Kind, loader)
	failures, err := buses[PageVolumes].Subscribe(context.Background(), EventFilter{Types: []EventType{EventRefreshFailed}})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := coordinator.Refresh(ctx, key, RefreshManual)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !HasErrorCode(err, ErrorCanceled) {
		t.Fatalf("canceled refresh error = %v", err)
	}
	select {
	case event := <-failures.Events():
		t.Fatalf("caller cancellation was broadcast as a failure: %#v", event)
	default:
	}
}

func TestRefreshCoordinatorShutdownCancelsActiveLoaders(t *testing.T) {
	key := RefreshKey{Kind: "system.engine"}
	started := make(chan struct{})
	loader := newCoordinatorLoader(func(ctx context.Context, _ RefreshKey) (EventPayload, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	coordinator, _ := newTestCoordinator(t, PageSystem, key.Kind, loader)
	refreshDone := make(chan error, 1)
	go func() {
		_, err := coordinator.Refresh(context.Background(), key, RefreshManual)
		refreshDone <- err
	}()
	<-started

	if err := coordinator.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := <-refreshDone; !HasErrorCode(err, ErrorCanceled) {
		t.Fatalf("active refresh error = %v", err)
	}
	if _, err := coordinator.Refresh(context.Background(), key, RefreshManual); !HasErrorCode(err, ErrorStreamClosed) {
		t.Fatalf("refresh after shutdown error = %v", err)
	}
}

type coordinatorLoader struct {
	mu     sync.Mutex
	count  int
	loadFn func(context.Context, RefreshKey) (EventPayload, error)
}

func newCoordinatorLoader(loadFn func(context.Context, RefreshKey) (EventPayload, error)) *coordinatorLoader {
	return &coordinatorLoader{loadFn: loadFn}
}

func (loader *coordinatorLoader) Load(ctx context.Context, key RefreshKey) (EventPayload, error) {
	loader.mu.Lock()
	loader.count++
	loader.mu.Unlock()
	return loader.loadFn(ctx, key)
}

func (loader *coordinatorLoader) Count() int {
	loader.mu.Lock()
	defer loader.mu.Unlock()
	return loader.count
}

func newTestCoordinator(
	t *testing.T,
	page Page,
	kind RefreshKind,
	loader RefreshLoader,
) (*RefreshCoordinator, map[Page]*EventBus) {
	t.Helper()
	buses := map[Page]*EventBus{
		PageContainers: NewEventBus(EventBusConfig{SubscriberBuffer: 8}),
		PageImages:     NewEventBus(EventBusConfig{SubscriberBuffer: 8}),
		PageVolumes:    NewEventBus(EventBusConfig{SubscriberBuffer: 8}),
		PageSystem:     NewEventBus(EventBusConfig{SubscriberBuffer: 8}),
	}
	registry := NewLoaderRegistry()
	if err := registry.Register(page, kind, loader); err != nil {
		t.Fatalf("register loader: %v", err)
	}
	coordinator, err := NewRefreshCoordinator(RefreshCoordinatorConfig{Buses: buses, Loaders: registry})
	if err != nil {
		t.Fatalf("new coordinator: %v", err)
	}
	t.Cleanup(func() {
		_ = coordinator.Shutdown(context.Background())
		for _, bus := range buses {
			_ = bus.Close()
		}
	})
	return coordinator, buses
}
