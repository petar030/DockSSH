package compose

import (
	"context"
	"fmt"
	"strings"
	"time"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	composeapi "github.com/docker/compose/v5/pkg/api"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

func (api *API) startRequest(projectName string, options ServiceOptions) (backend.CommandRequest, error) {
	return api.commandRequest(projectName, "start Compose project", "compose.start", func(ctx context.Context, name string) error {
		return api.compose.Start(ctx, name, composeapi.StartOptions{Services: cleanServices(options.Services)})
	})
}

func (api *API) stopRequest(projectName string, options StopOptions) (backend.CommandRequest, error) {
	return api.commandRequest(projectName, "stop Compose project", "compose.stop", func(ctx context.Context, name string) error {
		return api.compose.Stop(ctx, name, composeapi.StopOptions{
			Services: cleanServices(options.Services), Timeout: durationPointer(options.Timeout),
		})
	})
}

func (api *API) restartRequest(projectName string, options RestartOptions) (backend.CommandRequest, error) {
	return api.commandRequest(projectName, "restart Compose project", "compose.restart", func(ctx context.Context, name string) error {
		return api.compose.Restart(ctx, name, composeapi.RestartOptions{
			Services: cleanServices(options.Services), Timeout: durationPointer(options.Timeout), NoDeps: options.NoDeps,
		})
	})
}

func (api *API) pauseRequest(projectName string, options ServiceOptions) (backend.CommandRequest, error) {
	return api.commandRequest(projectName, "pause Compose project", "compose.pause", func(ctx context.Context, name string) error {
		return api.compose.Pause(ctx, name, composeapi.PauseOptions{Services: cleanServices(options.Services)})
	})
}

func (api *API) unpauseRequest(projectName string, options ServiceOptions) (backend.CommandRequest, error) {
	return api.commandRequest(projectName, "unpause Compose project", "compose.unpause", func(ctx context.Context, name string) error {
		return api.compose.UnPause(ctx, name, composeapi.PauseOptions{Services: cleanServices(options.Services)})
	})
}

func (api *API) scaleRequest(spec ProjectSpec, options ScaleOptions) (backend.CommandRequest, error) {
	name, err := requireProjectName("scale Compose project", spec.Name)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	serviceName := strings.TrimSpace(options.Service)
	if serviceName == "" || options.Replicas < 0 {
		return backend.CommandRequest{}, &backend.AppError{
			Code: backend.ErrorInvalidInput, Operation: "scale Compose service", Resource: serviceName, ID: name,
		}
	}
	return api.commandRequest(name, "scale Compose project", "compose.scale", func(ctx context.Context, _ string) error {
		project, loadErr := api.loadProject(ctx, spec)
		if loadErr != nil {
			return loadErr
		}
		found := false
		project, transformErr := project.WithServicesTransform(func(currentName string, service composetypes.ServiceConfig) (composetypes.ServiceConfig, error) {
			if currentName == serviceName {
				found = true
				service.SetScale(options.Replicas)
			}
			return service, nil
		})
		if transformErr != nil {
			return transformErr
		}
		if !found {
			return fmt.Errorf("service %q is not defined", serviceName)
		}
		return api.compose.Scale(ctx, project, composeapi.ScaleOptions{Services: []string{serviceName}})
	})
}

func (api *API) saveConfigRequest(options SaveConfigOptions) (backend.CommandRequest, error) {
	projectName, _, err := api.managedConfigPath(options.ProjectName)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	if err := validateConfigContent(options.Content); err != nil {
		return backend.CommandRequest{}, err
	}
	return backend.CommandRequest{
		OperationID: "compose.config.save",
		Operation:   "save Compose configuration",
		Affected:    []backend.AffectedResource{{Kind: "compose_project", ID: projectName}},
		// Saving a file changes no Docker state. Refreshing the active-project
		// list is harmless and lets an already-active project reconcile; a new
		// project becomes discoverable only after the separate Up job.
		RefreshKeys: []backend.RefreshKey{{Kind: RefreshKindList}},
		Run: func(context.Context) error {
			return api.writeConfig(projectName, options.Content)
		},
	}, nil
}

func (api *API) commandRequest(
	projectName, operation, operationID string,
	run func(context.Context, string) error,
) (backend.CommandRequest, error) {
	projectName, err := requireProjectName(operation, projectName)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	return backend.CommandRequest{
		OperationID: operationID,
		Operation:   operation,
		Affected:    []backend.AffectedResource{{Kind: "compose_project", ID: projectName}},
		RefreshKeys: composeRefreshKeys(projectName),
		Run: func(operationCtx context.Context) error {
			return classifyComposeError(operation, projectName, run(operationCtx, projectName))
		},
	}, nil
}

func composeRefreshKeys(projectName string) []backend.RefreshKey {
	return []backend.RefreshKey{
		{Kind: RefreshKindList},
		{Kind: RefreshKindDetails, ID: projectName},
		{Kind: containers.RefreshKindList},
		{Kind: dashboard.RefreshKindSummary},
	}
}

func cleanServices(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func durationPointer(value time.Duration) *time.Duration {
	if value <= 0 {
		return nil
	}
	return &value
}
