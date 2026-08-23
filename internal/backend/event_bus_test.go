package backend_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/test/testkit"
)

func TestEventBusFiltersCopiesAndSequencesEvents(t *testing.T) {
	clock := testkit.NewManualClock(time.Unix(500, 0))
	bus := backend.NewEventBus(backend.EventBusConfig{Clock: clock, SubscriberBuffer: 4})
	t.Cleanup(func() { _ = bus.Close() })
	scope := backend.RefreshScope{Resource: backend.ResourceContainer, View: backend.ViewSummary}
	subscription, err := bus.Subscribe(context.Background(), backend.EventFilter{
		Types:  []backend.EventType{backend.EventSnapshotUpdated},
		Scopes: []backend.RefreshScope{scope},
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if _, err := bus.Publish(backend.AppEvent{Type: backend.EventDockerObserved}); err != nil {
		t.Fatalf("publish filtered event: %v", err)
	}
	attributes := map[string]string{"state": "running"}
	published, err := bus.Publish(backend.AppEvent{
		Type:       backend.EventSnapshotUpdated,
		Scope:      scope,
		Attributes: attributes,
	})
	if err != nil {
		t.Fatalf("publish matching event: %v", err)
	}
	attributes["state"] = "mutated"

	select {
	case event := <-subscription.Events():
		if event.Sequence != 2 || published.Sequence != 2 {
			t.Fatalf("sequence = %d, want 2", event.Sequence)
		}
		if event.Time != clock.Now() {
			t.Fatalf("event time = %v, want %v", event.Time, clock.Now())
		}
		if event.Attributes["state"] != "running" {
			t.Fatalf("event attributes were aliased: %v", event.Attributes)
		}
	default:
		t.Fatal("matching event was not delivered")
	}
}

func TestEventBusSlowSubscriberGetsOverflowWithoutBlockingPublisher(t *testing.T) {
	bus := backend.NewEventBus(backend.EventBusConfig{SubscriberBuffer: 1})
	t.Cleanup(func() { _ = bus.Close() })
	subscription, err := bus.Subscribe(context.Background(), backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	for index := 0; index < 100; index++ {
		if _, err := bus.Publish(backend.AppEvent{Type: backend.EventSnapshotUpdated}); err != nil {
			t.Fatalf("publish %d: %v", index, err)
		}
	}
	event := <-subscription.Events()
	if event.Type != backend.EventOverflow {
		t.Fatalf("slow subscriber event = %q, want overflow", event.Type)
	}
}

func TestEventBusCancellationAndCloseAreIdempotent(t *testing.T) {
	bus := backend.NewEventBus(backend.EventBusConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	subscription, err := bus.Subscribe(ctx, backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	cancel()

	waitForClosed(t, subscription.Events())
	if err := subscription.Close(); err != nil {
		t.Fatalf("second subscription close: %v", err)
	}
	if err := bus.Close(); err != nil {
		t.Fatalf("bus close: %v", err)
	}
	if err := bus.Close(); err != nil {
		t.Fatalf("second bus close: %v", err)
	}
	if _, err := bus.Publish(backend.AppEvent{}); !backend.HasErrorCode(err, backend.ErrorStreamClosed) {
		t.Fatalf("publish after close error = %v", err)
	}
}

func TestEventBusConcurrentPublishPreservesSubscriberSequenceOrder(t *testing.T) {
	const publishers = 100
	bus := backend.NewEventBus(backend.EventBusConfig{SubscriberBuffer: publishers})
	t.Cleanup(func() { _ = bus.Close() })
	subscription, err := bus.Subscribe(context.Background(), backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	var wait sync.WaitGroup
	for range publishers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := bus.Publish(backend.AppEvent{Type: backend.EventDockerObserved}); err != nil {
				t.Errorf("publish: %v", err)
			}
		}()
	}
	wait.Wait()
	for wanted := uint64(1); wanted <= publishers; wanted++ {
		event := <-subscription.Events()
		if event.Sequence != wanted {
			t.Fatalf("event sequence = %d, want %d", event.Sequence, wanted)
		}
	}
}

func waitForClosed(t *testing.T, events <-chan backend.AppEvent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	select {
	case _, open := <-events:
		if open {
			t.Fatal("channel remained open")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for channel closure")
	}
}
