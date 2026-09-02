package events

import (
	"context"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const defaultRecentLimit = 100

type eventHistory interface {
	Recent(backend.EventFilter, int) []backend.EventEnvelope
}

// RefreshHandler exposes the Event Hub's bounded Docker-event history through
// the normal Refresh Manager path. It does not own a listener or a second bus.
type RefreshHandler struct {
	history eventHistory
	limit   int
}

func NewRefreshHandler(history eventHistory, limit int) (*RefreshHandler, error) {
	if history == nil || limit < 0 {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Events refresh handler"}
	}
	if limit == 0 {
		limit = defaultRecentLimit
	}
	return &RefreshHandler{history: history, limit: limit}, nil
}

// ReadRefresh is runtime-facing and called only by RefreshManager.
func (handler *RefreshHandler) ReadRefresh(_ context.Context, key backend.RefreshKey) (backend.EventPayload, error) {
	if key.Kind != RefreshKindRecent || key.ID != "" {
		return nil, &backend.AppError{Code: backend.ErrorUnsupported, Operation: "read Events refresh", Resource: string(key.Kind), ID: key.ID}
	}
	envelopes := handler.history.Recent(backend.EventFilter{Types: []backend.EventType{backend.EventDockerObserved}}, handler.limit)
	values := make([]Record, 0, len(envelopes))
	for _, envelope := range envelopes {
		observed, ok := envelope.Payload.(backend.DockerEventObserved)
		if !ok {
			continue
		}
		values = append(values, Record{
			Sequence: envelope.Sequence, ReceivedAt: envelope.Time, OccurredAt: observed.OccurredAt,
			Resource: observed.Resource, ResourceID: observed.ResourceID, Project: observed.Project,
			Action: observed.Action, Attributes: cloneStrings(observed.Attributes),
		})
	}
	return RecentUpdated{Events: values}, nil
}
