package containers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendcontainers "github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
)

type fakeBackend struct {
	mu           sync.Mutex
	calls        []string
	subscription *fakeSubscription
}

func (fake *fakeBackend) RequestRefresh(page backend.Page) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = append(fake.calls, "refresh:"+string(page))
	return nil
}

func (fake *fakeBackend) Subscribe(_ context.Context, page backend.Page, _ backend.EventFilter) (backend.Subscription, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = append(fake.calls, "subscribe:"+string(page))
	fake.subscription = newFakeSubscription()
	return fake.subscription, nil
}

func (fake *fakeBackend) recorded() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]string(nil), fake.calls...)
}

type fakeSubscription struct {
	events chan backend.EventEnvelope
	closed chan struct{}
	once   sync.Once
}

func newFakeSubscription() *fakeSubscription {
	return &fakeSubscription{events: make(chan backend.EventEnvelope, 16), closed: make(chan struct{})}
}
func (fake *fakeSubscription) Events() <-chan backend.EventEnvelope { return fake.events }
func (fake *fakeSubscription) Close() error {
	fake.once.Do(func() { close(fake.closed); close(fake.events) })
	return nil
}

type fakeAPI struct {
	requests      []backend.RefreshKey
	commands      []string
	commandResult backend.CommandResult
	commandErr    error
	logs          backend.Stream[backendcontainers.LogEntry]
	stats         backend.Stream[backendcontainers.StatsSample]
}

type cancelAPI struct {
	*fakeAPI
	started chan struct{}
}

func (api *cancelAPI) Start(ctx context.Context, _ string) (backend.CommandResult, error) {
	close(api.started)
	<-ctx.Done()
	return backend.CommandResult{}, ctx.Err()
}

func (fake *fakeAPI) RequestDetails(id string) error {
	fake.requests = append(fake.requests, backend.RefreshKey{Kind: backendcontainers.RefreshKindDetails, ID: id})
	return nil
}
func (fake *fakeAPI) RequestProcesses(id string) error {
	fake.requests = append(fake.requests, backend.RefreshKey{Kind: backendcontainers.RefreshKindProcesses, ID: id})
	return nil
}
func (fake *fakeAPI) command(value string) (backend.CommandResult, error) {
	fake.commands = append(fake.commands, value)
	return fake.commandResult, fake.commandErr
}
func (fake *fakeAPI) Start(context.Context, string) (backend.CommandResult, error) {
	return fake.command("start")
}
func (fake *fakeAPI) Stop(context.Context, string, backendcontainers.StopOptions) (backend.CommandResult, error) {
	return fake.command("stop")
}
func (fake *fakeAPI) Restart(context.Context, string, backendcontainers.RestartOptions) (backend.CommandResult, error) {
	return fake.command("restart")
}
func (fake *fakeAPI) Pause(context.Context, string) (backend.CommandResult, error) {
	return fake.command("pause")
}
func (fake *fakeAPI) Unpause(context.Context, string) (backend.CommandResult, error) {
	return fake.command("unpause")
}
func (fake *fakeAPI) Kill(context.Context, string, backendcontainers.KillOptions) (backend.CommandResult, error) {
	return fake.command("kill")
}
func (fake *fakeAPI) Rename(_ context.Context, _ string, options backendcontainers.RenameOptions) (backend.CommandResult, error) {
	return fake.command("rename:" + options.Name)
}
func (fake *fakeAPI) Remove(_ context.Context, _ string, options backendcontainers.RemoveOptions) (backend.CommandResult, error) {
	return fake.command("remove:" + boolText(options.Force) + ":" + boolText(options.RemoveVolumes))
}
func (fake *fakeAPI) Logs(context.Context, string, backendcontainers.LogsOptions) (backend.Stream[backendcontainers.LogEntry], error) {
	return fake.logs, nil
}
func (fake *fakeAPI) Stats(context.Context, string, backendcontainers.StatsOptions) (backend.Stream[backendcontainers.StatsSample], error) {
	return fake.stats, nil
}

