// Package docker wires the external SDK handles owned by application bootstrap
// and injects them into the shared backend and page APIs.
package docker

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/cli/cli/command"
	cliflags "github.com/docker/cli/cli/flags"
	composeapi "github.com/docker/compose/v5/pkg/api"
	composeservice "github.com/docker/compose/v5/pkg/compose"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	composepage "github.com/petar030/ssh-native-docker-tui/internal/backend/compose"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/eventhub"
	eventpage "github.com/petar030/ssh-native-docker-tui/internal/backend/events"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/images"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/networks"
	backendruntime "github.com/petar030/ssh-native-docker-tui/internal/backend/runtime"
	systempage "github.com/petar030/ssh-native-docker-tui/internal/backend/system"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/volumes"
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
	RefreshPending       int
	RefreshTimeout       time.Duration
	CommandWorkers       int
	CommandQueue         int
	CommandTimeout       time.Duration
	JobCapacity          int
	ComposeRoots         []string
	AllowSystemPrune     bool
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
	refreshes, err := backendruntime.NewRefreshManager(backendruntime.RefreshManagerConfig{
		Publisher: events, PendingCapacity: config.RefreshPending, Timeout: config.RefreshTimeout,
	})
	if err != nil {
		_ = events.Close()
		_ = dependencies.Client.Close()
		return nil, err
	}
	commands, err := backendruntime.NewCommandExecutor(backendruntime.CommandExecutorConfig{
		Refreshes: refreshes, Workers: config.CommandWorkers,
		QueueCapacity: config.CommandQueue, Timeout: config.CommandTimeout,
	})
	if err != nil {
		_ = refreshes.Close(context.Background())
		_ = events.Close()
		_ = dependencies.Client.Close()
		return nil, err
	}
	jobs, err := backendruntime.NewJobExecutor(backendruntime.JobExecutorConfig{
		Refreshes: refreshes, Publisher: events, Clock: clock, Capacity: config.JobCapacity,
	})
	if err != nil {
		_ = commands.Close(context.Background())
		_ = refreshes.Close(context.Background())
		_ = events.Close()
		_ = dependencies.Client.Close()
		return nil, err
	}
	cleanup := func() {
		_ = commands.Close(context.Background())
		_ = jobs.Close(context.Background())
		_ = refreshes.Close(context.Background())
		_ = events.Close()
		_ = dependencies.Client.Close()
	}

	dashboardRefresh, err := dashboard.NewRefreshHandler(dependencies.Client, events)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.RegisterPage(backend.PageDashboard, dashboard.RefreshKindSummary, dashboardRefresh.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	containersAPI, err := containers.NewAPI(dependencies.Client, commands, refreshes)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.RegisterPage(backend.PageContainers, containers.RefreshKindList, containersAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.Register(backend.PageContainers, containers.RefreshKindDetails, containersAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.Register(backend.PageContainers, containers.RefreshKindProcesses, containersAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	composeAPI, err := composepage.NewAPI(dependencies.ComposeAPI, commands, jobs, refreshes, config.ComposeRoots)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.RegisterPage(backend.PageCompose, composepage.RefreshKindList, composeAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.Register(backend.PageCompose, composepage.RefreshKindDetails, composeAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	imagesAPI, err := images.NewAPI(dependencies.Client, commands, jobs, refreshes)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.RegisterPage(backend.PageImages, images.RefreshKindList, imagesAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.Register(backend.PageImages, images.RefreshKindDetails, imagesAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.Register(backend.PageImages, images.RefreshKindHistory, imagesAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	volumesAPI, err := volumes.NewAPI(dependencies.Client, commands, refreshes)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.RegisterPage(backend.PageVolumes, volumes.RefreshKindList, volumesAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.Register(backend.PageVolumes, volumes.RefreshKindDetails, volumesAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.Register(backend.PageVolumes, volumes.RefreshKindAttachments, volumesAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	networksAPI, err := networks.NewAPI(dependencies.Client, commands, refreshes)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.RegisterPage(backend.PageNetworks, networks.RefreshKindList, networksAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.Register(backend.PageNetworks, networks.RefreshKindDetails, networksAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.Register(backend.PageNetworks, networks.RefreshKindConnections, networksAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	eventsRefresh, err := eventpage.NewRefreshHandler(events, 0)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.RegisterPage(backend.PageEvents, eventpage.RefreshKindRecent, eventsRefresh.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	systemAPI, err := systempage.NewAPI(dependencies.Client, commands, refreshes, config.AllowSystemPrune)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.RegisterPage(backend.PageSystem, systempage.RefreshKindInfo, systemAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}
	if err := refreshes.Register(backend.PageSystem, systempage.RefreshKindDiskUsage, systemAPI.ReadRefresh); err != nil {
		cleanup()
		return nil, err
	}

	eventSource := newMobyEventSource(dependencies.Client)
	applicationBackend, err := backendruntime.New(ctx, backendruntime.Config{
		Clock: clock, EventHub: events, Refreshes: refreshes, Commands: commands, Jobs: jobs,
		RefreshPolicies: config.RefreshPolicies, DockerEvents: eventSource,
		OwnedDockerClient: dependencies.Client, Containers: containersAPI, Compose: composeAPI,
		Images: imagesAPI, Volumes: volumesAPI, Networks: networksAPI, System: systemAPI,
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
