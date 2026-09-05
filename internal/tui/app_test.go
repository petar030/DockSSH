package tui

import (
	"context"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendcompose "github.com/petar030/ssh-native-docker-tui/internal/backend/compose"
	backendcontainers "github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	backendimages "github.com/petar030/ssh-native-docker-tui/internal/backend/images"
	backendnetworks "github.com/petar030/ssh-native-docker-tui/internal/backend/networks"
	backendsystem "github.com/petar030/ssh-native-docker-tui/internal/backend/system"
	backendvolumes "github.com/petar030/ssh-native-docker-tui/internal/backend/volumes"
)

type appBackend struct {
	mu            sync.Mutex
	subscriptions []*appSubscription
}

func (fake *appBackend) RequestRefresh(backend.Page) error  { return nil }
func (fake *appBackend) Containers() *backendcontainers.API { return nil }
func (fake *appBackend) Compose() *backendcompose.API       { return nil }
func (fake *appBackend) Images() *backendimages.API         { return nil }
func (fake *appBackend) Volumes() *backendvolumes.API       { return nil }
func (fake *appBackend) Networks() *backendnetworks.API     { return nil }
func (fake *appBackend) System() *backendsystem.API         { return nil }

func (fake *appBackend) Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	subscription := &appSubscription{events: make(chan backend.EventEnvelope, 1)}
	fake.subscriptions = append(fake.subscriptions, subscription)
	return subscription, nil
}

type appSubscription struct {
	events chan backend.EventEnvelope
	once   sync.Once
}

func (subscription *appSubscription) Events() <-chan backend.EventEnvelope {
	return subscription.events
}
func (subscription *appSubscription) Close() error {
	subscription.once.Do(func() { close(subscription.events) })
	return nil
}

func TestRootRendersPersistentFrameAndContainersPage(t *testing.T) {
	app := New(context.Background(), &appBackend{})
	_ = app.Init()
	_, _ = app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	dashboardView := app.View().Content
	if width, height := lipgloss.Width(dashboardView), lipgloss.Height(dashboardView); width > 120 || height > 30 {
		t.Fatalf("root frame is %dx%d, want at most 120x30", width, height)
	}
	for _, value := range []string{"DOCKER TUI", "Dashboard", "Containers", "Status"} {
		if value == "Status" {
			value = "Dashboard"
		}
		if !strings.Contains(dashboardView, value) {
			t.Fatalf("root view missing %q:\n%s", value, dashboardView)
		}
	}

	_, _ = app.Update(key("2"))
	if app.activeTab != 1 || app.dashboard.Active() {
		t.Fatalf("tab switch did not deactivate Dashboard: tab=%d active=%v", app.activeTab, app.dashboard.Active())
	}
	containerView := app.View().Content
	if !strings.Contains(containerView, "Loading containers") ||
		!strings.Contains(containerView, "DOCKER TUI") ||
		!strings.Contains(containerView, "1-8 open page") {
		t.Fatalf("Containers page did not retain shared frame:\n%s", containerView)
	}
}

func TestRootDirectAndAdjacentNavigationWraps(t *testing.T) {
	app := New(context.Background(), &appBackend{})
	_ = app.Init()

	_, _ = app.Update(key("["))
	if app.activeTab != len(tabs)-1 {
		t.Fatalf("previous tab from first = %d, want %d", app.activeTab, len(tabs)-1)
	}
	_, _ = app.Update(key("]"))
	if app.activeTab != 0 || !app.dashboard.Active() {
		t.Fatalf("next tab did not return to active Dashboard: tab=%d active=%v", app.activeTab, app.dashboard.Active())
	}
	_, _ = app.Update(key("6"))
	if app.activeTab != 5 {
		t.Fatalf("direct tab = %d, want 5", app.activeTab)
	}
}

func TestRootActivatesImplementedResourceTabs(t *testing.T) {
	app := New(context.Background(), &appBackend{})
	_ = app.Init()
	checks := []struct {
		key    string
		active func() bool
	}{{"3", func() bool { return app.compose.Active() }}, {"4", func() bool { return app.images.Active() }}, {"5", func() bool { return app.volumes.Active() }}, {"6", func() bool { return app.networks.Active() }}, {"7", func() bool { return app.events.Active() }}, {"8", func() bool { return app.system.Active() }}}
	for _, check := range checks {
		_, _ = app.Update(key(check.key))
		if !check.active() {
			t.Fatalf("tab %s did not activate its page model", check.key)
		}
	}
}

func TestRootSmallTerminalAndHelp(t *testing.T) {
	app := New(context.Background(), &appBackend{})
	_ = app.Init()
	_, _ = app.Update(tea.WindowSizeMsg{Width: 60, Height: 15})
	if view := app.View().Content; !strings.Contains(view, "Terminal too small") || !strings.Contains(view, "60x15") {
		t.Fatalf("small-terminal view:\n%s", view)
	}
	_, _ = app.Update(key("?"))
	if view := app.View().Content; !strings.Contains(view, "KEYBOARD HELP") || !strings.Contains(view, "q / ctrl+c quit") {
		t.Fatalf("small-terminal help view:\n%s", view)
	}
	_, _ = app.Update(key("?"))

	_, _ = app.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	_, _ = app.Update(key("?"))
	if view := app.View().Content; !strings.Contains(view, "KEYBOARD HELP") || !strings.Contains(view, "1–8") {
		t.Fatalf("help view:\n%s", view)
	}
}

func TestRootSessionModelsDoNotShareNavigationState(t *testing.T) {
	application := &appBackend{}
	first := New(context.Background(), application)
	second := New(context.Background(), application)
	_ = first.Init()
	_ = second.Init()

	_, _ = first.Update(key("4"))
	if first.activeTab != 3 || second.activeTab != 0 {
		t.Fatalf("session tabs leaked: first=%d second=%d", first.activeTab, second.activeTab)
	}
}

func TestRootQuitDeactivatesDashboard(t *testing.T) {
	app := New(context.Background(), &appBackend{})
	_ = app.Init()
	_, command := app.Update(key("q"))
	if app.dashboard.Active() {
		t.Fatal("Dashboard remained active after quit")
	}
	if command == nil {
		t.Fatal("quit returned no command")
	}
	if _, ok := command().(tea.QuitMsg); !ok {
		t.Fatalf("quit command returned %T", command())
	}
}

func TestLateDashboardSubscriptionIsClosedAfterTabSwitch(t *testing.T) {
	application := &appBackend{}
	app := New(context.Background(), application)
	command := app.Init()
	_, _ = app.Update(key("2"))
	ready := command()
	_, _ = app.Update(ready)
	if len(application.subscriptions) == 0 {
		t.Fatal("Dashboard subscription was not created")
	}
	select {
	case <-application.subscriptions[0].events:
	default:
		t.Fatal("late Dashboard subscription was not closed")
	}
}

func key(value string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Text: value, Code: []rune(value)[0]})
}
