package compose

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendcompose "github.com/petar030/ssh-native-docker-tui/internal/backend/compose"
)

type fakeBackend struct {
	requests int
	sub      *fakeSubscription
}

func (f *fakeBackend) RequestRefresh(page backend.Page) error { f.requests++; return nil }
func (f *fakeBackend) Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error) {
	f.sub = &fakeSubscription{events: make(chan backend.EventEnvelope, 4)}
	return f.sub, nil
}

type fakeSubscription struct {
	events chan backend.EventEnvelope
	closed bool
}

func (s *fakeSubscription) Events() <-chan backend.EventEnvelope { return s.events }
func (s *fakeSubscription) Close() error                         { s.closed = true; return nil }

type fakeAPI struct {
	details    []string
	commands   []string
	service    backendcompose.ServiceOptions
	stop       backendcompose.StopOptions
	restart    backendcompose.RestartOptions
	scale      backendcompose.ScaleOptions
	spec       backendcompose.ProjectSpec
	up         backendcompose.UpOptions
	down       backendcompose.DownOptions
	pull       backendcompose.PullOptions
	build      backendcompose.BuildOptions
	job        backend.Job
	stream     backend.Stream[backendcompose.LogEntry]
	configPath string
	config     backendcompose.ConfigDocument
	saved      backendcompose.SaveConfigOptions
	err        error
}

func (f *fakeAPI) RequestDetails(name string) error {
	f.details = append(f.details, name)
	return f.err
}
func (f *fakeAPI) Start(context.Context, string, backendcompose.ServiceOptions) (backend.CommandResult, error) {
	f.commands = append(f.commands, "start")
	return backend.CommandResult{}, f.err
}
func (f *fakeAPI) Stop(_ context.Context, _ string, options backendcompose.StopOptions) (backend.CommandResult, error) {
	f.commands = append(f.commands, "stop")
	f.stop = options
	return backend.CommandResult{}, f.err
}
func (f *fakeAPI) Restart(_ context.Context, _ string, options backendcompose.RestartOptions) (backend.CommandResult, error) {
	f.commands = append(f.commands, "restart")
	f.restart = options
	return backend.CommandResult{}, f.err
}
func (f *fakeAPI) Pause(context.Context, string, backendcompose.ServiceOptions) (backend.CommandResult, error) {
	f.commands = append(f.commands, "pause")
	return backend.CommandResult{}, f.err
}
func (f *fakeAPI) Unpause(context.Context, string, backendcompose.ServiceOptions) (backend.CommandResult, error) {
	f.commands = append(f.commands, "unpause")
	return backend.CommandResult{}, f.err
}
func (f *fakeAPI) Scale(_ context.Context, spec backendcompose.ProjectSpec, options backendcompose.ScaleOptions) (backend.CommandResult, error) {
	f.commands = append(f.commands, "scale")
	f.spec, f.scale = spec, options
	return backend.CommandResult{}, f.err
}
func (f *fakeAPI) Up(_ context.Context, spec backendcompose.ProjectSpec, options backendcompose.UpOptions) (backend.Job, error) {
	f.spec, f.up = spec, options
	return f.job, f.err
}
func (f *fakeAPI) Down(_ context.Context, _ string, options backendcompose.DownOptions) (backend.Job, error) {
	f.down = options
	return f.job, f.err
}
func (f *fakeAPI) Pull(_ context.Context, spec backendcompose.ProjectSpec, options backendcompose.PullOptions) (backend.Job, error) {
	f.spec, f.pull = spec, options
	return f.job, f.err
}
func (f *fakeAPI) Build(_ context.Context, spec backendcompose.ProjectSpec, options backendcompose.BuildOptions) (backend.Job, error) {
	f.spec, f.build = spec, options
	return f.job, f.err
}
func (f *fakeAPI) Logs(context.Context, string, backendcompose.LogsOptions) (backend.Stream[backendcompose.LogEntry], error) {
	return f.stream, f.err
}
func (f *fakeAPI) ConfigPath(string) (string, error) { return f.configPath, f.err }
func (f *fakeAPI) ReadConfig(context.Context, string) (backendcompose.ConfigDocument, error) {
	return f.config, f.err
}
func (f *fakeAPI) SaveConfig(_ context.Context, options backendcompose.SaveConfigOptions) (backend.CommandResult, error) {
	f.saved = options
	return backend.CommandResult{}, f.err
}

