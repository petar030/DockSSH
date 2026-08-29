package eventhub

import (
	"sync"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// History is a bounded process-local ring of normalized Docker events.
type History struct {
	mu      sync.RWMutex
	entries []backend.EventEnvelope
	start   int
	length  int
}

func NewHistory(capacity int) *History {
	if capacity < 0 {
		capacity = 0
	}
	return &History{entries: make([]backend.EventEnvelope, capacity)}
}

// Add records DockerEventObserved notifications only.
func (history *History) Add(event backend.EventEnvelope) bool {
	if event.Payload == nil || event.Payload.EventType() != backend.EventDockerObserved || len(history.entries) == 0 {
		return false
	}
	event = cloneEnvelope(event)
	history.mu.Lock()
	defer history.mu.Unlock()
	if history.length < len(history.entries) {
		index := (history.start + history.length) % len(history.entries)
		history.entries[index] = event
		history.length++
		return true
	}
	history.entries[history.start] = event
	history.start = (history.start + 1) % len(history.entries)
	return true
}

// Recent returns newest-first copied events matching filter. A non-positive
// limit returns every matching event retained by the history.
func (history *History) Recent(filter backend.EventFilter, limit int) []backend.EventEnvelope {
	history.mu.RLock()
	defer history.mu.RUnlock()
	if limit <= 0 || limit > history.length {
		limit = history.length
	}
	result := make([]backend.EventEnvelope, 0, limit)
	for offset := history.length - 1; offset >= 0 && len(result) < limit; offset-- {
		index := (history.start + offset) % len(history.entries)
		event := history.entries[index]
		if matchesEvent(filter, event) {
			result = append(result, cloneEnvelope(event))
		}
	}
	return result
}

func (history *History) Len() int {
	history.mu.RLock()
	defer history.mu.RUnlock()
	return history.length
}
