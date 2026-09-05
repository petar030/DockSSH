package events

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendevents "github.com/petar030/ssh-native-docker-tui/internal/backend/events"
)

type fakeBackend struct {
	steps    []string
	filters  []backend.EventFilter
	requests int
	subs     []*fakeSub
}

func (f *fakeBackend) RequestRefresh(p backend.Page) error {
	f.steps = append(f.steps, "refresh:"+string(p))
	f.requests++
	return nil
}
func (f *fakeBackend) Subscribe(_ context.Context, _ backend.Page, filter backend.EventFilter) (backend.Subscription, error) {
	f.steps = append(f.steps, "subscribe")
	s := &fakeSub{events: make(chan backend.EventEnvelope, 4)}
	f.filters = append(f.filters, filter)
	f.subs = append(f.subs, s)
	return s, nil
}

type fakeSub struct {
	events chan backend.EventEnvelope
	closed bool
}

func (s *fakeSub) Events() <-chan backend.EventEnvelope { return s.events }
func (s *fakeSub) Close() error                         { s.closed = true; return nil }
func ready(t *testing.T, b *fakeBackend) Model {
	t.Helper()
	start := len(b.steps)
	m, c := New(context.Background(), b).Activate()
	m, c = m.Update(c())
	batch := c().(tea.BatchMsg)
	_ = batch[1]()
	if strings.Join(b.steps[start:], ",") != "subscribe,refresh:events" {
		t.Fatalf("order=%v", b.steps)
	}
	return m
}
func key(v string) tea.KeyPressMsg { return tea.KeyPressMsg(tea.Key{Text: v, Code: []rune(v)[0]}) }

func TestRecentLiveOrderingDedupBoundAndSanitize(t *testing.T) {
	b := &fakeBackend{}
	m := ready(t, b)
	now := time.Now()
	m = m.apply(backend.EventEnvelope{Time: now, Payload: backendevents.RecentUpdated{Events: []backendevents.Record{{Sequence: 1, OccurredAt: now.Add(-time.Second), Resource: "container", ResourceID: "old", Action: "start"}}}})
	m = m.apply(backend.EventEnvelope{Sequence: 2, Time: now, Payload: backend.DockerEventObserved{OccurredAt: now, Resource: "image", ResourceID: "new", Action: "tag", Attributes: map[string]string{"evil\nkey": "x\x1b[31m"}}})
	m = m.apply(backend.EventEnvelope{Sequence: 3, Time: now, Payload: backend.DockerEventObserved{OccurredAt: now, Resource: "image", ResourceID: "new", Action: "tag"}})
	if len(m.rows) != 2 || m.rows[0].id != "new" {
		t.Fatalf("rows=%+v", m.rows)
	}
	for i := 0; i < displayLimit+20; i++ {
		at := now.Add(time.Duration(i+1) * time.Second)
		m.merge([]row{{sequence: uint64(i + 10), occurred: at, id: string(rune(i + 100)), action: "x"}})
	}
	if len(m.rows) != displayLimit {
		t.Fatalf("cap=%d", len(m.rows))
	}
	view := m.SetSize(120, 25).View()
	if strings.Contains(view, "\x1b[31m") || strings.Contains(view, "evil\nkey") {
		t.Fatal("attributes were not sanitized")
	}
}

func TestFilterReplacementClosesOldThenRefreshes(t *testing.T) {
	b := &fakeBackend{}
	m := ready(t, b)
	old := b.subs[0]
	m.filtering = true
	m.edits = [4]string{"container", "abc", "start", "demo"}
	m, c := m.handleKey(key("enter"))
	batch := c().(tea.BatchMsg)
	readyMsg := batch[0]()
	if old.closed {
		t.Fatal("old subscription closed before replacement opened")
	}
	m, c = m.Update(readyMsg)
	if !old.closed || len(b.filters) != 2 {
		t.Fatal("replacement did not close old")
	}
	filter := b.filters[1]
	if filter.DockerResources[0] != "container" || filter.DockerProjects[0] != "demo" {
		t.Fatalf("filter=%+v", filter)
	}
	batch = c().(tea.BatchMsg)
	_ = batch[1]()
	if b.requests != 2 {
		t.Fatalf("refreshes=%d", b.requests)
	}
}

func TestOverflowClearAndIndependentModels(t *testing.T) {
	b := &fakeBackend{}
	first := ready(t, b)
	second := ready(t, b)
	event := backend.EventEnvelope{Time: time.Now(), Payload: backend.DockerEventObserved{OccurredAt: time.Now(), Resource: "container", Action: "die"}}
	first = first.apply(event)
	second = second.apply(event)
	first, _ = first.handleKey(key("c"))
	if len(first.rows) != 0 || len(second.rows) != 1 {
		t.Fatal("session-local clear leaked")
	}
	before := b.requests
	first, c := first.Update(eventReceivedMsg{generation: first.generation, subscriptionGen: first.subscriptionGen, event: backend.EventEnvelope{Payload: backend.SubscriberOverflow{}}, open: true})
	if !first.stale || c == nil {
		t.Fatal("overflow did not recover")
	}
	batch := c().(tea.BatchMsg)
	_ = batch[1]()
	if b.requests != before+1 {
		t.Fatal("overflow did not request recent")
	}
	generation, sg := first.generation, first.subscriptionGen
	first = first.Deactivate()
	first, _ = first.Update(eventReceivedMsg{generation: generation, subscriptionGen: sg, open: false})
	if first.err != nil {
		t.Fatalf("normal close became error: %v", first.err)
	}
}
