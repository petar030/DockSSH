package docker

import (
	"testing"
	"time"

	"github.com/moby/moby/api/types/events"
)

func TestNormalizeDockerEvent(t *testing.T) {
	attributes := map[string]string{composeProjectLabel: "demo", "name": "web"}
	message := events.Message{
		Type: events.ContainerEventType, Action: events.ActionStart,
		Actor:    events.Actor{ID: "container-id", Attributes: attributes},
		TimeNano: time.Unix(1_700_000_000, 123).UnixNano(),
	}
	event := normalizeDockerEvent(message)
	attributes["name"] = "changed"
	if event.Resource != "container" || event.ResourceID != "container-id" || event.Action != "start" {
		t.Fatalf("normalized event = %#v", event)
	}
	if event.Project != "demo" || event.Attributes["name"] != "web" {
		t.Fatalf("normalized attributes = %#v", event)
	}
	if !event.OccurredAt.Equal(time.Unix(1_700_000_000, 123)) {
		t.Fatalf("event time = %v", event.OccurredAt)
	}
}

func TestNormalizeDockerEventFallsBackToSecondTimestamp(t *testing.T) {
	event := normalizeDockerEvent(events.Message{Time: 1_700_000_123})
	if !event.OccurredAt.Equal(time.Unix(1_700_000_123, 0)) {
		t.Fatalf("fallback event time = %v", event.OccurredAt)
	}
}
