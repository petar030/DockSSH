package eventhub_test

import (
	"context"
	"testing"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/eventhub"
)

func TestHubKeepsPageBusesSeparateAndRecordsDockerEvents(t *testing.T) {
	hub := eventhub.New(eventhub.Config{EventHistoryCapacity: 2})
	t.Cleanup(func() { _ = hub.Close() })
	eventsPage, err := hub.Subscribe(context.Background(), backend.PageEvents, backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe Events: %v", err)
	}
	containersPage, err := hub.Subscribe(context.Background(), backend.PageContainers, backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe Containers: %v", err)
	}
	published, err := hub.RecordDockerEvent(backend.DockerEventObserved{
		Resource: "container", ResourceID: "one", Action: "start",
	})
	if err != nil {
		t.Fatalf("record Docker event: %v", err)
	}
	if event := <-eventsPage.Events(); event.Sequence != published.Sequence {
		t.Fatalf("Events page event = %#v", event)
	}
	select {
	case event := <-containersPage.Events():
		t.Fatalf("raw Docker event leaked to Containers: %#v", event)
	default:
	}
	if recent := hub.Recent(backend.EventFilter{}, 1); len(recent) != 1 || recent[0].Sequence != published.Sequence {
		t.Fatalf("recent events = %#v", recent)
	}
}

func TestHubSubscriptionDoesNotPublishOrPerformWork(t *testing.T) {
	hub := eventhub.New(eventhub.Config{})
	t.Cleanup(func() { _ = hub.Close() })
	subscription, err := hub.Subscribe(context.Background(), backend.PageDashboard, backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	select {
	case event := <-subscription.Events():
		t.Fatalf("subscription produced an event by itself: %#v", event)
	default:
	}
}
