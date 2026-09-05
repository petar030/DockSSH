package compose

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	composeapi "github.com/docker/compose/v5/pkg/api"
	containertypes "github.com/moby/moby/api/types/container"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func TestRequestDetailsSubmitsTargetedRefresh(t *testing.T) {
	refreshes := &recordingRefreshRequester{}
	api := newTestAPI(t, &fakeComposeClient{}, refreshes, t.TempDir())

	if err := api.RequestDetails(" demo "); err != nil {
		t.Fatalf("request details: %v", err)
	}
	if len(refreshes.keys) != 1 || refreshes.keys[0] != (backend.RefreshKey{Kind: RefreshKindDetails, ID: "demo"}) {
		t.Fatalf("refresh keys = %#v", refreshes.keys)
	}
}

func TestReadRefreshListsProjects(t *testing.T) {
	root := t.TempDir()
	managedPath := filepath.Join(root, "saved", "compose.yaml")
	if err := os.MkdirAll(filepath.Dir(managedPath), 0o700); err != nil {
		t.Fatalf("create managed project: %v", err)
	}
	if err := os.WriteFile(managedPath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write managed project: %v", err)
	}
	client := &fakeComposeClient{stacks: []composeapi.Stack{
		{Name: "zeta", Status: composeapi.RUNNING, ConfigFiles: "/tmp/z/compose.yaml"},
		{Name: "alpha", Status: composeapi.UNKNOWN, ConfigFiles: "/tmp/a/compose.yaml"},
	}}
	api := newTestAPI(t, client, &recordingRefreshRequester{}, root)

	payload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindList})
	if err != nil {
		t.Fatalf("read projects: %v", err)
	}
	update := payload.(ProjectsUpdated)
	if len(update.Projects) != 3 || update.Projects[0].Name != "alpha" || update.Projects[1].Name != "saved" || update.Projects[1].Status != "not started" || update.Projects[2].Name != "zeta" {
		t.Fatalf("projects = %#v", update.Projects)
	}
}

func TestReadRefreshLoadsSavedProjectBeforeFirstUp(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "saved", "compose.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("create project directory: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("services:\n  web:\n    image: nginx\n"), 0o600); err != nil {
		t.Fatalf("write Compose config: %v", err)
	}
	client := &fakeComposeClient{project: &composetypes.Project{
		Name: "saved", WorkingDir: filepath.Dir(configPath), ComposeFiles: []string{configPath},
		Services: composetypes.Services{"web": {Name: "web", Image: "nginx"}},
	}}
	api := newTestAPI(t, client, &recordingRefreshRequester{}, root)

	payload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindDetails, ID: "saved"})
	if err != nil {
		t.Fatalf("read saved project details: %v", err)
	}
	details := payload.(ProjectUpdated).Project
	if details.Name != "saved" || details.Status != "not started" || len(details.Services) != 1 || details.ConfigFiles[0] != configPath {
		t.Fatalf("details = %#v", details)
	}
}

func TestReadRefreshLoadsAllowedProjectDetailsAndServices(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(configPath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write Compose config: %v", err)
	}
	client := &fakeComposeClient{
		stacks: []composeapi.Stack{{Name: "demo", Status: composeapi.RUNNING, ConfigFiles: configPath}},
		project: &composetypes.Project{
			Name: "demo", WorkingDir: root, ComposeFiles: []string{configPath},
			Services: composetypes.Services{
				"web": {Name: "web", Image: "nginx", Command: []string{"nginx"}, DependsOn: composetypes.DependsOnConfig{"db": {}}},
				"db":  {Name: "db", Image: "postgres"},
			},
		},
		containers: []composeapi.ContainerSummary{{
			ID: "abc", Name: "demo-web-1", Project: "demo", Service: "web", Image: "nginx",
			State: containertypes.StateRunning, Status: "Up", Health: containertypes.Healthy,
		}},
	}
	api := newTestAPI(t, client, &recordingRefreshRequester{}, root)

	payload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindDetails, ID: "demo"})
	if err != nil {
		t.Fatalf("read details: %v", err)
	}
	update := payload.(ProjectUpdated)
	if update.Project.Name != "demo" || len(update.Project.Services) != 2 || len(update.Project.Containers) != 1 {
		t.Fatalf("project = %#v", update.Project)
	}
	if update.Project.Services[1].Name != "web" || update.Project.Services[1].Replicas != 1 || update.Project.Services[1].DependsOn[0] != "db" {
		t.Fatalf("web service = %#v", update.Project.Services[1])
	}
	if !client.lastLoad.Offline || len(client.lastLoad.ConfigPaths) != 1 || client.lastLoad.ConfigPaths[0] != configPath {
		t.Fatalf("load options = %#v", client.lastLoad)
	}
}

