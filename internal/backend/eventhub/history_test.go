package eventhub_test

import (
	"testing"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/eventhub"
)

func TestHistoryEvictsOldestAndReturnsCopiedNewestFirst(t *testing.T) {
	history := eventhub.NewHistory(2)
	for sequence := uint64(1); sequence <= 3; sequence++ {
		if !history.Add(backend.EventEnvelope{
			Sequence: sequence,
			Payload: backend.DockerEventObserved{
				Resource: "container", Attributes: map[string]string{"value": "original"},
			},
		}) {
			t.Fatalf("event %d was not accepted", sequence)
		}
	}
	events := history.Recent(backend.EventFilter{}, 0)
	if history.Len() != 2 || len(events) != 2 || events[0].Sequence != 3 || events[1].Sequence != 2 {
		t.Fatalf("recent events = %#v", events)
	}
	events[0].Payload.(backend.DockerEventObserved).Attributes["value"] = "mutated"
	again := history.Recent(backend.EventFilter{}, 1)
	if again[0].Payload.(backend.DockerEventObserved).Attributes["value"] != "original" {
		t.Fatal("history returned aliased attributes")
	}
}

func TestHistoryRejectsNonDockerEventsAndZeroCapacity(t *testing.T) {
	if eventhub.NewHistory(0).Add(backend.EventEnvelope{Payload: backend.DockerEventObserved{}}) {
		t.Fatal("zero-capacity history accepted an event")
	}
	history := eventhub.NewHistory(1)
	if history.Add(backend.EventEnvelope{Payload: backend.BackendStatusUpdated{}}) {
		t.Fatal("history accepted a non-Docker event")
	}
}
