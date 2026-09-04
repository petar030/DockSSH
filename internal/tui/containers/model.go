// Package containers implements the session-local Bubble Tea model for the
// Containers page.
package containers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendcontainers "github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
)

const (
	maxLogLines  = 2000
	statsCadence = 350 * time.Millisecond
)

// Backend is the narrow common backend used by this page.
type Backend interface {
	RequestRefresh(backend.Page) error
	Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error)
}

// API is the TUI-facing Containers API. Docker SDK types do not cross it.
type API interface {
	RequestDetails(string) error
	RequestProcesses(string) error
	Start(context.Context, string) (backend.CommandResult, error)
	Stop(context.Context, string, backendcontainers.StopOptions) (backend.CommandResult, error)
	Restart(context.Context, string, backendcontainers.RestartOptions) (backend.CommandResult, error)
	Pause(context.Context, string) (backend.CommandResult, error)
	Unpause(context.Context, string) (backend.CommandResult, error)
	Kill(context.Context, string, backendcontainers.KillOptions) (backend.CommandResult, error)
	Rename(context.Context, string, backendcontainers.RenameOptions) (backend.CommandResult, error)
	Remove(context.Context, string, backendcontainers.RemoveOptions) (backend.CommandResult, error)
	Logs(context.Context, string, backendcontainers.LogsOptions) (backend.Stream[backendcontainers.LogEntry], error)
	Stats(context.Context, string, backendcontainers.StatsOptions) (backend.Stream[backendcontainers.StatsSample], error)
}

type viewMode uint8

const (
	detailsView viewMode = iota
	processesView
	logsView
	statsView
)

type overlayMode uint8

const (
	noOverlay overlayMode = iota
	filterOverlay
	renameOverlay
	confirmOverlay
	progressOverlay
	logsOverlay
)

type sortMode uint8

const (
	sortName sortMode = iota
	sortState
	sortCreated
)

type panelState struct {
	loading     bool
	stale       bool
	unavailable bool
	err         error
}

type Model struct {
	backend    Backend
	api        API
	sessionCtx context.Context
	pageCtx    context.Context
	cancelPage context.CancelFunc

	subscription backend.Subscription
	generation   uint64
	selectionGen uint64
	active       bool
	width        int
	height       int
	spinner      spinner.Model

	containers []backendcontainers.Summary
	hasData    bool
	loading    bool
	stale      bool
	err        error
	updatedAt  time.Time

	selectedID string
	filter     string
	filterEdit string
	sort       sortMode
	mode       viewMode
	scroll     int
	listOffset int
	overlay    overlayMode
	renameEdit string
	confirm    commandKind
	force      bool
	volumes    bool

	details      backendcontainers.Details
	detailsID    string
	detailsState panelState
	processes    backendcontainers.ProcessesUpdated
	processState panelState

	pendingOperation string
	notice           string

	streamGen    uint64
	streamCancel context.CancelFunc
	logStream    backend.Stream[backendcontainers.LogEntry]
	statsStream  backend.Stream[backendcontainers.StatsSample]
	logLines     []string
	logFragments map[backendcontainers.LogSource]string
	logErr       error
	logFollowing bool
	stats        backendcontainers.StatsSample
	pendingStats backendcontainers.StatsSample
	hasStats     bool
	statsDirty   bool
	statsErr     error
}

func New(sessionCtx context.Context, common Backend, api API) Model {
	if sessionCtx == nil {
		sessionCtx = context.Background()
	}
	return Model{backend: common, api: api, sessionCtx: sessionCtx, spinner: spinner.New(spinner.WithSpinner(spinner.Dot))}
}

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
	model = model.closeStream()
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
	model.overlay = noOverlay
	model.pendingOperation = ""
	return model
}

func (model Model) Refresh() (Model, tea.Cmd) {
	if !model.active || model.backend == nil {
		return model, nil
	}
	model.loading = true
	model.err = nil
	return model, tea.Batch(
		requestBase(model.backend, model.generation),
		model.spinner.Tick,
	)
}