func TestProjectLoadRejectsConfigOutsideAllowedRoots(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	configPath := filepath.Join(outside, "compose.yaml")
	if err := os.WriteFile(configPath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write Compose config: %v", err)
	}
	client := &fakeComposeClient{stacks: []composeapi.Stack{{Name: "demo", ConfigFiles: configPath}}}
	api := newTestAPI(t, client, &recordingRefreshRequester{}, allowed)

	_, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindDetails, ID: "demo"})
	if !backend.HasErrorCode(err, backend.ErrorPermissionDenied) {
		t.Fatalf("error = %v", err)
	}
	if client.loadCalls != 0 {
		t.Fatalf("LoadProject called %d times", client.loadCalls)
	}
}

func TestStartSubmitsComposeCallbackThroughCommandRunner(t *testing.T) {
	client := &fakeComposeClient{}
	api := newTestAPI(t, client, &recordingRefreshRequester{}, t.TempDir())

	result, err := api.Start(context.Background(), "demo", ServiceOptions{Services: []string{" web ", ""}})
	if err != nil {
		t.Fatalf("start project: %v", err)
	}
	if result.OperationID != "compose.start" || client.startName != "demo" {
		t.Fatalf("result=%#v startName=%q", result, client.startName)
	}
	if len(client.startOptions.Services) != 1 || client.startOptions.Services[0] != "web" {
		t.Fatalf("start options = %#v", client.startOptions)
	}
	if len(result.RefreshKeys) != 4 || result.RefreshKeys[1] != (backend.RefreshKey{Kind: RefreshKindDetails, ID: "demo"}) {
		t.Fatalf("refresh keys = %#v", result.RefreshKeys)
	}
}

func TestShortCommandMethodsInvokeMatchingComposeOperation(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		run       func(*API) (backend.CommandResult, error)
	}{
		{name: "stop", operation: "stop", run: func(api *API) (backend.CommandResult, error) {
			return api.Stop(context.Background(), "demo", StopOptions{})
		}},
		{name: "restart", operation: "restart", run: func(api *API) (backend.CommandResult, error) {
			return api.Restart(context.Background(), "demo", RestartOptions{})
		}},
		{name: "pause", operation: "pause", run: func(api *API) (backend.CommandResult, error) {
			return api.Pause(context.Background(), "demo", ServiceOptions{})
		}},
		{name: "unpause", operation: "unpause", run: func(api *API) (backend.CommandResult, error) {
			return api.Unpause(context.Background(), "demo", ServiceOptions{})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeComposeClient{}
			api := newTestAPI(t, client, &recordingRefreshRequester{}, t.TempDir())
			result, err := test.run(api)
			if err != nil {
				t.Fatalf("run command: %v", err)
			}
			if client.lastOperation != test.operation || result.OperationID != "compose."+test.operation {
				t.Fatalf("operation=%q result=%#v", client.lastOperation, result)
			}
		})
	}
}

func TestScaleLoadsAllowedDefinitionInsideCommandWorker(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(configPath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write Compose config: %v", err)
	}
	client := &fakeComposeClient{project: &composetypes.Project{
		Name: "demo", Services: composetypes.Services{"web": {Name: "web", Image: "nginx"}},
	}}
	api := newTestAPI(t, client, &recordingRefreshRequester{}, root)

	_, err := api.Scale(context.Background(), ProjectSpec{Name: "demo", ConfigFiles: []string{configPath}}, ScaleOptions{
		Service: "web", Replicas: 3,
	})
	if err != nil {
		t.Fatalf("scale project: %v", err)
	}
	service := client.scaledProject.Services["web"]
	if service.GetScale() != 3 || len(client.scaleOptions.Services) != 1 || client.scaleOptions.Services[0] != "web" {
		t.Fatalf("scaled service=%#v options=%#v", service, client.scaleOptions)
	}
}

func TestLogsReturnSessionOwnedTypedStream(t *testing.T) {
	client := &fakeComposeClient{logEntries: []LogEntry{
		{Container: "demo-web-1", Source: LogStdout, Data: "hello"},
		{Container: "demo-web-1", Source: LogStderr, Data: "warning"},
	}}
	api := newTestAPI(t, client, &recordingRefreshRequester{}, t.TempDir())

	stream, err := api.Logs(context.Background(), "demo", LogsOptions{Tail: 10})
	if err != nil {
		t.Fatalf("open logs: %v", err)
	}
	var values []LogEntry
	for value := range stream.Values() {
		values = append(values, value)
	}
	if err := <-stream.Done(); err != nil {
		t.Fatalf("stream done: %v", err)
	}
	if len(values) != 2 || values[0].Source != LogStdout || values[1].Source != LogStderr {
		t.Fatalf("logs = %#v", values)
	}
	if client.logOptions.Tail != "10" {
		t.Fatalf("log options = %#v", client.logOptions)
	}
}