type fakeTracker struct {
	jobs       map[string]bool
	registered int
}

func (f *fakeTracker) Register(job backend.Job, _ string) tea.Cmd {
	f.registered++
	f.jobs[job.ID()] = true
	return nil
}
func (f *fakeTracker) Has(id string) bool { return f.jobs[id] }

type fakeJob struct{ id string }

func (j *fakeJob) ID() string                                    { return j.id }
func (*fakeJob) Progress() <-chan backend.ProgressEvent          { return make(chan backend.ProgressEvent) }
func (*fakeJob) Wait(context.Context) (backend.JobResult, error) { return backend.JobResult{}, nil }
func (*fakeJob) Cancel() error                                   { return nil }

func activeModel(t *testing.T) (Model, *fakeBackend, *fakeAPI) {
	t.Helper()
	b, api := &fakeBackend{}, &fakeAPI{}
	m, command := New(context.Background(), b, api, nil).Activate()
	m, command = m.Update(command())
	batch := command().(tea.BatchMsg)
	message := batch[1]()
	m, _ = m.Update(message)
	return m, b, api
}
func key(value string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Text: value, Code: []rune(value)[0]})
}
func firstMessage(command tea.Cmd) tea.Msg {
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		return batch[0]()
	}
	return message
}

func TestLifecycleSelectionKeyIsolationAndOverflow(t *testing.T) {
	m, backendFake, api := activeModel(t)
	now := time.Now()
	m, command := m.handleEvent(backend.EventEnvelope{Time: now, Key: backend.RefreshKey{Kind: backendcompose.RefreshKindList}, Payload: backendcompose.ProjectsUpdated{Projects: []backendcompose.ProjectSummary{{Name: "z"}, {Name: "a", Status: "future-state", ConfigFiles: []string{"/allowed/a/compose.yaml"}}}}})
	_ = command()
	if m.selected != "a" || len(api.details) != 1 {
		t.Fatalf("selection/details = %q %v", m.selected, api.details)
	}
	m, _ = m.handleEvent(backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendcompose.RefreshKindDetails, ID: "z"}, Payload: backendcompose.ProjectUpdated{Project: backendcompose.ProjectDetails{Name: "z"}}})
	if m.detailsName != "" {
		t.Fatal("mismatched details applied")
	}
	m, _ = m.handleEvent(backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendcompose.RefreshKindDetails, ID: "a"}, Payload: backendcompose.ProjectUpdated{Project: backendcompose.ProjectDetails{Name: "a", Services: []backendcompose.Service{{Name: "web", Desired: 2}}, Containers: []backendcompose.Container{{Name: "web-1", State: "unknown-state", Health: "future-health"}}}}})
	view := m.SetSize(180, 28).View()
	for _, want := range []string{"future-state", "unknown-sta", "future-heal", "web"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	before := backendFake.requests
	m, command = m.handleEvent(backend.EventEnvelope{Payload: backend.SubscriberOverflow{}})
	for _, item := range command().(tea.BatchMsg) {
		if item != nil {
			_ = item()
		}
	}
	if backendFake.requests != before+1 || len(api.details) != 2 || !m.stale {
		t.Fatalf("overflow state requests=%d details=%v", backendFake.requests-before, api.details)
	}
	m = m.Deactivate()
	if !backendFake.sub.closed || m.Active() {
		t.Fatal("deactivation did not close subscription")
	}
}

