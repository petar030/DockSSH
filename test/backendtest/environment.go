// Package backendtest contains the reusable conformance suite for every
// implementation of the shared backend contracts.
package backendtest

import (
	"context"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	composepage "github.com/petar030/ssh-native-docker-tui/internal/backend/compose"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/images"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/networks"
	systempage "github.com/petar030/ssh-native-docker-tui/internal/backend/system"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/volumes"
)

// IntegrationEnvironment describes the isolated resources available to a
// production BackendFactory. Maps passed through this type must be treated as
// read-only by factories.
type IntegrationEnvironment struct {
	DockerEndpoint  string
	ComposeRoot     string
	ResourcePrefix  string
	ResourceLabels  map[string]string
	Timeout         time.Duration
	DedicatedDaemon bool
}

// BackendFactory constructs the real production backend. Tests subscribe before
// requesting any initial data.
type ConformanceBackend interface {
	backend.Backend
	Containers() *containers.API
	Compose() *composepage.API
	Images() *images.API
	Volumes() *volumes.API
	Networks() *networks.API
	System() *systempage.API
}

type BackendFactory func(context.Context, IntegrationEnvironment) (ConformanceBackend, error)
