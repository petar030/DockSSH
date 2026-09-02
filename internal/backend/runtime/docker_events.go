package runtime

import (
	"context"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const defaultDockerEventRetryInterval = time.Second

// DockerEventSource opens the Docker daemon event stream and calls handle for
// every normalized event. Listen returns when the stream fails or ctx ends.
type DockerEventSource interface {
	Listen(context.Context, func(backend.DockerEventObserved)) error
}

// dockerEventListener owns the Backend's single daemon event stream and
// reconnects after transient failures.
type dockerEventListener struct {
	source        DockerEventSource
	clock         backend.Clock
	retryInterval time.Duration
	handle        func(context.Context, backend.DockerEventObserved)
}

func newDockerEventListener(
	source DockerEventSource,
	clock backend.Clock,
	retryInterval time.Duration,
	handle func(context.Context, backend.DockerEventObserved),
) (*dockerEventListener, error) {
	if source == nil || clock == nil || handle == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Docker event listener"}
	}
	if retryInterval <= 0 {
		retryInterval = defaultDockerEventRetryInterval
	}
	return &dockerEventListener{
		source: source, clock: clock, retryInterval: retryInterval, handle: handle,
	}, nil
}

func (listener *dockerEventListener) Run(ctx context.Context) error {
	if ctx == nil {
		return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "run Docker event listener"}
	}
	for {
		_ = listener.source.Listen(ctx, func(event backend.DockerEventObserved) {
			listener.handle(ctx, event)
		})
		if ctx.Err() != nil {
			return nil
		}

		retry := listener.clock.NewTicker(listener.retryInterval)
		select {
		case <-ctx.Done():
			retry.Stop()
			return nil
		case <-retry.C():
			retry.Stop()
		}
	}
}

// PagesAffectedByDockerEvent maps a daemon resource event to base page
// refreshes. Detailed action-specific mapping is extended with later slices.
func PagesAffectedByDockerEvent(event backend.DockerEventObserved) []backend.Page {
	pages := []backend.Page{backend.PageDashboard}
	switch event.Resource {
	case "container":
		pages = append(pages, backend.PageContainers)
		if event.Project != "" {
			pages = append(pages, backend.PageCompose)
		}
		// Container creation and destruction change the authoritative reverse
		// view of which containers are attached to named volumes.
		if event.Action == "create" || event.Action == "destroy" {
			pages = append(pages, backend.PageVolumes, backend.PageNetworks)
		}
	case "image", "builder":
		pages = append(pages, backend.PageImages)
	case "volume":
		pages = append(pages, backend.PageVolumes)
	case "network":
		pages = append(pages, backend.PageNetworks)
		if event.Action == "connect" || event.Action == "disconnect" {
			pages = append(pages, backend.PageContainers)
		}
	case "daemon":
		pages = append(pages, backend.PageSystem)
	}
	return pages
}
