package eventhub

import (
	"context"
	"errors"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

var allPages = [...]backend.Page{
	backend.PageDashboard,
	backend.PageContainers,
	backend.PageCompose,
	backend.PageImages,
	backend.PageVolumes,
	backend.PageNetworks,
	backend.PageEvents,
	backend.PageSystem,
}

type Config struct {
	Clock                backend.Clock
	SubscriberBuffer     int
	EventHistoryCapacity int
}

// Hub owns every page bus and the bounded normalized Docker-event history.
// It delivers observations but never invokes page refresh handlers or Docker APIs.
type Hub struct {
	buses   map[backend.Page]*Bus
	history *History
}

func New(config Config) *Hub {
	buses := make(map[backend.Page]*Bus, len(allPages))
	for _, page := range allPages {
		buses[page] = NewBus(BusConfig{
			Clock: config.Clock, SubscriberBuffer: config.SubscriberBuffer,
		})
	}
	return &Hub{buses: buses, history: NewHistory(config.EventHistoryCapacity)}
}

func (hub *Hub) Subscribe(
	ctx context.Context,
	page backend.Page,
	filter backend.EventFilter,
) (backend.Subscription, error) {
	bus, ok := hub.buses[page]
	if !ok {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "subscribe to page", Resource: string(page)}
	}
	return bus.Subscribe(ctx, filter)
}

func (hub *Hub) Publish(page backend.Page, event backend.EventEnvelope) (backend.EventEnvelope, error) {
	bus, ok := hub.buses[page]
	if !ok {
		return backend.EventEnvelope{}, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "publish to page", Resource: string(page)}
	}
	return bus.Publish(event)
}

// RecordDockerEvent publishes one normalized observation to the Events page and
// retains that published envelope in bounded history.
func (hub *Hub) RecordDockerEvent(event backend.DockerEventObserved) (backend.EventEnvelope, error) {
	published, err := hub.Publish(backend.PageEvents, backend.EventEnvelope{
		Time: event.OccurredAt, Reason: backend.RefreshDockerEvent, Payload: event,
	})
	if err != nil {
		return backend.EventEnvelope{}, err
	}
	hub.history.Add(published)
	return published, nil
}

func (hub *Hub) Recent(filter backend.EventFilter, limit int) []backend.EventEnvelope {
	return hub.history.Recent(filter, limit)
}

func (hub *Hub) Close() error {
	var err error
	for _, page := range allPages {
		err = errors.Join(err, hub.buses[page].Close())
	}
	return err
}
