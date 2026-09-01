package volumes

import (
	"context"

	"github.com/moby/moby/client"
)

// volumeClient is the private subset of the process-owned Moby client used by
// the Volumes page.
type volumeClient interface {
	VolumeList(context.Context, client.VolumeListOptions) (client.VolumeListResult, error)
	VolumeInspect(context.Context, string, client.VolumeInspectOptions) (client.VolumeInspectResult, error)
	VolumeCreate(context.Context, client.VolumeCreateOptions) (client.VolumeCreateResult, error)
	VolumeRemove(context.Context, string, client.VolumeRemoveOptions) (client.VolumeRemoveResult, error)
	VolumePrune(context.Context, client.VolumePruneOptions) (client.VolumePruneResult, error)
	ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error)
}
