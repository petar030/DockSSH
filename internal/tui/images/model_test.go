package images

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendimages "github.com/petar030/ssh-native-docker-tui/internal/backend/images"
)

type fakeBackend struct {
	steps    []string
	requests int
	sub      *fakeSubscription
}

func (fake *fakeBackend) RequestRefresh(page backend.Page) error {
	fake.steps = append(fake.steps, "refresh:"+string(page))
	fake.requests++
	return nil
}
func (fake *fakeBackend) Subscribe(_ context.Context, page backend.Page, _ backend.EventFilter) (backend.Subscription, error) {
	fake.steps = append(fake.steps, "subscribe:"+string(page))
	fake.sub = &fakeSubscription{events: make(chan backend.EventEnvelope, 8)}
	return fake.sub, nil
}

type fakeSubscription struct {
	events chan backend.EventEnvelope
	closed bool
}

func (sub *fakeSubscription) Events() <-chan backend.EventEnvelope { return sub.events }
func (sub *fakeSubscription) Close() error                         { sub.closed = true; return nil }

type fakeAPI struct {
	details []string
	history []string
	tags    []backendimages.TagOptions
	removes []backendimages.RemoveOptions
	prunes  []backendimages.PruneOptions
	pulls   []backendimages.PullOptions
	err     error
	job     backend.Job
}

func (fake *fakeAPI) RequestDetails(id string) error {
	fake.details = append(fake.details, id)
	return fake.err
}
func (fake *fakeAPI) RequestHistory(id string) error {
	fake.history = append(fake.history, id)
	return fake.err
}
func (fake *fakeAPI) Tag(context.Context, string, backendimages.TagOptions) (backend.CommandResult, error) {
	panic("use recording API")
}
func (fake *fakeAPI) Remove(context.Context, string, backendimages.RemoveOptions) (backend.CommandResult, error) {
	panic("use recording API")
}
func (fake *fakeAPI) Prune(context.Context, backendimages.PruneOptions) (backend.CommandResult, error) {
	panic("use recording API")
}
func (fake *fakeAPI) Pull(_ context.Context, _ string, option backendimages.PullOptions) (backend.Job, error) {
	fake.pulls = append(fake.pulls, option)
	return fake.job, fake.err
}

type recordingAPI struct{ fakeAPI }

func (fake *recordingAPI) Tag(_ context.Context, _ string, option backendimages.TagOptions) (backend.CommandResult, error) {
	fake.tags = append(fake.tags, option)
	return backend.CommandResult{}, fake.err
}
func (fake *recordingAPI) Remove(_ context.Context, _ string, option backendimages.RemoveOptions) (backend.CommandResult, error) {
	fake.removes = append(fake.removes, option)
	return backend.CommandResult{}, fake.err
}
func (fake *recordingAPI) Prune(_ context.Context, option backendimages.PruneOptions) (backend.CommandResult, error) {
	fake.prunes = append(fake.prunes, option)
	return backend.CommandResult{}, fake.err
}

type fakeTracker struct {
	ids        map[string]bool
	registered int
}

func (fake *fakeTracker) Register(job backend.Job, _ string) tea.Cmd {
	fake.registered++
	fake.ids[job.ID()] = true
	return func() tea.Msg { return nil }
}
func (fake *fakeTracker) Has(id string) bool { return fake.ids[id] }

func activeModel(t *testing.T) (Model, *fakeBackend, *recordingAPI) {
	t.Helper()
	common, api := &fakeBackend{}, &recordingAPI{}
	model, command := New(context.Background(), common, api, nil).Activate()
	ready := command().(subscriptionReadyMsg)
	model, command = model.Update(ready)
	if len(common.steps) != 1 || common.steps[0] != "subscribe:images" {
		t.Fatalf("first step = %v", common.steps)
	}
	batch := command().(tea.BatchMsg)
	message := batch[1]().(requestFinishedMsg)
	model, _ = model.Update(message)
	if len(common.steps) < 2 || common.steps[1] != "refresh:images" {
		t.Fatalf("steps = %v", common.steps)
	}
	return model, common, api
}

