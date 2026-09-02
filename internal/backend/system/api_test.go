package system

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	buildtypes "github.com/moby/moby/api/types/build"
	containertypes "github.com/moby/moby/api/types/container"
	imagetypes "github.com/moby/moby/api/types/image"
	networktypes "github.com/moby/moby/api/types/network"
	systemtypes "github.com/moby/moby/api/types/system"
	volumetypes "github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func TestAPIReadsSystemInfoAndDetailedDiskUsage(t *testing.T) {
	lastUsed := time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC)
	docker := &fakeSystemClient{
		version: client.ServerVersionResult{
			Platform: client.PlatformInfo{Name: "Docker Engine"}, Version: "29.1.3", APIVersion: "1.52",
			MinAPIVersion: "1.24", Os: "linux", Arch: "amd64", Experimental: true,
			Components: []systemtypes.ComponentVersion{{Name: "containerd", Version: "2", Details: map[string]string{"commit": "abc"}}, {Name: "Engine", Version: "29"}},
		},
		info: client.SystemInfoResult{Info: systemtypes.Info{
			ID: "daemon", Name: "dev-host", ServerVersion: "29.1.3", KernelVersion: "6.0",
			OperatingSystem: "Pop!_OS", OSVersion: "24.04", OSType: "linux", Architecture: "x86_64",
			NCPU: 8, MemTotal: 16 << 30, Containers: 3, ContainersRunning: 1, ContainersPaused: 1,
			ContainersStopped: 1, Images: 7, Driver: "overlay2", DriverStatus: [][2]string{{"Backing Filesystem", "extfs"}},
			Plugins:     systemtypes.PluginsInfo{Volume: []string{"z", "local"}, Network: []string{"bridge"}},
			MemoryLimit: true, SwapLimit: true, CPUCfsQuota: true, CPUSet: true, PidsLimit: true,
			IPv4Forwarding: true, NFd: 42, NGoroutines: 11, NEventsListener: 2, SystemTime: "now",
			LoggingDriver: "json-file", CgroupDriver: "systemd", CgroupVersion: "2", DockerRootDir: "/var/lib/docker",
			DefaultRuntime: "runc", Runtimes: map[string]systemtypes.RuntimeWithStatus{
				"runc": {Runtime: systemtypes.Runtime{Path: "runc", Args: []string{"--debug"}, Type: "io.containerd.runc.v2"}, Status: map[string]string{"status": "ready"}},
			},
			LiveRestoreEnabled: true, SecurityOptions: []string{"seccomp", "apparmor"}, Labels: []string{"zone=dev"}, Warnings: []string{"notice"},
		}},
		disk: client.DiskUsageResult{
			Containers: client.ContainersDiskUsage{TotalCount: 1, ActiveCount: 1, TotalSize: 30, Reclaimable: 5, Items: []containertypes.Summary{{
				ID: "container-z", Names: []string{"/z", "/a"}, Image: "demo", State: containertypes.StateRunning,
				SizeRw: 10, SizeRootFs: 20, Created: 1_700_000_000, Labels: map[string]string{"kind": "test"},
			}}},
			Images: client.ImagesDiskUsage{TotalCount: 1, TotalSize: 40, Reclaimable: 10, Items: []imagetypes.Summary{{
				ID: "image-z", RepoTags: []string{"z:latest", "a:latest"}, Created: 1_700_000_001,
				Size: 40, SharedSize: 5, Containers: 1, Labels: map[string]string{"kind": "image"},
			}}},
			Volumes: client.VolumesDiskUsage{TotalCount: 1, TotalSize: 50, Reclaimable: 20, Items: []volumetypes.Volume{{
				Name: "volume-z", Driver: "local", CreatedAt: "2026-08-31T10:00:00Z", Labels: map[string]string{"kind": "volume"},
				UsageData: &volumetypes.UsageData{Size: 50, RefCount: 2},
			}}},
			BuildCache: client.BuildCacheDiskUsage{TotalCount: 1, TotalSize: 60, Reclaimable: 30, Items: []buildtypes.CacheRecord{{
				ID: "cache-z", Parents: []string{"parent-z", "parent-a"}, Type: "regular", Description: "RUN test",
				InUse: true, Shared: true, Size: 60, CreatedAt: time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC), LastUsedAt: &lastUsed, UsageCount: 3,
			}}},
		},
	}
	api := newSystemTestAPI(t, docker, false)

	payload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindInfo})
	if err != nil {
		t.Fatalf("read info: %v", err)
	}
	info := payload.(InfoUpdated)
	if info.Engine.Platform != "Docker Engine" || info.Engine.APIVersion != "1.52" || info.Engine.Components[0].Name != "Engine" ||
		info.Host.Name != "dev-host" || info.Host.CPUs != 8 || info.Host.Runtimes[0].Status["status"] != "ready" ||
		info.Host.Plugins.Volumes[0] != "local" {
		t.Fatalf("system info = %#v", info)
	}

	payload, err = api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindDiskUsage})
	if err != nil {
		t.Fatalf("read disk usage: %v", err)
	}
	disk := payload.(DiskUsageUpdated)
	if !docker.diskOptions.Verbose || !docker.diskOptions.Containers || !docker.diskOptions.Images || !docker.diskOptions.Volumes || !docker.diskOptions.BuildCache {
		t.Fatalf("disk options = %#v", docker.diskOptions)
	}
	if disk.Containers.TotalBytes != 30 || disk.ContainerItems[0].Names[0] != "/a" || disk.ImageItems[0].RepoTags[0] != "a:latest" ||
		disk.VolumeItems[0].References != 2 || disk.BuildCacheItems[0].LastUsed != lastUsed || disk.BuildCacheItems[0].Parents[0] != "parent-a" {
		t.Fatalf("disk usage = %#v", disk)
	}
}

