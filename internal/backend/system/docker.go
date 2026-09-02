package system

import (
	"context"

	"github.com/moby/moby/client"
)

// systemClient is the private System-page subset of the shared Moby client.
type systemClient interface {
	ServerVersion(context.Context, client.ServerVersionOptions) (client.ServerVersionResult, error)
	Info(context.Context, client.InfoOptions) (client.SystemInfoResult, error)
	DiskUsage(context.Context, client.DiskUsageOptions) (client.DiskUsageResult, error)
	ContainerPrune(context.Context, client.ContainerPruneOptions) (client.ContainerPruneResult, error)
	ImagePrune(context.Context, client.ImagePruneOptions) (client.ImagePruneResult, error)
	VolumePrune(context.Context, client.VolumePruneOptions) (client.VolumePruneResult, error)
	NetworkPrune(context.Context, client.NetworkPruneOptions) (client.NetworkPruneResult, error)
	BuildCachePrune(context.Context, client.BuildCachePruneOptions) (client.BuildCachePruneResult, error)
}
