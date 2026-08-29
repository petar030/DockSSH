package eventhub

import (
	"context"
	"sync"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const defaultSubscriberBuffer = 32

// BusConfig configures one page's bounded subscriber delivery.
type BusConfig struct {
	Clock            backend.Clock
	SubscriberBuffer int
}

// Bus is a typed, non-blocking in-process notification transport for one page.
type Bus struct {
	mu               sync.RWMutex
	subscribers      map[uint64]*subscription
	nextSubscriberID uint64
	nextSequence     uint64
	clock            backend.Clock
	bufferSize       int
	closed           bool
}

func NewBus(config BusConfig) *Bus {
	if config.Clock == nil {
		config.Clock = backend.NewRealClock()
	}
	if config.SubscriberBuffer <= 0 {
		config.SubscriberBuffer = defaultSubscriberBuffer
	}
	return &Bus{
		subscribers: make(map[uint64]*subscription),
		clock:       config.Clock,
		bufferSize:  config.SubscriberBuffer,
	}
}

// Publish assigns page-local sequence and time values, then offers the event
// to every matching subscriber without blocking.
func (bus *Bus) Publish(event backend.EventEnvelope) (backend.EventEnvelope, error) {
	if event.Payload == nil || event.Payload.EventType() == "" {
		return backend.EventEnvelope{}, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "publish event"}
	}

	bus.mu.Lock()
	if bus.closed {
		bus.mu.Unlock()
		return backend.EventEnvelope{}, &backend.AppError{Code: backend.ErrorStreamClosed, Operation: "publish event"}
	}
	bus.nextSequence++
	event.Sequence = bus.nextSequence
	if event.Time.IsZero() {
		event.Time = bus.clock.Now()
	}
	event = cloneEnvelope(event)
	for _, subscriber := range bus.subscribers {
		if matchesEvent(subscriber.filter, event) {
			subscriber.deliver(event)
		}
	}
	bus.mu.Unlock()
	return event, nil
}

// Subscribe registers an independent bounded subscription.
func (bus *Bus) Subscribe(ctx context.Context, filter backend.EventFilter) (backend.Subscription, error) {
	if ctx == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "subscribe"}
	}
	if err := ctx.Err(); err != nil {
		return nil, &backend.AppError{Code: backend.ErrorCanceled, Operation: "subscribe", Err: err}
	}

	bus.mu.Lock()
	if bus.closed {
		bus.mu.Unlock()
		return nil, &backend.AppError{Code: backend.ErrorStreamClosed, Operation: "subscribe"}
	}
	bus.nextSubscriberID++
	subscriber := &subscription{
		id:     bus.nextSubscriberID,
		bus:    bus,
		filter: cloneFilter(filter),
		events: make(chan backend.EventEnvelope, bus.bufferSize),
		done:   make(chan struct{}),
	}
	bus.subscribers[subscriber.id] = subscriber
	bus.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			_ = subscriber.Close()
		case <-subscriber.done:
		}
	}()
	return subscriber, nil
}

// Close closes all current subscriptions and rejects later operations.
func (bus *Bus) Close() error {
	bus.mu.Lock()
	if bus.closed {
		bus.mu.Unlock()
		return nil
	}
	bus.closed = true
	subscribers := make([]*subscription, 0, len(bus.subscribers))
	for _, subscriber := range bus.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	bus.subscribers = make(map[uint64]*subscription)
	bus.mu.Unlock()

	for _, subscriber := range subscribers {
		subscriber.closeChannel()
	}
	return nil
}

func (bus *Bus) unregister(id uint64) {
	bus.mu.Lock()
	delete(bus.subscribers, id)
	bus.mu.Unlock()
}

type subscription struct {
	id        uint64
	bus       *Bus
	filter    backend.EventFilter
	events    chan backend.EventEnvelope
	done      chan struct{}
	mu        sync.Mutex
	closeOnce sync.Once
}

func (subscriber *subscription) Events() <-chan backend.EventEnvelope {
	return subscriber.events
}

func (subscriber *subscription) Close() error {
	subscriber.closeOnce.Do(func() {
		subscriber.bus.unregister(subscriber.id)
		subscriber.closeChannel()
	})
	return nil
}

func (subscriber *subscription) closeChannel() {
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()
	select {
	case <-subscriber.done:
		return
	default:
		close(subscriber.done)
		close(subscriber.events)
	}
}

func (subscriber *subscription) deliver(event backend.EventEnvelope) {
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()
	select {
	case <-subscriber.done:
		return
	default:
	}

	select {
	case subscriber.events <- cloneEnvelope(event):
		return
	default:
	}

	select {
	case <-subscriber.events:
	default:
	}
	overflow := backend.EventEnvelope{
		Sequence: event.Sequence,
		Time:     event.Time,
		Key:      event.Key,
		Reason:   event.Reason,
		Payload:  backend.SubscriberOverflow{DroppedSequence: event.Sequence},
	}
	select {
	case subscriber.events <- overflow:
	default:
	}
}

func matchesEvent(filter backend.EventFilter, event backend.EventEnvelope) bool {
	if event.Payload.EventType() == backend.EventSubscriberOverflow {
		return true
	}
	return contains(filter.Types, event.Payload.EventType()) && contains(filter.Keys, event.Key)
}

func contains[T comparable](allowed []T, value T) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if candidate == value {
			return true
		}
	}
	return false
}

func cloneFilter(filter backend.EventFilter) backend.EventFilter {
	filter.Types = append([]backend.EventType(nil), filter.Types...)
	filter.Keys = append([]backend.RefreshKey(nil), filter.Keys...)
	return filter
}

func cloneEnvelope(event backend.EventEnvelope) backend.EventEnvelope {
	if payload, ok := event.Payload.(interface{ CloneEventPayload() backend.EventPayload }); ok {
		event.Payload = payload.CloneEventPayload()
		return event
	}
	switch payload := event.Payload.(type) {
	case backend.DockerEventObserved:
		payload.Attributes = cloneAttributes(payload.Attributes)
		event.Payload = payload
	case *backend.DockerEventObserved:
		if payload != nil {
			copy := *payload
			copy.Attributes = cloneAttributes(payload.Attributes)
			event.Payload = &copy
		}
	}
	return event
}

func cloneAttributes(attributes map[string]string) map[string]string {
	if attributes == nil {
		return nil
	}
	cloned := make(map[string]string, len(attributes))
	for key, value := range attributes {
		cloned[key] = value
	}
	return cloned
}
