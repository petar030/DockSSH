// Package compose implements the session-local Bubble Tea model for the
// Compose page.
package compose

import (
	"context"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendcompose "github.com/petar030/ssh-native-docker-tui/internal/backend/compose"
)

const maxLogLines = 2000

type Backend interface {
	RequestRefresh(backend.Page) error
	Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error)
}

type API interface {
	RequestDetails(string) error
	Start(context.Context, string, backendcompose.ServiceOptions) (backend.CommandResult, error)
	Stop(context.Context, string, backendcompose.StopOptions) (backend.CommandResult, error)
	Restart(context.Context, string, backendcompose.RestartOptions) (backend.CommandResult, error)
	Pause(context.Context, string, backendcompose.ServiceOptions) (backend.CommandResult, error)
	Unpause(context.Context, string, backendcompose.ServiceOptions) (backend.CommandResult, error)
	Scale(context.Context, backendcompose.ProjectSpec, backendcompose.ScaleOptions) (backend.CommandResult, error)
	Up(context.Context, backendcompose.ProjectSpec, backendcompose.UpOptions) (backend.Job, error)
	Down(context.Context, string, backendcompose.DownOptions) (backend.Job, error)
	Pull(context.Context, backendcompose.ProjectSpec, backendcompose.PullOptions) (backend.Job, error)
	Build(context.Context, backendcompose.ProjectSpec, backendcompose.BuildOptions) (backend.Job, error)
	Logs(context.Context, string, backendcompose.LogsOptions) (backend.Stream[backendcompose.LogEntry], error)
}

type JobTracker interface {
	Register(backend.Job, string) tea.Cmd
	Has(string) bool
}

type overlayMode uint8

type logFragmentKey struct {
	container string
	source    backendcompose.LogSource
}

const (
	noOverlay overlayMode = iota
	filterOverlay
	stopOverlay
	restartOverlay
	scaleOverlay
	upOverlay
	downOverlay
	pullOverlay
	buildOverlay
	logsOverlay
)

type Model struct {
	backend Backend
	api     API
	jobs    JobTracker
	session context.Context
	pageCtx context.Context
	cancel  context.CancelFunc

	subscription             backend.Subscription
	generation, selectionGen uint64
	active                   bool
	width, height            int
	spinner                  spinner.Model

	projects                []backendcompose.ProjectSummary
	hasData, loading, stale bool
	err                     error
	updatedAt               time.Time
	selected                string
	listStart               int
	filter, filterEdit      string
	details                 backendcompose.ProjectDetails
	detailsName             string
	detailScroll            int
	targetLoading           bool
	targetErr               error

	overlay overlayMode
	field   int
	fields  [4]string
	optionA bool
	optionB bool
	pending bool
	notice  string

	streamGen    uint64
	streamCancel context.CancelFunc
	logStream    backend.Stream[backendcompose.LogEntry]
	logLines     []string
	logFragments map[logFragmentKey]string
	logErr       error
	logFollowing bool
	logScroll    int
}

func New(ctx context.Context, common Backend, api API, jobs JobTracker) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	return Model{backend: common, api: api, jobs: jobs, session: ctx, spinner: spinner.New(spinner.WithSpinner(spinner.Dot))}
}

func (m Model) Activate() (Model, tea.Cmd) {
	m = m.Deactivate()
	m.generation++
	m.pageCtx, m.cancel = context.WithCancel(m.session)
	m.active, m.loading, m.stale, m.err = true, true, m.hasData, nil
	return m, subscribe(m.pageCtx, m.backend, m.generation)
}

func (m Model) Deactivate() Model {
	m = m.closeLogs()
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

func (m Model) SetSize(width, height int) Model {
	m.width, m.height = max(width, 0), max(height, 0)
	m.ensureVisible()
	m.detailScroll = min(m.detailScroll, m.detailMaxScroll())
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
	if m.overlay == logsOverlay {
		return "j/k scroll │ g/G top/bottom │ esc close logs"
	}
	if m.overlay != noOverlay {
		return "tab next field │ enter submit │ esc cancel"
	}
	return "↑↓ Move │ pgup/pgdn Details │ f Filter │ s Start │ x Stop │ e Restart │ p/P Pause/Unpause │ c Scale │ u Up │ d Down │ o Pull │ b Build │ l Logs"
}
func (m Model) Status() string {
	if m.notice != "" {
		return m.notice
	}
	if m.pending {
		return "Working…"
	}
	if m.loading && !m.hasData {
		return "Loading Compose projects"
	}
	if m.loading {
		return "Refreshing Compose projects"
	}
	if m.err != nil {
		return "Compose data may be stale"
	}
	if !m.updatedAt.IsZero() {
		return "Compose updated " + m.updatedAt.Format(time.TimeOnly)
	}
	return "Compose waiting for data"
}

func (m Model) visible() []backendcompose.ProjectSummary {
	values := append([]backendcompose.ProjectSummary(nil), m.projects...)
	query := strings.ToLower(strings.TrimSpace(m.filter))
	if query != "" {
		filtered := values[:0]
		for _, value := range values {
			text := strings.ToLower(value.Name + " " + value.Status + " " + strings.Join(value.ConfigFiles, " "))
			if strings.Contains(text, query) {
				filtered = append(filtered, value)
			}
		}
		values = filtered
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values
}

func (m Model) rows() int {
	height := m.height
	if m.width < 110 {
		height = max(height/2, 8)
	}
	return max(height-7, 1)
}

func (m *Model) ensureVisible() {
	values, rows := m.visible(), m.rows()
	m.listStart = max(0, min(m.listStart, max(len(values)-rows, 0)))
	for index, value := range values {
		if value.Name != m.selected {
			continue
		}
		if index < m.listStart {
			m.listStart = index
		} else if index >= m.listStart+rows {
			m.listStart = index - rows + 1
		}
		return
	}
}

func (m *Model) choose() {
	for _, value := range m.visible() {
		if value.Name == m.selected {
			m.ensureVisible()
			return
		}
	}
	m.clearSelection()
	if values := m.visible(); len(values) > 0 {
		m.selected = values[0].Name
		m.selectionGen++
	}
	m.ensureVisible()
}

func (m *Model) clearSelection() {
	m.selected = ""
	m.selectionGen++
	m.details = backendcompose.ProjectDetails{}
	m.detailsName = ""
	m.detailScroll = 0
	m.targetLoading = false
	m.targetErr = nil
}

func (m Model) selectedSummary() backendcompose.ProjectSummary {
	for _, value := range m.projects {
		if value.Name == m.selected {
			return value
		}
	}
	return backendcompose.ProjectSummary{}
}

func (m Model) current(generation uint64) bool { return m.active && m.generation == generation }

func (m Model) detailRows() int {
	if m.width < 110 {
		return max(m.height-max(m.height/2, 8)-3, 1)
	}
	return max(m.height-3, 1)
}

func (m Model) detailMaxScroll() int {
	return max(len(m.detailLines())-m.detailRows(), 0)
}