func TestLifecycleListSelectionAndTargetIsolation(t *testing.T) {
	model, common, api := activeModel(t)
	now := time.Now()
	model, command := model.handleEvent(backend.EventEnvelope{Time: now, Key: backend.RefreshKey{Kind: backendimages.RefreshKindList}, Payload: backendimages.ListUpdated{Images: []backendimages.Summary{{ID: "a", RepoTags: []string{"aaa:latest"}}, {ID: "b", RepoTags: []string{"zzz:latest"}}}}})
	if model.selected != "a" || !model.hasData || command == nil {
		t.Fatalf("list state = %+v", model)
	}
	message := command().(requestFinishedMsg)
	model, _ = model.Update(message)
	if len(api.details) != 1 || api.details[0] != "a" {
		t.Fatalf("details requests = %v", api.details)
	}
	model, _ = model.handleEvent(backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendimages.RefreshKindDetails, ID: "b"}, Payload: backendimages.DetailsUpdated{Image: backendimages.Details{ID: "b", Author: "wrong"}}})
	if model.detailsID != "" {
		t.Fatal("mismatched detail was applied")
	}
	model, _ = model.handleEvent(backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendimages.RefreshKindDetails, ID: "a"}, Payload: backendimages.DetailsUpdated{Image: backendimages.Details{ID: "a", Author: "ok"}}})
	if model.details.Author != "ok" {
		t.Fatal("matching detail was not applied")
	}
	model, _ = model.handleEvent(backend.EventEnvelope{Payload: backendimages.ListUpdated{Images: []backendimages.Summary{{ID: "b"}}}})
	if model.selected != "b" || model.detailsID != "" {
		t.Fatal("vanished identity was not cleared and reselected")
	}
	model = model.Deactivate()
	if !common.sub.closed || model.Active() {
		t.Fatal("deactivate did not clean subscription")
	}
}

func TestOverflowFilterSortAndSafeRendering(t *testing.T) {
	model, common, _ := activeModel(t)
	model.images = []backendimages.Summary{{ID: "a", RepoTags: []string{"x\nBAD"}, Size: 1}, {ID: "b", RepoTags: []string{"b"}, Size: 9}}
	model.hasData, model.selected, model.detailsID = true, "a", "a"
	model.details = backendimages.Details{ID: "a", Environment: []string{"TOKEN=secret"}, Labels: map[string]string{"evil\nkey": "value"}}
	model, command := model.handleEvent(backend.EventEnvelope{Payload: backend.SubscriberOverflow{}})
	if !model.stale || command == nil {
		t.Fatal("overflow did not enter stale recovery")
	}
	before := common.requests
	for _, cmd := range command().(tea.BatchMsg) {
		if cmd != nil {
			_ = cmd()
		}
	}
	if common.requests != before+1 {
		t.Fatalf("overflow base requests = %d", common.requests-before)
	}
	model.sort = sortSize
	if got := model.visible()[0].ID; got != "b" {
		t.Fatalf("size sort first = %s", got)
	}
	view := model.SetSize(120, 25).View()
	if !strings.Contains(view, "TOKEN=secret") || strings.Contains(view, "evil\nkey") {
		t.Fatalf("unsafe/missing rendering:\n%s", view)
	}
	if narrow := model.SetSize(90, 25).View(); narrow == "" {
		t.Fatal("narrow layout empty")
	}
	if narrow := model.SetSize(90, 25).View(); !strings.Contains(narrow, "Sort:") {
		t.Fatalf("active sort is hidden on a narrow layout:\n%s", narrow)
	}
}

