package backend_test

import (
	"context"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/test/testkit"
)

func TestCoreSubscribesBeforeInitialRefreshAndClosesOwnedClientOnce(t *testing.T) {
	clock := testkit.NewManualClock(time.Unix(6_000, 0))
	key := backend.RefreshKey{Kind: "backend.status"}
	registry := backend.NewLoaderRegistry()
	loader := testkit.NewRecordingLoader(1, func(context.Context, backend.RefreshKey) (backend.EventPayload, error) {
		return backend.BackendStatusUpdated{APIVersion: "1.48"}, nil
	})
	if err := registry.RegisterPageRefresh(backend.PageSystem, key.Kind, loader); err != nil {
		t.Fatalf("register loader: %v", err)
	}
	owner := testkit.NewRecordingCloser(nil)
	core, err := backend.NewCore(context.Background(), backend.CoreConfig{
		Clock: clock, Loaders: registry, OwnedDockerClient: owner,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if loader.Count() != 0 {
		t.Fatal("core performed a hidden startup refresh")
	}
	subscription, err := core.Subscribe(context.Background(), backend.PageSystem, backend.EventFilter{Keys: []backend.RefreshKey{key}})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	err = core.Refresh(context.Background(), backend.PageSystem)
	if err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	event := <-subscription.Events()
	if event.Sequence == 0 || event.Key != key || event.Reason != backend.RefreshManual {
		t.Fatalf("initial event = %#v", event)
	}

	if err := core.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := core.Close(context.Background()); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if owner.Count() != 1 {
		t.Fatalf("owned client close count = %d, want 1", owner.Count())
	}
}

func TestCoreBuffersObservedDockerEvents(t *testing.T) {
	registry := backend.NewLoaderRegistry()
	core, err := backend.NewCore(context.Background(), backend.CoreConfig{
		Loaders: registry, EventBufferCapacity: 2,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	t.Cleanup(func() { _ = core.Close(context.Background()) })
	eventsPage, err := core.Subscribe(context.Background(), backend.PageEvents, backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe to Events page: %v", err)
	}
	containersPage, err := core.Subscribe(context.Background(), backend.PageContainers, backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe to Containers page: %v", err)
	}

	published, err := core.ObserveDockerEvent(backend.DockerEventObserved{
		Resource:   "container",
		ResourceID: "container-one",
		Action:     "start",
	})
	if err != nil {
		t.Fatalf("observe event: %v", err)
	}
	if event := <-eventsPage.Events(); event.Sequence != published.Sequence {
		t.Fatalf("Events page received %#v, want sequence %d", event, published.Sequence)
	}
	select {
	case event := <-containersPage.Events():
		t.Fatalf("Docker event leaked to Containers page bus: %#v", event)
	default:
	}
	events := core.RecentDockerEvents(backend.EventFilter{}, 1)
	if len(events) != 1 {
		t.Fatalf("recent events = %#v", events)
	}
	dockerEvent, ok := events[0].Payload.(backend.DockerEventObserved)
	if !ok || events[0].Sequence != published.Sequence || dockerEvent.Action != "start" {
		t.Fatalf("recent events = %#v", events)
	}
}

func TestCoreConstructionFailureClosesOwnedClient(t *testing.T) {
	owner := testkit.NewRecordingCloser(nil)
	core, err := backend.NewCore(context.Background(), backend.CoreConfig{
		Loaders:           backend.NewLoaderRegistry(),
		OwnedDockerClient: owner,
		RefreshPolicies: []backend.RefreshPolicy{{
			Key: backend.RefreshKey{Kind: "backend.status"},
		}},
	})
	if err == nil || core != nil {
		t.Fatalf("construction result = %#v, %v", core, err)
	}
	if owner.Count() != 1 {
		t.Fatalf("owned client close count = %d, want 1", owner.Count())
	}
}
