package volumes

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendvolumes "github.com/petar030/ssh-native-docker-tui/internal/backend/volumes"
)

type fakeBackend struct {
	steps    []string
	requests int
	sub      *fakeSubscription
}

func (f *fakeBackend) RequestRefresh(p backend.Page) error {
	f.steps = append(f.steps, "refresh:"+string(p))
	f.requests++
	return nil
}
func (f *fakeBackend) Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error) {
	f.steps = append(f.steps, "subscribe")
	f.sub = &fakeSubscription{events: make(chan backend.EventEnvelope, 2)}
	return f.sub, nil
}

type fakeSubscription struct {
	events chan backend.EventEnvelope
	closed bool
}

func (s *fakeSubscription) Events() <-chan backend.EventEnvelope { return s.events }
func (s *fakeSubscription) Close() error                         { s.closed = true; return nil }

type fakeAPI struct {
	details, attachments []string
	creates              []backendvolumes.CreateOptions
	removes              []backendvolumes.RemoveOptions
	prunes               []backendvolumes.PruneOptions
	err                  error
}

func (f *fakeAPI) RequestDetails(v string) error { f.details = append(f.details, v); return nil }
func (f *fakeAPI) RequestAttachments(v string) error {
	f.attachments = append(f.attachments, v)
	return nil
}
func (f *fakeAPI) Create(_ context.Context, v backendvolumes.CreateOptions) (backend.CommandResult, error) {
	f.creates = append(f.creates, v)
	return backend.CommandResult{}, f.err
}
func (f *fakeAPI) Remove(_ context.Context, _ string, v backendvolumes.RemoveOptions) (backend.CommandResult, error) {
	f.removes = append(f.removes, v)
	return backend.CommandResult{}, f.err
}
func (f *fakeAPI) Prune(_ context.Context, v backendvolumes.PruneOptions) (backend.CommandResult, error) {
	f.prunes = append(f.prunes, v)
	return backend.CommandResult{}, f.err
}

func ready(t *testing.T) (Model, *fakeBackend, *fakeAPI) {
	t.Helper()
	b, a := &fakeBackend{}, &fakeAPI{}
	m, cmd := New(context.Background(), b, a).Activate()
	m, batch := updateReady(m, cmd())
	_ = batch[1]()
	if strings.Join(b.steps, ",") != "subscribe,refresh:volumes" {
		t.Fatalf("order=%v", b.steps)
	}
	return m, b, a
}
func updateReady(m Model, msg tea.Msg) (Model, tea.BatchMsg) {
	m, cmd := m.Update(msg)
	return m, cmd().(tea.BatchMsg)
}
func key(v string) tea.KeyPressMsg { return tea.KeyPressMsg(tea.Key{Text: v, Code: []rune(v)[0]}) }

func commandMessage(command tea.Cmd) tea.Msg {
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		return batch[0]()
	}
	return message
}

func TestListWarningsUnknownUsageAndKeyedTargets(t *testing.T) {
	m, b, a := ready(t)
	m, cmd := m.handleEvent(backend.EventEnvelope{Time: time.Now(), Payload: backendvolumes.ListUpdated{Volumes: []backendvolumes.Volume{{Name: "alpha", UsageKnown: false}, {Name: "beta", UsageKnown: true, Size: 1024}}, Warnings: []string{"warn\nunsafe"}}})
	_ = cmd()
	if m.selected != "alpha" || len(a.details) != 1 {
		t.Fatalf("selection/details=%q %v", m.selected, a.details)
	}
	m, _ = m.handleEvent(backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendvolumes.RefreshKindDetails, ID: "beta"}, Payload: backendvolumes.DetailsUpdated{Volume: backendvolumes.Volume{Name: "beta"}}})
	if m.detailsName != "" {
		t.Fatal("mismatched detail applied")
	}
	m, _ = m.handleEvent(backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendvolumes.RefreshKindDetails, ID: "alpha"}, Payload: backendvolumes.DetailsUpdated{Volume: backendvolumes.Volume{Name: "alpha"}}})
	view := m.SetSize(120, 25).View()
	if !strings.Contains(view, "Docker warning: warn unsafe") || !strings.Contains(view, "unknown") {
		t.Fatalf("warning/unknown missing:\n%s", view)
	}
	m.showAttachments = true
	m, overflow := m.handleEvent(backend.EventEnvelope{Payload: backend.SubscriberOverflow{}})
	before := b.requests
	batch := overflow().(tea.BatchMsg)
	_ = batch[0]()
	_ = batch[2]()
	if b.requests != before+1 || len(a.attachments) != 1 {
		t.Fatalf("overflow recovery %d %v", b.requests-before, a.attachments)
	}
	m = m.Deactivate()
	if !b.sub.closed {
		t.Fatal("subscription not closed")
	}
}

func TestIdentityFilterCreateRemoveAndSafePrune(t *testing.T) {
	m, _, a := ready(t)
	m.values = []backendvolumes.Volume{{Name: "z"}, {Name: "a"}}
	m.hasData = true
	m.chooseExisting()
	if m.selected != "a" {
		t.Fatalf("selected=%s", m.selected)
	}
	m.filter = "z"
	m.chooseExisting()
	if m.selected != "z" {
		t.Fatalf("filtered selected=%s", m.selected)
	}
	m.overlay = pruneOverlay
	m, cmd := m.handleOverlay(key("enter"))
	if cmd != nil || len(a.prunes) != 0 || m.notice == "" {
		t.Fatal("unscoped prune submitted")
	}
	m.fields[0] = "fixture"
	m.fields[1] = "yes"
	m.pruneAll = true
	m, cmd = m.handleOverlay(key("enter"))
	m, _ = m.Update(commandMessage(cmd))
	if len(a.prunes) != 1 || !a.prunes[0].All || a.prunes[0].Labels["fixture"] != "yes" {
		t.Fatalf("prune=%+v", a.prunes)
	}
	m.overlay = removeOverlay
	m.removeForce = true
	m.selected = "z"
	m, cmd = m.handleOverlay(key("y"))
	m, _ = m.Update(commandMessage(cmd))
	if len(a.removes) != 1 || !a.removes[0].Force {
		t.Fatalf("remove=%+v", a.removes)
	}
	m.overlay = createOverlay
	m.fields = [6]string{"new", "local", "team", "dev", "type", "none"}
	m, cmd = m.handleOverlay(key("enter"))
	m, _ = m.Update(commandMessage(cmd))
	if len(a.creates) != 1 || a.creates[0].Labels["team"] != "dev" || a.creates[0].DriverOptions["type"] != "none" {
		t.Fatalf("create=%+v", a.creates)
	}
}

func TestConflictStaysSelectedAndLayoutsSanitize(t *testing.T) {
	m, _, a := ready(t)
	m.selected = "v"
	m.values = []backendvolumes.Volume{{Name: "v\x1b[31m"}}
	m.hasData = true
	a.err = &backend.AppError{Code: backend.ErrorConflict, Operation: "remove"}
	m.overlay = removeOverlay
	m, cmd := m.handleOverlay(key("y"))
	m, _ = m.Update(commandMessage(cmd))
	if m.selected != "v" || !strings.Contains(m.notice, "in use") {
		t.Fatalf("conflict=%q selected=%q", m.notice, m.selected)
	}
	wide := m.SetSize(120, 25).View()
	narrow := m.SetSize(90, 25).View()
	if strings.Contains(wide, "\x1b[31m") || wide == "" || narrow == "" {
		t.Fatal("layout/sanitization failed")
	}
}
