package dashboard

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backenddashboard "github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

type fakeBackend struct {
	mu              sync.Mutex
	calls           []string
	subscription    *fakeSubscription
	subscribeErr    error
	refreshErr      error
	subscriptionCtx context.Context
}

func (fake *fakeBackend) RequestRefresh(page backend.Page) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = append(fake.calls, "refresh:"+string(page))
	return fake.refreshErr
}

func (fake *fakeBackend) Subscribe(ctx context.Context, page backend.Page, filter backend.EventFilter) (backend.Subscription, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = append(fake.calls, "subscribe:"+string(page))
	fake.subscriptionCtx = ctx
	if fake.subscribeErr != nil {
		return nil, fake.subscribeErr
	}
	if fake.subscription == nil {
		fake.subscription = newFakeSubscription()
	}
	return fake.subscription, nil
}

func (fake *fakeBackend) recordedCalls() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]string(nil), fake.calls...)
}

type fakeSubscription struct {
	events    chan backend.EventEnvelope
	closeOnce sync.Once
	closed    chan struct{}
}

func newFakeSubscription() *fakeSubscription {
	return &fakeSubscription{events: make(chan backend.EventEnvelope, 8), closed: make(chan struct{})}
}

func (subscription *fakeSubscription) Events() <-chan backend.EventEnvelope {
	return subscription.events
}

func (subscription *fakeSubscription) Close() error {
	subscription.closeOnce.Do(func() {
		close(subscription.closed)
		close(subscription.events)
	})
	return nil
}

func TestDashboardSubscribesBeforeRefreshAndAppliesSummary(t *testing.T) {
	application := &fakeBackend{}
	model := New(context.Background(), application).SetSize(100, 20)
	model, subscribeCommand := model.Activate()

	ready := subscribeCommand().(subscriptionReadyMsg)
	if calls := application.recordedCalls(); len(calls) != 1 || calls[0] != "subscribe:dashboard" {
		t.Fatalf("calls after activation = %v", calls)
	}
	model, command := model.Update(ready)
	batch, ok := command().(tea.BatchMsg)
	if !ok || len(batch) != 3 {
		t.Fatalf("subscription command = %#v", command)
	}
	requested := batch[1]().(refreshRequestedMsg)
	if calls := application.recordedCalls(); len(calls) != 2 || calls[1] != "refresh:dashboard" {
		t.Fatalf("calls after subscription = %v", calls)
	}

	model, _ = model.Update(requested)
	want := backenddashboard.SummaryUpdated{
		Engine:    backenddashboard.EngineSummary{Available: true, Name: "engine", ServerVersion: "29.1.3"},
		Resources: backenddashboard.ResourceCounts{Containers: 3, ContainersRunning: 2, Images: 7},
	}
	now := time.Date(2026, 9, 3, 18, 30, 0, 0, time.UTC)
	application.subscription.events <- backend.EventEnvelope{
		Time: now, Reason: backend.RefreshManual, Payload: want,
	}
	received := batch[0]().(eventReceivedMsg)
	model, _ = model.Update(received)

	if !model.hasData || model.loading || model.stale || model.err != nil {
		t.Fatalf("unexpected state: hasData=%v loading=%v stale=%v err=%v", model.hasData, model.loading, model.stale, model.err)
	}
	if model.summary.Resources.Containers != 3 || model.lastUpdated != now {
		t.Fatalf("summary was not applied: %#v", model.summary)
	}
	view := model.View()
	for _, text := range []string{"ENGINE", "Docker 29.1.3", "RESOURCES", "Containers  3", "DISK USAGE", "RECENT DOCKER EVENTS"} {
		if !strings.Contains(view, text) {
			t.Fatalf("Dashboard view missing %q:\n%s", text, view)
		}
	}
	if width := lipgloss.Width(view); width > 100 {
		t.Fatalf("Dashboard width = %d, want <= 100", width)
	}
	if height := lipgloss.Height(view); height > 20 {
		t.Fatalf("Dashboard height = %d, want <= 20", height)
	}
}

func TestDashboardFailurePreservesLastSuccessfulData(t *testing.T) {
	model := New(context.Background(), &fakeBackend{}).SetSize(100, 20)
	model.active = true
	model.generation = 4
	model.subscription = newFakeSubscription()
	model.hasData = true
	model.summary.Engine.Name = "existing-engine"
	model.loading = true

	wantErr := errors.New("daemon unavailable")
	model, _ = model.Update(eventReceivedMsg{
		generation: 4,
		open:       true,
		event:      backend.EventEnvelope{Payload: backend.RefreshFailed{Err: wantErr}},
	})
	if !model.hasData || !model.stale || model.loading || !errors.Is(model.err, wantErr) {
		t.Fatalf("failure did not preserve stale data: %#v", model)
	}
	if !strings.Contains(model.View(), "existing-engine") || !strings.Contains(model.View(), "stale") {
		t.Fatalf("stale view did not retain existing data:\n%s", model.View())
	}
}

