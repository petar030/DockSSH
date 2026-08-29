package backend_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/test/testkit"
)

func TestDockerEventListenerReconnectsAndKeepsOneStreamOpen(t *testing.T) {
	clock := testkit.NewManualClock(time.Unix(10, 0))
	source := &scriptedEventSource{calls: make(chan int, 3)}
	received := make(chan backend.DockerEventObserved, 1)
	listener, err := backend.NewDockerEventListener(source, clock, time.Second, func(_ context.Context, event backend.DockerEventObserved) {
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

	select {
	case call := <-source.calls:
		if call != 1 {
			t.Fatalf("first listen call = %d", call)
		}
	case <-waitCtx.Done():
		t.Fatal("first event stream did not open")
	}
	if err := clock.WaitForTickers(waitCtx, 1); err != nil {
		t.Fatalf("wait for reconnect timer: %v", err)
	}
	clock.Advance(time.Second)
	select {
	case call := <-source.calls:
		if call != 2 {
			t.Fatalf("second listen call = %d", call)
		}
	case <-waitCtx.Done():
		t.Fatal("second event stream did not open")
	}
	select {
	case event := <-received:
		if event.ResourceID != "second-stream" {
			t.Fatalf("event = %#v", event)
		}
	case <-waitCtx.Done():
		t.Fatal("reconnected stream did not deliver an event")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("listener shutdown: %v", err)
		}
	case <-waitCtx.Done():
		t.Fatal("listener did not stop")
	}
	if source.MaxActive() != 1 {
		t.Fatalf("maximum active streams = %d, want 1", source.MaxActive())
	}
}

func TestDockerEventPageMapping(t *testing.T) {
	tests := []struct {
		event backend.DockerEventObserved
		want  []backend.Page
	}{
		{backend.DockerEventObserved{Resource: "container"}, []backend.Page{backend.PageDashboard, backend.PageContainers}},
		{backend.DockerEventObserved{Resource: "container", Project: "demo"}, []backend.Page{backend.PageDashboard, backend.PageContainers, backend.PageCompose}},
		{backend.DockerEventObserved{Resource: "image"}, []backend.Page{backend.PageDashboard, backend.PageImages}},
		{backend.DockerEventObserved{Resource: "volume"}, []backend.Page{backend.PageDashboard, backend.PageVolumes}},
		{backend.DockerEventObserved{Resource: "network"}, []backend.Page{backend.PageDashboard, backend.PageNetworks}},
	}
	for _, test := range tests {
		got := backend.PagesAffectedByDockerEvent(test.event)
		if len(got) != len(test.want) {
			t.Fatalf("mapping for %q = %v, want %v", test.event.Resource, got, test.want)
		}
		for index := range got {
			if got[index] != test.want[index] {
				t.Fatalf("mapping for %q = %v, want %v", test.event.Resource, got, test.want)
			}
		}
	}
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
