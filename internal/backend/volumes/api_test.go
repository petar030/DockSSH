package volumes

import (
	"context"
	"reflect"
	"testing"

	"github.com/containerd/errdefs"
	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	volumetypes "github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

func TestAPIReadsVolumeListDetailsAndAttachments(t *testing.T) {
	volume := volumetypes.Volume{
		Name: "demo", Driver: "local", Scope: "local", CreatedAt: "2026-08-30T10:11:12.123456789Z",
		Mountpoint: "/var/lib/docker/volumes/demo", Labels: map[string]string{"kind": "test"},
		Options: map[string]string{"type": "none"}, Status: map[string]any{"healthy": true},
		UsageData: &volumetypes.UsageData{Size: 42, RefCount: 1},
	}
	docker := &fakeVolumeClient{
		list:    client.VolumeListResult{Items: []volumetypes.Volume{volume}, Warnings: []string{"warning"}},
		inspect: client.VolumeInspectResult{Volume: volume},
		containers: client.ContainerListResult{Items: []containertypes.Summary{{
			ID: "container-one", Names: []string{"/demo-container"}, State: containertypes.StateExited,
			Status: "Exited", Mounts: []containertypes.MountPoint{
				{Type: mount.TypeVolume, Name: "other", Destination: "/ignore"},
				{Type: mount.TypeVolume, Name: "demo", Destination: "/data", Mode: "z", RW: true},
			},
		}}},
	}
	api := newVolumeTestAPI(t, docker)

	listPayload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindList})
	if err != nil {
		t.Fatalf("read list: %v", err)
	}
	list := listPayload.(ListUpdated)
	if len(list.Volumes) != 1 || list.Volumes[0].Name != "demo" || list.Volumes[0].Size != 42 || list.Warnings[0] != "warning" {
		t.Fatalf("volume list = %#v", list)
	}

	detailsPayload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindDetails, ID: "demo"})
	if err != nil {
		t.Fatalf("read details: %v", err)
	}
	details := detailsPayload.(DetailsUpdated).Volume
	if details.Driver != "local" || details.Status["healthy"] != "true" || !details.UsageKnown {
		t.Fatalf("volume details = %#v", details)
	}

	attachmentsPayload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindAttachments, ID: "demo"})
	if err != nil {
		t.Fatalf("read attachments: %v", err)
	}
	attachments := attachmentsPayload.(AttachmentsUpdated)
	if len(attachments.Attachments) != 1 || attachments.Attachments[0].ContainerName != "demo-container" ||
		attachments.Attachments[0].Destination != "/data" || !attachments.Attachments[0].ReadWrite {
		t.Fatalf("attachments = %#v", attachments)
	}
	if !docker.containerListOptions.All || !docker.containerListOptions.Filters["volume"]["demo"] {
		t.Fatalf("container list options = %#v", docker.containerListOptions)
	}
}

func TestVolumeTargetedRefreshRequests(t *testing.T) {
	refreshes := &volumeRefreshRecorder{}
	api := newVolumeTestAPIWith(t, &fakeVolumeClient{}, &volumeCommandRunner{}, refreshes)
	if err := api.RequestDetails(" demo "); err != nil {
		t.Fatalf("request details: %v", err)
	}
	if err := api.RequestAttachments("demo"); err != nil {
		t.Fatalf("request attachments: %v", err)
	}
	want := []volumeRefreshCall{
		{key: backend.RefreshKey{Kind: RefreshKindDetails, ID: "demo"}, reason: backend.RefreshManual},
		{key: backend.RefreshKey{Kind: RefreshKindAttachments, ID: "demo"}, reason: backend.RefreshManual},
	}
	if !reflect.DeepEqual(refreshes.calls, want) {
		t.Fatalf("refresh calls = %#v, want %#v", refreshes.calls, want)
	}
}

func TestVolumesAPIRejectsMissingDependencies(t *testing.T) {
	docker := &fakeVolumeClient{}
	commands := &volumeCommandRunner{}
	refreshes := &volumeRefreshRecorder{}
	for _, construct := range []func() (*API, error){
		func() (*API, error) { return NewAPI(nil, commands, refreshes) },
		func() (*API, error) { return NewAPI(docker, nil, refreshes) },
		func() (*API, error) { return NewAPI(docker, commands, nil) },
	} {
		if _, err := construct(); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
			t.Fatalf("constructor error = %v", err)
		}
	}
}

