package backend

import (
	"context"
	"strconv"
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
func (bus *EventBus) Publish(event AppEvent) (AppEvent, error) {
	bus.mu.Lock()
	if bus.closed {
		bus.mu.Unlock()
		return AppEvent{}, &AppError{Code: ErrorStreamClosed, Operation: "publish event"}
	}
	bus.nextSequence++
	event.Sequence = bus.nextSequence
	if event.Time.IsZero() {
		event.Time = bus.clock.Now()
	}
	event.Attributes = cloneAttributes(event.Attributes)
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
		return nil, &AppError{Code: ErrorInvalidInput, Operation: "subscribe", Err: context.Canceled}
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
		events: make(chan AppEvent, bus.bufferSize),
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
	events    chan AppEvent
	done      chan struct{}
	mu        sync.Mutex
	closeOnce sync.Once
}

func (subscription *eventSubscription) Events() <-chan AppEvent {
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
	select {
	case <-subscription.done:
		subscription.mu.Unlock()
		return
	default:
		close(subscription.done)
		close(subscription.events)
		subscription.mu.Unlock()
	}
}

func (subscription *eventSubscription) deliver(event AppEvent) {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	select {
	case <-subscription.done:
		return
	default:
	}

	event.Attributes = cloneAttributes(event.Attributes)
	select {
	case subscription.events <- event:
		return
	default:
	}

	// Make the overflow observable without blocking the publisher. The oldest
	// pending notification is discarded and replaced by a recovery signal.
	select {
	case <-subscription.events:
	default:
	}
	overflow := AppEvent{
		Sequence: event.Sequence,
		Type:     EventOverflow,
		Time:     event.Time,
		Attributes: map[string]string{
			"dropped_sequence": strconv.FormatUint(event.Sequence, 10),
		},
	}
	select {
	case subscription.events <- overflow:
	default:
	}
}

func matchesEvent(filter EventFilter, event AppEvent) bool {
	if event.Type == EventOverflow {
		return true
	}
	if !contains(filter.Types, event.Type) || !contains(filter.ResourceTypes, event.ResourceType) {
		return false
	}
	if filter.ResourceID != "" && filter.ResourceID != event.ResourceID {
		return false
	}
	if filter.Project != "" && filter.Project != event.Project {
		return false
	}
	if len(filter.Scopes) > 0 {
		matched := false
		for _, scope := range filter.Scopes {
			if scope == event.Scope {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
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
	filter.ResourceTypes = append([]ResourceType(nil), filter.ResourceTypes...)
	filter.Scopes = append([]RefreshScope(nil), filter.Scopes...)
	return filter
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
