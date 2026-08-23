// Package backendtest contains the reusable conformance suite for every
// implementation of the shared backend contracts.
package backendtest

import (
	"context"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
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

// BackendFactory constructs the real production backend and returns only after
// startup synchronization has completed.
type BackendFactory func(context.Context, IntegrationEnvironment) (backend.Backend, error)