func TestVolumeCommandsUseExecutorAndExpectedRefreshes(t *testing.T) {
	tests := []struct {
		name string
		run  func(*API) (backend.CommandResult, error)
		call string
		keys []backend.RefreshKind
	}{
		{"create", func(api *API) (backend.CommandResult, error) {
			return api.Create(context.Background(), CreateOptions{Name: "demo", Labels: map[string]string{"kind": "test"}})
		}, "create", []backend.RefreshKind{RefreshKindList, RefreshKindDetails, dashboard.RefreshKindSummary}},
		{"remove", func(api *API) (backend.CommandResult, error) {
			return api.Remove(context.Background(), "demo", RemoveOptions{})
		}, "remove", []backend.RefreshKind{RefreshKindList, containers.RefreshKindList, dashboard.RefreshKindSummary}},
		{"prune", func(api *API) (backend.CommandResult, error) {
			return api.Prune(context.Background(), PruneOptions{All: true, Labels: map[string]string{"kind": "test"}})
		}, "prune", []backend.RefreshKind{RefreshKindList, containers.RefreshKindList, dashboard.RefreshKindSummary}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			docker := &fakeVolumeClient{}
			runner := &volumeCommandRunner{execute: true}
			api := newVolumeTestAPIWith(t, docker, runner, &volumeRefreshRecorder{})
			result, err := test.run(api)
			if err != nil {
				t.Fatalf("command: %v", err)
			}
			if !reflect.DeepEqual(docker.calls, []string{test.call}) {
				t.Fatalf("Docker calls = %v", docker.calls)
			}
			got := make([]backend.RefreshKind, len(result.RefreshKeys))
			for i, key := range result.RefreshKeys {
				got[i] = key.Kind
			}
			if !reflect.DeepEqual(got, test.keys) {
				t.Fatalf("refresh keys = %v, want %v", got, test.keys)
			}
		})
	}
}

func TestVolumeCreateCopiesCallerMapsBeforeSubmission(t *testing.T) {
	docker := &fakeVolumeClient{}
	runner := &volumeCommandRunner{}
	api := newVolumeTestAPIWith(t, docker, runner, &volumeRefreshRecorder{})
	labels := map[string]string{"kind": "original"}
	driverOptions := map[string]string{"type": "none"}
	if _, err := api.Create(context.Background(), CreateOptions{Name: "demo", Labels: labels, DriverOptions: driverOptions}); err != nil {
		t.Fatalf("create: %v", err)
	}
	labels["kind"] = "changed"
	driverOptions["type"] = "changed"
	if err := runner.requests[0].Run(context.Background()); err != nil {
		t.Fatalf("run callback: %v", err)
	}
	if docker.createOptions.Labels["kind"] != "original" || docker.createOptions.DriverOpts["type"] != "none" {
		t.Fatalf("create options mutated = %#v", docker.createOptions)
	}
}

func TestVolumePruneRequiresLabelFilter(t *testing.T) {
	for _, options := range []PruneOptions{{}, {All: true}, {Labels: map[string]string{"": "bad"}}} {
		runner := &volumeCommandRunner{}
		api := newVolumeTestAPIWith(t, &fakeVolumeClient{}, runner, &volumeRefreshRecorder{})
		if _, err := api.Prune(context.Background(), options); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
			t.Fatalf("Prune(%#v) error = %v", options, err)
		}
		if len(runner.requests) != 0 {
			t.Fatal("invalid prune reached Command Executor")
		}
	}
}

