// Package dashboard implements the session-local Bubble Tea model for the
// read-only Dashboard page.
package dashboard

import (
	"context"
	"errors"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backenddashboard "github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

// Backend is the narrow part of the shared backend used by Dashboard.
type Backend interface {
	RequestRefresh(backend.Page) error
	Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error)
}

type Model struct {
	backend    Backend
	sessionCtx context.Context
	pageCtx    context.Context
	cancelPage context.CancelFunc

	subscription backend.Subscription
	generation   uint64
	active       bool

	width  int
	height int

	spinner     spinner.Model
	summary     backenddashboard.SummaryUpdated
	hasData     bool
	loading     bool
	stale       bool
	err         error
	lastUpdated time.Time
	lastReason  backend.RefreshReason
}

func New(sessionCtx context.Context, application Backend) Model {
	if sessionCtx == nil {
		sessionCtx = context.Background()
	}
	return Model{
		backend: application, sessionCtx: sessionCtx,
		spinner: ui.NewSpinner(),
	}
}

// Activate creates one page-lifetime subscription. It must be paired with
// Deactivate when the user leaves the tab.
func (model Model) Activate() (Model, tea.Cmd) {
	model = model.Deactivate()
	model.generation++
	model.pageCtx, model.cancelPage = context.WithCancel(model.sessionCtx)
	model.active = true
	model.loading = true
	model.stale = model.hasData
	model.err = nil
	return model, subscribe(model.pageCtx, model.backend, model.generation)
}

func (model Model) Deactivate() Model {
	if model.cancelPage != nil {
		model.cancelPage()
	}
	if model.subscription != nil {
		_ = model.subscription.Close()
	}
	model.cancelPage = nil
	model.pageCtx = nil
	model.subscription = nil
	model.active = false
	model.loading = false
	return model
}

func (model Model) Refresh() (Model, tea.Cmd) {
	if !model.active || model.backend == nil {
		return model, nil
	}
	model.loading = true
	model.err = nil
	return model, tea.Batch(
		requestRefresh(model.backend, model.generation),
		model.spinner.Tick,
	)
}

func (model Model) SetSize(width, height int) Model {
	model.width = max(width, 0)
	model.height = max(height, 0)
	return model
}

func (model Model) Active() bool { return model.active }

func (model Model) HasData() bool { return model.hasData }

func (model Model) EngineAvailable() bool {
	return model.hasData && model.summary.Engine.Available
}

// Activity returns the compact shared-header indicator while this page is
// waiting for an authoritative refresh.
func (model Model) Activity() string {
	if model.loading {
		return model.spinner.View()
	}
	return ""
}

func (model Model) Status() string {
	switch {
	case model.loading && !model.hasData:
		return "Loading Dashboard"
	case model.loading:
		return "Refreshing Dashboard"
	case model.err != nil && model.stale:
		return "Dashboard data may be stale"
	case model.err != nil:
		return "Dashboard unavailable"
	case !model.lastUpdated.IsZero():
		return "Dashboard updated " + model.lastUpdated.Format(time.TimeOnly)
	default:
		return "Dashboard waiting for data"
	}
}

func (model Model) updateSpinner(message tea.Msg) (Model, tea.Cmd) {
	if !model.loading {
		return model, nil
	}
	var command tea.Cmd
	model.spinner, command = model.spinner.Update(message)
	return model, command
}

func closedSubscriptionError() error {
	return &backend.AppError{Code: backend.ErrorStreamClosed, Operation: "receive Dashboard events"}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	var applicationError *backend.AppError
	if errors.As(err, &applicationError) {
		return applicationError.Error()
	}
	return err.Error()
}
