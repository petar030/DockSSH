// Package system implements the System page refreshes and deliberately guarded
// prune commands.
package system

import (
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const (
	RefreshKindInfo      backend.RefreshKind = "system.info"
	RefreshKindDiskUsage backend.RefreshKind = "system.disk_usage"

	EventInfoUpdated      backend.EventType = "system_info_updated"
	EventDiskUsageUpdated backend.EventType = "system_disk_usage_updated"

	// SystemPruneConfirmation must be supplied exactly for the deliberately
	// broad system-prune boundary, in addition to bootstrap opt-in.
	SystemPruneConfirmation = "PRUNE ALL UNUSED DOCKER RESOURCES"
)

type Component struct {
	Name    string
	Version string
	Details map[string]string
}

type EngineVersion struct {
	Platform      string
	Version       string
	APIVersion    string
	MinAPIVersion string
	OS            string
	Architecture  string
	Experimental  bool
	Components    []Component
}

type Pair struct {
	Name  string
	Value string
}

type Plugins struct {
	Volumes       []string
	Networks      []string
	Authorization []string
	Logs          []string
}

type Runtime struct {
	Name   string
	Path   string
	Args   []string
	Type   string
	Status map[string]string
}

type HostInfo struct {
	ID                string
	Name              string
	ServerVersion     string
	KernelVersion     string
	OperatingSystem   string
	OSVersion         string
	OSType            string
	Architecture      string
	CPUs              int
	MemoryBytes       int64
	Containers        int
	ContainersRunning int
	ContainersPaused  int
	ContainersStopped int
	Images            int
	StorageDriver     string
	DriverStatus      []Pair
	Plugins           Plugins
	MemoryLimit       bool
	SwapLimit         bool
	CPUQuota          bool
	CPUSet            bool
	PIDsLimit         bool
	IPv4Forwarding    bool
	Debug             bool
	FileDescriptors   int
	Goroutines        int
	EventListeners    int
	SystemTime        string
	LoggingDriver     string
	CgroupDriver      string
	CgroupVersion     string
	DockerRootDir     string
	DefaultRuntime    string
	Runtimes          []Runtime
	LiveRestore       bool
	SecurityOptions   []string
	Labels            []string
	Warnings          []string
}

type InfoUpdated struct {
	Engine EngineVersion
	Host   HostInfo
}

func (InfoUpdated) EventType() backend.EventType { return EventInfoUpdated }

func (update InfoUpdated) CloneEventPayload() backend.EventPayload {
	update.Engine = cloneEngine(update.Engine)
	update.Host = cloneHost(update.Host)
	return update
}

type ResourceDiskUsage struct {
	Count            int64
	Active           int64
	TotalBytes       int64
	ReclaimableBytes int64
}

type ContainerDiskUsage struct {
	ID         string
	Names      []string
	Image      string
	State      string
	SizeRW     int64
	SizeRootFS int64
	Created    time.Time
	Labels     map[string]string
}

type ImageDiskUsage struct {
	ID         string
	RepoTags   []string
	Created    time.Time
	Size       int64
	SharedSize int64
	Containers int64
	Labels     map[string]string
}

type VolumeDiskUsage struct {
	Name       string
	Driver     string
	Created    time.Time
	Size       int64
	References int64
	Labels     map[string]string
}

type BuildCacheDiskUsage struct {
	ID          string
	Parents     []string
	Type        string
	Description string
	InUse       bool
	Shared      bool
	Size        int64
	Created     time.Time
	LastUsed    time.Time
	UsageCount  int
}

type DiskUsageUpdated struct {
	Containers ResourceDiskUsage
	Images     ResourceDiskUsage
	Volumes    ResourceDiskUsage
	BuildCache ResourceDiskUsage

	ContainerItems  []ContainerDiskUsage
	ImageItems      []ImageDiskUsage
	VolumeItems     []VolumeDiskUsage
	BuildCacheItems []BuildCacheDiskUsage
}

func (DiskUsageUpdated) EventType() backend.EventType { return EventDiskUsageUpdated }

func (update DiskUsageUpdated) CloneEventPayload() backend.EventPayload {
	update.ContainerItems = cloneContainerUsage(update.ContainerItems)
	update.ImageItems = cloneImageUsage(update.ImageItems)
	update.VolumeItems = cloneVolumeUsage(update.VolumeItems)
	update.BuildCacheItems = cloneBuildCacheUsage(update.BuildCacheItems)
	return update
}

type ContainerPruneOptions struct {
	Until  string
	Labels map[string]string
}

type ImagePruneOptions struct {
	Dangling *bool
	Until    string
	Labels   map[string]string
}

type VolumePruneOptions struct {
	All    bool
	Labels map[string]string
}

type NetworkPruneOptions struct {
	Until  string
	Labels map[string]string
}

type SystemPruneOptions struct {
	Confirmation   string
	IncludeVolumes bool
}

type ImageDeletion struct {
	Deleted  string
	Untagged string
}

type PruneReport struct {
	ContainersDeleted []string
	ImagesDeleted     []ImageDeletion
	VolumesDeleted    []string
	NetworksDeleted   []string
	BuildCacheDeleted []string
	SpaceReclaimed    uint64
}

// PruneResult combines executor metadata with the daemon's typed prune report.
type PruneResult struct {
	Command backend.CommandResult
	Report  PruneReport
}

func cloneEngine(value EngineVersion) EngineVersion {
	value.Components = append([]Component(nil), value.Components...)
	for index := range value.Components {
		value.Components[index].Details = cloneStrings(value.Components[index].Details)
	}
	return value
}

func cloneHost(value HostInfo) HostInfo {
	value.DriverStatus = append([]Pair(nil), value.DriverStatus...)
	value.Plugins.Volumes = append([]string(nil), value.Plugins.Volumes...)
	value.Plugins.Networks = append([]string(nil), value.Plugins.Networks...)
	value.Plugins.Authorization = append([]string(nil), value.Plugins.Authorization...)
	value.Plugins.Logs = append([]string(nil), value.Plugins.Logs...)
	value.Runtimes = append([]Runtime(nil), value.Runtimes...)
	for index := range value.Runtimes {
		value.Runtimes[index].Args = append([]string(nil), value.Runtimes[index].Args...)
		value.Runtimes[index].Status = cloneStrings(value.Runtimes[index].Status)
	}
	value.SecurityOptions = append([]string(nil), value.SecurityOptions...)
	value.Labels = append([]string(nil), value.Labels...)
	value.Warnings = append([]string(nil), value.Warnings...)
	return value
}

func cloneContainerUsage(values []ContainerDiskUsage) []ContainerDiskUsage {
	cloned := append([]ContainerDiskUsage(nil), values...)
	for index := range cloned {
		cloned[index].Names = append([]string(nil), cloned[index].Names...)
		cloned[index].Labels = cloneStrings(cloned[index].Labels)
	}
	return cloned
}

func cloneImageUsage(values []ImageDiskUsage) []ImageDiskUsage {
	cloned := append([]ImageDiskUsage(nil), values...)
	for index := range cloned {
		cloned[index].RepoTags = append([]string(nil), cloned[index].RepoTags...)
		cloned[index].Labels = cloneStrings(cloned[index].Labels)
	}
	return cloned
}

func cloneVolumeUsage(values []VolumeDiskUsage) []VolumeDiskUsage {
	cloned := append([]VolumeDiskUsage(nil), values...)
	for index := range cloned {
		cloned[index].Labels = cloneStrings(cloned[index].Labels)
	}
	return cloned
}

func cloneBuildCacheUsage(values []BuildCacheDiskUsage) []BuildCacheDiskUsage {
	cloned := append([]BuildCacheDiskUsage(nil), values...)
	for index := range cloned {
		cloned[index].Parents = append([]string(nil), cloned[index].Parents...)
	}
	return cloned
}

func cloneStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
