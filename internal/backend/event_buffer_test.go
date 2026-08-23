package backend_test

import (
	"testing"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func TestEventBufferEvictsOldestAndReturnsCopiedNewestFirst(t *testing.T) {
	buffer := backend.NewEventBuffer(2)
	for sequence := uint64(1); sequence <= 3; sequence++ {
		if !buffer.Add(backend.AppEvent{
			Sequence:     sequence,
			Type:         backend.EventDockerObserved,
			ResourceType: backend.ResourceContainer,
			Attributes:   map[string]string{"value": "original"},
		}) {
			t.Fatalf("event %d was not accepted", sequence)
		}
	}
	if buffer.Len() != 2 {
		t.Fatalf("buffer length = %d, want 2", buffer.Len())
	}

	events := buffer.Recent(backend.EventFilter{ResourceTypes: []backend.ResourceType{backend.ResourceContainer}}, 0)
	if len(events) != 2 || events[0].Sequence != 3 || events[1].Sequence != 2 {
		t.Fatalf("recent events = %#v", events)
	}
	events[0].Attributes["value"] = "mutated"
	again := buffer.Recent(backend.EventFilter{}, 1)
	if again[0].Attributes["value"] != "original" {
		t.Fatalf("buffer attributes were aliased: %v", again[0].Attributes)
	}
}

func TestEventBufferRejectsNonDockerEventsAndZeroCapacity(t *testing.T) {
	buffer := backend.NewEventBuffer(0)
	if buffer.Add(backend.AppEvent{Type: backend.EventDockerObserved}) {
		t.Fatal("zero-capacity buffer accepted an event")
	}
	buffer = backend.NewEventBuffer(1)
	if buffer.Add(backend.AppEvent{Type: backend.EventSnapshotUpdated}) {
		t.Fatal("buffer accepted a non-Docker event")
	}
}
