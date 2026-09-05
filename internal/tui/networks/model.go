// Package networks implements the session-local Networks page.
package networks

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendnetworks "github.com/petar030/ssh-native-docker-tui/internal/backend/networks"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
	"sort"
	"strings"
	"time"
)

type Backend interface {
	RequestRefresh(backend.Page) error
	Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error)
}
type API interface {
	RequestDetails(string) error
	RequestConnections(string) error
	Create(context.Context, backendnetworks.CreateOptions) (backend.CommandResult, error)
	Remove(context.Context, string, backendnetworks.RemoveOptions) (backend.CommandResult, error)
	Prune(context.Context, backendnetworks.PruneOptions) (backend.CommandResult, error)
	Connect(context.Context, string, backendnetworks.ConnectOptions) (backend.CommandResult, error)
	Disconnect(context.Context, string, backendnetworks.DisconnectOptions) (backend.CommandResult, error)
}
type overlayMode uint8

const (
	noOverlay overlayMode = iota
	filterOverlay
	createOverlay
	removeOverlay
	connectOverlay
	disconnectOverlay
	pruneOverlay
)

type Model struct {
	backend                                                    Backend
	api                                                        API
	session, pageCtx                                           context.Context
	cancel                                                     context.CancelFunc
	subscription                                               backend.Subscription
	generation, selectionGen                                   uint64
	active                                                     bool
	width, height                                              int
	spinner                                                    spinner.Model
	values                                                     []backendnetworks.Summary
	hasData, loading, stale                                    bool
	err                                                        error
	updatedAt                                                  time.Time
	selected                                                   string
	listStart                                                  int
	filter, filterEdit                                         string
	details                                                    backendnetworks.Details
	detailsID                                                  string
	connections                                                backendnetworks.ConnectionsUpdated
	showConnections, targetLoading                             bool
	connectionsStart                                           int
	targetErr                                                  error
	overlay                                                    overlayMode
	field                                                      int
	fields                                                     [8]string
	createInternal, createAttachable, disconnectForce, pending bool
	notice                                                     string
}

func New(ctx context.Context, b Backend, a API) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	return Model{backend: b, api: a, session: ctx, spinner: ui.NewSpinner()}
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
func (m Model) CapturesInput() bool { return m.overlay != noOverlay || m.pending }
func (m Model) Activity() string {
	if m.loading || m.pending {
		return m.spinner.View()
	}
	return ""
}
func (m Model) Help() string {
	if m.overlay != noOverlay {
		return "tab next field │ enter submit │ esc cancel"
	}
	return "↑↓ Move │ f Filter │ i Details │ a Connections (j/k scroll) │ c Create │ n Connect │ x Disconnect │ d Remove │ p Prune"
}
func (m Model) Status() string {
	if m.notice != "" {
		return m.notice
	}
	if m.loading && !m.hasData {
		return "Loading networks"
	}
	if m.loading {
		return "Refreshing networks"
	}
	if m.pending {
		return "Working…"
	}
	if m.err != nil {
		return "Network data may be stale"
	}
	if !m.updatedAt.IsZero() {
		return "Networks updated " + m.updatedAt.Format(time.TimeOnly)
	}
	return "Networks waiting for data"
}
func (m Model) visible() []backendnetworks.Summary {
	v := append([]backendnetworks.Summary(nil), m.values...)
	q := strings.ToLower(strings.TrimSpace(m.filter))
	if q != "" {
		o := v[:0]
		for _, n := range v {
			if strings.Contains(strings.ToLower(n.Name+" "+n.ID+" "+n.Driver+" "+n.Scope), q) {
				o = append(o, n)
			}
		}
		v = o
	}
	sort.SliceStable(v, func(i, j int) bool { return v[i].Name < v[j].Name })
	return v
}
func (m Model) rows() int {
	h := m.height
	if m.width < 110 {
		h = max(h/2, 8)
	}
	return max(h-6, 1)
}
func (m *Model) ensureVisible() {
	v, rows := m.visible(), m.rows()
	m.listStart = max(0, min(m.listStart, max(len(v)-rows, 0)))
	for i, n := range v {
		if n.ID == m.selected {
			if i < m.listStart {
				m.listStart = i
			} else if i >= m.listStart+rows {
				m.listStart = i - rows + 1
			}
			return
		}
	}
}
func (m *Model) choose() {
	v := m.visible()
	for _, n := range v {
		if n.ID == m.selected {
			m.ensureVisible()
			return
		}
	}
	m.clear()
	if len(v) > 0 {
		m.selected = v[0].ID
		m.selectionGen++
	}
	m.ensureVisible()
}
func (m *Model) clear() {
	m.selected = ""
	m.selectionGen++
	m.details = backendnetworks.Details{}
	m.detailsID = ""
	m.connections = backendnetworks.ConnectionsUpdated{}
	m.showConnections = false
	m.targetErr = nil
}
func (m Model) exists(id string) bool {
	for _, n := range m.values {
		if n.ID == id {
			return true
		}
	}
	return false
}
