package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func TestRefreshManagerPublishesTypedFailureAndRejectsDuplicateRoutes(t *testing.T) {
	publisher := &recordingRefreshPublisher{events: make(chan publishedRefresh, 2)}
	manager, err := NewRefreshManager(RefreshManagerConfig{Publisher: publisher, Timeout: time.Second})
	if err != nil {
		t.Fatalf("new Refresh Manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	readErr := errors.New("daemon unavailable")
	handler := func(context.Context, backend.RefreshKey) (backend.EventPayload, error) {
		return nil, readErr
	}
	if err := manager.RegisterPage(backend.PageImages, "images.list", handler); err != nil {
		t.Fatalf("register page: %v", err)
	}
	if err := manager.Register(backend.PageImages, "images.list", handler); !backend.HasErrorCode(err, backend.ErrorConflict) {
		t.Fatalf("duplicate registration = %v", err)
	}
	if err := manager.RequestPage(backend.PageImages, backend.RefreshManual); err != nil {
		t.Fatalf("request page: %v", err)
	}
	published := receiveWithin(t, publisher.events)
	failure, ok := published.event.Payload.(backend.RefreshFailed)
	if !ok || !backend.HasErrorCode(failure.Err, backend.ErrorInternal) || !errors.Is(failure.Err, readErr) {
		t.Fatalf("failure event = %#v", published.event)
	}
}

func TestRefreshManagerTimesOutReadAndPublishesTimeout(t *testing.T) {
	publisher := &recordingRefreshPublisher{events: make(chan publishedRefresh, 1)}
	manager, err := NewRefreshManager(RefreshManagerConfig{Publisher: publisher, Timeout: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("new Refresh Manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	if err := manager.RegisterPage(backend.PageDashboard, "dashboard.summary", func(ctx context.Context, _ backend.RefreshKey) (backend.EventPayload, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}); err != nil {
		t.Fatalf("register page: %v", err)
	}
	if err := manager.RequestPage(backend.PageDashboard, backend.RefreshScheduled); err != nil {
		t.Fatalf("request page: %v", err)
	}
	published := receiveWithin(t, publisher.events)
	failure := published.event.Payload.(backend.RefreshFailed)
	if !backend.HasErrorCode(failure.Err, backend.ErrorTimeout) {
		t.Fatalf("timeout failure = %v", failure.Err)
	}
}

func TestRefreshManagerRoutesPublishesAndKeepsWorkBackendOwned(t *testing.T) {
	publisher := &recordingRefreshPublisher{events: make(chan publishedRefresh, 2)}
	manager, err := NewRefreshManager(RefreshManagerConfig{Publisher: publisher, Timeout: time.Second})
	if err != nil {
		t.Fatalf("new Refresh Manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })

	started := make(chan context.Context, 1)
	release := make(chan struct{})
	if err := manager.RegisterPage(backend.PageContainers, "containers.list", func(ctx context.Context, _ backend.RefreshKey) (backend.EventPayload, error) {
		started <- ctx
		select {
		case <-release:
			return backend.BackendStatusUpdated{APIVersion: "test"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}); err != nil {
		t.Fatalf("register page: %v", err)
	}
	if err := manager.RequestPage(backend.PageContainers, backend.RefreshManual); err != nil {
		t.Fatalf("request page: %v", err)
	}
	loadContext := receiveWithin(t, started)
	if loadContext.Err() != nil {
		t.Fatalf("load context already canceled: %v", loadContext.Err())
	}
	close(release)
	published := receiveWithin(t, publisher.events)
	if published.page != backend.PageContainers || published.event.Reason != backend.RefreshManual || published.event.Key.Kind != "containers.list" {
		t.Fatalf("published refresh = %#v", published)
	}
}

func TestRefreshManagerDeduplicatesPendingFullKeysButRetainsTriggerDuringRead(t *testing.T) {
	publisher := &recordingRefreshPublisher{events: make(chan publishedRefresh, 4)}
	manager, err := NewRefreshManager(RefreshManagerConfig{Publisher: publisher, PendingCapacity: 2, Timeout: time.Second})
	if err != nil {
		t.Fatalf("new Refresh Manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })

	started := make(chan backend.RefreshKey, 4)
	release := make(chan struct{}, 4)
	if err := manager.Register(backend.PageContainers, "container.details", func(ctx context.Context, key backend.RefreshKey) (backend.EventPayload, error) {
		started <- key
		select {
		case <-release:
			return backend.BackendStatusUpdated{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}); err != nil {
		t.Fatalf("register details: %v", err)
	}
	key := backend.RefreshKey{Kind: "container.details", ID: "one"}
	if err := manager.Request(key, backend.RefreshDockerEvent); err != nil {
		t.Fatalf("request first: %v", err)
	}
	if got := receiveWithin(t, started); got != key {
		t.Fatalf("first key = %#v", got)
	}
	for range 20 {
		if err := manager.Request(key, backend.RefreshCommand); err != nil {
			t.Fatalf("request matching: %v", err)
		}
	}
	release <- struct{}{}
	if got := receiveWithin(t, started); got != key {
		t.Fatalf("second key = %#v", got)
	}
	release <- struct{}{}
	receiveWithin(t, publisher.events)
	receiveWithin(t, publisher.events)
	select {
	case extra := <-started:
		t.Fatalf("unexpected third refresh: %#v", extra)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestRefreshManagerBoundsPendingWorkAndRejectsAfterClose(t *testing.T) {
	publisher := &recordingRefreshPublisher{events: make(chan publishedRefresh, 1)}
	manager, err := NewRefreshManager(RefreshManagerConfig{Publisher: publisher, PendingCapacity: 1, Timeout: time.Second})
	if err != nil {
		t.Fatalf("new Refresh Manager: %v", err)
	}
	started := make(chan struct{})
	if err := manager.Register(backend.PageContainers, "container.details", func(ctx context.Context, _ backend.RefreshKey) (backend.EventPayload, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	first := backend.RefreshKey{Kind: "container.details", ID: "one"}
	if err := manager.Request(first, backend.RefreshManual); err != nil {
		t.Fatalf("request first: %v", err)
	}
	receiveWithin(t, started)
	if err := manager.Request(backend.RefreshKey{Kind: "container.details", ID: "two"}, backend.RefreshManual); err != nil {
		t.Fatalf("request pending: %v", err)
	}
	if err := manager.Request(backend.RefreshKey{Kind: "container.details", ID: "three"}, backend.RefreshManual); !backend.HasErrorCode(err, backend.ErrorConflict) {
		t.Fatalf("overflow error = %v", err)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := manager.Request(first, backend.RefreshManual); !backend.HasErrorCode(err, backend.ErrorStreamClosed) {
		t.Fatalf("request after close = %v", err)
	}
}

type publishedRefresh struct {
	page  backend.Page
	event backend.EventEnvelope
}

type recordingRefreshPublisher struct {
	mu     sync.Mutex
	next   uint64
	events chan publishedRefresh
}

func (publisher *recordingRefreshPublisher) Publish(page backend.Page, event backend.EventEnvelope) (backend.EventEnvelope, error) {
	publisher.mu.Lock()
	publisher.next++
	event.Sequence = publisher.next
	publisher.mu.Unlock()
	publisher.events <- publishedRefresh{page: page, event: event}
	return event, nil
}

func receiveWithin[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for value")
		var zero T
		return zero
	}
}
