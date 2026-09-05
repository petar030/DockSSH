// Package events implements the session-local Docker Events page.
package events

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendevents "github.com/petar030/ssh-native-docker-tui/internal/backend/events"
	"sort"
	"strings"
	"time"
)

const displayLimit = 500

type Backend interface {
	RequestRefresh(backend.Page) error
	Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error)
}
type row struct {
	sequence                      uint64
	received, occurred            time.Time
	resource, id, project, action string
	attributes                    map[string]string
}
type Model struct {
	backend                     Backend
	session, pageCtx            context.Context
	cancel                      context.CancelFunc
	subscription                backend.Subscription
	generation, subscriptionGen uint64
	active                      bool
	width, height               int
	spinner                     spinner.Model
	rows                        []row
	hasData, loading, stale     bool
	err                         error
	updatedAt                   time.Time
	filters                     [4]string
	edits                       [4]string
	filtering                   bool
	field                       int
	scroll                      int
	notice                      string
}

func New(ctx context.Context, b Backend) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	return Model{backend: b, session: ctx, spinner: spinner.New(spinner.WithSpinner(spinner.Dot))}
}
func (m Model) Activate() (Model, tea.Cmd) {
	m = m.Deactivate()
	m.generation++
	m.subscriptionGen++
	m.pageCtx, m.cancel = context.WithCancel(m.session)
	m.active, m.loading, m.stale, m.err = true, true, m.hasData, nil
	return m, subscribe(m.pageCtx, m.backend, m.generation, m.subscriptionGen, m.eventFilter(), false)
}
func (m Model) Deactivate() Model {
	if m.cancel != nil {
		m.cancel()
	}
	if m.subscription != nil {
		_ = m.subscription.Close()
	}
	m.cancel, m.pageCtx, m.subscription = nil, nil, nil
	m.active, m.loading, m.filtering = false, false, false
	return m
}
func (m Model) Refresh() (Model, tea.Cmd) {
	if !m.active || m.backend == nil {
		return m, nil
	}
	m.loading, m.err = true, nil
	return m, tea.Batch(requestRecent(m.backend, m.generation, m.subscriptionGen), m.spinner.Tick)
}
func (m Model) SetSize(w, h int) Model {
	m.width, m.height = max(w, 0), max(h, 0)
	m.clampScroll()
	return m
}
func (m Model) Active() bool        { return m.active }
func (m Model) CapturesInput() bool { return m.filtering }
func (m Model) Activity() string {
	if m.loading {
		return m.spinner.View()
	}
	return ""
}
func (m Model) Help() string {
	if m.filtering {
		return "tab next field │ enter apply/resubscribe │ esc cancel"
	}
	return "j/k Scroll │ f Filters │ c Clear this session"
}
func (m Model) Status() string {
	if m.notice != "" {
		return m.notice
	}
	if m.loading && !m.hasData {
		return "Loading Docker events"
	}
	if m.loading {
		return "Refreshing Docker events"
	}
	if m.err != nil {
		return "Event data may be stale"
	}
	if !m.updatedAt.IsZero() {
		return "Events updated " + m.updatedAt.Format(time.TimeOnly)
	}
	return "Waiting for Docker events"
}
func (m Model) eventFilter() backend.EventFilter {
	return backend.EventFilter{Types: []backend.EventType{backendevents.EventRecentUpdated, backend.EventDockerObserved, backend.EventRefreshFailed, backend.EventSubscriberOverflow}, DockerResources: one(m.filters[0]), DockerResourceIDs: one(m.filters[1]), DockerActions: one(m.filters[2]), DockerProjects: one(m.filters[3])}
}
func one(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return []string{v}
}
func (m *Model) clampScroll()    { m.scroll = max(0, min(m.scroll, max(len(m.rows)-m.visibleRows(), 0))) }
func (m Model) visibleRows() int { return max(m.height-7, 1) }
func (m *Model) merge(values []row) {
	seen := make(map[string]bool, len(values)+len(m.rows))
	out := make([]row, 0, len(values)+len(m.rows))
	for _, v := range append(values, m.rows...) {
		key := rowKey(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].occurred, out[j].occurred
		if a.Equal(b) {
			return out[i].sequence > out[j].sequence
		}
		return a.After(b)
	})
	if len(out) > displayLimit {
		out = out[:displayLimit]
	}
	m.rows = out
	m.clampScroll()
}

func (m Model) matches(value row) bool {
	fields := []string{value.resource, value.id, value.action, value.project}
	for index, filter := range m.filters {
		if filter != "" && fields[index] != filter {
			return false
		}
	}
	return true
}

func (m *Model) filterRows() {
	filtered := m.rows[:0]
	for _, value := range m.rows {
		if m.matches(value) {
			filtered = append(filtered, value)
		}
	}
	m.rows = filtered
	m.clampScroll()
}
func rowKey(v row) string {
	return v.occurred.UTC().Format(time.RFC3339Nano) + "\x00" + v.resource + "\x00" + v.id + "\x00" + v.project + "\x00" + v.action
}
