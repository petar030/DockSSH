package compose

import (
	"context"
	"strings"

	composeapi "github.com/docker/compose/v5/pkg/api"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func (api *API) upJob(spec ProjectSpec, options UpOptions) (backend.JobRequest, error) {
	return api.projectJob(spec.Name, "up Compose project", "compose.up", func(ctx context.Context, name string, report func(backend.ProgressEvent)) error {
		spec.Name = name
		report(backend.ProgressEvent{Status: "loading", Resource: name, Message: "loading Compose definition"})
		project, err := api.loadProject(ctx, spec)
		if err != nil {
			return err
		}
		report(backend.ProgressEvent{Status: "running", Resource: name, Message: "creating and starting services"})
		return classifyComposeError("up Compose project", name, api.compose.Up(ctx, project, composeapi.UpOptions{
			Create: composeapi.CreateOptions{Services: cleanServices(options.Services), RemoveOrphans: options.RemoveOrphans},
			Start:  composeapi.StartOptions{Services: cleanServices(options.Services)},
		}))
	})
}

func (api *API) downJob(projectName string, options DownOptions) (backend.JobRequest, error) {
	return api.projectJob(projectName, "down Compose project", "compose.down", func(ctx context.Context, name string, report func(backend.ProgressEvent)) error {
		report(backend.ProgressEvent{Status: "running", Resource: name, Message: "stopping and removing services"})
		return classifyComposeError("down Compose project", name, api.compose.Down(ctx, name, composeapi.DownOptions{
			Services: cleanServices(options.Services), RemoveOrphans: options.RemoveOrphans,
			Volumes: options.Volumes, Timeout: durationPointer(options.Timeout),
		}))
	})
}

func (api *API) pullJob(spec ProjectSpec, options PullOptions) (backend.JobRequest, error) {
	return api.projectJob(spec.Name, "pull Compose project", "compose.pull", func(ctx context.Context, name string, report func(backend.ProgressEvent)) error {
		spec.Name = name
		report(backend.ProgressEvent{Status: "loading", Resource: name, Message: "loading Compose definition"})
		project, err := api.loadProject(ctx, spec)
		if err != nil {
			return err
		}
		if services := cleanServices(options.Services); len(services) > 0 {
			project, err = project.WithSelectedServices(services)
			if err != nil {
				return classifyComposeError("select Compose services", name, err)
			}
		}
		report(backend.ProgressEvent{Status: "running", Resource: name, Message: "pulling service images"})
		return classifyComposeError("pull Compose project", name, api.compose.Pull(ctx, project, composeapi.PullOptions{
			IgnoreFailures: options.IgnoreFailures,
		}))
	})
}

func (api *API) buildJob(spec ProjectSpec, options BuildOptions) (backend.JobRequest, error) {
	return api.projectJob(spec.Name, "build Compose project", "compose.build", func(ctx context.Context, name string, report func(backend.ProgressEvent)) error {
		spec.Name = name
		report(backend.ProgressEvent{Status: "loading", Resource: name, Message: "loading Compose definition"})
		project, err := api.loadProject(ctx, spec)
		if err != nil {
			return err
		}
		report(backend.ProgressEvent{Status: "running", Resource: name, Message: "building service images"})
		return classifyComposeError("build Compose project", name, api.compose.Build(ctx, project, composeapi.BuildOptions{
			Services: cleanServices(options.Services), Pull: options.Pull, NoCache: options.NoCache,
			Progress: "plain", Out: progressWriter{resource: name, report: report},
		}))
	})
}

func (api *API) projectJob(
	projectName, operation, operationID string,
	run func(context.Context, string, func(backend.ProgressEvent)) error,
) (backend.JobRequest, error) {
	projectName, err := requireProjectName(operation, projectName)
	if err != nil {
		return backend.JobRequest{}, err
	}
	return backend.JobRequest{
		Page: backend.PageCompose, OperationID: operationID, Operation: operation, ConflictKey: "compose:" + projectName,
		Affected:    []backend.AffectedResource{{Kind: "compose_project", ID: projectName}},
		RefreshKeys: composeRefreshKeys(projectName),
		Run: func(ctx context.Context, report func(backend.ProgressEvent)) error {
			return run(ctx, projectName, report)
		},
	}, nil
}

type progressWriter struct {
	resource string
	report   func(backend.ProgressEvent)
}

func (writer progressWriter) Write(value []byte) (int, error) {
	message := strings.TrimSpace(string(value))
	if message != "" {
		writer.report(backend.ProgressEvent{Status: "running", Resource: writer.resource, Message: message})
	}
	return len(value), nil
}
