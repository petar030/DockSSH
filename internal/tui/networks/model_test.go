package networks

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendnetworks "github.com/petar030/ssh-native-docker-tui/internal/backend/networks"
	"strings"
	"testing"
)

type fakeBackend struct {
	steps    []string
	requests int
	sub      *fakeSub
}

func (f *fakeBackend) RequestRefresh(p backend.Page) error {
	f.steps = append(f.steps, "refresh:"+string(p))
	f.requests++
	return nil
}
func (f *fakeBackend) Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error) {
	f.steps = append(f.steps, "subscribe")
	f.sub = &fakeSub{events: make(chan backend.EventEnvelope, 2)}
	return f.sub, nil
}

type fakeSub struct {
	events chan backend.EventEnvelope
	closed bool
}

func (s *fakeSub) Events() <-chan backend.EventEnvelope { return s.events }
func (s *fakeSub) Close() error                         { s.closed = true; return nil }

type fakeAPI struct {
	details, connections []string
	creates              []backendnetworks.CreateOptions
	removes              int
	prunes               []backendnetworks.PruneOptions
	connects             []backendnetworks.ConnectOptions
	disconnects          []backendnetworks.DisconnectOptions
	err                  error
}

func (f *fakeAPI) RequestDetails(v string) error { f.details = append(f.details, v); return nil }
func (f *fakeAPI) RequestConnections(v string) error {
	f.connections = append(f.connections, v)
	return nil
}
func (f *fakeAPI) Create(_ context.Context, v backendnetworks.CreateOptions) (backend.CommandResult, error) {
	f.creates = append(f.creates, v)
	return backend.CommandResult{}, f.err
}
func (f *fakeAPI) Remove(context.Context, string, backendnetworks.RemoveOptions) (backend.CommandResult, error) {
	f.removes++
	return backend.CommandResult{}, f.err
}
func (f *fakeAPI) Prune(_ context.Context, v backendnetworks.PruneOptions) (backend.CommandResult, error) {
	f.prunes = append(f.prunes, v)
	return backend.CommandResult{}, f.err
}
func (f *fakeAPI) Connect(_ context.Context, _ string, v backendnetworks.ConnectOptions) (backend.CommandResult, error) {
	f.connects = append(f.connects, v)
	return backend.CommandResult{}, f.err
}
func (f *fakeAPI) Disconnect(_ context.Context, _ string, v backendnetworks.DisconnectOptions) (backend.CommandResult, error) {
	f.disconnects = append(f.disconnects, v)
	return backend.CommandResult{}, f.err
}
func ready(t *testing.T) (Model, *fakeBackend, *fakeAPI) {
	b, a := &fakeBackend{}, &fakeAPI{}
	m, c := New(context.Background(), b, a).Activate()
	m, c = m.Update(c())
	batch := c().(tea.BatchMsg)
	_ = batch[1]()
	if strings.Join(b.steps, ",") != "subscribe,refresh:networks" {
		t.Fatalf("order=%v", b.steps)
	}
	return m, b, a
}
func key(v string) tea.KeyPressMsg { return tea.KeyPressMsg(tea.Key{Text: v, Code: []rune(v)[0]}) }
func TestTargetsAddressesOverflowAndCleanup(t *testing.T) {
	m, b, a := ready(t)
	m, c := m.handleEvent(backend.EventEnvelope{Payload: backendnetworks.ListUpdated{Networks: []backendnetworks.Summary{{ID: "1", Name: "alpha"}, {ID: "2", Name: "beta"}}}})
	_ = c()
	m, _ = m.handleEvent(backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendnetworks.RefreshKindDetails, ID: "2"}, Payload: backendnetworks.DetailsUpdated{Network: backendnetworks.Details{Summary: backendnetworks.Summary{ID: "2"}}}})
	if m.detailsID != "" {
		t.Fatal("mismatch applied")
	}
	m.showConnections = true
	m.selected = "1"
	m, _ = m.handleEvent(backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendnetworks.RefreshKindConnections, ID: "1"}, Payload: backendnetworks.ConnectionsUpdated{NetworkID: "1", Connections: []backendnetworks.Connection{{ContainerName: "c", MACAddress: "", IPv4Address: "10.0.0.2/24"}}}})
	view := m.SetSize(120, 25).View()
	if !strings.Contains(view, "10.0.0") || !strings.Contains(view, "—") {
		t.Fatalf("addresses=%s", view)
	}
	m, c = m.handleEvent(backend.EventEnvelope{Payload: backend.SubscriberOverflow{}})
	before := b.requests
	batch := c().(tea.BatchMsg)
	_ = batch[0]()
	_ = batch[2]()
	if b.requests != before+1 || len(a.connections) != 1 {
		t.Fatal("overflow recovery failed")
	}
	m = m.Deactivate()
	if !b.sub.closed {
		t.Fatal("not closed")
	}
}
func TestFormsShapesSafePruneAndConflict(t *testing.T) {
	m, _, a := ready(t)
	m.selected = "net"
	m.overlay = pruneOverlay
	m, c := m.handleOverlay(key("enter"))
	if c != nil || len(a.prunes) != 0 {
		t.Fatal("empty prune submitted")
	}
	m.fields[0] = "24h"
	m, c = m.handleOverlay(key("enter"))
	m, _ = m.Update(c())
	if len(a.prunes) != 1 || a.prunes[0].Until != "24h" {
		t.Fatalf("prune=%+v", a.prunes)
	}
	m.overlay = connectOverlay
	m.fields = [8]string{"container", "10.0.0.5", "fd00::5", "one,two", "4"}
	m, c = m.handleOverlay(key("enter"))
	m, _ = m.Update(c())
	if len(a.connects) != 1 || len(a.connects[0].Aliases) != 2 || a.connects[0].GwPriority != 4 {
		t.Fatalf("connect=%+v", a.connects)
	}
	m.overlay = disconnectOverlay
	m.fields[0] = "container"
	m.disconnectForce = true
	m, c = m.handleOverlay(key("enter"))
	m, _ = m.Update(c())
	if len(a.disconnects) != 1 || !a.disconnects[0].Force {
		t.Fatalf("disconnect=%+v", a.disconnects)
	}
	a.err = &backend.AppError{Code: backend.ErrorConflict, Operation: "remove"}
	m.overlay = removeOverlay
	m, c = m.handleOverlay(key("y"))
	m, _ = m.Update(c())
	if !strings.Contains(m.notice, "active endpoints") {
		t.Fatalf("conflict=%q", m.notice)
	}
}
func TestCreateIdentityAndSanitizeLayouts(t *testing.T) {
	m, _, a := ready(t)
	m.values = []backendnetworks.Summary{{ID: "z", Name: "z"}, {ID: "a", Name: "a\x1b[31m"}}
	m.hasData = true
	m.choose()
	if m.selected != "a" {
		t.Fatalf("selection=%s", m.selected)
	}
	m.overlay = createOverlay
	m.fields = [8]string{"fixture", "bridge", "local", "test", "yes", "com.docker.network.bridge.name", "10.20.0.0/24", "10.20.0.1"}
	m, c := m.handleOverlay(key("enter"))
	m, _ = m.Update(c())
	if len(a.creates) != 1 || a.creates[0].IPAM[0].Subnet != "10.20.0.0/24" {
		t.Fatalf("create=%+v", a.creates)
	}
	if v := m.SetSize(120, 25).View(); strings.Contains(v, "\x1b[31m") {
		t.Fatal("escape was not sanitized")
	}
	if m.SetSize(90, 25).View() == "" {
		t.Fatal("narrow empty")
	}
}