func TestActivationSubscribesBeforeRefreshAndAppliesList(t *testing.T) {
	common, api := &fakeBackend{}, &fakeAPI{}
	model, command := New(context.Background(), common, api).Activate()
	ready := command().(subscriptionReadyMsg)
	if calls := common.recorded(); len(calls) != 1 || calls[0] != "subscribe:containers" {
		t.Fatalf("activation calls = %v", calls)
	}
	model, command = model.Update(ready)
	commands := batchCommands(command)
	if len(commands) != 3 {
		t.Fatalf("subscription batch has %d commands", len(commands))
	}
	requested := commands[1]().(requestFinishedMsg)
	model, _ = model.Update(requested)
	if calls := common.recorded(); len(calls) != 2 || calls[1] != "refresh:containers" {
		t.Fatalf("post-subscribe calls = %v", calls)
	}

	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	model, detailCommand := model.Update(eventReceivedMsg{generation: model.generation, open: true, event: backend.EventEnvelope{
		Time: now, Key: backend.RefreshKey{Kind: backendcontainers.RefreshKindList},
		Payload: backendcontainers.ListUpdated{Containers: []backendcontainers.Summary{{ID: "b", Names: []string{"beta"}}, {ID: "a", Names: []string{"alpha"}}}},
	}})
	if !model.hasData || model.loading || model.selectedID != "a" || model.updatedAt != now {
		t.Fatalf("list state not applied: %#v", model)
	}
	if detailCommand == nil {
		t.Fatal("initial selection did not automatically request details")
	}
	detailCommands := batchCommands(detailCommand)
	if len(detailCommands) != 2 {
		t.Fatalf("detail update returned %d commands", len(detailCommands))
	}
	_ = detailCommands[1]()
	if len(api.requests) != 1 || api.requests[0].ID != "a" || api.requests[0].Kind != backendcontainers.RefreshKindDetails {
		t.Fatalf("automatic detail requests = %#v", api.requests)
	}
	if view := model.SetSize(100, 20).View(); !strings.Contains(view, "alpha") || !strings.Contains(view, "beta") {
		t.Fatalf("list view missing containers:\n%s", view)
	}
}

func TestManualRefreshStartsHeaderSpinner(t *testing.T) {
	model := New(context.Background(), &fakeBackend{}, &fakeAPI{})
	model.active, model.generation = true, 1
	model, command := model.Refresh()
	if model.Activity() == "" {
		t.Fatal("manual refresh did not expose header activity")
	}
	if commands := batchCommands(command); len(commands) != 2 {
		t.Fatalf("manual refresh returned %d commands, want request and spinner tick", len(commands))
	}
}

func TestSelectionLoadsDetailsAndQuickActionsAreInline(t *testing.T) {
	api := &fakeAPI{}
	model := New(context.Background(), &fakeBackend{}, api).SetSize(140, 28)
	model.active, model.generation, model.hasData = true, 1, true
	model.containers = []backendcontainers.Summary{{ID: "a", Names: []string{"alpha"}}, {ID: "b", Names: []string{"beta"}}}
	model.selectedID, model.detailsID = "a", "a"

	model, command := model.handleKey(keyPress("down"))
	if model.selectedID != "b" || command == nil {
		t.Fatalf("selection did not request new details: selected=%q command=%v", model.selectedID, command)
	}
	_ = command()
	if len(api.requests) != 1 || api.requests[0].ID != "b" {
		t.Fatalf("selection detail requests = %#v", api.requests)
	}
	view := model.View()
	for _, want := range []string{"CONTAINERS", "CONTAINER DETAILS", "STATE", "HEALTH", "PORTS", "PROJECT"} {
		if !strings.Contains(view, want) {
			t.Fatalf("workspace missing %q:\n%s", want, view)
		}
	}
}