func TestVolumePruneMapsLabelAndAllOptions(t *testing.T) {
	docker := &fakeVolumeClient{}
	api := newVolumeTestAPIWith(t, docker, &volumeCommandRunner{execute: true}, &volumeRefreshRecorder{})
	if _, err := api.Prune(context.Background(), PruneOptions{All: true, Labels: map[string]string{"kind": "test"}}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if !docker.pruneOptions.All || !docker.pruneOptions.Filters["label"]["kind=test"] || len(docker.pruneOptions.Filters) != 1 {
		t.Fatalf("prune options = %#v", docker.pruneOptions)
	}
}

func TestVolumeInUseMapsToConflict(t *testing.T) {
	docker := &fakeVolumeClient{err: errdefs.ErrConflict}
	api := newVolumeTestAPIWith(t, docker, &volumeCommandRunner{execute: true}, &volumeRefreshRecorder{})
	if _, err := api.Remove(context.Background(), "in-use", RemoveOptions{}); !backend.HasErrorCode(err, backend.ErrorConflict) {
		t.Fatalf("remove error = %v", err)
	}
}

func TestVolumeErrorsMalformedKeysAndDeepCopies(t *testing.T) {
	api := newVolumeTestAPI(t, &fakeVolumeClient{err: errdefs.ErrNotFound})
	if _, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindDetails, ID: "missing"}); !backend.HasErrorCode(err, backend.ErrorNotFound) {
		t.Fatalf("details error = %v", err)
	}
	for _, key := range []backend.RefreshKey{{Kind: "unknown"}, {Kind: RefreshKindList, ID: "bad"}, {Kind: RefreshKindAttachments}} {
		if _, err := api.ReadRefresh(context.Background(), key); err == nil {
			t.Fatalf("ReadRefresh(%#v) succeeded", key)
		}
	}
	original := ListUpdated{Volumes: []Volume{{Labels: map[string]string{"key": "value"}}}, Warnings: []string{"one"}}
	clone := original.CloneEventPayload().(ListUpdated)
	original.Volumes[0].Labels["key"] = "changed"
	original.Warnings[0] = "changed"
	if clone.Volumes[0].Labels["key"] != "value" || clone.Warnings[0] != "one" {
		t.Fatalf("clone changed = %#v", clone)
	}
}

func newVolumeTestAPI(t *testing.T, docker volumeClient) *API {
	t.Helper()
	return newVolumeTestAPIWith(t, docker, &volumeCommandRunner{}, &volumeRefreshRecorder{})
}

func newVolumeTestAPIWith(t *testing.T, docker volumeClient, commands backend.CommandRunner, refreshes backend.RefreshRequester) *API {
	t.Helper()
	api, err := NewAPI(docker, commands, refreshes)
	if err != nil {
		t.Fatalf("new Volumes API: %v", err)
	}
	return api
}

type volumeRefreshCall struct {
	key    backend.RefreshKey
	reason backend.RefreshReason
}

type volumeRefreshRecorder struct{ calls []volumeRefreshCall }

func (recorder *volumeRefreshRecorder) Request(key backend.RefreshKey, reason backend.RefreshReason) error {
	recorder.calls = append(recorder.calls, volumeRefreshCall{key, reason})
	return nil
}

type volumeCommandRunner struct {
	requests []backend.CommandRequest
	execute  bool
}

func (runner *volumeCommandRunner) Run(_ context.Context, request backend.CommandRequest) (backend.CommandResult, error) {
	runner.requests = append(runner.requests, request)
	var err error
	if runner.execute {
		err = request.Run(context.Background())
	}
	return backend.CommandResult{OperationID: request.OperationID, Affected: request.Affected, RefreshKeys: request.RefreshKeys}, err
}

type fakeVolumeClient struct {
	list                 client.VolumeListResult
	inspect              client.VolumeInspectResult
	containers           client.ContainerListResult
	containerListOptions client.ContainerListOptions
	createOptions        client.VolumeCreateOptions
	pruneOptions         client.VolumePruneOptions
	calls                []string
	err                  error
}

func (fake *fakeVolumeClient) VolumeList(context.Context, client.VolumeListOptions) (client.VolumeListResult, error) {
	return fake.list, fake.err
}
func (fake *fakeVolumeClient) VolumeInspect(context.Context, string, client.VolumeInspectOptions) (client.VolumeInspectResult, error) {
	return fake.inspect, fake.err
}
func (fake *fakeVolumeClient) VolumeCreate(_ context.Context, options client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
	fake.calls = append(fake.calls, "create")
	fake.createOptions = options
	return client.VolumeCreateResult{}, fake.err
}
func (fake *fakeVolumeClient) VolumeRemove(context.Context, string, client.VolumeRemoveOptions) (client.VolumeRemoveResult, error) {
	fake.calls = append(fake.calls, "remove")
	return client.VolumeRemoveResult{}, fake.err
}
func (fake *fakeVolumeClient) VolumePrune(_ context.Context, options client.VolumePruneOptions) (client.VolumePruneResult, error) {
	fake.calls = append(fake.calls, "prune")
	fake.pruneOptions = options
	return client.VolumePruneResult{}, fake.err
}
func (fake *fakeVolumeClient) ContainerList(_ context.Context, options client.ContainerListOptions) (client.ContainerListResult, error) {
	fake.containerListOptions = options
	return fake.containers, fake.err
}