func TestCommandShapesGuardsAndErrors(t *testing.T) {
	model, _, api := activeModel(t)
	model.selected = "img"
	model.overlay = pruneOverlay
	model, cmd := model.handleOverlay(testKey("enter"))
	model, _ = model.Update(commandMessage(cmd))
	if len(api.prunes) != 1 || api.prunes[0].Dangling == nil || !*api.prunes[0].Dangling {
		t.Fatalf("dangling prune options = %+v", api.prunes)
	}
	model.hasData = true
	model.overlay = pruneOverlay
	if !strings.Contains(model.SetSize(100, 25).View(), "dangling images") {
		t.Fatal("prune form does not describe the dangling scope")
	}
	model.overlay, model.editSecondary = pruneOverlay, "not-a-label"
	model, cmd = model.handleOverlay(testKey("enter"))
	if cmd != nil || !strings.Contains(model.notice, "key=value") {
		t.Fatalf("invalid label was submitted: %q", model.notice)
	}
	model.editSecondary = "team=dev"
	model, cmd = model.handleOverlay(testKey("enter"))
	model, _ = model.Update(commandMessage(cmd))
	if len(api.prunes) != 2 || api.prunes[1].Labels["team"] != "dev" {
		t.Fatalf("label prune options = %+v", api.prunes)
	}
	model.overlay, model.removeForce, model.removeParents = removeOverlay, true, true
	model, cmd = model.handleOverlay(testKey("y"))
	model, _ = model.Update(commandMessage(cmd))
	if len(api.removes) != 1 || !api.removes[0].Force || !api.removes[0].PruneChildren {
		t.Fatalf("remove = %+v", api.removes)
	}
	api.err = &backend.AppError{Code: backend.ErrorConflict, Operation: "tag"}
	model.overlay, model.editPrimary = tagOverlay, "repo:tag"
	model, cmd = model.handleOverlay(testKey("enter"))
	model, _ = model.Update(commandMessage(cmd))
	if !strings.Contains(model.notice, "conflict") {
		t.Fatalf("notice = %q", model.notice)
	}
	model.hasData = true
	if view := model.SetSize(120, 25).View(); !strings.Contains(view, "operation conflicts") {
		t.Fatalf("command failure is not visible in the page: %s", view)
	}
	if text := commandErrorText(removeOverlay, api.err); !strings.Contains(text, "still used by a container") {
		t.Fatalf("remove error is not actionable: %q", text)
	}
	if got := errorText(errors.New("raw")); !strings.Contains(got, "operation failed") {
		t.Fatalf("fallback = %q", got)
	}
}

func TestPullValidationAndTrackerRegistration(t *testing.T) {
	model, _, api := activeModel(t)
	tracker := &fakeTracker{ids: map[string]bool{}}
	model.jobs, model.overlay, model.editPrimary, model.editSecondary = tracker, pullOverlay, "alpine", "bad"
	model, cmd := model.handleOverlay(testKey("enter"))
	if cmd != nil || len(api.pulls) != 0 {
		t.Fatal("invalid platform was submitted")
	}
	job := &testJob{id: "job-1"}
	api.job = job
	model.editSecondary = "linux/amd64"
	model, cmd = model.handleOverlay(testKey("enter"))
	model, register := model.Update(commandMessage(cmd))
	if register == nil {
		t.Fatal("pull handle was not registered")
	}
	_ = register()
	if tracker.registered != 1 || model.overlay != noOverlay {
		t.Fatal("tracker/overlay state incorrect")
	}
	api.err = &backend.AppError{Code: backend.ErrorConflict, Operation: "pull"}
	model.overlay, model.editPrimary = pullOverlay, "busy"
	model, cmd = model.handleOverlay(testKey("enter"))
	model, _ = model.Update(commandMessage(cmd))
	if tracker.registered != 1 || model.overlay != pullOverlay {
		t.Fatal("conflicted pull registered or closed editor")
	}
}

type testJob struct{ id string }

func (job *testJob) ID() string                                      { return job.id }
func (*testJob) Progress() <-chan backend.ProgressEvent              { return make(chan backend.ProgressEvent) }
func (job *testJob) Wait(context.Context) (backend.JobResult, error) { return backend.JobResult{}, nil }
func (*testJob) Cancel() error                                       { return nil }

func testKey(value string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Text: value, Code: []rune(value)[0]})
}

func commandMessage(command tea.Cmd) tea.Msg {
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		return batch[0]()
	}
	return message
}