func TestContainerWorkspaceShowsOperationalColumnsAndComposeIdentity(t *testing.T) {
	model := New(context.Background(), &fakeBackend{}, &fakeAPI{}).SetSize(170, 30)
	model.active, model.hasData, model.selectedID, model.detailsID = true, true, "abc", "abc"
	model.containers = []backendcontainers.Summary{{
		ID: "abc", Names: []string{"web-1"}, Image: "nginx:alpine", State: "running", Health: "healthy",
		Ports:  []backendcontainers.Port{{IP: "0.0.0.0", PublicPort: 8080, PrivatePort: 80, Protocol: "tcp"}},
		Labels: map[string]string{"com.docker.compose.project": "website", "com.docker.compose.service": "web"},
	}}
	model.details = backendcontainers.Details{ID: "abc", Name: "web-1", Image: "nginx:alpine", State: backendcontainers.State{Status: "running", Health: "healthy"}}

	view := model.View()
	for _, want := range []string{"web-1", "nginx:alpine", "running", "healthy", "8080→80/tcp", "website", "Service:", "web"} {
		if !strings.Contains(view, want) {
			t.Fatalf("container workspace missing %q:\n%s", want, view)
		}
	}
}

func TestSelectionFiltersAndVanishedContainer(t *testing.T) {
	model := New(context.Background(), &fakeBackend{}, &fakeAPI{})
	model.active, model.hasData, model.generation = true, true, 1
	model.containers = []backendcontainers.Summary{
		{ID: "1", Names: []string{"web"}, State: "running", Labels: map[string]string{"team": "blue"}},
		{ID: "2", Names: []string{"db"}, State: "exited", Labels: map[string]string{"team": "red"}},
	}
	model.selectedID = "1"
	model.filter = "state:exited label:team=red"
	model.ensureVisibleSelection()
	if visible := model.visible(); len(visible) != 1 || visible[0].ID != "2" || model.selectedID != "2" {
		t.Fatalf("filter/selection = %#v selected=%q", visible, model.selectedID)
	}
	model.mode, model.detailsID = detailsView, "2"
	model, _ = model.handleEvent(backend.EventEnvelope{Payload: backendcontainers.ListUpdated{Containers: []backendcontainers.Summary{{ID: "1", Names: []string{"web"}}}}})
	if model.selectedID != "" || model.mode != detailsView || model.detailsID != "" {
		t.Fatalf("vanished selection remained: selected=%q mode=%d details=%q", model.selectedID, model.mode, model.detailsID)
	}
}

func TestTargetedEventsRequireCurrentKeyAndIdentity(t *testing.T) {
	model := New(context.Background(), &fakeBackend{}, &fakeAPI{})
	model.active, model.generation, model.selectedID, model.mode = true, 3, "wanted", detailsView
	wrong := backendcontainers.DetailsUpdated{Container: backendcontainers.Details{ID: "other", Name: "wrong"}}
	model, _ = model.handleEvent(backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendcontainers.RefreshKindDetails, ID: "other"}, Payload: wrong})
	if model.detailsID != "" {
		t.Fatal("mismatched details were accepted")
	}
	want := backendcontainers.DetailsUpdated{Container: backendcontainers.Details{ID: "wanted", Name: "right"}}
	model, _ = model.handleEvent(backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendcontainers.RefreshKindDetails, ID: "wanted"}, Payload: want})
	if model.detailsID != "wanted" || model.details.Name != "right" {
		t.Fatal("matching details were not accepted")
	}
}

func TestRefreshFailureAndOverflowPreserveData(t *testing.T) {
	common := &fakeBackend{}
	model := New(context.Background(), common, &fakeAPI{})
	model.active, model.generation, model.hasData = true, 2, true
	model.containers = []backendcontainers.Summary{{ID: "a"}}
	model, _ = model.handleEvent(backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendcontainers.RefreshKindList}, Payload: backend.RefreshFailed{Err: errors.New("unavailable")}})
	if !model.hasData || !model.stale || model.err == nil {
		t.Fatalf("failure erased data or stale state: %#v", model)
	}
	model, command := model.handleEvent(backend.EventEnvelope{Payload: backend.SubscriberOverflow{DroppedSequence: 4}})
	if !model.loading || !model.stale || len(batchCommands(command)) < 2 {
		t.Fatalf("overflow recovery state/commands invalid")
	}
}

