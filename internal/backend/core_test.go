package backend_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/test/testkit"
)

func TestCoreSynchronizesStartupAndClosesOwnedClientOnce(t *testing.T) {
	clock := testkit.NewManualClock(time.Unix(6_000, 0))
	scope := backend.RefreshScope{Resource: backend.ResourceContainer, View: backend.ViewSummary}
	registry := backend.NewLoaderRegistry()
	loader := testkit.NewRecordingLoader(1, func(context.Context, backend.RefreshScope) (any, error) {
		return []string{}, nil
	})
	if err := registry.Register(scope, loader); err != nil {
		t.Fatalf("register loader: %v", err)
	}
	owner := testkit.NewRecordingCloser(nil)
	core, err := backend.NewCore(context.Background(), backend.CoreConfig{
		Clock: clock, Loaders: registry, StartupScopes: []backend.RefreshScope{scope}, OwnedDockerClient: owner,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	meta, err := core.GetSnapshotVersion(context.Background(), scope)
	if err != nil || meta.Version != 1 || meta.Reason != backend.RefreshStartup {
		t.Fatalf("startup metadata = %#v, %v", meta, err)
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

	published, err := core.ObserveDockerEvent(backend.AppEvent{
		ResourceType: backend.ResourceContainer,
		ResourceID:   "container-one",
		Action:       "start",
	})
	if err != nil {
		t.Fatalf("observe event: %v", err)
	}
	events := core.RecentDockerEvents(backend.EventFilter{}, 1)
	if len(events) != 1 || events[0].Sequence != published.Sequence || events[0].Action != "start" {
		t.Fatalf("recent events = %#v", events)
	}
}

func TestCoreStartupFailureClosesOwnedClient(t *testing.T) {
	registry := backend.NewLoaderRegistry()
	scope := backend.RefreshScope{Resource: backend.ResourceContainer, View: backend.ViewSummary}
	if err := registry.Register(scope, backend.SnapshotLoaderFunc(func(context.Context, backend.RefreshScope) (any, error) {
		return nil, errors.New("startup failed")
	})); err != nil {
		t.Fatalf("register loader: %v", err)
	}
	owner := testkit.NewRecordingCloser(nil)
	core, err := backend.NewCore(context.Background(), backend.CoreConfig{
		Loaders: registry, StartupScopes: []backend.RefreshScope{scope}, OwnedDockerClient: owner,
	})
	if err == nil || core != nil {
		t.Fatalf("startup result = %#v, %v", core, err)
	}
	if owner.Count() != 1 {
		t.Fatalf("owned client close count = %d, want 1", owner.Count())
	}
}
