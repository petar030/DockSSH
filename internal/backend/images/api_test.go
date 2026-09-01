package images

import (
	"context"
	"io"
	"iter"
	"reflect"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	imagetypes "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/api/types/storage"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

func TestAPIReadsImageListDetailsAndHistory(t *testing.T) {
	created := "2026-08-30T10:11:12.123456789Z"
	docker := &fakeImageClient{
		list: client.ImageListResult{Items: []imagetypes.Summary{{
			ID: "sha256:one", ParentID: "sha256:parent", RepoTags: []string{"z:latest", "a:latest"},
			RepoDigests: []string{"z@sha256:2", "a@sha256:1"}, Created: 1_700_000_000,
			Size: 100, SharedSize: 20, Containers: 2, Labels: map[string]string{"kind": "test"},
		}}},
		inspect: client.ImageInspectResult{InspectResponse: imagetypes.InspectResponse{
			ID: "sha256:one", RepoTags: []string{"z:latest", "a:latest"}, Created: created,
			Architecture: "amd64", Os: "linux", Size: 100,
			Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{
				User: "1000", Env: []string{"MODE=test"}, Entrypoint: []string{"/entry"}, Cmd: []string{"run"},
				WorkingDir: "/work", Labels: map[string]string{"kind": "test"},
				ExposedPorts: map[string]struct{}{"80/tcp": {}}, Volumes: map[string]struct{}{"/data": {}},
			}},
			RootFS:      imagetypes.RootFS{Type: "layers", Layers: []string{"sha256:layer"}},
			GraphDriver: &storage.DriverData{Name: "overlay2", Data: map[string]string{"UpperDir": "/tmp/upper"}},
		}},
		history: client.ImageHistoryResult{Items: []imagetypes.HistoryResponseItem{{
			ID: "sha256:layer", Created: 1_700_000_001, CreatedBy: "RUN true", Tags: []string{"z", "a"}, Size: 12,
		}}},
	}
	api := newImageTestAPI(t, docker)

	listPayload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindList})
	if err != nil {
		t.Fatalf("read list: %v", err)
	}
	list := listPayload.(ListUpdated)
	if len(list.Images) != 1 || list.Images[0].RepoTags[0] != "a:latest" || list.Images[0].Labels["kind"] != "test" {
		t.Fatalf("image list = %#v", list)
	}
	if !docker.listOptions.All || !docker.listOptions.SharedSize {
		t.Fatalf("image list options = %#v", docker.listOptions)
	}

	detailsPayload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindDetails, ID: "sha256:one"})
	if err != nil {
		t.Fatalf("read details: %v", err)
	}
	details := detailsPayload.(DetailsUpdated).Image
	if details.ID != "sha256:one" || details.User != "1000" || details.ExposedPorts[0] != "80/tcp" ||
		details.GraphDriver != "overlay2" || details.Created.Format(time.RFC3339Nano) != created {
		t.Fatalf("image details = %#v", details)
	}

	historyPayload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindHistory, ID: "sha256:one"})
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	history := historyPayload.(HistoryUpdated)
	if history.ImageID != "sha256:one" || len(history.Entries) != 1 || history.Entries[0].Tags[0] != "a" {
		t.Fatalf("image history = %#v", history)
	}
}

func TestImageTargetedRefreshRequests(t *testing.T) {
	refreshes := &imageRefreshRecorder{}
	api := newImageTestAPIWith(t, &fakeImageClient{}, &imageCommandRunner{}, &imageJobRunner{}, refreshes)
	if err := api.RequestDetails(" sha256:one "); err != nil {
		t.Fatalf("request details: %v", err)
	}
	if err := api.RequestHistory("sha256:one"); err != nil {
		t.Fatalf("request history: %v", err)
	}
	want := []imageRefreshCall{
		{key: backend.RefreshKey{Kind: RefreshKindDetails, ID: "sha256:one"}, reason: backend.RefreshManual},
		{key: backend.RefreshKey{Kind: RefreshKindHistory, ID: "sha256:one"}, reason: backend.RefreshManual},
	}
	if !reflect.DeepEqual(refreshes.calls, want) {
		t.Fatalf("refresh calls = %#v, want %#v", refreshes.calls, want)
	}
}