func TestSystemTargetedRefreshAndConstructorValidation(t *testing.T) {
	refreshes := &systemRefreshRecorder{}
	api := newSystemTestAPIWith(t, &fakeSystemClient{}, &systemCommandRunner{}, refreshes, false)
	if err := api.RequestDiskUsage(); err != nil {
		t.Fatalf("request disk usage: %v", err)
	}
	want := []systemRefreshCall{{backend.RefreshKey{Kind: RefreshKindDiskUsage}, backend.RefreshManual}}
	if !reflect.DeepEqual(refreshes.calls, want) {
		t.Fatalf("refresh calls = %#v", refreshes.calls)
	}
	for _, construct := range []func() (*API, error){
		func() (*API, error) { return NewAPI(nil, &systemCommandRunner{}, refreshes, false) },
		func() (*API, error) { return NewAPI(&fakeSystemClient{}, nil, refreshes, false) },
		func() (*API, error) { return NewAPI(&fakeSystemClient{}, &systemCommandRunner{}, nil, false) },
	} {
		if _, err := construct(); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
			t.Fatalf("constructor error = %v", err)
		}
	}
}

func TestSystemIndividualPrunesMapOptionsReportsAndRefreshes(t *testing.T) {
	trueValue := true
	tests := []struct {
		name        string
		run         func(*API) (PruneResult, error)
		call        string
		operationID string
		keys        []backend.RefreshKey
		assert      func(*testing.T, *fakeSystemClient, PruneReport)
	}{
		{
			name: "containers", run: func(api *API) (PruneResult, error) {
				return api.PruneContainers(context.Background(), ContainerPruneOptions{Until: "24h", Labels: map[string]string{"kind": "test"}})
			}, call: "containers", operationID: "system.prune.containers", keys: containerPruneRefreshKeys(),
			assert: func(t *testing.T, docker *fakeSystemClient, report PruneReport) {
				if !docker.containerPruneOptions.Filters["until"]["24h"] || !docker.containerPruneOptions.Filters["label"]["kind=test"] ||
					!reflect.DeepEqual(report.ContainersDeleted, []string{"container-one"}) || report.SpaceReclaimed != 1 {
					t.Fatalf("container prune options/report = %#v/%#v", docker.containerPruneOptions, report)
				}
			},
		},
		{
			name: "images", run: func(api *API) (PruneResult, error) {
				return api.PruneImages(context.Background(), ImagePruneOptions{Dangling: &trueValue, Labels: map[string]string{"kind": "test"}})
			}, call: "images", operationID: "system.prune.images", keys: imagePruneRefreshKeys(),
			assert: func(t *testing.T, docker *fakeSystemClient, report PruneReport) {
				if !docker.imagePruneOptions.Filters["dangling"]["true"] || !docker.imagePruneOptions.Filters["label"]["kind=test"] ||
					len(report.ImagesDeleted) != 1 || report.ImagesDeleted[0].Deleted != "image-one" || report.SpaceReclaimed != 2 {
					t.Fatalf("image prune options/report = %#v/%#v", docker.imagePruneOptions, report)
				}
			},
		},
		{
			name: "volumes", run: func(api *API) (PruneResult, error) {
				return api.PruneVolumes(context.Background(), VolumePruneOptions{All: true, Labels: map[string]string{"kind": "test"}})
			}, call: "volumes", operationID: "system.prune.volumes", keys: volumePruneRefreshKeys(),
			assert: func(t *testing.T, docker *fakeSystemClient, report PruneReport) {
				if !docker.volumePruneOptions.All || !docker.volumePruneOptions.Filters["label"]["kind=test"] ||
					!reflect.DeepEqual(report.VolumesDeleted, []string{"volume-one"}) || report.SpaceReclaimed != 3 {
					t.Fatalf("volume prune options/report = %#v/%#v", docker.volumePruneOptions, report)
				}
			},
		},
		{
			name: "networks", run: func(api *API) (PruneResult, error) {
				return api.PruneNetworks(context.Background(), NetworkPruneOptions{Labels: map[string]string{"kind": "test"}})
			}, call: "networks", operationID: "system.prune.networks", keys: networkPruneRefreshKeys(),
			assert: func(t *testing.T, docker *fakeSystemClient, report PruneReport) {
				if !docker.networkPruneOptions.Filters["label"]["kind=test"] ||
					!reflect.DeepEqual(report.NetworksDeleted, []string{"network-one"}) {
					t.Fatalf("network prune options/report = %#v/%#v", docker.networkPruneOptions, report)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			docker := successfulPruneClient()
			api := newSystemTestAPIWith(t, docker, &systemCommandRunner{execute: true}, &systemRefreshRecorder{}, false)
			result, err := test.run(api)
			if err != nil {
				t.Fatalf("prune: %v", err)
			}
			if !reflect.DeepEqual(docker.calls, []string{test.call}) || result.Command.OperationID != test.operationID ||
				!reflect.DeepEqual(result.Command.RefreshKeys, test.keys) {
				t.Fatalf("calls/result = %v/%#v", docker.calls, result)
			}
			test.assert(t, docker, result.Report)
		})
	}
}

func TestSystemPruneGuardsAndAggregatesDedicatedOperation(t *testing.T) {
	disabled := newSystemTestAPI(t, successfulPruneClient(), false)
	if _, err := disabled.PruneSystem(context.Background(), SystemPruneOptions{Confirmation: SystemPruneConfirmation}); !backend.HasErrorCode(err, backend.ErrorPermissionDenied) {
		t.Fatalf("disabled system prune error = %v", err)
	}
	enabled := newSystemTestAPI(t, successfulPruneClient(), true)
	if _, err := enabled.PruneSystem(context.Background(), SystemPruneOptions{}); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
		t.Fatalf("missing confirmation error = %v", err)
	}

	docker := successfulPruneClient()
	api := newSystemTestAPIWith(t, docker, &systemCommandRunner{execute: true}, &systemRefreshRecorder{}, true)
	result, err := api.PruneSystem(context.Background(), SystemPruneOptions{Confirmation: SystemPruneConfirmation, IncludeVolumes: true})
	if err != nil {
		t.Fatalf("system prune: %v", err)
	}
	if !reflect.DeepEqual(docker.calls, []string{"containers", "networks", "images", "build-cache", "volumes"}) ||
		result.Command.OperationID != "system.prune" || result.Report.SpaceReclaimed != 10 ||
		len(result.Report.ContainersDeleted) != 1 || len(result.Report.ImagesDeleted) != 1 ||
		len(result.Report.NetworksDeleted) != 1 || len(result.Report.BuildCacheDeleted) != 1 || len(result.Report.VolumesDeleted) != 1 {
		t.Fatalf("system prune calls/result = %v/%#v", docker.calls, result)
	}
	if !docker.imagePruneOptions.Filters["dangling"]["false"] || !docker.buildPruneOptions.All || !docker.volumePruneOptions.All {
		t.Fatalf("system prune options = images:%#v cache:%#v volumes:%#v", docker.imagePruneOptions, docker.buildPruneOptions, docker.volumePruneOptions)
	}
}

