package compose

import (
	"context"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	composeapi "github.com/docker/compose/v5/pkg/api"
)

type composeProject = composetypes.Project

// composeClient is the private subset of the process-wide Compose service used
// by this page. Compose SDK types never cross the page API boundary.
type composeClient interface {
	List(context.Context, composeapi.ListOptions) ([]composeapi.Stack, error)
	Ps(context.Context, string, composeapi.PsOptions) ([]composeapi.ContainerSummary, error)
	LoadProject(context.Context, composeapi.ProjectLoadOptions) (*composetypes.Project, error)
	Start(context.Context, string, composeapi.StartOptions) error
	Stop(context.Context, string, composeapi.StopOptions) error
	Restart(context.Context, string, composeapi.RestartOptions) error
	Pause(context.Context, string, composeapi.PauseOptions) error
	UnPause(context.Context, string, composeapi.PauseOptions) error
	Scale(context.Context, *composetypes.Project, composeapi.ScaleOptions) error
	Logs(context.Context, string, composeapi.LogConsumer, composeapi.LogOptions) error
	Up(context.Context, *composetypes.Project, composeapi.UpOptions) error
	Down(context.Context, string, composeapi.DownOptions) error
	Pull(context.Context, *composetypes.Project, composeapi.PullOptions) error
	Build(context.Context, *composetypes.Project, composeapi.BuildOptions) error
}