func TestImagesAPIRejectsMissingDependencies(t *testing.T) {
	docker := &fakeImageClient{}
	commands := &imageCommandRunner{}
	jobs := &imageJobRunner{}
	refreshes := &imageRefreshRecorder{}
	for _, construct := range []func() (*API, error){
		func() (*API, error) { return NewAPI(nil, commands, jobs, refreshes) },
		func() (*API, error) { return NewAPI(docker, nil, jobs, refreshes) },
		func() (*API, error) { return NewAPI(docker, commands, nil, refreshes) },
		func() (*API, error) { return NewAPI(docker, commands, jobs, nil) },
	} {
		if _, err := construct(); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
			t.Fatalf("constructor error = %v", err)
		}
	}
}

func TestImageCommandsUseExecutorAndExpectedRefreshes(t *testing.T) {
	dangling := true
	tests := []struct {
		name string
		run  func(*API) (backend.CommandResult, error)
		call string
		keys int
	}{
		{"tag", func(api *API) (backend.CommandResult, error) {
			return api.Tag(context.Background(), "sha256:one", TagOptions{Reference: "demo:test"})
		}, "tag", 3},
		{"remove", func(api *API) (backend.CommandResult, error) {
			return api.Remove(context.Background(), "sha256:one", RemoveOptions{Force: true})
		}, "remove", 2},
		{"prune", func(api *API) (backend.CommandResult, error) {
			return api.Prune(context.Background(), PruneOptions{Dangling: &dangling})
		}, "prune", 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			docker := &fakeImageClient{}
			runner := &imageCommandRunner{execute: true}
			api := newImageTestAPIWith(t, docker, runner, &imageJobRunner{}, &imageRefreshRecorder{})
			result, err := test.run(api)
			if err != nil {
				t.Fatalf("command: %v", err)
			}
			if !reflect.DeepEqual(docker.calls, []string{test.call}) || len(result.RefreshKeys) != test.keys {
				t.Fatalf("calls=%v result=%#v", docker.calls, result)
			}
			if result.RefreshKeys[len(result.RefreshKeys)-1].Kind != dashboard.RefreshKindSummary {
				t.Fatalf("refresh keys = %#v", result.RefreshKeys)
			}
		})
	}
}

func TestImagePruneRequiresExplicitSafeFilters(t *testing.T) {
	falseValue := false
	for _, options := range []PruneOptions{{}, {Dangling: &falseValue}, {Labels: map[string]string{"": "bad"}}} {
		runner := &imageCommandRunner{}
		api := newImageTestAPIWith(t, &fakeImageClient{}, runner, &imageJobRunner{}, &imageRefreshRecorder{})
		if _, err := api.Prune(context.Background(), options); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
			t.Fatalf("Prune(%#v) error = %v", options, err)
		}
		if len(runner.requests) != 0 {
			t.Fatal("invalid prune reached Command Executor")
		}
	}
}

