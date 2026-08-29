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
	"github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/eventhub"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/refresh"
	backendruntime "github.com/petar030/ssh-native-docker-tui/internal/backend/runtime"
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

// EngineEvent is the Moby event message accepted by the event normalizer.
type EngineEvent = events.Message

// BackendConfig controls the production shared backend foundation.
type BackendConfig struct {
	Endpoint             string
	Clock                backend.Clock
	EventHistoryCapacity int
	RefreshPolicies      []backend.RefreshPolicy
}

// Application retains all SDK handles while exposing the shared backend API.
type Application struct {
	*backendruntime.Backend
	dependencies Dependencies
}

// NewBackend constructs the production observer backend. Sessions subscribe
// before requesting the initial status they need.
func NewBackend(ctx context.Context, config BackendConfig) (*Application, error) {
	dependencies, err := NewDependencies(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if config.EventHistoryCapacity == 0 {
		config.EventHistoryCapacity = 256
	}

	clock := config.Clock
	if clock == nil {
		clock = backend.NewRealClock()
	}
	events := eventhub.New(eventhub.Config{
		Clock: clock, EventHistoryCapacity: config.EventHistoryCapacity,
	})
	dashboardLoader, err := dashboard.NewLoader(dependencies.Client, events)
	if err != nil {
		_ = events.Close()
		_ = dependencies.Client.Close()
		return nil, err
	}
	catalog := refresh.NewCatalog()
	if err := catalog.RegisterPage(backend.PageDashboard, dashboard.RefreshKindSummary, dashboardLoader); err != nil {
		_ = events.Close()
		_ = dependencies.Client.Close()
		return nil, err
	}
	containersLoader, err := containers.NewLoader(dependencies.Client)
	if err != nil {
		_ = events.Close()
		_ = dependencies.Client.Close()
		return nil, err
	}
	if err := catalog.RegisterPage(backend.PageContainers, containers.RefreshKindList, containersLoader); err != nil {
		_ = events.Close()
		_ = dependencies.Client.Close()
		return nil, err
	}
	if err := catalog.Register(backend.PageContainers, containers.RefreshKindDetails, containersLoader); err != nil {
		_ = events.Close()
		_ = dependencies.Client.Close()
		return nil, err
	}
	if err := catalog.Register(backend.PageContainers, containers.RefreshKindProcesses, containersLoader); err != nil {
		_ = events.Close()
		_ = dependencies.Client.Close()
		return nil, err
	}
	if err := catalog.RegisterPage(backend.PageSystem, backend.RefreshKindBackendStatus, backend.RefreshLoaderFunc(func(ctx context.Context, _ backend.RefreshKey) (backend.EventPayload, error) {
		result, err := dependencies.Client.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
		if err != nil {
			return nil, err
		}
		return backend.BackendStatusUpdated{
			APIVersion: result.APIVersion, OSType: result.OSType, Experimental: result.Experimental,
		}, nil
	})); err != nil {
		_ = events.Close()
		_ = dependencies.Client.Close()
		return nil, err
	}
	refreshes, err := refresh.NewCoordinator(refresh.CoordinatorConfig{
		Publisher: events, Catalog: catalog,
	})
	if err != nil {
		_ = events.Close()
		_ = dependencies.Client.Close()
		return nil, err
	}
	dispatcher, err := backendruntime.NewRefreshDispatcher(refreshes)
	if err != nil {
		_ = refreshes.Shutdown(context.Background())
		_ = events.Close()
		_ = dependencies.Client.Close()
		return nil, err
	}
	containersService, err := containers.NewService(dependencies.Client, refreshes, dispatcher)
	if err != nil {
		_ = dispatcher.Close()
		_ = refreshes.Shutdown(context.Background())
		_ = events.Close()
		_ = dependencies.Client.Close()
		return nil, err
	}

	eventSource := newMobyEventSource(dependencies.Client)
	applicationBackend, err := backendruntime.New(ctx, backendruntime.Config{
		Clock: clock, EventHub: events, Refreshes: refreshes,
		RefreshDispatcher: dispatcher,
		RefreshPolicies:   config.RefreshPolicies, DockerEvents: eventSource,
		OwnedDockerClient: dependencies.Client, Containers: containersService,
	})
	if err != nil {
		return nil, err
	}
	if err := eventSource.waitReady(ctx); err != nil {
		_ = applicationBackend.Close(context.Background())
		return nil, err
	}
	return &Application{Backend: applicationBackend, dependencies: dependencies}, nil
}

func (application *Application) UsesSharedMobyClient() bool {
	return application.dependencies.UsesSharedClient()
}

var _ backend.Backend = (*Application)(nil)
var _ containers.Backend = (*Application)(nil)
