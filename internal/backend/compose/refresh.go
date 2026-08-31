package compose

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	composeapi "github.com/docker/compose/v5/pkg/api"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// ReadRefresh is registered by runtime bootstrap and called only by the
// backend-owned RefreshManager. TUI sessions receive its typed result from the
// Compose page bus.
func (api *API) ReadRefresh(ctx context.Context, key backend.RefreshKey) (backend.EventPayload, error) {
	switch key.Kind {
	case RefreshKindList:
		if key.ID != "" {
			return nil, unsupportedRefresh(key)
		}
		return api.readProjects(ctx)
	case RefreshKindDetails:
		name, err := requireProjectName("read Compose project details", key.ID)
		if err != nil {
			return nil, err
		}
		return api.readProject(ctx, name)
	default:
		return nil, unsupportedRefresh(key)
	}
}

func (api *API) readProjects(ctx context.Context) (backend.EventPayload, error) {
	stacks, err := api.compose.List(ctx, composeapi.ListOptions{All: true})
	if err != nil {
		return nil, classifyComposeError("list Compose projects", "", err)
	}
	projects := make([]ProjectSummary, 0, len(stacks))
	for _, stack := range stacks {
		projects = append(projects, projectSummary(stack))
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	return ProjectsUpdated{Projects: projects}, nil
}

func (api *API) readProject(ctx context.Context, name string) (backend.EventPayload, error) {
	stack, err := api.findProject(ctx, name)
	if err != nil {
		return nil, err
	}
	project, err := api.loadProject(ctx, ProjectSpec{Name: name, ConfigFiles: splitConfigFiles(stack.ConfigFiles)})
	if err != nil {
		return nil, err
	}
	containers, err := api.compose.Ps(ctx, name, composeapi.PsOptions{Project: project, All: true})
	if err != nil {
		return nil, classifyComposeError("list Compose project containers", name, err)
	}

	details := ProjectDetails{
		Name: name, Status: stack.Status, WorkingDir: project.WorkingDir,
		ConfigFiles: append([]string(nil), project.ComposeFiles...),
	}
	serviceIndex := make(map[string]int, len(project.Services))
	for _, serviceName := range project.ServiceNames() {
		service := project.Services[serviceName]
		dependsOn := make([]string, 0, len(service.DependsOn))
		for dependency := range service.DependsOn {
			dependsOn = append(dependsOn, dependency)
		}
		sort.Strings(dependsOn)
		details.Services = append(details.Services, Service{
			Name: serviceName, Image: service.Image,
			Command: append([]string(nil), service.Command...), Profiles: append([]string(nil), service.Profiles...),
			DependsOn: dependsOn, Desired: service.GetScale(),
		})
		serviceIndex[serviceName] = len(details.Services) - 1
	}
	for _, value := range containers {
		details.Containers = append(details.Containers, Container{
			ID: value.ID, Name: value.Name, Service: value.Service, Image: value.Image,
			State: string(value.State), Status: value.Status, Health: string(value.Health),
			ExitCode: value.ExitCode, Created: time.Unix(value.Created, 0),
		})
		if index, ok := serviceIndex[value.Service]; ok {
			details.Services[index].Containers = append(details.Services[index].Containers, value.ID)
			if string(value.State) == "running" {
				details.Services[index].Replicas++
			}
		}
	}
	sort.Slice(details.Containers, func(i, j int) bool { return details.Containers[i].Name < details.Containers[j].Name })
	return ProjectUpdated{Project: details}, nil
}

func (api *API) findProject(ctx context.Context, name string) (composeapi.Stack, error) {
	stacks, err := api.compose.List(ctx, composeapi.ListOptions{All: true})
	if err != nil {
		return composeapi.Stack{}, classifyComposeError("find Compose project", name, err)
	}
	for _, stack := range stacks {
		if stack.Name == name {
			return stack, nil
		}
	}
	return composeapi.Stack{}, &backend.AppError{
		Code: backend.ErrorNotFound, Operation: "find Compose project", Resource: "compose project", ID: name,
	}
}

func (api *API) loadProject(ctx context.Context, spec ProjectSpec) (*composeProject, error) {
	name, err := requireProjectName("load Compose project", spec.Name)
	if err != nil {
		return nil, err
	}
	configFiles, err := api.validateConfigFiles(spec.ConfigFiles)
	if err != nil {
		return nil, err
	}
	project, err := api.compose.LoadProject(ctx, composeapi.ProjectLoadOptions{
		ProjectName: name, ConfigPaths: configFiles, WorkingDir: filepath.Dir(configFiles[0]),
		Profiles: append([]string(nil), spec.Profiles...), Offline: true, All: true,
	})
	if err != nil {
		return nil, classifyComposeError("load Compose project", name, err)
	}
	return project, nil
}

func projectSummary(stack composeapi.Stack) ProjectSummary {
	return ProjectSummary{
		Name: stack.Name, Status: stack.Status,
		ConfigFiles: splitConfigFiles(stack.ConfigFiles), Reason: stack.Reason,
	}
}

func splitConfigFiles(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
