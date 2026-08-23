package backend

import (
	"context"
	"sync"
)

const defaultSubscriberBuffer = 32

// EventBusConfig configures bounded subscriber delivery.
type EventBusConfig struct {
	Clock            Clock
	SubscriberBuffer int
}

// EventBus is a typed, non-blocking in-process notification transport.
type EventBus struct {
	mu               sync.RWMutex
	subscribers      map[uint64]*eventSubscription
	nextSubscriberID uint64
	nextSequence     uint64
	clock            Clock
	bufferSize       int
	closed           bool
}

func NewEventBus(config EventBusConfig) *EventBus {
	if config.Clock == nil {
		config.Clock = realClock{}
	}
	if config.SubscriberBuffer <= 0 {
		config.SubscriberBuffer = defaultSubscriberBuffer
	}
	return &EventBus{
		subscribers: make(map[uint64]*eventSubscription),
		clock:       config.Clock,
		bufferSize:  config.SubscriberBuffer,
	}
}

// Publish assigns process-local sequence and time values, then offers the
// event to every matching subscriber without blocking.
func (bus *EventBus) Publish(event EventEnvelope) (EventEnvelope, error) {
	if event.Payload == nil || event.Payload.EventType() == "" {
		return EventEnvelope{}, &AppError{Code: ErrorInvalidInput, Operation: "publish event"}
	}

	bus.mu.Lock()
	if bus.closed {
		bus.mu.Unlock()
		return EventEnvelope{}, &AppError{Code: ErrorStreamClosed, Operation: "publish event"}
	}
	bus.nextSequence++
	event.Sequence = bus.nextSequence
	if event.Time.IsZero() {
		event.Time = bus.clock.Now()
	}
	event = cloneEnvelope(event)
	for _, subscription := range bus.subscribers {
		if matchesEvent(subscription.filter, event) {
			subscription.deliver(event)
		}
	}
	bus.mu.Unlock()
	return event, nil
}

// Subscribe registers an independent bounded subscription.
func (bus *EventBus) Subscribe(ctx context.Context, filter EventFilter) (Subscription, error) {
	if ctx == nil {
		return nil, &AppError{Code: ErrorInvalidInput, Operation: "subscribe"}
	}
	if err := ctx.Err(); err != nil {
		return nil, &AppError{Code: ErrorCanceled, Operation: "subscribe", Err: err}
	}

	bus.mu.Lock()
	if bus.closed {
		bus.mu.Unlock()
		return nil, &AppError{Code: ErrorStreamClosed, Operation: "subscribe"}
	}
	bus.nextSubscriberID++
	subscription := &eventSubscription{
		id:     bus.nextSubscriberID,
		bus:    bus,
		filter: cloneFilter(filter),
		events: make(chan EventEnvelope, bus.bufferSize),
		done:   make(chan struct{}),
	}
	bus.subscribers[subscription.id] = subscription
	bus.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			_ = subscription.Close()
		case <-subscription.done:
		}
	}()
	return subscription, nil
}

// Close closes all current subscriptions and rejects later operations.
func (bus *EventBus) Close() error {
	bus.mu.Lock()
	if bus.closed {
		bus.mu.Unlock()
		return nil
	}
	bus.closed = true
	subscribers := make([]*eventSubscription, 0, len(bus.subscribers))
	for _, subscription := range bus.subscribers {
		subscribers = append(subscribers, subscription)
	}
	bus.subscribers = make(map[uint64]*eventSubscription)
	bus.mu.Unlock()

	for _, subscription := range subscribers {
		subscription.closeChannel()
	}
	return nil
}

func (bus *EventBus) unregister(id uint64) {
	bus.mu.Lock()
	delete(bus.subscribers, id)
	bus.mu.Unlock()
}

type eventSubscription struct {
	id        uint64
	bus       *EventBus
	filter    EventFilter
	events    chan EventEnvelope
	done      chan struct{}
	mu        sync.Mutex
	closeOnce sync.Once
}

func (subscription *eventSubscription) Events() <-chan EventEnvelope {
	return subscription.events
}

func (subscription *eventSubscription) Close() error {
	subscription.closeOnce.Do(func() {
		subscription.bus.unregister(subscription.id)
		subscription.closeChannel()
	})
	return nil
}

func (subscription *eventSubscription) closeChannel() {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	select {
	case <-subscription.done:
		return
	default:
		close(subscription.done)
		close(subscription.events)
	}
}

func (subscription *eventSubscription) deliver(event EventEnvelope) {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	select {
	case <-subscription.done:
		return
	default:
	}

	select {
	case subscription.events <- cloneEnvelope(event):
		return
	default:
	}

	// Drop the oldest queued event and retain an explicit recovery signal. A
	// slow observer can keep this subscription and request a fresh visible view.
	select {
	case <-subscription.events:
	default:
	}
	overflow := EventEnvelope{
		Sequence: event.Sequence,
		Time:     event.Time,
		Key:      event.Key,
		Reason:   event.Reason,
		Payload:  SubscriberOverflow{DroppedSequence: event.Sequence},
	}
	select {
	case subscription.events <- overflow:
	default:
	}
}

func matchesEvent(filter EventFilter, event EventEnvelope) bool {
	if event.Payload.EventType() == EventSubscriberOverflow {
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

func cloneFilter(filter EventFilter) EventFilter {
	filter.Types = append([]EventType(nil), filter.Types...)
	filter.Keys = append([]RefreshKey(nil), filter.Keys...)
	return filter
}

func cloneEnvelope(event EventEnvelope) EventEnvelope {
	switch payload := event.Payload.(type) {
	case DockerEventObserved:
		payload.Attributes = cloneAttributes(payload.Attributes)
		event.Payload = payload
	case *DockerEventObserved:
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
	copy := make(map[string]string, len(attributes))
	for key, value := range attributes {
		copy[key] = value
	}
	return copy
}