func TestCommandsProjectSpecJobsAndErrors(t *testing.T) {
	m, _, api := activeModel(t)
	m.active, m.hasData, m.selected = true, true, "demo"
	m.detailsName = "demo"
	m.details.ConfigFiles = []string{"/allowed/demo/compose.yaml"}
	m, command := m.handleKey(key("s"))
	if !m.pending || !m.CapturesInput() || m.Activity() == "" {
		t.Fatal("short command did not enter blocking spinner state")
	}
	m, _ = m.Update(firstMessage(command))
	if len(api.commands) != 1 || m.pending {
		t.Fatal("start command did not complete")
	}
	m.openOverlay(scaleOverlay)
	m.fields = [4]string{"/allowed/demo/compose.yaml", "web", "3"}
	m, command = m.submitOverlay()
	m, _ = m.Update(firstMessage(command))
	if api.scale.Service != "web" || api.scale.Replicas != 3 || api.spec.Name != "demo" {
		t.Fatalf("scale = %+v spec=%+v", api.scale, api.spec)
	}
	tracker := &fakeTracker{jobs: map[string]bool{}}
	m.jobs, api.job = tracker, &fakeJob{id: "compose.up-1"}
	m.openOverlay(upOverlay)
	m.fields = [4]string{"/allowed/demo/compose.yaml", "web, worker", "dev"}
	m.optionA = true
	m, command = m.submitOverlay()
	m, register := m.Update(firstMessage(command))
	if register != nil {
		_ = register()
	}
	if tracker.registered != 1 || !api.up.RemoveOrphans || len(api.spec.Profiles) != 1 {
		t.Fatalf("job registration/options=%d %+v %+v", tracker.registered, api.up, api.spec)
	}
	api.err = &backend.AppError{Code: backend.ErrorConflict, Operation: "start job"}
	m.openOverlay(downOverlay)
	m, command = m.submitOverlay()
	m, _ = m.Update(firstMessage(command))
	if !strings.Contains(m.notice, "already running") || m.overlay != downOverlay {
		t.Fatalf("job failure=%q overlay=%v", m.notice, m.overlay)
	}
}

func TestAcceptedJobIsTrackedAfterTabSwitch(t *testing.T) {
	tracker := &fakeTracker{jobs: map[string]bool{}}
	m := New(context.Background(), nil, nil, tracker)
	m.generation = 4
	m.active = false
	job := &fakeJob{id: "compose.build-late"}
	_, command := m.Update(jobStartedMsg{generation: 3, operation: buildOverlay, job: job})
	if command != nil {
		_ = command()
	}
	if tracker.registered != 1 || !tracker.Has(job.ID()) {
		t.Fatal("accepted job was lost after page deactivation")
	}
}

func TestValidationFilteringAndSanitization(t *testing.T) {
	m, _, _ := activeModel(t)
	m.projects = []backendcompose.ProjectSummary{{Name: "alpha\x1b[31m"}, {Name: "beta"}}
	m.hasData = true
	m.filterEdit, m.overlay = "beta", filterOverlay
	m, command := m.handleOverlay(key("enter"))
	if command == nil || m.selected != "beta" {
		t.Fatalf("filter selection=%q", m.selected)
	}
	m.overlay = scaleOverlay
	m.fields = [4]string{"", "web", "bad"}
	m, command = m.submitOverlay()
	if command != nil || m.notice == "" {
		t.Fatal("invalid scale submitted")
	}
	if strings.Contains(m.SetSize(90, 24).View(), "\x1b[31m") {
		t.Fatal("terminal escape was rendered")
	}
}

func TestLogsAreBoundedSanitizedAndScrollable(t *testing.T) {
	m := New(context.Background(), nil, nil, nil)
	m.active, m.generation, m.overlay, m.width, m.height = true, 1, logsOverlay, 100, 24
	for index := 0; index < maxLogLines+5; index++ {
		m.appendLog(backendcompose.LogEntry{Container: "web", Source: backendcompose.LogStdout, Data: "line\x1b[2J\n"})
	}
	if len(m.logLines) != maxLogLines || strings.Contains(strings.Join(m.logLines, ""), "\x1b[2J") {
		t.Fatal("log retention/sanitization failed")
	}
	m.logScroll = m.logMaxScroll()
	m.logFollowing = false
	previous := m.logScroll
	m, _ = m.handleLogKey("k")
	if m.logScroll >= previous {
		t.Fatal("log scroll did not move upward")
	}
	m, _ = m.handleLogKey("esc")
	if m.overlay != noOverlay {
		t.Fatal("logs did not close")
	}
}

func TestLogsAssembleFragmentsByContainer(t *testing.T) {
	m := New(context.Background(), nil, nil, nil)
	m.appendLog(backendcompose.LogEntry{Container: "web-1", Source: backendcompose.LogStdout, Data: "hel"})
	m.appendLog(backendcompose.LogEntry{Container: "worker-1", Source: backendcompose.LogStdout, Data: "other\n"})
	m.appendLog(backendcompose.LogEntry{Container: "web-1", Source: backendcompose.LogStdout, Data: "lo\n"})
	joined := strings.Join(m.logLines, "\n")
	if !strings.Contains(joined, "[worker-1] [stdout] other") || !strings.Contains(joined, "[web-1] [stdout] hello") {
		t.Fatalf("fragment assembly mixed containers:\n%s", joined)
	}
}

