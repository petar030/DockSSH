package system

import (
	"context"
	"sort"
	"time"

	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// ReadRefresh is registered by bootstrap and called only by RefreshManager.
func (api *API) ReadRefresh(ctx context.Context, key backend.RefreshKey) (backend.EventPayload, error) {
	switch key.Kind {
	case RefreshKindInfo:
		if key.ID != "" {
			return nil, unsupportedRefresh(key)
		}
		return api.readInfo(ctx)
	case RefreshKindDiskUsage:
		if key.ID != "" {
			return nil, unsupportedRefresh(key)
		}
		return api.readDiskUsage(ctx)
	default:
		return nil, unsupportedRefresh(key)
	}
}

func (api *API) readInfo(ctx context.Context) (backend.EventPayload, error) {
	version, err := api.docker.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return nil, classifyDockerError("read Docker version", err)
	}
	result, err := api.docker.Info(ctx, client.InfoOptions{})
	if err != nil {
		return nil, classifyDockerError("read Docker information", err)
	}
	components := make([]Component, 0, len(version.Components))
	for _, value := range version.Components {
		components = append(components, Component{Name: value.Name, Version: value.Version, Details: cloneStrings(value.Details)})
	}
	sort.Slice(components, func(i, j int) bool { return components[i].Name < components[j].Name })
	return InfoUpdated{
		Engine: EngineVersion{
			Platform: version.Platform.Name, Version: version.Version, APIVersion: version.APIVersion,
			MinAPIVersion: version.MinAPIVersion, OS: version.Os, Architecture: version.Arch,
			Experimental: version.Experimental, Components: components,
		},
		Host: mapHostInfo(result.Info),
	}, nil
}

func mapHostInfo(info system.Info) HostInfo {
	driverStatus := make([]Pair, 0, len(info.DriverStatus))
	for _, pair := range info.DriverStatus {
		driverStatus = append(driverStatus, Pair{Name: pair[0], Value: pair[1]})
	}
	runtimes := make([]Runtime, 0, len(info.Runtimes))
	for name, value := range info.Runtimes {
		runtimes = append(runtimes, Runtime{
			Name: name, Path: value.Path, Args: append([]string(nil), value.Args...), Type: value.Type, Status: cloneStrings(value.Status),
		})
	}
	sort.Slice(runtimes, func(i, j int) bool { return runtimes[i].Name < runtimes[j].Name })
	return HostInfo{
		ID: info.ID, Name: info.Name, ServerVersion: info.ServerVersion, KernelVersion: info.KernelVersion,
		OperatingSystem: info.OperatingSystem, OSVersion: info.OSVersion, OSType: info.OSType,
		Architecture: info.Architecture, CPUs: info.NCPU, MemoryBytes: info.MemTotal,
		Containers: info.Containers, ContainersRunning: info.ContainersRunning, ContainersPaused: info.ContainersPaused,
		ContainersStopped: info.ContainersStopped, Images: info.Images, StorageDriver: info.Driver,
		DriverStatus: driverStatus, Plugins: Plugins{
			Volumes: sortedCopy(info.Plugins.Volume), Networks: sortedCopy(info.Plugins.Network),
			Authorization: sortedCopy(info.Plugins.Authorization), Logs: sortedCopy(info.Plugins.Log),
		},
		MemoryLimit: info.MemoryLimit, SwapLimit: info.SwapLimit, CPUQuota: info.CPUCfsQuota,
		CPUSet: info.CPUSet, PIDsLimit: info.PidsLimit, IPv4Forwarding: info.IPv4Forwarding,
		Debug: info.Debug, FileDescriptors: info.NFd, Goroutines: info.NGoroutines,
		EventListeners: info.NEventsListener, SystemTime: info.SystemTime, LoggingDriver: info.LoggingDriver,
		CgroupDriver: info.CgroupDriver, CgroupVersion: info.CgroupVersion, DockerRootDir: info.DockerRootDir,
		DefaultRuntime: info.DefaultRuntime, Runtimes: runtimes, LiveRestore: info.LiveRestoreEnabled,
		SecurityOptions: sortedCopy(info.SecurityOptions), Labels: sortedCopy(info.Labels), Warnings: append([]string(nil), info.Warnings...),
	}
}

