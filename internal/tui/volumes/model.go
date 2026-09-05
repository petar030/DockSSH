// Package volumes implements the session-local Volumes page.
package volumes

import (
	"context"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendvolumes "github.com/petar030/ssh-native-docker-tui/internal/backend/volumes"
)

type Backend interface {
	RequestRefresh(backend.Page) error
	Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error)
}

type API interface {
	RequestDetails(string) error
	RequestAttachments(string) error
	Create(context.Context, backendvolumes.CreateOptions) (backend.CommandResult, error)
	Remove(context.Context, string, backendvolumes.RemoveOptions) (backend.CommandResult, error)
	Prune(context.Context, backendvolumes.PruneOptions) (backend.CommandResult, error)
}

type overlayMode uint8

const (
	noOverlay overlayMode = iota
	filterOverlay
	createOverlay
	removeOverlay
	pruneOverlay
)

type Model struct {
	backend                  Backend
	api                      API
	session, pageCtx         context.Context
	cancel                   context.CancelFunc
	subscription             backend.Subscription
	generation, selectionGen uint64
	active                   bool
	width, height            int
	spinner                  spinner.Model

	values                  []backendvolumes.Volume
	warnings                []string
	hasData, loading, stale bool
	err                     error
	updatedAt               time.Time
	selected                string
	listStart               int
	filter, filterEdit      string
	details                 backendvolumes.Volume
	detailsName             string
	attachments             backendvolumes.AttachmentsUpdated
	showAttachments         bool
	targetLoading           bool
	targetErr               error

	overlay                        overlayMode
	field                          int
	fields                         [6]string
	removeForce, pruneAll, pending bool
	notice                         string
}

func New(ctx context.Context, common Backend, api API) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	return Model{backend: common, api: api, session: ctx, spinner: spinner.New(spinner.WithSpinner(spinner.Dot))}
}
func (m Model) Activate() (Model, tea.Cmd) {
	m = m.Deactivate()
	m.generation++
	m.pageCtx, m.cancel = context.WithCancel(m.session)
	m.active, m.loading, m.stale, m.err = true, true, m.hasData, nil
	return m, subscribe(m.pageCtx, m.backend, m.generation)
}
func (m Model) Deactivate() Model {
	if m.cancel != nil {
		m.cancel()
	}
	if m.subscription != nil {
		_ = m.subscription.Close()
	}
	m.cancel, m.pageCtx, m.subscription = nil, nil, nil
	m.active, m.loading, m.overlay, m.pending = false, false, noOverlay, false
	return m
}
func (m Model) Refresh() (Model, tea.Cmd) {
	if !m.active || m.backend == nil {
		return m, nil
	}
	m.loading, m.err = true, nil
	return m, tea.Batch(requestBase(m.backend, m.generation), m.spinner.Tick)
}
func (m Model) SetSize(w, h int) Model {
	m.width, m.height = max(w, 0), max(h, 0)
	m.ensureVisible()
	return m
}
func (m Model) Active() bool        { return m.active }
func (m Model) CapturesInput() bool { return m.overlay != noOverlay }
func (m Model) Activity() string {
	if m.loading {
		return m.spinner.View()
	}
	return ""
}
func (m Model) Help() string {
	if m.overlay != noOverlay {
		return "tab next field │ enter submit │ esc cancel"
	}
	return "↑↓ Move │ f Filter │ i Details │ a Attachments │ c Create │ d Remove │ p Prune"
}
func (m Model) Status() string {
	if m.notice != "" {
		return m.notice
	}
	if m.loading && !m.hasData {
		return "Loading volumes"
	}
	if m.loading {
		return "Refreshing volumes"
	}
	if m.err != nil {
		return "Volume data may be stale"
	}
	if !m.updatedAt.IsZero() {
		return "Volumes updated " + m.updatedAt.Format(time.TimeOnly)
	}
	return "Volumes waiting for data"
}

func (m Model) visible() []backendvolumes.Volume {
	values := append([]backendvolumes.Volume(nil), m.values...)
	q := strings.ToLower(strings.TrimSpace(m.filter))
	if q != "" {
		out := values[:0]
		for _, v := range values {
			if strings.Contains(strings.ToLower(v.Name+" "+v.Driver+" "+v.Scope), q) {
				out = append(out, v)
			}
		}
		values = out
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values
}
func (m Model) rows() int {
	h := m.height
	if m.width < 110 {
		h = max(h/2, 8)
	}
	return max(h-7, 1)
}
func (m *Model) ensureVisible() {
	values, rows := m.visible(), m.rows()
	m.listStart = max(0, min(m.listStart, max(len(values)-rows, 0)))
	for i, v := range values {
		if v.Name == m.selected {
			if i < m.listStart {
				m.listStart = i
			} else if i >= m.listStart+rows {
				m.listStart = i - rows + 1
			}
			return
		}
	}
}
func (m *Model) chooseExisting() {
	values := m.visible()
	for _, v := range values {
		if v.Name == m.selected {
			m.ensureVisible()
			return
		}
	}
	m.clearSelection()
	if len(values) > 0 {
		m.selected = values[0].Name
		m.selectionGen++
	}
	m.ensureVisible()
}
func (m *Model) clearSelection() {
	m.selected = ""
	m.selectionGen++
	m.details = backendvolumes.Volume{}
	m.detailsName = ""
	m.attachments = backendvolumes.AttachmentsUpdated{}
	m.targetErr = nil
	m.showAttachments = false
}
func (m Model) exists(name string) bool {
	for _, v := range m.values {
		if v.Name == name {
			return true
		}
	}
	return false
}
