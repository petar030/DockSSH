package eventhub_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/eventhub"
	"github.com/petar030/ssh-native-docker-tui/test/testkit"
)

func TestBusFiltersCopiesAndSequencesEvents(t *testing.T) {
	clock := testkit.NewManualClock(time.Unix(500, 0))
	bus := eventhub.NewBus(eventhub.BusConfig{Clock: clock, SubscriberBuffer: 4})
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
		Key: key, Reason: backend.RefreshManual,
		Payload: backend.BackendStatusUpdated{APIVersion: "1.48"},
	})
	if err != nil {
		t.Fatalf("publish matching event: %v", err)
	}
	event := <-subscription.Events()
	if event.Sequence != 2 || published.Sequence != 2 || event.Time != clock.Now() {
		t.Fatalf("published event = %#v", event)
	}

	dockerSubscription, err := bus.Subscribe(context.Background(), backend.EventFilter{
		Types: []backend.EventType{backend.EventDockerObserved},
	})
	if err != nil {
		t.Fatalf("subscribe to Docker events: %v", err)
	}
	if _, err := bus.Publish(backend.EventEnvelope{Payload: backend.DockerEventObserved{Attributes: attributes}}); err != nil {
		t.Fatalf("publish Docker event: %v", err)
	}
	attributes["state"] = "mutated"
	dockerEvent := (<-dockerSubscription.Events()).Payload.(backend.DockerEventObserved)
	if dockerEvent.Attributes["state"] != "running" {
		t.Fatalf("payload attributes were aliased: %v", dockerEvent.Attributes)
	}
}

func TestBusSlowSubscriberGetsOverflowAndRemainsUsable(t *testing.T) {
	bus := eventhub.NewBus(eventhub.BusConfig{SubscriberBuffer: 1})
	t.Cleanup(func() { _ = bus.Close() })
	subscription, err := bus.Subscribe(context.Background(), backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	key := backend.RefreshKey{Kind: "backend.status"}
	for range 100 {
		if _, err := bus.Publish(backend.EventEnvelope{Key: key, Payload: backend.BackendStatusUpdated{}}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	event := <-subscription.Events()
	if _, ok := event.Payload.(backend.SubscriberOverflow); !ok {
		t.Fatalf("slow subscriber event = %#v", event)
	}
	if _, err := bus.Publish(backend.EventEnvelope{Key: key, Payload: backend.BackendStatusUpdated{APIVersion: "next"}}); err != nil {
		t.Fatalf("publish after overflow: %v", err)
	}
	if event := <-subscription.Events(); event.Payload.EventType() != backend.EventBackendStatusUpdated {
		t.Fatalf("event after overflow = %#v", event)
	}
}

func TestBusCancellationCloseAndConcurrentOrdering(t *testing.T) {
	const publishers = 100
	bus := eventhub.NewBus(eventhub.BusConfig{SubscriberBuffer: publishers})
	ctx, cancel := context.WithCancel(context.Background())
	canceled, err := bus.Subscribe(ctx, backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe canceled observer: %v", err)
	}
	ordered, err := bus.Subscribe(context.Background(), backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe ordered observer: %v", err)
	}
	cancel()
	waitForClosed(t, canceled.Events())

	var wait sync.WaitGroup
	for range publishers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _ = bus.Publish(backend.EventEnvelope{Payload: backend.BackendStatusUpdated{}})
		}()
	}
	wait.Wait()
	for wanted := uint64(1); wanted <= publishers; wanted++ {
		if event := <-ordered.Events(); event.Sequence != wanted {
			t.Fatalf("event sequence = %d, want %d", event.Sequence, wanted)
		}
	}
	if err := canceled.Close(); err != nil {
		t.Fatalf("idempotent subscription close: %v", err)
	}
	if err := bus.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := bus.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := bus.Publish(backend.EventEnvelope{Payload: backend.BackendStatusUpdated{}}); !backend.HasErrorCode(err, backend.ErrorStreamClosed) {
		t.Fatalf("publish after close error = %v", err)
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