func TestDashboardOverflowMarksStaleAndRequestsRecovery(t *testing.T) {
	application := &fakeBackend{}
	model := New(context.Background(), application)
	model.active = true
	model.generation = 2
	model.subscription = newFakeSubscription()
	model.hasData = true

	model, command := model.Update(eventReceivedMsg{
		generation: 2, open: true,
		event: backend.EventEnvelope{Payload: backend.SubscriberOverflow{DroppedSequence: 9}},
	})
	if !model.loading || !model.stale {
		t.Fatalf("overflow state: loading=%v stale=%v", model.loading, model.stale)
	}
	batch, ok := command().(tea.BatchMsg)
	if !ok || len(batch) != 3 {
		t.Fatalf("overflow command = %#v, want three-command batch", command)
	}
	if _, ok := batch[1]().(refreshRequestedMsg); !ok {
		t.Fatal("overflow did not return a recovery refresh message")
	}
	if calls := application.recordedCalls(); len(calls) != 1 || calls[0] != "refresh:dashboard" {
		t.Fatalf("overflow refresh calls = %v", calls)
	}
}

func TestDashboardDeactivationClosesSubscriptionAndRejectsLateMessage(t *testing.T) {
	application := &fakeBackend{}
	model := New(context.Background(), application)
	model, command := model.Activate()
	ready := command().(subscriptionReadyMsg)
	model, _ = model.Update(ready)
	generation := model.generation
	subscriptionCtx := application.subscriptionCtx

	model = model.Deactivate()
	select {
	case <-application.subscription.closed:
	default:
		t.Fatal("subscription was not closed")
	}
	select {
	case <-subscriptionCtx.Done():
	default:
		t.Fatal("page context was not canceled")
	}

	late := backenddashboard.SummaryUpdated{Engine: backenddashboard.EngineSummary{Name: "late"}}
	model, _ = model.Update(eventReceivedMsg{
		generation: generation, open: true,
		event: backend.EventEnvelope{Payload: late},
	})
	if model.hasData || model.summary.Engine.Name == "late" {
		t.Fatal("late event changed inactive Dashboard")
	}
}

func TestDashboardSanitizesDockerText(t *testing.T) {
	model := New(context.Background(), &fakeBackend{}).SetSize(100, 20)
	model.hasData = true
	model.summary.Engine = backenddashboard.EngineSummary{
		Available: true, Name: "host\x1b[2Jbad", ServerVersion: "29",
	}
	view := model.View()
	if strings.Contains(view, "\x1b[2J") {
		t.Fatalf("Docker escape sequence reached terminal output: %q", view)
	}
}

func TestDashboardFitsMinimumContentArea(t *testing.T) {
	model := New(context.Background(), &fakeBackend{}).SetSize(80, 20)
	model.hasData = true
	model.summary = backenddashboard.SummaryUpdated{
		Engine: backenddashboard.EngineSummary{Available: true, Name: "long-engine-name", ServerVersion: "29.1.3", MemoryBytes: 14_000_000_000},
		DiskUsage: backenddashboard.DiskUsage{
			Containers: backenddashboard.ResourceDiskUsage{TotalBytes: 1_024, ReclaimableBytes: 1_024},
			Images:     backenddashboard.ResourceDiskUsage{TotalBytes: 9_000_000_000, ReclaimableBytes: 999_999_999},
			Volumes:    backenddashboard.ResourceDiskUsage{TotalBytes: 999_999_999, ReclaimableBytes: 999_999_999},
			BuildCache: backenddashboard.ResourceDiskUsage{TotalBytes: 9_000_000_000, ReclaimableBytes: 1_100_000_000},
		},
	}
	for index := range 5 {
		model.summary.RecentEvents = append(model.summary.RecentEvents, backenddashboard.RecentEvent{
			Time: time.Date(2026, 9, 3, 18, index, 0, 0, time.UTC), Resource: "container",
			Action: "start", ResourceID: strings.Repeat("a", 64),
		})
	}

	view := model.View()
	if width := lipgloss.Width(view); width > 80 {
		t.Fatalf("minimum Dashboard width = %d, want <= 80\n%s", width, view)
	}
	if height := lipgloss.Height(view); height > 20 {
		t.Fatalf("minimum Dashboard height = %d, want <= 20\n%s", height, view)
	}
}