func TestSystemPruneValidationStopsBeforeExecutor(t *testing.T) {
	falseValue := false
	tests := []func(*API) (PruneResult, error){
		func(api *API) (PruneResult, error) {
			return api.PruneContainers(context.Background(), ContainerPruneOptions{})
		},
		func(api *API) (PruneResult, error) { return api.PruneImages(context.Background(), ImagePruneOptions{}) },
		func(api *API) (PruneResult, error) {
			return api.PruneImages(context.Background(), ImagePruneOptions{Dangling: &falseValue})
		},
		func(api *API) (PruneResult, error) {
			return api.PruneVolumes(context.Background(), VolumePruneOptions{All: true})
		},
		func(api *API) (PruneResult, error) {
			return api.PruneNetworks(context.Background(), NetworkPruneOptions{})
		},
	}
	for index, run := range tests {
		runner := &systemCommandRunner{}
		api := newSystemTestAPIWith(t, &fakeSystemClient{}, runner, &systemRefreshRecorder{}, false)
		if _, err := run(api); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
			t.Fatalf("validation case %d error = %v", index, err)
		}
		if len(runner.requests) != 0 {
			t.Fatalf("validation case %d reached executor", index)
		}
	}
}

func TestSystemErrorsMalformedRefreshesAndPayloadCopies(t *testing.T) {
	api := newSystemTestAPI(t, &fakeSystemClient{err: errdefs.ErrUnavailable}, false)
	if _, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindInfo}); !backend.HasErrorCode(err, backend.ErrorDaemonUnavailable) {
		t.Fatalf("info error = %v", err)
	}
	api = newSystemTestAPI(t, &fakeSystemClient{}, false)
	for _, key := range []backend.RefreshKey{{Kind: "unknown"}, {Kind: RefreshKindInfo, ID: "bad"}, {Kind: RefreshKindDiskUsage, ID: "bad"}} {
		if _, err := api.ReadRefresh(context.Background(), key); !backend.HasErrorCode(err, backend.ErrorUnsupported) {
			t.Fatalf("ReadRefresh(%#v) error = %v", key, err)
		}
	}
	info := InfoUpdated{Engine: EngineVersion{Components: []Component{{Details: map[string]string{"key": "value"}}}}, Host: HostInfo{Runtimes: []Runtime{{Args: []string{"one"}, Status: map[string]string{"state": "ready"}}}}}
	infoClone := info.CloneEventPayload().(InfoUpdated)
	info.Engine.Components[0].Details["key"] = "changed"
	info.Host.Runtimes[0].Args[0] = "changed"
	if infoClone.Engine.Components[0].Details["key"] != "value" || infoClone.Host.Runtimes[0].Args[0] != "one" {
		t.Fatalf("info clone changed = %#v", infoClone)
	}
	disk := DiskUsageUpdated{ContainerItems: []ContainerDiskUsage{{Names: []string{"one"}, Labels: map[string]string{"key": "value"}}}}
	diskClone := disk.CloneEventPayload().(DiskUsageUpdated)
	disk.ContainerItems[0].Names[0] = "changed"
	disk.ContainerItems[0].Labels["key"] = "changed"
	if diskClone.ContainerItems[0].Names[0] != "one" || diskClone.ContainerItems[0].Labels["key"] != "value" {
		t.Fatalf("disk clone changed = %#v", diskClone)
	}
}

