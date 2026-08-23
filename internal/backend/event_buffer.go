package backend

import "sync"

// EventBuffer is a bounded process-local ring of normalized Docker events.
type EventBuffer struct {
	mu      sync.RWMutex
	entries []EventEnvelope
	start   int
	length  int
}

func NewEventBuffer(capacity int) *EventBuffer {
	if capacity < 0 {
		capacity = 0
	}
	return &EventBuffer{entries: make([]EventEnvelope, capacity)}
}

// Add records DockerEventObserved notifications only.
func (buffer *EventBuffer) Add(event EventEnvelope) bool {
	if event.Payload == nil || event.Payload.EventType() != EventDockerObserved || len(buffer.entries) == 0 {
		return false
	}
	event = cloneEnvelope(event)
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.length < len(buffer.entries) {
		index := (buffer.start + buffer.length) % len(buffer.entries)
		buffer.entries[index] = event
		buffer.length++
		return true
	}
	buffer.entries[buffer.start] = event
	buffer.start = (buffer.start + 1) % len(buffer.entries)
	return true
}

// Recent returns newest-first copied events matching filter. A non-positive
// limit returns every matching event retained by the buffer.
func (buffer *EventBuffer) Recent(filter EventFilter, limit int) []EventEnvelope {
	buffer.mu.RLock()
	defer buffer.mu.RUnlock()
	if limit <= 0 || limit > buffer.length {
		limit = buffer.length
	}
	result := make([]EventEnvelope, 0, limit)
	for offset := buffer.length - 1; offset >= 0 && len(result) < limit; offset-- {
		index := (buffer.start + offset) % len(buffer.entries)
		event := buffer.entries[index]
		if matchesEvent(filter, event) {
			result = append(result, cloneEnvelope(event))
		}
	}
	return result
}

func (buffer *EventBuffer) Len() int {
	buffer.mu.RLock()
	defer buffer.mu.RUnlock()
	return buffer.length
}
