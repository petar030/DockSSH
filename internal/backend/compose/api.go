package compose

import (
	"context"
	"strings"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// API is the Compose page facade used by TUI sessions. Backend-only reads,
// Docker/Compose callbacks, and stream goroutines live in separate files.
type API struct {
	compose   composeClient
	commands  backend.CommandRunner
	jobs      backend.JobRunner
	refreshes backend.RefreshRequester
	roots     []string
}

// Start submits a short Compose start command.
func (api *API) Start(ctx context.Context, projectName string, options ServiceOptions) (backend.CommandResult, error) {
	request, err := api.startRequest(projectName, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// Stop submits a short Compose stop command.
func (api *API) Stop(ctx context.Context, projectName string, options StopOptions) (backend.CommandResult, error) {
	request, err := api.stopRequest(projectName, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// Restart submits a short Compose restart command.
func (api *API) Restart(ctx context.Context, projectName string, options RestartOptions) (backend.CommandResult, error) {
	request, err := api.restartRequest(projectName, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// Pause submits a short Compose pause command.
func (api *API) Pause(ctx context.Context, projectName string, options ServiceOptions) (backend.CommandResult, error) {
	request, err := api.pauseRequest(projectName, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// Unpause submits a short Compose unpause command.
func (api *API) Unpause(ctx context.Context, projectName string, options ServiceOptions) (backend.CommandResult, error) {
	request, err := api.unpauseRequest(projectName, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// Scale submits a short Compose scale command for one service definition.
func (api *API) Scale(ctx context.Context, project ProjectSpec, options ScaleOptions) (backend.CommandResult, error) {
	request, err := api.scaleRequest(project, options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// SaveConfig atomically creates or replaces the managed default Compose file
// through the shared short-command executor. Saving does not start a project.
func (api *API) SaveConfig(ctx context.Context, options SaveConfigOptions) (backend.CommandResult, error) {
	request, err := api.saveConfigRequest(options)
	if err != nil {
		return backend.CommandResult{}, err
	}
	return api.commands.Run(ctx, request)
}

// ReadConfig reads one managed default Compose file. The project name, rather
// than an arbitrary path, selects <first root>/<project>/compose.yaml.
func (api *API) ReadConfig(ctx context.Context, projectName string) (ConfigDocument, error) {
	return api.readConfig(ctx, projectName)
}

// ConfigPath returns the deterministic managed path without creating it.
func (api *API) ConfigPath(projectName string) (string, error) {
	_, path, err := api.managedConfigPath(projectName)
	return path, err
}

// Logs opens a session-owned Compose log stream.
func (api *API) Logs(ctx context.Context, projectName string, options LogsOptions) (backend.Stream[LogEntry], error) {
	return api.logs(ctx, projectName, options)
}

// Up starts a backend-owned Compose up job.
func (api *API) Up(ctx context.Context, project ProjectSpec, options UpOptions) (backend.Job, error) {
	request, err := api.upJob(project, options)
	if err != nil {
		return nil, err
	}
	return api.jobs.Start(ctx, request)
}

// Down starts a backend-owned Compose down job.
func (api *API) Down(ctx context.Context, projectName string, options DownOptions) (backend.Job, error) {
	request, err := api.downJob(projectName, options)
	if err != nil {
		return nil, err
	}
	return api.jobs.Start(ctx, request)
}

// Pull starts a backend-owned Compose image-pull job.
func (api *API) Pull(ctx context.Context, project ProjectSpec, options PullOptions) (backend.Job, error) {
	request, err := api.pullJob(project, options)
	if err != nil {
		return nil, err
	}
	return api.jobs.Start(ctx, request)
}

// Build starts a backend-owned Compose build job.
func (api *API) Build(ctx context.Context, project ProjectSpec, options BuildOptions) (backend.Job, error) {
	request, err := api.buildJob(project, options)
	if err != nil {
		return nil, err
	}
	return api.jobs.Start(ctx, request)
}

func NewAPI(
	compose composeClient,
	commands backend.CommandRunner,
	jobs backend.JobRunner,
	refreshes backend.RefreshRequester,
	allowedRoots []string,
) (*API, error) {
	if compose == nil || commands == nil || jobs == nil || refreshes == nil {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "create Compose API"}
	}
	roots, err := normalizeAllowedRoots(allowedRoots)
	if err != nil {
		return nil, err
	}
	return &API{compose: compose, commands: commands, jobs: jobs, refreshes: refreshes, roots: roots}, nil
}

// RequestDetails requests an authoritative project/services update on the
// Compose page bus. The project must currently be known to Compose.
func (api *API) RequestDetails(projectName string) error {
	projectName, err := requireProjectName("request Compose project details", projectName)
	if err != nil {
		return err
	}
	return api.refreshes.Request(backend.RefreshKey{Kind: RefreshKindDetails, ID: projectName}, backend.RefreshManual)
}

func requireProjectName(operation, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &backend.AppError{
			Code: backend.ErrorInvalidInput, Operation: operation, Resource: "compose project",
		}
	}
	return name, nil
}
