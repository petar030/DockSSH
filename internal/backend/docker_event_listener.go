package backend

import (
	"context"
	"time"
)

const defaultDockerEventRetryInterval = time.Second

// DockerEventSource opens the Docker daemon event stream and calls handle for
// every normalized event. Listen returns when the stream fails or ctx ends.
type DockerEventSource interface {
	Listen(context.Context, func(DockerEventObserved)) error
}

// DockerEventListener owns the single daemon event stream for the process and
// reconnects after transient stream failures.
type DockerEventListener struct {
	source        DockerEventSource
	clock         Clock
	retryInterval time.Duration
	handle        func(context.Context, DockerEventObserved)
}

func NewDockerEventListener(
	source DockerEventSource,
	clock Clock,
	retryInterval time.Duration,
	handle func(context.Context, DockerEventObserved),
) (*DockerEventListener, error) {
	if source == nil || clock == nil || handle == nil {
		return nil, &AppError{Code: ErrorInvalidInput, Operation: "create Docker event listener"}
	}
	if retryInterval <= 0 {
		retryInterval = defaultDockerEventRetryInterval
	}
	return &DockerEventListener{
		source: source, clock: clock, retryInterval: retryInterval, handle: handle,
	}, nil
}

// Run listens until ctx is canceled. A failed stream is reopened after the
// configured delay; there is never more than one open stream at a time.
func (listener *DockerEventListener) Run(ctx context.Context) error {
	if ctx == nil {
		return &AppError{Code: ErrorInvalidInput, Operation: "run Docker event listener"}
	}
	for {
		_ = listener.source.Listen(ctx, func(event DockerEventObserved) {
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
func PagesAffectedByDockerEvent(event DockerEventObserved) []Page {
	pages := []Page{PageDashboard}
	switch event.Resource {
	case "container":
		pages = append(pages, PageContainers)
		if event.Project != "" {
			pages = append(pages, PageCompose)
		}
	case "image", "builder":
		pages = append(pages, PageImages)
	case "volume":
		pages = append(pages, PageVolumes)
	case "network":
		pages = append(pages, PageNetworks)
	case "daemon":
		pages = append(pages, PageSystem)
	}
	return pages
}
