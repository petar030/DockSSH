// Package system implements the session-local System page.
package system

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendsystem "github.com/petar030/ssh-native-docker-tui/internal/backend/system"
	"time"
)

type Backend interface {
	RequestRefresh(backend.Page) error
	Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error)
}
type API interface {
	RequestDiskUsage() error
	PruneContainers(context.Context, backendsystem.ContainerPruneOptions) (backendsystem.PruneResult, error)
	PruneImages(context.Context, backendsystem.ImagePruneOptions) (backendsystem.PruneResult, error)
	PruneVolumes(context.Context, backendsystem.VolumePruneOptions) (backendsystem.PruneResult, error)
	PruneNetworks(context.Context, backendsystem.NetworkPruneOptions) (backendsystem.PruneResult, error)
	PruneSystem(context.Context, backendsystem.SystemPruneOptions) (backendsystem.PruneResult, error)
}
type overlayMode uint8

const (
	noOverlay overlayMode = iota
	pruneMenu
	containerPrune
	imagePrune
	volumePrune
	networkPrune
	systemPrune
)

type Model struct {
	backend                                                          Backend
	api                                                              API
	session, pageCtx                                                 context.Context
	cancel                                                           context.CancelFunc
	subscription                                                     backend.Subscription
	generation                                                       uint64
	active                                                           bool
	width, height                                                    int
	spinner                                                          spinner.Model
	info                                                             backendsystem.InfoUpdated
	disk                                                             backendsystem.DiskUsageUpdated
	hasInfo, hasDisk, loading, stale                                 bool
	err                                                              error
	updatedAt                                                        time.Time
	diskMode                                                         int
	scroll                                                           int
	overlay                                                          overlayMode
	field                                                            int
	fields                                                           [3]string
	dangling, allVolumes, includeVolumes, pending, systemPruneDenied bool
	notice                                                           string
	report                                                           backendsystem.PruneReport
	reportVisible                                                    bool
}

func New(ctx context.Context, b Backend, a API) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	return Model{backend: b, api: a, session: ctx, spinner: spinner.New(spinner.WithSpinner(spinner.Dot))}
}
func (m Model) Activate() (Model, tea.Cmd) {
	m = m.Deactivate()
	m.generation++
	m.pageCtx, m.cancel = context.WithCancel(m.session)
	m.active, m.loading, m.stale, m.err = true, true, m.hasInfo, nil
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
	if !m.active {
		return m, nil
	}
	m.loading, m.err = true, nil
	return m, tea.Batch(requestBase(m.backend, m.generation), requestDisk(m.api, m.generation), m.spinner.Tick)
}
func (m Model) SetSize(w, h int) Model { m.width, m.height = max(w, 0), max(h, 0); return m }
func (m Model) Active() bool           { return m.active }
func (m Model) CapturesInput() bool    { return m.overlay != noOverlay || m.pending }
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
	return "i Cycle disk details │ j/k Scroll │ p Prune options"
}
func (m Model) Status() string {
	if m.notice != "" {
		return m.notice
	}
	if m.loading && !m.hasInfo {
		return "Loading system information"
	}
	if m.loading {
		return "Refreshing system information"
	}
	if m.pending {
		return "Working…"
	}
	if m.err != nil {
		return "System data may be stale"
	}
	if !m.updatedAt.IsZero() {
		return "System updated " + m.updatedAt.Format(time.TimeOnly)
	}
	return "System waiting for data"
}