func TestCommandsAndDestructiveConfirmation(t *testing.T) {
	api := &fakeAPI{}
	model := New(context.Background(), &fakeBackend{}, api)
	model.active, model.generation, model.selectedID, model.pageCtx = true, 1, "abc", context.Background()
	model.containers = []backendcontainers.Summary{{ID: "abc", Names: []string{"web"}}}
	model, command := model.startCommand(commandStart, "")
	if model.pendingOperation != "start" {
		t.Fatal("start was not marked pending")
	}
	finished := mustFinish(t, command)
	model, _ = model.Update(finished)
	if len(api.commands) != 1 || api.commands[0] != "start" || model.pendingOperation != "" || model.notice != "" {
		t.Fatalf("start command state = %v pending=%q notice=%q", api.commands, model.pendingOperation, model.notice)
	}

	model, command = model.chooseAction(commandRemove)
	if command != nil || model.overlay != confirmOverlay || len(api.commands) != 1 {
		t.Fatal("remove executed without confirmation")
	}
	model.force, model.volumes = true, true
	model.overlay = noOverlay
	model, command = model.startCommand(commandRemove, "")
	_ = mustFinish(t, command)
	if got := api.commands[len(api.commands)-1]; got != "remove:true:true" {
		t.Fatalf("remove options = %q", got)
	}
}

func TestQuickCommandsRequireYNConfirmation(t *testing.T) {
	api := &fakeAPI{}
	model := New(context.Background(), &fakeBackend{}, api).SetSize(80, 24)
	model.active, model.generation, model.selectedID, model.hasData, model.pageCtx = true, 1, "abc", true, context.Background()
	model.containers = []backendcontainers.Summary{{ID: "abc", Names: []string{"web"}, State: "running"}}

	for _, key := range []string{"s", "x", "R", "p", "d"} {
		updated, command := model.handleKey(keyPress(key))
		if command != nil || updated.overlay != confirmOverlay {
			t.Fatalf("%s executed without confirmation: overlay=%v cmd=%v", key, updated.overlay, command != nil)
		}
		if !strings.Contains(updated.View(), "Are you sure you want to") {
			t.Fatalf("%s confirmation prompt missing:\n%s", key, updated.View())
		}
		canceled, command := updated.handleKey(keyPress("n"))
		if command != nil || canceled.overlay != noOverlay || len(api.commands) != 0 {
			t.Fatalf("%s n did not cancel cleanly", key)
		}
	}

	model, command := model.handleKey(keyPress("s"))
	if command != nil || model.confirm != commandStart {
		t.Fatal("start confirmation was not opened")
	}
	model, command = model.handleKey(keyPress("y"))
	if command == nil || model.overlay != progressOverlay || model.pendingOperation != "start" {
		t.Fatal("y did not start the confirmed command")
	}
	if !strings.Contains(model.View(), "Start web") {
		t.Fatalf("progress overlay missing:\n%s", model.View())
	}
	model, extra := model.handleKey(keyPress("s"))
	if extra != nil || model.overlay != progressOverlay || len(api.commands) != 0 {
		t.Fatal("repeat command was accepted while Docker was still working")
	}
	finished := mustFinish(t, command)
	model, _ = model.Update(finished)
	if model.overlay != noOverlay || model.pendingOperation != "" {
		t.Fatal("progress overlay was not closed after the command finished")
	}
	if api.commands[0] != "start" {
		t.Fatalf("confirmed command = %v", api.commands)
	}
}

