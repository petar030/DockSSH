// Package events implements the runtime-owned recent window for the Events
// tab. Live events use backend.DockerEventObserved on the shared PageEvents bus.
package events

import (
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const (
	RefreshKindRecent  backend.RefreshKind = "events.recent"
	EventRecentUpdated backend.EventType   = "docker_events_recent_updated"
)

// Record is one immutable, normalized Docker event retained by the Event Hub.
// Sequence and ReceivedAt are process-local delivery metadata.
type Record struct {
	Sequence   uint64
	ReceivedAt time.Time
	OccurredAt time.Time
	Resource   string
	ResourceID string
	Project    string
	Action     string
	Attributes map[string]string
}

type RecentUpdated struct {
	Events []Record
}

func (RecentUpdated) EventType() backend.EventType { return EventRecentUpdated }

func (update RecentUpdated) CloneEventPayload() backend.EventPayload {
	update.Events = cloneRecords(update.Events)
	return update
}

func cloneRecords(values []Record) []Record {
	cloned := make([]Record, len(values))
	for index, value := range values {
		value.Attributes = cloneStrings(value.Attributes)
		cloned[index] = value
	}
	return cloned
}

func cloneStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