func successfulPruneClient() *fakeSystemClient {
	return &fakeSystemClient{
		containerPrune: client.ContainerPruneResult{Report: containertypes.PruneReport{ContainersDeleted: []string{"container-one"}, SpaceReclaimed: 1}},
		imagePrune:     client.ImagePruneResult{Report: imagetypes.PruneReport{ImagesDeleted: []imagetypes.DeleteResponse{{Deleted: "image-one"}}, SpaceReclaimed: 2}},
		volumePrune:    client.VolumePruneResult{Report: volumetypes.PruneReport{VolumesDeleted: []string{"volume-one"}, SpaceReclaimed: 3}},
		networkPrune:   client.NetworkPruneResult{Report: networktypes.PruneReport{NetworksDeleted: []string{"network-one"}}},
		buildPrune:     client.BuildCachePruneResult{Report: buildtypes.CachePruneReport{CachesDeleted: []string{"cache-one"}, SpaceReclaimed: 4}},
	}
}

func newSystemTestAPI(t *testing.T, docker systemClient, allowSystemPrune bool) *API {
	t.Helper()
	return newSystemTestAPIWith(t, docker, &systemCommandRunner{execute: true}, &systemRefreshRecorder{}, allowSystemPrune)
}

func newSystemTestAPIWith(t *testing.T, docker systemClient, commands backend.CommandRunner, refreshes backend.RefreshRequester, allowSystemPrune bool) *API {
	t.Helper()
	api, err := NewAPI(docker, commands, refreshes, allowSystemPrune)
	if err != nil {
		t.Fatalf("new System API: %v", err)
	}
	return api
}