func TestCommandProgressSpinnerKeepsTicking(t *testing.T) {
	model := New(context.Background(), &fakeBackend{}, &fakeAPI{})
	model.active, model.generation, model.selectedID, model.pageCtx = true, 1, "abc", context.Background()
	model, command := model.startCommand(commandStart, "")
	commands := commandCmds(command)
	if len(commands) < 2 {
		t.Fatal("command progress did not start a spinner tick")
	}
	var tick tea.Msg
	for _, item := range commands {
		message := item()
		if _, ok := message.(commandFinishedMsg); ok {
			continue
		}
		tick = message
	}
	if tick == nil {
		t.Fatal("command batch did not include a spinner tick message")
	}
	model, next := model.Update(tick)
	if model.Activity() == "" || next == nil {
		t.Fatal("progress spinner did not continue after the first tick")
	}
}

func TestEveryContainerCommandUsesExistingAPI(t *testing.T) {
	want := []string{"start", "stop", "restart", "pause", "unpause", "kill", "rename:new-name", "remove:false:false"}
	operations := []struct {
		kind commandKind
		name string
	}{
		{commandStart, ""}, {commandStop, ""}, {commandRestart, ""}, {commandPause, ""},
		{commandUnpause, ""}, {commandKill, ""}, {commandRename, "new-name"}, {commandRemove, ""},
	}
	api := &fakeAPI{}
	for _, operation := range operations {
		model := New(context.Background(), &fakeBackend{}, api)
		model.active, model.generation, model.selectedID, model.pageCtx = true, 1, "abc", context.Background()
		_, command := model.startCommand(operation.kind, operation.name)
		if command == nil {
			t.Fatalf("%s returned no command", operation.kind)
		}
		_ = mustFinish(t, command)
	}
	if strings.Join(api.commands, ",") != strings.Join(want, ",") {
		t.Fatalf("commands = %v, want %v", api.commands, want)
	}
}

func TestLeavingPageCancelsCommandWaitAndRejectsItsLateMessage(t *testing.T) {
	api := &cancelAPI{fakeAPI: &fakeAPI{}, started: make(chan struct{})}
	model := New(context.Background(), &fakeBackend{}, api)
	model.active, model.generation, model.selectedID = true, 4, "abc"
	model.pageCtx, model.cancelPage = context.WithCancel(context.Background())
	model, command := model.startCommand(commandStart, "")
	results := make(chan tea.Msg, 1)
	go func() {
		result, ok := finishResult(command)
		if ok {
			results <- result
		}
	}()
	<-api.started
	model = model.Deactivate()
	message := <-results
	model, _ = model.Update(message)
	if model.notice != "" || model.pendingOperation != "" {
		t.Fatalf("late canceled wait changed inactive page: notice=%q pending=%q", model.notice, model.pendingOperation)
	}
}

func TestDetailsRenderEnvironmentValuesSafelyAndFit(t *testing.T) {
	model := New(context.Background(), &fakeBackend{}, &fakeAPI{}).SetSize(160, 30)
	model.active, model.hasData, model.selectedID, model.mode = true, true, "abc", detailsView
	model.containers = []backendcontainers.Summary{{ID: "abc", Names: []string{"demo"}}}
	model.detailsID = "abc"
	model.details = backendcontainers.Details{
		ID: "abc", Name: "demo", Environment: []string{"TOKEN=visible-value", "BAD=\x1b[2Jclear"},
		State: backendcontainers.State{Status: "future-state"},
	}
	view := model.View()
	if !strings.Contains(view, "TOKEN=visible-value") || strings.Contains(view, "\x1b[2J") {
		t.Fatalf("environment rendering is incorrect:\n%s", view)
	}
	if lines := strings.Count(view, "\n") + 1; lines > 30 {
		t.Fatalf("view height = %d, want <= 30", lines)
	}
}