func (model Model) SetSize(width, height int) Model {
	model.width = max(width, 0)
	model.height = max(height, 0)
	model.ensureListSelectionVisible()
	return model
}

func (model Model) Active() bool { return model.active }

func (model Model) CapturesInput() bool { return model.overlay != noOverlay }

// Activity returns the compact shared-header indicator while the Containers
// base data is being refreshed.
func (model Model) Activity() string {
	if model.loading || model.pendingOperation != "" {
		return model.spinner.View()
	}
	return ""
}

func (model Model) Help() string {
	if model.overlay == logsOverlay {
		return "j/k Scroll │ PgUp/PgDn Page │ g Top │ G Bottom/follow │ esc Close logs"
	}
	if model.overlay == confirmOverlay {
		return "y Confirm │ n Cancel"
	}
	if model.overlay == progressOverlay {
		return "Waiting for Docker…   ctrl+c quit"
	}
	if model.overlay != noOverlay {
		return "enter confirm/apply   esc cancel   ctrl+c quit"
	}
	return "↑↓ Move │ f Find │ l Logs │ t Stats │ P Proc │ s Start │ x Stop │ R Restart │ p Pause/Unpause │ K Kill │ n Rename │ d Remove"
}

func (model Model) Status() string {
	switch {
	case model.pendingOperation != "":
		return fmt.Sprintf("%s %s…", commandLabel(commandKind(model.pendingOperation)), model.selectedName())
	case model.notice != "":
		return model.notice
	case model.loading && !model.hasData:
		return "Loading containers"
	case model.loading:
		return "Refreshing containers"
	case model.err != nil:
		return "Container data may be stale"
	case !model.updatedAt.IsZero():
		return "Containers updated " + model.updatedAt.Format(time.TimeOnly)
	default:
		return "Containers waiting for data"
	}
}

func (model Model) visible() []backendcontainers.Summary {
	values := append([]backendcontainers.Summary(nil), model.containers...)
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(model.filter)))
	values = slicesDeleteFunc(values, func(value backendcontainers.Summary) bool {
		return !matches(value, terms)
	})
	sort.SliceStable(values, func(i, j int) bool {
		switch model.sort {
		case sortState:
			if values[i].State != values[j].State {
				return values[i].State < values[j].State
			}
		case sortCreated:
			if !values[i].Created.Equal(values[j].Created) {
				return values[i].Created.After(values[j].Created)
			}
		}
		return displayName(values[i]) < displayName(values[j])
	})
	return values
}

func matches(value backendcontainers.Summary, terms []string) bool {
	plain := strings.ToLower(strings.Join(append(append([]string{}, value.Names...), value.ID, value.Image, value.State, value.Status, value.Health), " "))
	for _, term := range terms {
		switch {
		case strings.HasPrefix(term, "state:"):
			if !strings.EqualFold(value.State, strings.TrimPrefix(term, "state:")) {
				return false
			}
		case strings.HasPrefix(term, "label:"):
			wanted := strings.TrimPrefix(term, "label:")
			parts := strings.SplitN(wanted, "=", 2)
			actual, ok := value.Labels[parts[0]]
			if !ok || len(parts) == 2 && !strings.EqualFold(actual, parts[1]) {
				return false
			}
		case !strings.Contains(plain, term):
			return false
		}
	}
	return true
}

func displayName(value backendcontainers.Summary) string {
	if len(value.Names) > 0 && strings.TrimSpace(value.Names[0]) != "" {
		return value.Names[0]
	}
	return shortID(value.ID)
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func slicesDeleteFunc[T any](values []T, remove func(T) bool) []T {
	kept := values[:0]
	for _, value := range values {
		if !remove(value) {
			kept = append(kept, value)
		}
	}
	return kept
}
