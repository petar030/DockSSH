// Package docker wires the external SDK handles owned by application bootstrap
// and provides Docker-backed loaders to the shared backend.
package docker

import (
	"context"
	"fmt"

	"github.com/docker/cli/cli/command"
	cliflags "github.com/docker/cli/cli/flags"
	composeapi "github.com/docker/compose/v5/pkg/api"
	composeservice "github.com/docker/compose/v5/pkg/compose"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

// Dependencies groups the SDK objects shared by backend services. Bootstrap
// will create and close Client; individual services only borrow these handles.
type Dependencies struct {
	Client     *client.Client
	DockerCLI  *command.DockerCli
	ComposeAPI composeapi.Compose
}

// NewDependencies creates the one Moby client owned by the process and injects
// the same instance into the Docker CLI object used by Compose.
func NewDependencies(endpoint string) (Dependencies, error) {
	options := []client.Opt{client.FromEnv}
	if endpoint != "" {
		options = append(options, client.WithHost(endpoint))
	}
	mobyClient, err := client.New(options...)
	if err != nil {
		return Dependencies{}, fmt.Errorf("create Moby client: %w", err)
	}
	dockerCLI, err := command.NewDockerCli()
	if err != nil {
		_ = mobyClient.Close()
		return Dependencies{}, fmt.Errorf("create Docker CLI: %w", err)
	}
	if err := dockerCLI.Initialize(cliflags.NewClientOptions(), command.WithAPIClient(mobyClient)); err != nil {
		_ = mobyClient.Close()
		return Dependencies{}, fmt.Errorf("initialize Docker CLI: %w", err)
	}
	composeAPI, err := composeservice.NewComposeService(dockerCLI)
	if err != nil {
		_ = mobyClient.Close()
		return Dependencies{}, fmt.Errorf("create Compose service: %w", err)
	}
	return Dependencies{Client: mobyClient, DockerCLI: dockerCLI, ComposeAPI: composeAPI}, nil
}

// UsesSharedClient verifies the ownership invariant without inspecting Moby
// client internals.
func (dependencies Dependencies) UsesSharedClient() bool {
	return dependencies.Client != nil && dependencies.DockerCLI != nil &&
		dependencies.DockerCLI.Client() == dependencies.Client
}

// EngineEvent is the supported Moby event message used by the future event
// normalizer.
type EngineEvent = events.Message

// BackendConfig controls the production shared backend foundation.
type BackendConfig struct {
	Endpoint            string
	Clock               backend.Clock
	EventBufferCapacity int
	RefreshPolicies     []backend.RefreshPolicy
}

// Application retains all SDK handles while exposing the shared backend API.
type Application struct {
	*backend.Core
	dependencies Dependencies
}

// NewBackend constructs the production observer backend. Sessions subscribe
// before requesting the initial status they need.
func NewBackend(ctx context.Context, config BackendConfig) (*Application, error) {
	dependencies, err := NewDependencies(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if config.EventBufferCapacity == 0 {
		config.EventBufferCapacity = 256
	}

	registry := backend.NewLoaderRegistry()
	eventBuffer := backend.NewEventBuffer(config.EventBufferCapacity)
	dashboardLoader, err := dashboard.NewLoader(dependencies.Client, eventBuffer)
	if err != nil {
		_ = dependencies.Client.Close()
		return nil, err
	}
	if err := registry.RegisterPageRefresh(backend.PageDashboard, dashboard.RefreshKindSummary, dashboardLoader); err != nil {
		_ = dependencies.Client.Close()
		return nil, err
	}
	if err := registry.RegisterPageRefresh(backend.PageSystem, backend.RefreshKindBackendStatus, backend.RefreshLoaderFunc(func(ctx context.Context, _ backend.RefreshKey) (backend.EventPayload, error) {
		result, err := dependencies.Client.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
		if err != nil {
			return nil, err
		}
		return backend.BackendStatusUpdated{
			APIVersion: result.APIVersion, OSType: result.OSType, Experimental: result.Experimental,
		}, nil
	})); err != nil {
		_ = dependencies.Client.Close()
		return nil, err
	}

	eventSource := newMobyEventSource(dependencies.Client)
	core, err := backend.NewCore(ctx, backend.CoreConfig{
		Clock:             config.Clock,
		Loaders:           registry,
		RefreshPolicies:   config.RefreshPolicies,
		EventBuffer:       eventBuffer,
		DockerEvents:      eventSource,
		OwnedDockerClient: dependencies.Client,
	})
	if err != nil {
		return nil, err
	}
	if err := eventSource.waitReady(ctx); err != nil {
		_ = core.Close(context.Background())
		return nil, err
	}
	return &Application{Core: core, dependencies: dependencies}, nil
}

func (application *Application) UsesSharedMobyClient() bool {
	return application.dependencies.UsesSharedClient()
}

var _ backend.Backend = (*Application)(nil)
