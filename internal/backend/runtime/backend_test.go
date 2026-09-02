package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/eventhub"
	"github.com/petar030/ssh-native-docker-tui/test/testkit"
)

func TestBackendSubscribesBeforeRefreshWithoutHiddenWorkAndClosesOwnerOnce(t *testing.T) {
	clock := testkit.NewManualClock(time.Unix(6_000, 0))
	key := backend.RefreshKey{Kind: backend.RefreshKindBackendStatus}
	handler := testkit.NewRecordingRefreshHandler(1, func(context.Context, backend.RefreshKey) (backend.EventPayload, error) {
		return backend.BackendStatusUpdated{APIVersion: "1.48"}, nil
	})
	hub, refreshes, commands := runtimeDependencies(t, backend.PageSystem, key.Kind, handler)
	owner := testkit.NewRecordingCloser(nil)
	application, err := New(context.Background(), Config{
		Clock: clock, EventHub: hub, Refreshes: refreshes, Commands: commands, OwnedDockerClient: owner,
	})
	if err != nil {
		t.Fatalf("new Backend: %v", err)
	}
	if handler.Count() != 0 {
		t.Fatal("Backend performed hidden startup work")
	}
	subscription, err := application.Subscribe(context.Background(), backend.PageSystem, backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if handler.Count() != 0 {
		t.Fatal("subscribing performed refresh work")
	}
	if err := application.RequestRefresh(backend.PageSystem); err != nil {
		t.Fatalf("request refresh: %v", err)
	}
	event := <-subscription.Events()
	if event.Key != key || event.Reason != backend.RefreshManual || event.Sequence == 0 {
		t.Fatalf("refresh event = %#v", event)
	}
	if err := application.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := application.Close(context.Background()); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if owner.Count() != 1 {
		t.Fatalf("owner close count = %d, want 1", owner.Count())
	}
}

func TestBackendConstructionFailureClosesOwnedClient(t *testing.T) {
	hub, refreshes, commands := runtimeDependencies(
		t,
		backend.PageSystem,
		backend.RefreshKindBackendStatus,
		testkit.NewRecordingRefreshHandler(0, func(context.Context, backend.RefreshKey) (backend.EventPayload, error) {
			return backend.BackendStatusUpdated{}, nil
		}),
	)
	owner := testkit.NewRecordingCloser(nil)
	application, err := New(context.Background(), Config{
		EventHub: hub, Refreshes: refreshes, Commands: commands, OwnedDockerClient: owner,
		RefreshPolicies: []backend.RefreshPolicy{{Key: backend.RefreshKey{Kind: backend.RefreshKindBackendStatus}}},
	})
	if err == nil || application != nil {
		t.Fatalf("construction result = %#v, %v", application, err)
	}
	if owner.Count() != 1 {
		t.Fatalf("owner close count = %d, want 1", owner.Count())
	}
}

func TestDockerEventPublishesRawObservationAndRefreshesDashboardIndependently(t *testing.T) {
	key := backend.RefreshKey{Kind: "dashboard.summary"}
	handler := testkit.NewRecordingRefreshHandler(1, func(context.Context, backend.RefreshKey) (backend.EventPayload, error) {
		return backend.BackendStatusUpdated{APIVersion: "dashboard"}, nil
	})
	hub, refreshes, commands := runtimeDependencies(t, backend.PageDashboard, key.Kind, handler)
	source := &oneEventSource{
		release: make(chan struct{}),
		event: backend.DockerEventObserved{
			Resource: "volume", ResourceID: "volume-one", Action: "create",
		},
	}
	application, err := New(context.Background(), Config{
		EventHub: hub, Refreshes: refreshes, Commands: commands, DockerEvents: source,
	})
	if err != nil {
		t.Fatalf("new Backend: %v", err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })
	eventsPage, _ := application.Subscribe(context.Background(), backend.PageEvents, backend.EventFilter{})
	dashboardPage, _ := application.Subscribe(context.Background(), backend.PageDashboard, backend.EventFilter{})
	close(source.release)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	raw := receive(t, ctx, eventsPage.Events())
	if observed := raw.Payload.(backend.DockerEventObserved); observed.ResourceID != "volume-one" {
		t.Fatalf("raw event = %#v", observed)
	}
	updated := receive(t, ctx, dashboardPage.Events())
	if updated.Reason != backend.RefreshDockerEvent || updated.Key != key {
		t.Fatalf("Dashboard update = %#v", updated)
	}
	if recent := hub.Recent(backend.EventFilter{}, 1); len(recent) != 1 || recent[0].Sequence != raw.Sequence {
		t.Fatalf("event history = %#v", recent)
	}
}

func TestDockerEventsRemainReadableWhileDashboardRefreshIsBlocked(t *testing.T) {
	gate := testkit.NewGate()
	handler := testkit.NewRecordingRefreshHandler(2, func(ctx context.Context, _ backend.RefreshKey) (backend.EventPayload, error) {
		if err := gate.Block(ctx); err != nil {
			return nil, err
		}
		return backend.BackendStatusUpdated{}, nil
	})
	hub, refreshes, commands := runtimeDependencies(t, backend.PageDashboard, "dashboard.summary", handler)
	source := &burstEventSource{release: make(chan struct{}), started: make(chan struct{})}
	application, err := New(context.Background(), Config{
		EventHub: hub, Refreshes: refreshes, Commands: commands, DockerEvents: source,
	})
	if err != nil {
		t.Fatalf("new Backend: %v", err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })
	subscription, err := application.Subscribe(context.Background(), backend.PageEvents, backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe Events: %v", err)
	}
	close(source.release)
	<-source.started
	first := <-subscription.Events()
	second := <-subscription.Events()
	if first.Payload.(backend.DockerEventObserved).ResourceID != "one" ||
		second.Payload.(backend.DockerEventObserved).ResourceID != "two" {
		t.Fatalf("events = %#v, %#v", first, second)
	}
	gate.Release()
}

func TestDockerEventListenerReconnectsAndKeepsOneStreamOpen(t *testing.T) {
	clock := testkit.NewManualClock(time.Unix(10, 0))
	source := &scriptedEventSource{calls: make(chan int, 3)}
	received := make(chan backend.DockerEventObserved, 1)
	listener, err := newDockerEventListener(source, clock, time.Second, func(_ context.Context, event backend.DockerEventObserved) {
		received <- event
	})
	if err != nil {
		t.Fatalf("new listener: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if call := <-source.calls; call != 1 {
		t.Fatalf("first call = %d", call)
	}
	if err := clock.WaitForTickers(waitCtx, 1); err != nil {
		t.Fatalf("wait reconnect ticker: %v", err)
	}
	clock.Advance(time.Second)
	if call := <-source.calls; call != 2 {
		t.Fatalf("second call = %d", call)
	}
	if event := <-received; event.ResourceID != "second-stream" {
		t.Fatalf("reconnected event = %#v", event)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("listener shutdown: %v", err)
	}
	if source.MaxActive() != 1 {
		t.Fatalf("maximum active streams = %d", source.MaxActive())
	}
}

func TestDockerEventPageMapping(t *testing.T) {
	tests := []struct {
		event backend.DockerEventObserved
		want  []backend.Page
	}{
		{backend.DockerEventObserved{Resource: "container"}, []backend.Page{backend.PageDashboard, backend.PageContainers}},
		{backend.DockerEventObserved{Resource: "container", Action: "create"}, []backend.Page{backend.PageDashboard, backend.PageContainers, backend.PageVolumes, backend.PageNetworks}},
		{backend.DockerEventObserved{Resource: "container", Action: "destroy"}, []backend.Page{backend.PageDashboard, backend.PageContainers, backend.PageVolumes, backend.PageNetworks}},
		{backend.DockerEventObserved{Resource: "container", Project: "demo"}, []backend.Page{backend.PageDashboard, backend.PageContainers, backend.PageCompose}},
		{backend.DockerEventObserved{Resource: "image"}, []backend.Page{backend.PageDashboard, backend.PageImages}},
		{backend.DockerEventObserved{Resource: "volume"}, []backend.Page{backend.PageDashboard, backend.PageVolumes}},
		{backend.DockerEventObserved{Resource: "network"}, []backend.Page{backend.PageDashboard, backend.PageNetworks}},
		{backend.DockerEventObserved{Resource: "network", Action: "connect"}, []backend.Page{backend.PageDashboard, backend.PageNetworks, backend.PageContainers}},
		{backend.DockerEventObserved{Resource: "network", Action: "disconnect"}, []backend.Page{backend.PageDashboard, backend.PageNetworks, backend.PageContainers}},
	}
	for _, test := range tests {
		got := PagesAffectedByDockerEvent(test.event)
		if len(got) != len(test.want) {
			t.Fatalf("mapping %q = %v, want %v", test.event.Resource, got, test.want)
		}
		for index := range got {
			if got[index] != test.want[index] {
				t.Fatalf("mapping %q = %v, want %v", test.event.Resource, got, test.want)
			}
		}
	}
}

func runtimeDependencies(
	t *testing.T,
	page backend.Page,
	kind backend.RefreshKind,
	handler interface {
		ReadRefresh(context.Context, backend.RefreshKey) (backend.EventPayload, error)
	},
) (*eventhub.Hub, *RefreshManager, *CommandExecutor) {
	t.Helper()
	hub := eventhub.New(eventhub.Config{EventHistoryCapacity: 4})
	manager, err := NewRefreshManager(RefreshManagerConfig{Publisher: hub, Timeout: time.Second})
	if err != nil {
		t.Fatalf("new Refresh Manager: %v", err)
	}
	if err := manager.RegisterPage(page, kind, handler.ReadRefresh); err != nil {
		t.Fatalf("register page: %v", err)
	}
	commands, err := NewCommandExecutor(CommandExecutorConfig{Refreshes: manager, Workers: 1, QueueCapacity: 2, Timeout: time.Second})
	if err != nil {
		t.Fatalf("new Command Executor: %v", err)
	}
	return hub, manager, commands
}

func receive(t *testing.T, ctx context.Context, events <-chan backend.EventEnvelope) backend.EventEnvelope {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-ctx.Done():
		t.Fatalf("receive event: %v", ctx.Err())
		return backend.EventEnvelope{}
	}
}

type oneEventSource struct {
	once    sync.Once
	release chan struct{}
	event   backend.DockerEventObserved
}

type burstEventSource struct {
	once    sync.Once
	release chan struct{}
	started chan struct{}
}

func (source *burstEventSource) Listen(ctx context.Context, handle func(backend.DockerEventObserved)) error {
	source.once.Do(func() {
		<-source.release
		handle(backend.DockerEventObserved{Resource: "unknown", ResourceID: "one"})
		handle(backend.DockerEventObserved{Resource: "unknown", ResourceID: "two"})
		close(source.started)
	})
	<-ctx.Done()
	return ctx.Err()
}

func (source *oneEventSource) Listen(ctx context.Context, handle func(backend.DockerEventObserved)) error {
	source.once.Do(func() {
		<-source.release
		handle(source.event)
	})
	<-ctx.Done()
	return ctx.Err()
}

type scriptedEventSource struct {
	mu        sync.Mutex
	callCount int
	active    int
	maxActive int
	calls     chan int
}

func (source *scriptedEventSource) Listen(ctx context.Context, handle func(backend.DockerEventObserved)) error {
	source.mu.Lock()
	source.callCount++
	call := source.callCount
	source.active++
	if source.active > source.maxActive {
		source.maxActive = source.active
	}
	source.mu.Unlock()
	source.calls <- call
	defer func() {
		source.mu.Lock()
		source.active--
		source.mu.Unlock()
	}()
	if call == 1 {
		return errors.New("stream interrupted")
	}
	handle(backend.DockerEventObserved{Resource: "container", ResourceID: "second-stream"})
	<-ctx.Done()
	return ctx.Err()
}

func (source *scriptedEventSource) MaxActive() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.maxActive
}
