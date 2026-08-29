package docker

import (
	"context"
	"io"
	"maps"
	"sync"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const composeProjectLabel = "com.docker.compose.project"

// mobyEventSource adapts the shared Moby client's event stream to the small
// normalized event contract understood by the backend.
type mobyEventSource struct {
	client *client.Client
	ready  chan struct{}
	once   sync.Once
}

func newMobyEventSource(client *client.Client) *mobyEventSource {
	return &mobyEventSource{client: client, ready: make(chan struct{})}
}

func (source *mobyEventSource) Listen(ctx context.Context, handle func(backend.DockerEventObserved)) error {
	stream := source.client.Events(ctx, client.EventsListOptions{})
	source.once.Do(func() { close(source.ready) })
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message, open := <-stream.Messages:
			if !open {
				return io.EOF
			}
			handle(normalizeDockerEvent(message))
		case err, open := <-stream.Err:
			if !open || err == nil {
				return io.EOF
			}
			return err
		}
	}
}

func (source *mobyEventSource) waitReady(ctx context.Context) error {
	select {
	case <-source.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func normalizeDockerEvent(message events.Message) backend.DockerEventObserved {
	occurredAt := time.Time{}
	if message.TimeNano != 0 {
		occurredAt = time.Unix(0, message.TimeNano)
	} else if message.Time != 0 {
		occurredAt = time.Unix(message.Time, 0)
	}
	return backend.DockerEventObserved{
		OccurredAt: occurredAt,
		Resource:   string(message.Type),
		ResourceID: message.Actor.ID,
		Project:    message.Actor.Attributes[composeProjectLabel],
		Action:     string(message.Action),
		Attributes: maps.Clone(message.Actor.Attributes),
	}
}