func (api *API) readDiskUsage(ctx context.Context) (backend.EventPayload, error) {
	result, err := api.docker.DiskUsage(ctx, client.DiskUsageOptions{
		Containers: true, Images: true, Volumes: true, BuildCache: true, Verbose: true,
	})
	if err != nil {
		return nil, classifyDockerError("read Docker disk usage", err)
	}
	update := DiskUsageUpdated{
		Containers: resourceUsage(result.Containers.TotalCount, result.Containers.ActiveCount, result.Containers.TotalSize, result.Containers.Reclaimable),
		Images:     resourceUsage(result.Images.TotalCount, result.Images.ActiveCount, result.Images.TotalSize, result.Images.Reclaimable),
		Volumes:    resourceUsage(result.Volumes.TotalCount, result.Volumes.ActiveCount, result.Volumes.TotalSize, result.Volumes.Reclaimable),
		BuildCache: resourceUsage(result.BuildCache.TotalCount, result.BuildCache.ActiveCount, result.BuildCache.TotalSize, result.BuildCache.Reclaimable),
	}
	for _, value := range result.Containers.Items {
		update.ContainerItems = append(update.ContainerItems, ContainerDiskUsage{
			ID: value.ID, Names: sortedCopy(value.Names), Image: value.Image, State: string(value.State),
			SizeRW: value.SizeRw, SizeRootFS: value.SizeRootFs, Created: time.Unix(value.Created, 0), Labels: cloneStrings(value.Labels),
		})
	}
	for _, value := range result.Images.Items {
		update.ImageItems = append(update.ImageItems, ImageDiskUsage{
			ID: value.ID, RepoTags: sortedCopy(value.RepoTags), Created: time.Unix(value.Created, 0),
			Size: value.Size, SharedSize: value.SharedSize, Containers: value.Containers, Labels: cloneStrings(value.Labels),
		})
	}
	for _, value := range result.Volumes.Items {
		item := VolumeDiskUsage{Name: value.Name, Driver: value.Driver, Created: parseDockerTime(value.CreatedAt), Labels: cloneStrings(value.Labels)}
		if value.UsageData != nil {
			item.Size, item.References = value.UsageData.Size, value.UsageData.RefCount
		}
		update.VolumeItems = append(update.VolumeItems, item)
	}
	for _, value := range result.BuildCache.Items {
		item := BuildCacheDiskUsage{
			ID: value.ID, Parents: sortedCopy(value.Parents), Type: value.Type, Description: value.Description,
			InUse: value.InUse, Shared: value.Shared, Size: value.Size, Created: value.CreatedAt, UsageCount: value.UsageCount,
		}
		if value.LastUsedAt != nil {
			item.LastUsed = *value.LastUsedAt
		}
		update.BuildCacheItems = append(update.BuildCacheItems, item)
	}
	sort.Slice(update.ContainerItems, func(i, j int) bool { return update.ContainerItems[i].ID < update.ContainerItems[j].ID })
	sort.Slice(update.ImageItems, func(i, j int) bool { return update.ImageItems[i].ID < update.ImageItems[j].ID })
	sort.Slice(update.VolumeItems, func(i, j int) bool { return update.VolumeItems[i].Name < update.VolumeItems[j].Name })
	sort.Slice(update.BuildCacheItems, func(i, j int) bool { return update.BuildCacheItems[i].ID < update.BuildCacheItems[j].ID })
	return update, nil
}

func resourceUsage(count, active, total, reclaimable int64) ResourceDiskUsage {
	return ResourceDiskUsage{Count: count, Active: active, TotalBytes: total, ReclaimableBytes: reclaimable}
}

func parseDockerTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func sortedCopy(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func unsupportedRefresh(key backend.RefreshKey) error {
	return &backend.AppError{Code: backend.ErrorUnsupported, Operation: "read System refresh", Resource: string(key.Kind), ID: key.ID}
}
