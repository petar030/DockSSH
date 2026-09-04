// Package tui implements the shared Bubble Tea frame and delegates page state
// to page-local models.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/dashboard"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

const frameRows = 4

// App is one SSH session's root model. It owns the persistent frame; page
// packages own everything rendered inside the content area.
type App struct {
	width  int
	height int

	activeTab int
	showHelp  bool
	dashboard dashboard.Model
}

func New(sessionCtx context.Context, application dashboard.Backend) *App {
	return &App{dashboard: dashboard.New(sessionCtx, application)}
}

func (app *App) Init() tea.Cmd {
	var command tea.Cmd
	app.dashboard, command = app.dashboard.Activate()
	return command
}

func (app *App) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		app.width = max(message.Width, 0)
		app.height = max(message.Height, 0)
		app.resizeActivePage()
		return app, nil

	case tea.KeyPressMsg:
		key := message.String()
		switch key {
		case "ctrl+c", "q":
			app.dashboard = app.dashboard.Deactivate()
			return app, tea.Quit
		case "?":
			app.showHelp = !app.showHelp
			return app, nil
		case "[", "]":
			return app.switchTab(adjacentTab(app.activeTab, key))
		case "r":
			if app.activeTab == 0 {
				var command tea.Cmd
				app.dashboard, command = app.dashboard.Refresh()
				return app, command
			}
		}
		if index, ok := tabFromKey(key); ok {
			return app.switchTab(index)
		}
	}

	if app.activeTab == 0 {
		updated, command := app.dashboard.Update(message)
		app.dashboard = updated
		return app, command
	}
	return app, nil
}

func (app *App) View() tea.View {
	content := app.render()
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "SSH Docker TUI"
	return view
}

func (app *App) switchTab(index int) (tea.Model, tea.Cmd) {
	if index < 0 || index >= len(tabs) || index == app.activeTab {
		return app, nil
	}
	if app.activeTab == 0 {
		app.dashboard = app.dashboard.Deactivate()
	}
	app.activeTab = index
	app.showHelp = false
	if app.activeTab == 0 {
		var command tea.Cmd
		app.dashboard, command = app.dashboard.Activate()
		app.resizeActivePage()
		return app, command
	}
	return app, nil
}

func (app *App) resizeActivePage() {
	if app.activeTab == 0 {
		app.dashboard = app.dashboard.SetSize(app.width, max(app.height-frameRows, 0))
	}
}

func (app *App) render() string {
	if app.width < ui.MinimumWidth || app.height < ui.MinimumHeight {
		if app.showHelp {
			return renderSmallHelp(app.width, app.height)
		}
		return renderTooSmall(app.width, app.height)
	}

	connection := "Docker: loading"
	if app.dashboard.HasData() {
		connection = "Docker: unavailable"
		if app.dashboard.EngineAvailable() {
			connection = "Docker: connected"
		}
	}

	body := app.pageContent()
	status := app.pageStatus()
	help := "[ ] tabs   1-8 direct   r refresh   ? help   q quit"
	if app.showHelp {
		body = app.helpView()
	}

	contentHeight := app.height - frameRows
	body = lipgloss.NewStyle().Width(app.width).Height(contentHeight).Render(body)
	return strings.Join([]string{
		renderTopBar(app.width, connection),
		renderTabs(app.width, app.activeTab),
		body,
		renderFooter(app.width, status, help),
	}, "\n")
}

func (app *App) pageContent() string {
	if app.activeTab == 0 {
		return app.dashboard.View()
	}
	item := tabs[app.activeTab]
	return lipgloss.NewStyle().Padding(2, 3).Render(fmt.Sprintf(
		"%s\n\nThis page is planned for a later TUI slice.\nThe shared layout and navigation are already active.",
		item.label,
	))
}

func (app *App) pageStatus() string {
	if app.activeTab == 0 {
		return app.dashboard.Status()
	}
	return tabs[app.activeTab].label + " is not implemented yet"
}

func (app *App) helpView() string {
	return lipgloss.NewStyle().Padding(1, 3).Render(strings.Join([]string{
		"KEYBOARD HELP",
		"",
		"[ / ]        previous / next tab",
		"1–8          open a tab directly",
		"r            refresh the active implemented page",
		"?            close this help",
		"q / ctrl+c   end this SSH TUI session",
		"",
		"Page-specific keys will appear as each page is implemented.",
	}, "\n"))
}

var _ tea.Model = (*App)(nil)