func TestComposeLogConsumerRestoresLineTerminator(t *testing.T) {
	values := make(chan LogEntry, 1)
	consumer := &logConsumer{ctx: context.Background(), values: values}
	consumer.Log("demo-web-1", "hello")
	entry := <-values
	if entry.Data != "hello\n" {
		t.Fatalf("Compose log data = %q, want complete line", entry.Data)
	}
}

func TestClosingComposeLogStreamCancelsReaderAndClosesChannels(t *testing.T) {
	started := make(chan struct{})
	client := &fakeComposeClient{logsFunc: func(ctx context.Context, _ string, _ composeapi.LogConsumer, _ composeapi.LogOptions) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	api := newTestAPI(t, client, &recordingRefreshRequester{}, t.TempDir())
	stream, err := api.Logs(context.Background(), "demo", LogsOptions{Follow: true})
	if err != nil {
		t.Fatalf("open logs: %v", err)
	}
	<-started
	if err := stream.Close(); err != nil {
		t.Fatalf("close stream: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("close stream again: %v", err)
	}
	select {
	case err := <-stream.Done():
		if err != nil {
			t.Fatalf("stream shutdown error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Compose stream did not finish after Close")
	}
	if _, open := <-stream.Values(); open {
		t.Fatal("Compose stream values remained open after Close")
	}
}

func TestUpBuildsBackendOwnedJobFromAllowedDefinition(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(configPath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write Compose config: %v", err)
	}
	client := &fakeComposeClient{project: &composetypes.Project{Name: "demo", Services: composetypes.Services{}}}
	api := newTestAPI(t, client, &recordingRefreshRequester{}, root)

	job, err := api.Up(context.Background(), ProjectSpec{Name: "demo", ConfigFiles: []string{configPath}}, UpOptions{})
	if err != nil {
		t.Fatalf("start up job: %v", err)
	}
	result, err := job.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait up job: %v", err)
	}
	if result.JobID != "compose.up-test" || client.upProject == nil || client.upProject.Name != "demo" {
		t.Fatalf("result=%#v project=%#v", result, client.upProject)
	}
}

func TestDownPullAndBuildCreateMatchingJobs(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(configPath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write Compose config: %v", err)
	}
	tests := []struct {
		name      string
		operation string
		run       func(*API) (backend.Job, error)
	}{
		{name: "down", operation: "down", run: func(api *API) (backend.Job, error) {
			return api.Down(context.Background(), "demo", DownOptions{})
		}},
		{name: "pull", operation: "pull", run: func(api *API) (backend.Job, error) {
			return api.Pull(context.Background(), ProjectSpec{Name: "demo", ConfigFiles: []string{configPath}}, PullOptions{})
		}},
		{name: "build", operation: "build", run: func(api *API) (backend.Job, error) {
			return api.Build(context.Background(), ProjectSpec{Name: "demo", ConfigFiles: []string{configPath}}, BuildOptions{})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeComposeClient{project: &composetypes.Project{Name: "demo", Services: composetypes.Services{}}}
			api := newTestAPI(t, client, &recordingRefreshRequester{}, root)
			job, err := test.run(api)
			if err != nil {
				t.Fatalf("start job: %v", err)
			}
			if _, err := job.Wait(context.Background()); err != nil {
				t.Fatalf("wait job: %v", err)
			}
			if client.lastOperation != test.operation || job.ID() != "compose."+test.operation+"-test" {
				t.Fatalf("operation=%q job=%q", client.lastOperation, job.ID())
			}
		})
	}
}

func newTestAPI(t *testing.T, client composeClient, refreshes backend.RefreshRequester, roots ...string) *API {
	t.Helper()
	api, err := NewAPI(client, immediateCommandRunner{}, immediateJobRunner{}, refreshes, roots)
	if err != nil {
		t.Fatalf("new Compose API: %v", err)
	}
	return api
}

type recordingRefreshRequester struct {
	keys []backend.RefreshKey
}

func (requester *recordingRefreshRequester) Request(key backend.RefreshKey, _ backend.RefreshReason) error {
	requester.keys = append(requester.keys, key)
	return nil
}

type immediateCommandRunner struct{}

func (immediateCommandRunner) Run(_ context.Context, request backend.CommandRequest) (backend.CommandResult, error) {
	err := request.Run(context.Background())
	return backend.CommandResult{OperationID: request.OperationID, Affected: request.Affected, RefreshKeys: request.RefreshKeys}, err
}

type immediateJobRunner struct{}

func (immediateJobRunner) Start(_ context.Context, request backend.JobRequest) (backend.Job, error) {
	err := request.Run(context.Background(), func(backend.ProgressEvent) {})
	return &completedJob{
		id: request.OperationID + "-test",
		result: backend.JobResult{
			JobID: request.OperationID + "-test", Affected: request.Affected,
			RefreshKeys: request.RefreshKeys,
		},
		err: err,
	}, nil
}

type completedJob struct {
	id     string
	result backend.JobResult
	err    error
}

func (job *completedJob) ID() string                             { return job.id }
func (job *completedJob) Progress() <-chan backend.ProgressEvent { return closedProgress() }
func (job *completedJob) Wait(context.Context) (backend.JobResult, error) {
	return job.result, job.err
}
func (*completedJob) Cancel() error { return nil }

func closedProgress() <-chan backend.ProgressEvent {
	values := make(chan backend.ProgressEvent)
	close(values)
	return values
}

type fakeComposeClient struct {
	stacks        []composeapi.Stack
	containers    []composeapi.ContainerSummary
	project       *composetypes.Project
	lastLoad      composeapi.ProjectLoadOptions
	loadCalls     int
	startName     string
	startOptions  composeapi.StartOptions
	scaledProject *composetypes.Project
	scaleOptions  composeapi.ScaleOptions
	logEntries    []LogEntry
	logOptions    composeapi.LogOptions
	logsFunc      func(context.Context, string, composeapi.LogConsumer, composeapi.LogOptions) error
	upProject     *composetypes.Project
	lastOperation string
}

func (client *fakeComposeClient) List(context.Context, composeapi.ListOptions) ([]composeapi.Stack, error) {
	return append([]composeapi.Stack(nil), client.stacks...), nil
}
func (client *fakeComposeClient) Ps(context.Context, string, composeapi.PsOptions) ([]composeapi.ContainerSummary, error) {
	return append([]composeapi.ContainerSummary(nil), client.containers...), nil
}
func (client *fakeComposeClient) LoadProject(_ context.Context, options composeapi.ProjectLoadOptions) (*composetypes.Project, error) {
	client.loadCalls++
	client.lastLoad = options
	return client.project, nil
}
func (client *fakeComposeClient) Start(_ context.Context, name string, options composeapi.StartOptions) error {
	client.startName, client.startOptions = name, options
	client.lastOperation = "start"
	return nil
}
func (client *fakeComposeClient) Stop(context.Context, string, composeapi.StopOptions) error {
	client.lastOperation = "stop"
	return nil
}
func (client *fakeComposeClient) Restart(context.Context, string, composeapi.RestartOptions) error {
	client.lastOperation = "restart"
	return nil
}
func (client *fakeComposeClient) Pause(context.Context, string, composeapi.PauseOptions) error {
	client.lastOperation = "pause"
	return nil
}
func (client *fakeComposeClient) UnPause(context.Context, string, composeapi.PauseOptions) error {
	client.lastOperation = "unpause"
	return nil
}

func (client *fakeComposeClient) Scale(_ context.Context, project *composetypes.Project, options composeapi.ScaleOptions) error {
	client.scaledProject, client.scaleOptions = project, options
	return nil
}

func (client *fakeComposeClient) Logs(ctx context.Context, project string, consumer composeapi.LogConsumer, options composeapi.LogOptions) error {
	if client.logsFunc != nil {
		return client.logsFunc(ctx, project, consumer, options)
	}
	client.logOptions = options
	for _, entry := range client.logEntries {
		switch entry.Source {
		case LogStderr:
			consumer.Err(entry.Container, entry.Data)
		case LogStatus:
			consumer.Status(entry.Container, entry.Data)
		default:
			consumer.Log(entry.Container, entry.Data)
		}
	}
	return nil
}

func (client *fakeComposeClient) Up(_ context.Context, project *composetypes.Project, _ composeapi.UpOptions) error {
	client.upProject = project
	client.lastOperation = "up"
	return nil
}
func (client *fakeComposeClient) Down(context.Context, string, composeapi.DownOptions) error {
	client.lastOperation = "down"
	return nil
}
func (client *fakeComposeClient) Pull(context.Context, *composetypes.Project, composeapi.PullOptions) error {
	client.lastOperation = "pull"
	return nil
}
func (client *fakeComposeClient) Build(context.Context, *composetypes.Project, composeapi.BuildOptions) error {
	client.lastOperation = "build"
	return nil
}
