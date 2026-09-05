// Package tui implements the shared Bubble Tea frame and delegates page state
// to page-local models.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	backendimages "github.com/petar030/ssh-native-docker-tui/internal/backend/images"
	backendnetworks "github.com/petar030/ssh-native-docker-tui/internal/backend/networks"
	backendvolumes "github.com/petar030/ssh-native-docker-tui/internal/backend/volumes"
	containerstui "github.com/petar030/ssh-native-docker-tui/internal/tui/containers"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/dashboard"
	eventstui "github.com/petar030/ssh-native-docker-tui/internal/tui/events"
	imagestui "github.com/petar030/ssh-native-docker-tui/internal/tui/images"
	networkstui "github.com/petar030/ssh-native-docker-tui/internal/tui/networks"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
	volumestui "github.com/petar030/ssh-native-docker-tui/internal/tui/volumes"
)

const frameRows = 9

// App is one SSH session's root model. It owns the persistent frame; page
// packages own everything rendered inside the content area.
type App struct {
	width  int
	height int

	activeTab  int
	showHelp   bool
	dashboard  dashboard.Model
	containers containerstui.Model
	images     imagestui.Model
	volumes    volumestui.Model
	networks   networkstui.Model
	events     eventstui.Model
	jobs       *jobTracker
}

type Application interface {
	dashboard.Backend
	Containers() *containers.API
	Images() *backendimages.API
	Volumes() *backendvolumes.API
	Networks() *backendnetworks.API
}

func New(sessionCtx context.Context, application Application) *App {
	jobs := newJobTracker(sessionCtx)
	return &App{
		dashboard:  dashboard.New(sessionCtx, application),
		containers: containerstui.New(sessionCtx, application, application.Containers()),
		images:     imagestui.New(sessionCtx, application, application.Images(), jobs),
		volumes:    volumestui.New(sessionCtx, application, application.Volumes()),
		networks:   networkstui.New(sessionCtx, application, application.Networks()),
		events:     eventstui.New(sessionCtx, application),
		jobs:       jobs,
	}
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
		if message.String() == "ctrl+c" {
			app.deactivateAll()
			return app, tea.Quit
		}
		if app.jobs.CapturesInput() {
			return app, app.jobs.HandleKey(message.String())
		}
		if app.activeTab == 1 && app.containers.CapturesInput() {
			updated, command := app.containers.Update(message)
			app.containers = updated
			return app, command
		}
		if app.activeTab == 3 && app.images.CapturesInput() {
			updated, command := app.images.Update(message)
			app.images = updated
			return app, command
		}
		if app.activeTab == 4 && app.volumes.CapturesInput() {
			updated, command := app.volumes.Update(message)
			app.volumes = updated
			return app, command
		}
		if app.activeTab == 5 && app.networks.CapturesInput() {
			updated, command := app.networks.Update(message)
			app.networks = updated
			return app, command
		}
		if app.activeTab == 6 && app.events.CapturesInput() {
			updated, command := app.events.Update(message)
			app.events = updated
			return app, command
		}
		key := message.String()
		switch key {
		case "q":
			app.deactivateAll()
			return app, tea.Quit
		case "J":
			app.jobs.Open()
			return app, nil
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
			if app.activeTab == 1 {
				var command tea.Cmd
				app.containers, command = app.containers.Refresh()
				return app, command
			}
			if app.activeTab == 3 {
				var command tea.Cmd
				app.images, command = app.images.Refresh()
				return app, command
			}
			if app.activeTab == 4 {
				var command tea.Cmd
				app.volumes, command = app.volumes.Refresh()
				return app, command
			}
			if app.activeTab == 5 {
				var command tea.Cmd
				app.networks, command = app.networks.Refresh()
				return app, command
			}
			if app.activeTab == 6 {
				var command tea.Cmd
				app.events, command = app.events.Refresh()
				return app, command
			}
		}
		if index, ok := tabFromKey(key); ok {
			return app.switchTab(index)
		}
	}

	if _, isKey := message.(tea.KeyPressMsg); isKey {
		if app.activeTab == 0 {
			updated, command := app.dashboard.Update(message)
			app.dashboard = updated
			return app, command
		}
		if app.activeTab == 1 {
			updated, command := app.containers.Update(message)
			app.containers = updated
			return app, command
		}
		if app.activeTab == 3 {
			updated, command := app.images.Update(message)
			app.images = updated
			return app, command
		}
		if app.activeTab == 4 {
			updated, command := app.volumes.Update(message)
			app.volumes = updated
			return app, command
		}
		if app.activeTab == 5 {
			updated, command := app.networks.Update(message)
			app.networks = updated
			return app, command
		}
		if app.activeTab == 6 {
			updated, command := app.events.Update(message)
			app.events = updated
			return app, command
		}
		return app, nil
	}

	// Background messages are offered to every page model. Their concrete
	// page-local types ensure only the owner handles them. This also lets an
	// inactive page close a subscription or stream that finished opening after
	// the user had already changed tabs.
	updatedDashboard, dashboardCommand := app.dashboard.Update(message)
	updatedContainers, containersCommand := app.containers.Update(message)
	updatedImages, imagesCommand := app.images.Update(message)
	updatedVolumes, volumesCommand := app.volumes.Update(message)
	updatedNetworks, networksCommand := app.networks.Update(message)
	updatedEvents, eventsCommand := app.events.Update(message)
	jobsCommand := app.jobs.Update(message)
	app.dashboard, app.containers, app.images, app.volumes, app.networks, app.events = updatedDashboard, updatedContainers, updatedImages, updatedVolumes, updatedNetworks, updatedEvents
	return app, tea.Batch(dashboardCommand, containersCommand, imagesCommand, volumesCommand, networksCommand, eventsCommand, jobsCommand)
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
	} else if app.activeTab == 1 {
		app.containers = app.containers.Deactivate()
	} else if app.activeTab == 3 {
		app.images = app.images.Deactivate()
	} else if app.activeTab == 4 {
		app.volumes = app.volumes.Deactivate()
	} else if app.activeTab == 5 {
		app.networks = app.networks.Deactivate()
	} else if app.activeTab == 6 {
		app.events = app.events.Deactivate()
	}
	app.activeTab = index
	app.showHelp = false
	if app.activeTab == 0 {
		var command tea.Cmd
		app.dashboard, command = app.dashboard.Activate()
		app.resizeActivePage()
		return app, command
	}
	if app.activeTab == 1 {
		var command tea.Cmd
		app.containers, command = app.containers.Activate()
		app.resizeActivePage()
		return app, command
	}
	if app.activeTab == 3 {
		var command tea.Cmd
		app.images, command = app.images.Activate()
		app.resizeActivePage()
		return app, command
	}
	if app.activeTab == 4 {
		var command tea.Cmd
		app.volumes, command = app.volumes.Activate()
		app.resizeActivePage()
		return app, command
	}
	if app.activeTab == 5 {
		var command tea.Cmd
		app.networks, command = app.networks.Activate()
		app.resizeActivePage()
		return app, command
	}
	if app.activeTab == 6 {
		var command tea.Cmd
		app.events, command = app.events.Activate()
		app.resizeActivePage()
		return app, command
	}
	return app, nil
}

