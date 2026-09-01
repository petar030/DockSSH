package images

import (
	"context"

	"github.com/moby/moby/client"
)

// imageClient is the private subset of the process-owned Moby client required
// by the Images page.
type imageClient interface {
	ImageList(context.Context, client.ImageListOptions) (client.ImageListResult, error)
	ImageInspect(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImageHistory(context.Context, string, ...client.ImageHistoryOption) (client.ImageHistoryResult, error)
	ImageTag(context.Context, client.ImageTagOptions) (client.ImageTagResult, error)
	ImageRemove(context.Context, string, client.ImageRemoveOptions) (client.ImageRemoveResult, error)
	ImagePrune(context.Context, client.ImagePruneOptions) (client.ImagePruneResult, error)
	ImagePull(context.Context, string, client.ImagePullOptions) (client.ImagePullResponse, error)
}
