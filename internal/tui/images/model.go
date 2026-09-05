// Package images implements the session-local Images page.
package images

import (
	"context"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendimages "github.com/petar030/ssh-native-docker-tui/internal/backend/images"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

type Backend interface {
	RequestRefresh(backend.Page) error
	Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error)
}

type API interface {
	RequestDetails(string) error
	RequestHistory(string) error
	Tag(context.Context, string, backendimages.TagOptions) (backend.CommandResult, error)
	Remove(context.Context, string, backendimages.RemoveOptions) (backend.CommandResult, error)
	Prune(context.Context, backendimages.PruneOptions) (backend.CommandResult, error)
	Pull(context.Context, string, backendimages.PullOptions) (backend.Job, error)
}

type JobTracker interface {
	Register(backend.Job, string) tea.Cmd
	Has(string) bool
}

type panelMode uint8

const (
	detailsPanel panelMode = iota
	historyPanel
)

type overlayMode uint8

const (
	noOverlay overlayMode = iota
	filterOverlay
	tagOverlay
	removeOverlay
	pruneOverlay
	pullOverlay
)

type sortMode uint8

const (
	sortReference sortMode = iota
	sortSize
	sortCreated
)

type panelState struct {
	loading bool
	stale   bool
	err     error
}

type Model struct {
	backend Backend
	api     API
	jobs    JobTracker
	session context.Context
	pageCtx context.Context
	cancel  context.CancelFunc

	subscription backend.Subscription
	generation   uint64
	selectionGen uint64
	active       bool
	width        int
	height       int
	spinner      spinner.Model

	images    []backendimages.Summary
	hasData   bool
	loading   bool
	stale     bool
	err       error
	updatedAt time.Time
	selected  string
	listStart int

	filter     string
	filterEdit string
	dangling   int // -1 all, 0 tagged, 1 dangling
	sort       sortMode
	panel      panelMode
	overlay    overlayMode
	field      int

	details      backendimages.Details
	detailsID    string
	detailsState panelState
	history      backendimages.HistoryUpdated
	historyState panelState

	editPrimary   string
	editSecondary string
	removeForce   bool
	removeParents bool
	pending       bool
	notice        string
	remoteJob     backend.JobProgressed
}

func New(ctx context.Context, common Backend, api API, jobs JobTracker) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	return Model{backend: common, api: api, jobs: jobs, session: ctx, dangling: -1, spinner: ui.NewSpinner()}
}

func (model Model) Activate() (Model, tea.Cmd) {
	model = model.Deactivate()
	model.generation++
	model.pageCtx, model.cancel = context.WithCancel(model.session)
	model.active, model.loading, model.stale, model.err = true, true, model.hasData, nil
	return model, subscribe(model.pageCtx, model.backend, model.generation)
}

func (model Model) Deactivate() Model {
	if model.cancel != nil {
		model.cancel()
	}
	if model.subscription != nil {
		_ = model.subscription.Close()
	}
	model.cancel, model.pageCtx, model.subscription = nil, nil, nil
	model.active, model.loading, model.overlay, model.pending = false, false, noOverlay, false
	return model
}

func (model Model) Refresh() (Model, tea.Cmd) {
	if !model.active || model.backend == nil {
		return model, nil
	}
	model.loading, model.err = true, nil
	return model, tea.Batch(requestBase(model.backend, model.generation), model.spinner.Tick)
}

func (model Model) SetSize(width, height int) Model {
	model.width, model.height = max(width, 0), max(height, 0)
	model.ensureSelectionVisible()
	return model
}

func (model Model) Active() bool        { return model.active }
func (model Model) CapturesInput() bool { return model.overlay != noOverlay || model.pending }
func (model Model) Activity() string {
	if model.loading || model.pending {
		return model.spinner.View()
	}
	return ""
}
func (model Model) Help() string {
	if model.overlay != noOverlay {
		return "tab next field │ enter submit │ esc cancel"
	}
	return "↑↓ Move │ f Filter │ v All/Tagged/Dangling │ o Sort │ i Details │ h History │ t Tag │ d Remove │ p Prune │ u Pull"
}
func (model Model) Status() string {
	if model.notice != "" {
		return model.notice
	}
	if model.loading && !model.hasData {
		return "Loading images"
	}
	if model.pending {
		return "Working…"
	}
	if model.loading {
		return "Refreshing images"
	}
	if model.err != nil {
		return "Image data may be stale"
	}
	if !model.updatedAt.IsZero() {
		return "Images updated " + model.updatedAt.Format(time.TimeOnly)
	}
	return "Images waiting for data"
}

func (model Model) visible() []backendimages.Summary {
	values := append([]backendimages.Summary(nil), model.images...)
	query := strings.ToLower(strings.TrimSpace(model.filter))
	values = deleteFunc(values, func(value backendimages.Summary) bool {
		dangling := len(value.RepoTags) == 0
		if model.dangling == 1 && !dangling || model.dangling == 0 && dangling {
			return true
		}
		plain := strings.ToLower(strings.Join(append(append([]string{}, value.RepoTags...), value.RepoDigests...), " ") + " " + value.ID)
		return query != "" && !strings.Contains(plain, query)
	})
	sort.SliceStable(values, func(i, j int) bool {
		switch model.sort {
		case sortSize:
			if values[i].Size != values[j].Size {
				return values[i].Size > values[j].Size
			}
		case sortCreated:
			if !values[i].Created.Equal(values[j].Created) {
				return values[i].Created.After(values[j].Created)
			}
		}
		return imageName(values[i]) < imageName(values[j])
	})
	return values
}

func imageName(value backendimages.Summary) string {
	if len(value.RepoTags) > 0 {
		return value.RepoTags[0]
	}
	return "<none>"
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func deleteFunc[T any](values []T, remove func(T) bool) []T {
	result := values[:0]
	for _, value := range values {
		if !remove(value) {
			result = append(result, value)
		}
	}
	return result
}

func (model Model) listRows() int {
	height := model.height
	if model.width < 110 {
		height = max(height/2, 8)
	}
	return max(height-6, 1)
}

func (model *Model) ensureSelectionVisible() {
	values, rows := model.visible(), model.listRows()
	model.listStart = max(0, min(model.listStart, max(len(values)-rows, 0)))
	for index := range values {
		if values[index].ID != model.selected {
			continue
		}
		if index < model.listStart {
			model.listStart = index
		} else if index >= model.listStart+rows {
			model.listStart = index - rows + 1
		}
		return
	}
}