func TestProcessesConflictIsTargetedUnavailable(t *testing.T) {
	model := New(context.Background(), &fakeBackend{}, &fakeAPI{})
	model.active, model.generation, model.hasData, model.selectedID, model.mode = true, 1, true, "abc", processesView
	model.containers = []backendcontainers.Summary{{ID: "abc"}}
	err := &backend.AppError{Code: backend.ErrorConflict, Operation: "list container processes"}
	model.applyRefreshFailure(backend.RefreshKey{Kind: backendcontainers.RefreshKindProcesses, ID: "abc"}, err)
	if !model.processState.unavailable || model.err != nil || !model.hasData {
		t.Fatalf("process conflict leaked to page: process=%#v pageErr=%v", model.processState, model.err)
	}
}

func TestProcessesOpenAsScrollableFloatingOverlay(t *testing.T) {
	model := New(context.Background(), &fakeBackend{}, &fakeAPI{}).SetSize(160, 30)
	model.active, model.hasData, model.generation, model.pageCtx = true, true, 1, context.Background()
	model.selectedID = "abc"
	model.containers = []backendcontainers.Summary{{ID: "abc", Names: []string{"demo"}}}
	model, command := model.handleKey(keyPress("P"))
	if command == nil || model.overlay != processesOverlay || model.mode != processesView {
		t.Fatalf("processes did not open as an overlay: overlay=%d mode=%d command=%v", model.overlay, model.mode, command != nil)
	}
	model.processState = panelState{}
	model.processes = backendcontainers.ProcessesUpdated{
		ContainerID: "abc",
		Titles:      []string{"PID", "USER", "COMMAND"},
		Rows:        [][]string{{"1", "root", "worker --serve"}, {"2", "root", "helper"}},
	}
	model.processFilter = "worker"

	view := model.View()
	for _, want := range []string{"CONTAINERS", "PROCESSES — demo", "Filter: worker", "PID  USER  COMMAND", "worker --serve", "j/k scroll", "esc close"} {
		if !strings.Contains(view, want) {
			t.Fatalf("process overlay missing %q:\n%s", want, view)
		}
	}
}

func TestDeactivationClosesSubscriptionAndRejectsLateEvent(t *testing.T) {
	common := &fakeBackend{}
	model, command := New(context.Background(), common, &fakeAPI{}).Activate()
	ready := command().(subscriptionReadyMsg)
	model, _ = model.Update(ready)
	generation := model.generation
	model = model.Deactivate()
	select {
	case <-common.subscription.closed:
	default:
		t.Fatal("subscription was not closed")
	}
	model, _ = model.Update(eventReceivedMsg{generation: generation, open: true, event: backend.EventEnvelope{Payload: backendcontainers.ListUpdated{Containers: []backendcontainers.Summary{{ID: "late"}}}}})
	if len(model.containers) != 0 {
		t.Fatal("late event changed inactive model")
	}
}

func commandCmds(command tea.Cmd) []tea.Cmd {
	if command == nil {
		return nil
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		return append([]tea.Cmd(nil), batch...)
	}
	return []tea.Cmd{func() tea.Msg { return message }}
}

func mustFinish(t *testing.T, command tea.Cmd) commandFinishedMsg {
	t.Helper()
	finished, ok := finishResult(command)
	if !ok {
		t.Fatal("command did not produce a finished result")
	}
	return finished
}

func finishResult(command tea.Cmd) (commandFinishedMsg, bool) {
	for _, item := range commandCmds(command) {
		if item == nil {
			continue
		}
		message := item()
		if finished, ok := message.(commandFinishedMsg); ok {
			return finished, true
		}
	}
	return commandFinishedMsg{}, false
}

func batchCommands(command tea.Cmd) tea.BatchMsg {
	if command == nil {
		return nil
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		return batch
	}
	return tea.BatchMsg{func() tea.Msg { return message }}
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func keyPress(value string) tea.KeyPressMsg {
	if value == "down" {
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})
	}
	return tea.KeyPressMsg(tea.Key{Text: value, Code: []rune(value)[0]})
}
