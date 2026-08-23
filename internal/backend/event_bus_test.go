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
	key := backend.RefreshKey{Kind: "backend.status"}
	subscription, err := bus.Subscribe(context.Background(), backend.EventFilter{
		Types: []backend.EventType{backend.EventBackendStatusUpdated},
		Keys:  []backend.RefreshKey{key},
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	attributes := map[string]string{"state": "running"}
	if _, err := bus.Publish(backend.EventEnvelope{Payload: backend.DockerEventObserved{Attributes: attributes}}); err != nil {
		t.Fatalf("publish filtered event: %v", err)
	}
	published, err := bus.Publish(backend.EventEnvelope{
		Key:     key,
		Reason:  backend.RefreshManual,
		Payload: backend.BackendStatusUpdated{APIVersion: "1.48"},
	})
	if err != nil {
		t.Fatalf("publish matching event: %v", err)
	}

	select {
	case event := <-subscription.Events():
		if event.Sequence != 2 || published.Sequence != 2 {
			t.Fatalf("sequence = %d, want 2", event.Sequence)
		}
		if event.Time != clock.Now() || event.Key != key || event.Reason != backend.RefreshManual {
			t.Fatalf("event metadata = %#v", event)
		}
		status, ok := event.Payload.(backend.BackendStatusUpdated)
		if !ok || status.APIVersion != "1.48" {
			t.Fatalf("event payload = %#v", event.Payload)
		}
	default:
		t.Fatal("matching event was not delivered")
	}

	dockerSubscription, err := bus.Subscribe(context.Background(), backend.EventFilter{Types: []backend.EventType{backend.EventDockerObserved}})
	if err != nil {
		t.Fatalf("subscribe to Docker events: %v", err)
	}
	if _, err := bus.Publish(backend.EventEnvelope{Payload: backend.DockerEventObserved{Attributes: attributes}}); err != nil {
		t.Fatalf("publish Docker event: %v", err)
	}
	attributes["state"] = "mutated"
	event := <-dockerSubscription.Events()
	dockerEvent := event.Payload.(backend.DockerEventObserved)
	if dockerEvent.Attributes["state"] != "running" {
		t.Fatalf("payload attributes were aliased: %v", dockerEvent.Attributes)
	}
}

func TestEventBusSlowSubscriberGetsOverflowWithoutBlockingPublisher(t *testing.T) {
	bus := backend.NewEventBus(backend.EventBusConfig{SubscriberBuffer: 1})
	t.Cleanup(func() { _ = bus.Close() })
	subscription, err := bus.Subscribe(context.Background(), backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	key := backend.RefreshKey{Kind: "backend.status"}
	for index := 0; index < 100; index++ {
		if _, err := bus.Publish(backend.EventEnvelope{Key: key, Payload: backend.BackendStatusUpdated{}}); err != nil {
			t.Fatalf("publish %d: %v", index, err)
		}
	}
	event := <-subscription.Events()
	overflow, ok := event.Payload.(backend.SubscriberOverflow)
	if !ok || overflow.DroppedSequence == 0 || event.Key != key {
		t.Fatalf("slow subscriber event = %#v, want overflow", event)
	}

	// The affected subscription remains usable after its caller resynchronizes.
	if _, err := bus.Publish(backend.EventEnvelope{Key: key, Payload: backend.BackendStatusUpdated{APIVersion: "next"}}); err != nil {
		t.Fatalf("publish after overflow: %v", err)
	}
	if event := <-subscription.Events(); event.Payload.EventType() != backend.EventBackendStatusUpdated {
		t.Fatalf("event after overflow = %#v", event)
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
	if _, err := bus.Publish(backend.EventEnvelope{Payload: backend.BackendStatusUpdated{}}); !backend.HasErrorCode(err, backend.ErrorStreamClosed) {
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
			if _, err := bus.Publish(backend.EventEnvelope{Payload: backend.BackendStatusUpdated{}}); err != nil {
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

func waitForClosed(t *testing.T, events <-chan backend.EventEnvelope) {
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