type systemRefreshCall struct {
	key    backend.RefreshKey
	reason backend.RefreshReason
}

type systemRefreshRecorder struct{ calls []systemRefreshCall }

func (recorder *systemRefreshRecorder) Request(key backend.RefreshKey, reason backend.RefreshReason) error {
	recorder.calls = append(recorder.calls, systemRefreshCall{key, reason})
	return nil
}

type systemCommandRunner struct {
	requests []backend.CommandRequest
	execute  bool
}

func (runner *systemCommandRunner) Run(_ context.Context, request backend.CommandRequest) (backend.CommandResult, error) {
	runner.requests = append(runner.requests, request)
	var err error
	if runner.execute {
		err = request.Run(context.Background())
	}
	return backend.CommandResult{OperationID: request.OperationID, Affected: request.Affected, RefreshKeys: request.RefreshKeys}, err
}

type fakeSystemClient struct {
	version               client.ServerVersionResult
	info                  client.SystemInfoResult
	disk                  client.DiskUsageResult
	diskOptions           client.DiskUsageOptions
	containerPrune        client.ContainerPruneResult
	imagePrune            client.ImagePruneResult
	volumePrune           client.VolumePruneResult
	networkPrune          client.NetworkPruneResult
	buildPrune            client.BuildCachePruneResult
	containerPruneOptions client.ContainerPruneOptions
	imagePruneOptions     client.ImagePruneOptions
	volumePruneOptions    client.VolumePruneOptions
	networkPruneOptions   client.NetworkPruneOptions
	buildPruneOptions     client.BuildCachePruneOptions
	calls                 []string
	err                   error
}

func (fake *fakeSystemClient) ServerVersion(context.Context, client.ServerVersionOptions) (client.ServerVersionResult, error) {
	return fake.version, fake.err
}

func (fake *fakeSystemClient) Info(context.Context, client.InfoOptions) (client.SystemInfoResult, error) {
	return fake.info, fake.err
}

func (fake *fakeSystemClient) DiskUsage(_ context.Context, options client.DiskUsageOptions) (client.DiskUsageResult, error) {
	fake.diskOptions = options
	return fake.disk, fake.err
}

func (fake *fakeSystemClient) ContainerPrune(_ context.Context, options client.ContainerPruneOptions) (client.ContainerPruneResult, error) {
	fake.calls = append(fake.calls, "containers")
	fake.containerPruneOptions = options
	return fake.containerPrune, fake.err
}

func (fake *fakeSystemClient) ImagePrune(_ context.Context, options client.ImagePruneOptions) (client.ImagePruneResult, error) {
	fake.calls = append(fake.calls, "images")
	fake.imagePruneOptions = options
	return fake.imagePrune, fake.err
}

func (fake *fakeSystemClient) VolumePrune(_ context.Context, options client.VolumePruneOptions) (client.VolumePruneResult, error) {
	fake.calls = append(fake.calls, "volumes")
	fake.volumePruneOptions = options
	return fake.volumePrune, fake.err
}

func (fake *fakeSystemClient) NetworkPrune(_ context.Context, options client.NetworkPruneOptions) (client.NetworkPruneResult, error) {
	fake.calls = append(fake.calls, "networks")
	fake.networkPruneOptions = options
	return fake.networkPrune, fake.err
}

func (fake *fakeSystemClient) BuildCachePrune(_ context.Context, options client.BuildCachePruneOptions) (client.BuildCachePruneResult, error) {
	fake.calls = append(fake.calls, "build-cache")
	fake.buildPruneOptions = options
	return fake.buildPrune, fake.err
}