func TestNewConfigEditorValidatesThenSavesWithoutStartingProject(t *testing.T) {
	m, _, api := activeModel(t)
	api.configPath = "/allowed/new-project/compose.yaml"
	m, _ = m.handleKey(key("n"))
	m.fields[0] = "new-project"
	m, command := m.handleOverlay(key("enter"))
	if !m.pending || !m.CapturesInput() {
		t.Fatal("managed-path request did not block page input")
	}
	m, _ = m.Update(firstMessage(command))
	m = m.SetSize(120, 30)
	if m.overlay != configEditorOverlay || m.editorProject != "new-project" || m.editorPath != api.configPath {
		t.Fatalf("editor target = %q %q overlay=%v", m.editorProject, m.editorPath, m.overlay)
	}
	if m.editor.Value() != starterConfig || !strings.Contains(m.View(), "/allowed/new-project/") {
		t.Fatalf("new editor lacks its template or derived target: value=%q\n%s", m.editor.Value(), m.View())
	}

	m.editor.SetValue("services: [")
	invalidContent := m.editor.Value()
	m, command = m.saveConfig()
	m, _ = m.Update(firstMessage(command))
	if api.saved.ProjectName != "" || m.overlay != configEditorOverlay || m.editor.Value() != invalidContent || !strings.Contains(m.notice, "syntax") {
		t.Fatalf("invalid save reached API or lost editor: saved=%+v notice=%q", api.saved, m.notice)
	}

	m.editor.SetValue(starterConfig)
	m, command = m.saveConfig()
	m, _ = m.Update(firstMessage(command))
	if api.saved.ProjectName != "new-project" || api.saved.Content != starterConfig {
		t.Fatalf("save options = %+v", api.saved)
	}
	if m.overlay != noOverlay || m.selected != "new-project" || m.selectedSummary().ConfigFiles[0] != api.configPath {
		t.Fatalf("post-save project = overlay %v selected %q summary=%+v", m.overlay, m.selected, m.selectedSummary())
	}
	if api.job != nil {
		t.Fatal("saving automatically started a Compose job")
	}
}

func TestEditConfigLoadsOnlyManagedDocumentAndRejectsLateResult(t *testing.T) {
	m, _, api := activeModel(t)
	m.selected = "demo"
	api.config = backendcompose.ConfigDocument{
		ProjectName: "demo", Path: "/allowed/demo/compose.yaml", Content: validEditorConfig,
	}
	m, command := m.loadConfig()
	message := firstMessage(command)
	m.editorGeneration++
	m, _ = m.Update(message)
	if m.overlay == configEditorOverlay {
		t.Fatal("late config read replaced a newer editor generation")
	}

	m, command = m.loadConfig()
	m, _ = m.Update(firstMessage(command))
	if m.overlay != configEditorOverlay || m.editor.Value() != validEditorConfig || m.editorPath != api.config.Path {
		t.Fatalf("loaded editor = overlay %v path %q value %q", m.overlay, m.editorPath, m.editor.Value())
	}
	if strings.Contains(m.overlayView(), "Path:") && strings.Contains(m.overlayView(), "_") {
		t.Fatal("editor exposed an editable arbitrary path field")
	}
}

func TestConfigSaveFailureKeepsContentAndEditor(t *testing.T) {
	m, _, api := activeModel(t)
	m, _ = m.openConfigEditor("demo", "/allowed/demo/compose.yaml", validEditorConfig)
	api.err = &backend.AppError{Code: backend.ErrorPermissionDenied, Operation: "save Compose configuration"}
	m, command := m.saveConfig()
	m, _ = m.Update(firstMessage(command))
	if m.overlay != configEditorOverlay || m.editor.Value() != validEditorConfig || !strings.Contains(m.notice, "failed") {
		t.Fatalf("failed editor state = overlay %v content %q notice %q", m.overlay, m.editor.Value(), m.notice)
	}
}

func TestConfigErrorExplainsMissingComposeRoot(t *testing.T) {
	err := &backend.AppError{
		Code: backend.ErrorPermissionDenied, Operation: "select managed Compose configuration",
		Resource: "configured Compose root",
	}
	message := configErrorText("Create configuration", err)
	if !strings.Contains(message, "-compose-root") || strings.Contains(message, "permission_denied") {
		t.Fatalf("missing-root message = %q", message)
	}
}

const validEditorConfig = "services:\n  app:\n    image: nginx:latest\n"