func TestImagePruneMapsOnlyReviewedFilters(t *testing.T) {
	dangling := true
	docker := &fakeImageClient{}
	api := newImageTestAPIWith(t, docker, &imageCommandRunner{execute: true}, &imageJobRunner{}, &imageRefreshRecorder{})
	if _, err := api.Prune(context.Background(), PruneOptions{
		Dangling: &dangling, Until: "24h", Labels: map[string]string{"kind": "test"},
	}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	filters := docker.pruneOptions.Filters
	if !filters["dangling"]["true"] || !filters["until"]["24h"] || !filters["label"]["kind=test"] || len(filters) != 3 {
		t.Fatalf("prune filters = %#v", filters)
	}
}

func TestImagePullBuildsBackendJobAndReportsProgress(t *testing.T) {
	response := &fakePullResponse{messages: []jsonstream.Message{{
		Status: "Downloading", ID: "layer-one", Progress: &jsonstream.Progress{Current: 5, Total: 10},
	}}}
	docker := &fakeImageClient{pull: response}
	runner := &imageJobRunner{execute: true}
	api := newImageTestAPIWith(t, docker, &imageCommandRunner{}, runner, &imageRefreshRecorder{})
	if _, err := api.Pull(context.Background(), "alpine", PullOptions{Platform: "linux/amd64"}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(runner.requests) != 1 || runner.requests[0].Page != backend.PageImages ||
		runner.requests[0].ConflictKey != "image.pull:alpine:latest" {
		t.Fatalf("job request = %#v", runner.requests)
	}
	if len(runner.progress) != 1 || runner.progress[0].Resource != "layer-one" || runner.progress[0].Current != 5 {
		t.Fatalf("progress = %#v", runner.progress)
	}
	if !response.closed || docker.pullReference != "alpine:latest" || len(docker.pullOptions.Platforms) != 1 {
		t.Fatalf("pull response/options: closed=%v ref=%q options=%#v", response.closed, docker.pullReference, docker.pullOptions)
	}
}

func TestImagePullPropagatesEmbeddedDaemonErrorAndCloses(t *testing.T) {
	response := &fakePullResponse{messages: []jsonstream.Message{{Error: &jsonstream.Error{Message: "denied"}}}}
	docker := &fakeImageClient{pull: response}
	runner := &imageJobRunner{execute: true}
	api := newImageTestAPIWith(t, docker, &imageCommandRunner{}, runner, &imageRefreshRecorder{})
	if _, err := api.Pull(context.Background(), "example.invalid/private:test", PullOptions{}); err == nil {
		t.Fatal("pull succeeded")
	}
	if !response.closed {
		t.Fatal("pull response was not closed")
	}
}

func TestImagePullCallbackHonorsCancellationAndCloses(t *testing.T) {
	response := &fakePullResponse{}
	docker := &fakeImageClient{pull: response}
	api := newImageTestAPI(t, docker)
	request, err := api.pullJob("alpine:latest", PullOptions{})
	if err != nil {
		t.Fatalf("build pull job: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = request.Run(ctx, func(backend.ProgressEvent) {})
	if !backend.HasErrorCode(err, backend.ErrorCanceled) || !response.closed {
		t.Fatalf("canceled pull: err=%v closed=%v", err, response.closed)
	}
}

func TestImageErrorsAndMalformedRefreshKeys(t *testing.T) {
	docker := &fakeImageClient{err: errdefs.ErrNotFound}
	api := newImageTestAPI(t, docker)
	if _, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindDetails, ID: "missing"}); !backend.HasErrorCode(err, backend.ErrorNotFound) {
		t.Fatalf("details error = %v", err)
	}
	for _, key := range []backend.RefreshKey{{Kind: "unknown"}, {Kind: RefreshKindList, ID: "bad"}, {Kind: RefreshKindHistory}} {
		if _, err := api.ReadRefresh(context.Background(), key); err == nil {
			t.Fatalf("ReadRefresh(%#v) succeeded", key)
		}
	}
}

func TestImagePayloadsAreDeepCopied(t *testing.T) {
	original := ListUpdated{Images: []Summary{{RepoTags: []string{"one"}, Labels: map[string]string{"key": "value"}}}}
	clone := original.CloneEventPayload().(ListUpdated)
	original.Images[0].RepoTags[0] = "changed"
	original.Images[0].Labels["key"] = "changed"
	if clone.Images[0].RepoTags[0] != "one" || clone.Images[0].Labels["key"] != "value" {
		t.Fatalf("clone changed = %#v", clone)
	}
}

func newImageTestAPI(t *testing.T, docker imageClient) *API {
	t.Helper()
	return newImageTestAPIWith(t, docker, &imageCommandRunner{}, &imageJobRunner{}, &imageRefreshRecorder{})
}

func newImageTestAPIWith(t *testing.T, docker imageClient, commands backend.CommandRunner, jobs backend.JobRunner, refreshes backend.RefreshRequester) *API {
	t.Helper()
	api, err := NewAPI(docker, commands, jobs, refreshes)
	if err != nil {
		t.Fatalf("new Images API: %v", err)
	}
	return api
}

type imageRefreshCall struct {
	key    backend.RefreshKey
	reason backend.RefreshReason
}

type imageRefreshRecorder struct{ calls []imageRefreshCall }

func (recorder *imageRefreshRecorder) Request(key backend.RefreshKey, reason backend.RefreshReason) error {
	recorder.calls = append(recorder.calls, imageRefreshCall{key, reason})
	return nil
}

type imageCommandRunner struct {
	requests []backend.CommandRequest
	execute  bool
}

func (runner *imageCommandRunner) Run(_ context.Context, request backend.CommandRequest) (backend.CommandResult, error) {
	runner.requests = append(runner.requests, request)
	var err error
	if runner.execute {
		err = request.Run(context.Background())
	}
	return backend.CommandResult{OperationID: request.OperationID, Affected: request.Affected, RefreshKeys: request.RefreshKeys}, err
}

type imageJobRunner struct {
	requests []backend.JobRequest
	progress []backend.ProgressEvent
	execute  bool
}

func (runner *imageJobRunner) Start(_ context.Context, request backend.JobRequest) (backend.Job, error) {
	runner.requests = append(runner.requests, request)
	var err error
	if runner.execute {
		err = request.Run(context.Background(), func(event backend.ProgressEvent) { runner.progress = append(runner.progress, event) })
	}
	return imageTestJob{err: err}, err
}

type imageTestJob struct{ err error }

func (imageTestJob) ID() string                             { return "test-job" }
func (imageTestJob) Progress() <-chan backend.ProgressEvent { return nil }
func (job imageTestJob) Wait(context.Context) (backend.JobResult, error) {
	return backend.JobResult{}, job.err
}
func (imageTestJob) Cancel() error { return nil }

type fakeImageClient struct {
	list          client.ImageListResult
	listOptions   client.ImageListOptions
	inspect       client.ImageInspectResult
	history       client.ImageHistoryResult
	pull          client.ImagePullResponse
	pullReference string
	pullOptions   client.ImagePullOptions
	pruneOptions  client.ImagePruneOptions
	calls         []string
	err           error
}

func (fake *fakeImageClient) ImageList(_ context.Context, options client.ImageListOptions) (client.ImageListResult, error) {
	fake.listOptions = options
	return fake.list, fake.err
}
func (fake *fakeImageClient) ImageInspect(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	return fake.inspect, fake.err
}
func (fake *fakeImageClient) ImageHistory(context.Context, string, ...client.ImageHistoryOption) (client.ImageHistoryResult, error) {
	return fake.history, fake.err
}
func (fake *fakeImageClient) ImageTag(context.Context, client.ImageTagOptions) (client.ImageTagResult, error) {
	fake.calls = append(fake.calls, "tag")
	return client.ImageTagResult{}, fake.err
}
func (fake *fakeImageClient) ImageRemove(context.Context, string, client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
	fake.calls = append(fake.calls, "remove")
	return client.ImageRemoveResult{}, fake.err
}
func (fake *fakeImageClient) ImagePrune(_ context.Context, options client.ImagePruneOptions) (client.ImagePruneResult, error) {
	fake.calls = append(fake.calls, "prune")
	fake.pruneOptions = options
	return client.ImagePruneResult{}, fake.err
}
func (fake *fakeImageClient) ImagePull(_ context.Context, reference string, options client.ImagePullOptions) (client.ImagePullResponse, error) {
	fake.pullReference, fake.pullOptions = reference, options
	return fake.pull, fake.err
}

type fakePullResponse struct {
	messages []jsonstream.Message
	err      error
	closed   bool
}

func (response *fakePullResponse) Read([]byte) (int, error)   { return 0, io.EOF }
func (response *fakePullResponse) Close() error               { response.closed = true; return nil }
func (response *fakePullResponse) Wait(context.Context) error { return response.err }
func (response *fakePullResponse) JSONMessages(ctx context.Context) iter.Seq2[jsonstream.Message, error] {
	return func(yield func(jsonstream.Message, error) bool) {
		for _, message := range response.messages {
			if ctx.Err() != nil || !yield(message, nil) {
				return
			}
		}
		if response.err != nil {
			yield(jsonstream.Message{}, response.err)
		}
	}
}
