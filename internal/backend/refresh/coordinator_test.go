package refresh_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/eventhub"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/refresh"
)

func TestCoordinatorLoadsEveryRequestAndPublishesOnlyToItsPage(t *testing.T) {
	key := backend.RefreshKey{Kind: backend.RefreshKindBackendStatus}
	loader := newCoordinatorLoader(func(context.Context, backend.RefreshKey) (backend.EventPayload, error) {
		return backend.BackendStatusUpdated{APIVersion: "1.48"}, nil
	})
	coordinator, hub := newTestCoordinator(t, backend.PageSystem, key.Kind, loader)
	system, _ := hub.Subscribe(context.Background(), backend.PageSystem, backend.EventFilter{})
	other, _ := hub.Subscribe(context.Background(), backend.PageContainers, backend.EventFilter{})

	for index := 0; index < 2; index++ {
		result, err := coordinator.Refresh(context.Background(), key, backend.RefreshManual)
		if err != nil {
			t.Fatalf("refresh %d: %v", index+1, err)
		}
		if event := <-system.Events(); event.Sequence != result.Sequence || event.Key != key {
			t.Fatalf("refresh result/event = %#v / %#v", result, event)
		}
	}
	if loader.Count() != 2 {
		t.Fatalf("loader calls = %d, want 2", loader.Count())
	}
	select {
	case event := <-other.Events():
		t.Fatalf("System update leaked to Containers: %#v", event)
	default:
	}
}

func TestCoordinatorPublishesFailureOnlyToItsPage(t *testing.T) {
	key := backend.RefreshKey{Kind: "images.list"}
	loader := newCoordinatorLoader(func(context.Context, backend.RefreshKey) (backend.EventPayload, error) {
		return nil, errors.New("daemon disappeared")
	})
	coordinator, hub := newTestCoordinator(t, backend.PageImages, key.Kind, loader)
	failures, _ := hub.Subscribe(context.Background(), backend.PageImages, backend.EventFilter{
		Types: []backend.EventType{backend.EventRefreshFailed},
	})
	if _, err := coordinator.Refresh(context.Background(), key, backend.RefreshManual); !backend.HasErrorCode(err, backend.ErrorInternal) {
		t.Fatalf("refresh failure = %v", err)
	}
	event := <-failures.Events()
	if failure, ok := event.Payload.(backend.RefreshFailed); !ok || failure.Err == nil || event.Key != key {
		t.Fatalf("failure event = %#v", event)
	}
}

func TestCoordinatorDoesNotCoalesceConcurrentRequests(t *testing.T) {
	key := backend.RefreshKey{Kind: "containers.list"}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	loader := newCoordinatorLoader(func(ctx context.Context, _ backend.RefreshKey) (backend.EventPayload, error) {
		started <- struct{}{}
		select {
		case <-release:
			return backend.BackendStatusUpdated{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	coordinator, _ := newTestCoordinator(t, backend.PageContainers, key.Kind, loader)
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := coordinator.Refresh(context.Background(), key, backend.RefreshManual)
			done <- err
		}()
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for range 2 {
		select {
		case <-started:
		case <-waitCtx.Done():
			t.Fatal("an identical refresh waited for another request")
		}
	}
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("refresh: %v", err)
		}
	}
}

func TestCoordinatorCancellationAndShutdown(t *testing.T) {
	key := backend.RefreshKey{Kind: "volumes.list"}
	started := make(chan struct{})
	loader := newCoordinatorLoader(func(ctx context.Context, _ backend.RefreshKey) (backend.EventPayload, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	coordinator, hub := newTestCoordinator(t, backend.PageVolumes, key.Kind, loader)
	failures, _ := hub.Subscribe(context.Background(), backend.PageVolumes, backend.EventFilter{
		Types: []backend.EventType{backend.EventRefreshFailed},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := coordinator.Refresh(ctx, key, backend.RefreshManual)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !backend.HasErrorCode(err, backend.ErrorCanceled) {
		t.Fatalf("canceled refresh = %v", err)
	}
	select {
	case event := <-failures.Events():
		t.Fatalf("cancellation was broadcast: %#v", event)
	default:
	}
	if err := coordinator.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if _, err := coordinator.Refresh(context.Background(), key, backend.RefreshManual); !backend.HasErrorCode(err, backend.ErrorStreamClosed) {
		t.Fatalf("refresh after shutdown = %v", err)
	}
}

func TestCoordinatorShutdownCancelsActiveLoader(t *testing.T) {
	key := backend.RefreshKey{Kind: "system.engine"}
	started := make(chan struct{})
	loader := newCoordinatorLoader(func(ctx context.Context, _ backend.RefreshKey) (backend.EventPayload, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	coordinator, _ := newTestCoordinator(t, backend.PageSystem, key.Kind, loader)
	refreshDone := make(chan error, 1)
	go func() {
		_, err := coordinator.Refresh(context.Background(), key, backend.RefreshManual)
		refreshDone <- err
	}()
	<-started
	if err := coordinator.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := <-refreshDone; !backend.HasErrorCode(err, backend.ErrorCanceled) {
		t.Fatalf("active refresh error = %v", err)
	}
}

type coordinatorLoader struct {
	mu     sync.Mutex
	count  int
	loadFn func(context.Context, backend.RefreshKey) (backend.EventPayload, error)
}

func newCoordinatorLoader(loadFn func(context.Context, backend.RefreshKey) (backend.EventPayload, error)) *coordinatorLoader {
	return &coordinatorLoader{loadFn: loadFn}
}

func (loader *coordinatorLoader) Load(ctx context.Context, key backend.RefreshKey) (backend.EventPayload, error) {
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
	page backend.Page,
	kind backend.RefreshKind,
	loader backend.RefreshLoader,
) (*refresh.Coordinator, *eventhub.Hub) {
	t.Helper()
	hub := eventhub.New(eventhub.Config{SubscriberBuffer: 8})
	catalog := refresh.NewCatalog()
	if err := catalog.Register(page, kind, loader); err != nil {
		t.Fatalf("register loader: %v", err)
	}
	coordinator, err := refresh.NewCoordinator(refresh.CoordinatorConfig{Publisher: hub, Catalog: catalog})
	if err != nil {
		t.Fatalf("new coordinator: %v", err)
	}
	t.Cleanup(func() {
		_ = coordinator.Shutdown(context.Background())
		_ = hub.Close()
	})
	return coordinator, hub
}