func (app *App) resizeActivePage() {
	if app.activeTab == 0 {
		app.dashboard = app.dashboard.SetSize(app.width, max(app.height-frameRows, 0))
	} else if app.activeTab == 1 {
		app.containers = app.containers.SetSize(app.width, max(app.height-frameRows, 0))
	} else if app.activeTab == 3 {
		app.images = app.images.SetSize(app.width, max(app.height-frameRows, 0))
	} else if app.activeTab == 4 {
		app.volumes = app.volumes.SetSize(app.width, max(app.height-frameRows, 0))
	} else if app.activeTab == 5 {
		app.networks = app.networks.SetSize(app.width, max(app.height-frameRows, 0))
	} else if app.activeTab == 6 {
		app.events = app.events.SetSize(app.width, max(app.height-frameRows, 0))
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
	pageHelp := ""
	if app.activeTab == 1 {
		pageHelp = app.containers.Help()
	} else if app.activeTab == 3 {
		pageHelp = app.images.Help()
	} else if app.activeTab == 4 {
		pageHelp = app.volumes.Help()
	} else if app.activeTab == 5 {
		pageHelp = app.networks.Help()
	} else if app.activeTab == 6 {
		pageHelp = app.events.Help()
	}
	globalHelp := "[ / ] switch tab   1-8 open page   r refresh   ? help   q quit"
	if app.showHelp {
		body = app.helpView()
	}

	contentHeight := app.height - frameRows
	body = lipgloss.NewStyle().Width(app.width).Height(contentHeight).Render(body)
	rendered := strings.Join([]string{
		renderHeader(app.width, app.activeTab, connection, app.pageActivity()),
		body,
		renderFooter(app.width, status, pageHelp, app.jobHelp(globalHelp)),
	}, "\n")
	if app.jobs.CapturesInput() {
		return ui.OverlayCentered(rendered, app.jobs.View(app.width), app.width, app.height)
	}
	return rendered
}

func (app *App) pageActivity() string {
	if app.activeTab == 0 {
		return app.dashboard.Activity()
	}
	if app.activeTab == 1 {
		return app.containers.Activity()
	}
	if app.activeTab == 3 {
		return app.images.Activity()
	}
	if app.activeTab == 4 {
		return app.volumes.Activity()
	}
	if app.activeTab == 5 {
		return app.networks.Activity()
	}
	if app.activeTab == 6 {
		return app.events.Activity()
	}
	return ""
}

func (app *App) pageContent() string {
	if app.activeTab == 0 {
		return app.dashboard.View()
	}
	if app.activeTab == 1 {
		return app.containers.View()
	}
	if app.activeTab == 3 {
		return app.images.View()
	}
	if app.activeTab == 4 {
		return app.volumes.View()
	}
	if app.activeTab == 5 {
		return app.networks.View()
	}
	if app.activeTab == 6 {
		return app.events.View()
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
	if app.activeTab == 1 {
		return app.containers.Status()
	}
	if app.activeTab == 3 {
		return app.images.Status()
	}
	if app.activeTab == 4 {
		return app.volumes.Status()
	}
	if app.activeTab == 5 {
		return app.networks.Status()
	}
	if app.activeTab == 6 {
		return app.events.Status()
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
		"J            show this session's jobs",
		"?            close this help",
		"q / ctrl+c   end this SSH TUI session",
		"",
		"Page-specific keys will appear as each page is implemented.",
	}, "\n"))
}

func (app *App) deactivateAll() {
	app.dashboard = app.dashboard.Deactivate()
	app.containers = app.containers.Deactivate()
	app.images = app.images.Deactivate()
	app.volumes = app.volumes.Deactivate()
	app.networks = app.networks.Deactivate()
	app.events = app.events.Deactivate()
}

func (app *App) jobHelp(global string) string {
	if summary := app.jobs.Summary(); summary != "" {
		return summary + "   " + global
	}
	return "J jobs   " + global
}

var _ tea.Model = (*App)(nil)
