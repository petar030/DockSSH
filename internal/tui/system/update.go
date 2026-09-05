package system

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendsystem "github.com/petar030/ssh-native-docker-tui/internal/backend/system"
	"strings"
	"unicode/utf8"
)

type subscriptionReadyMsg struct {
	generation   uint64
	subscription backend.Subscription
	err          error
}
type requestFinishedMsg struct {
	generation uint64
	key        backend.RefreshKey
	err        error
}
type eventReceivedMsg struct {
	generation uint64
	event      backend.EventEnvelope
	open       bool
}
type pruneFinishedMsg struct {
	generation uint64
	kind       overlayMode
	result     backendsystem.PruneResult
	err        error
}

func subscribe(ctx context.Context, b Backend, g uint64) tea.Cmd {
	return func() tea.Msg {
		if b == nil {
			return subscriptionReadyMsg{generation: g, err: invalid("subscribe to System")}
		}
		s, e := b.Subscribe(ctx, backend.PageSystem, backend.EventFilter{Types: []backend.EventType{backendsystem.EventInfoUpdated, backendsystem.EventDiskUsageUpdated, backend.EventRefreshFailed, backend.EventSubscriberOverflow}})
		return subscriptionReadyMsg{g, s, e}
	}
}
func requestBase(b Backend, g uint64) tea.Cmd {
	return func() tea.Msg {
		return requestFinishedMsg{generation: g, key: backend.RefreshKey{Kind: backendsystem.RefreshKindInfo}, err: b.RequestRefresh(backend.PageSystem)}
	}
}
func requestDisk(a API, g uint64) tea.Cmd {
	return func() tea.Msg {
		var e error
		if a == nil {
			e = invalid("request disk usage")
		} else {
			e = a.RequestDiskUsage()
		}
		return requestFinishedMsg{generation: g, key: backend.RefreshKey{Kind: backendsystem.RefreshKindDiskUsage}, err: e}
	}
}
func waitEvent(s backend.Subscription, g uint64) tea.Cmd {
	return func() tea.Msg { e, ok := <-s.Events(); return eventReceivedMsg{g, e, ok} }
}
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch x := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(x)
	case subscriptionReadyMsg:
		if !m.current(x.generation) {
			if x.subscription != nil {
				_ = x.subscription.Close()
			}
			return m, nil
		}
		if x.err != nil {
			m.loading, m.stale, m.err = false, m.hasInfo, x.err
			return m, nil
		}
		m.subscription = x.subscription
		return m, tea.Batch(waitEvent(m.subscription, m.generation), requestBase(m.backend, m.generation), requestDisk(m.api, m.generation), m.spinner.Tick)
	case requestFinishedMsg:
		if !m.current(x.generation) {
			return m, nil
		}
		if x.err != nil {
			m.loading = false
			m.stale = m.hasInfo || m.hasDisk
			m.err = x.err
		}
	case eventReceivedMsg:
		if !m.current(x.generation) {
			return m, nil
		}
		if !x.open {
			m.subscription = nil
			m.loading = false
			if m.pageCtx == nil || m.pageCtx.Err() == nil {
				m.err = invalid("receive System events")
			}
			return m, nil
		}
		var extra tea.Cmd
		switch p := x.event.Payload.(type) {
		case backendsystem.InfoUpdated:
			m.info = p
			m.hasInfo = true
			m.loading = false
			m.stale = false
			m.err = nil
			m.updatedAt = x.event.Time
			if m.hasDisk {
				extra = requestDisk(m.api, m.generation)
			}
		case backendsystem.DiskUsageUpdated:
			if x.event.Key.Kind == backendsystem.RefreshKindDiskUsage {
				m.disk = p
				m.hasDisk = true
				m.loading = false
				m.stale = false
				m.err = nil
				m.updatedAt = x.event.Time
			}
		case backend.RefreshFailed:
			m.loading = false
			m.stale = m.hasInfo || m.hasDisk
			m.err = p.Err
		case backend.SubscriberOverflow:
			m.loading = true
			m.stale = m.hasInfo || m.hasDisk
			extra = tea.Batch(requestBase(m.backend, m.generation), requestDisk(m.api, m.generation), m.spinner.Tick)
		}
		return m, tea.Batch(waitEvent(m.subscription, m.generation), extra)
	case pruneFinishedMsg:
		if !m.current(x.generation) {
			return m, nil
		}
		m.pending = false
		if x.err != nil {
			m.notice = errorText(x.err)
			if x.kind == systemPrune && backend.HasErrorCode(x.err, backend.ErrorPermissionDenied) {
				m.systemPruneDenied = true
				m.overlay = systemPrune
			} else if backend.HasErrorCode(x.err, backend.ErrorInvalidInput) {
				m.overlay = x.kind
			}
			if x.result.Command.OperationID != "" {
				m.stale = true
				m.notice += "; final disk state uncertain until refresh succeeds"
			}
			return m, nil
		}
		m.notice = ""
		m.report = x.result.Report
		m.reportVisible = true
		return m, requestDisk(m.api, m.generation)
	default:
		if m.loading {
			var c tea.Cmd
			m.spinner, c = m.spinner.Update(msg)
			return m, c
		}
	}
	return m, nil
}
func (m Model) handleKey(k tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.overlay != noOverlay {
		return m.handleOverlay(k)
	}
	switch k.String() {
	case "i":
		m.diskMode = (m.diskMode + 1) % 5
		m.scroll = 0
	case "down", "j":
		m.scroll++
	case "up", "k":
		m.scroll = max(0, m.scroll-1)
	case "p":
		if !m.pending {
			m.overlay = pruneMenu
			m.notice = ""
		}
	case "esc":
		m.reportVisible = false
	}
	return m, nil
}
func (m Model) handleOverlay(k tea.KeyPressMsg) (Model, tea.Cmd) {
	key := k.String()
	if key == "esc" {
		m.overlay = noOverlay
		m.notice = ""
		return m, nil
	}
	if m.overlay == pruneMenu {
		switch key {
		case "c":
			m.openForm(containerPrune)
		case "i":
			m.openForm(imagePrune)
		case "v":
			m.openForm(volumePrune)
		case "n":
			m.openForm(networkPrune)
		case "s":
			m.openForm(systemPrune)
		}
		return m, nil
	}
	if m.overlay == imagePrune && key == "d" {
		m.dangling = !m.dangling
		return m, nil
	}
	if m.overlay == volumePrune && key == "a" {
		m.allVolumes = !m.allVolumes
		return m, nil
	}
	if m.overlay == systemPrune && key == "v" {
		m.includeVolumes = !m.includeVolumes
		return m, nil
	}
	if key == "tab" {
		limit := 3
		if m.overlay == systemPrune {
			limit = 1
		}
		m.field = (m.field + 1) % limit
		return m, nil
	}
	if key == "enter" {
		labels := pair(m.fields[1], m.fields[2])
		switch m.overlay {
		case containerPrune:
			if strings.TrimSpace(m.fields[0]) == "" && len(labels) == 0 {
				return m.refuse("Until or a label is required")
			}
			o := backendsystem.ContainerPruneOptions{Until: strings.TrimSpace(m.fields[0]), Labels: labels}
			return m.run(containerPrune, func(ctx context.Context) (backendsystem.PruneResult, error) { return m.api.PruneContainers(ctx, o) })
		case imagePrune:
			if !m.dangling && strings.TrimSpace(m.fields[0]) == "" && len(labels) == 0 {
				return m.refuse("Dangling=true, until, or a label is required")
			}
			var d *bool
			if m.dangling {
				x := true
				d = &x
			}
			o := backendsystem.ImagePruneOptions{Dangling: d, Until: strings.TrimSpace(m.fields[0]), Labels: labels}
			return m.run(imagePrune, func(ctx context.Context) (backendsystem.PruneResult, error) { return m.api.PruneImages(ctx, o) })
		case volumePrune:
			if len(labels) == 0 {
				return m.refuse("A label is required; All does not remove this scope")
			}
			o := backendsystem.VolumePruneOptions{All: m.allVolumes, Labels: labels}
			return m.run(volumePrune, func(ctx context.Context) (backendsystem.PruneResult, error) { return m.api.PruneVolumes(ctx, o) })
		case networkPrune:
			if strings.TrimSpace(m.fields[0]) == "" && len(labels) == 0 {
				return m.refuse("Until or a label is required")
			}
			o := backendsystem.NetworkPruneOptions{Until: strings.TrimSpace(m.fields[0]), Labels: labels}
			return m.run(networkPrune, func(ctx context.Context) (backendsystem.PruneResult, error) { return m.api.PruneNetworks(ctx, o) })
		case systemPrune:
			if m.systemPruneDenied {
				return m.refuse("Broad system prune is unavailable by server policy")
			}
			if m.fields[0] != backendsystem.SystemPruneConfirmation {
				return m.refuse("Type the exact confirmation token")
			}
			o := backendsystem.SystemPruneOptions{Confirmation: m.fields[0], IncludeVolumes: m.includeVolumes}
			return m.run(systemPrune, func(ctx context.Context) (backendsystem.PruneResult, error) { return m.api.PruneSystem(ctx, o) })
		}
	}
	if key == "backspace" {
		m.fields[m.field] = trim(m.fields[m.field])
		return m, nil
	}
	if k.Key().Text != "" {
		m.fields[m.field] += k.Key().Text
	}
	return m, nil
}
func (m *Model) openForm(kind overlayMode) {
	m.overlay = kind
	m.field = 0
	m.fields = [3]string{}
	m.dangling, m.allVolumes, m.includeVolumes = false, false, false
	m.notice = ""
}
func (m Model) refuse(v string) (Model, tea.Cmd) { m.notice = v; return m, nil }
func (m Model) run(kind overlayMode, fn func(context.Context) (backendsystem.PruneResult, error)) (Model, tea.Cmd) {
	if m.pending || m.api == nil {
		return m, nil
	}
	m.pending = true
	m.notice = ""
	m.overlay = noOverlay
	g, ctx := m.generation, m.pageCtx
	return m, func() tea.Msg { r, e := fn(ctx); return pruneFinishedMsg{g, kind, r, e} }
}
func (m Model) current(g uint64) bool { return m.active && m.generation == g }
func pair(k, v string) map[string]string {
	k = strings.TrimSpace(k)
	if k == "" {
		return nil
	}
	return map[string]string{k: strings.TrimSpace(v)}
}
func trim(v string) string {
	_, n := utf8.DecodeLastRuneInString(v)
	if n == 0 {
		return v
	}
	return v[:len(v)-n]
}
func invalid(o string) error { return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: o} }
func errorText(e error) string {
	for _, x := range []struct {
		c backend.ErrorCode
		t string
	}{{backend.ErrorPermissionDenied, "unavailable by server policy"}, {backend.ErrorDaemonUnavailable, "Docker unavailable"}, {backend.ErrorTimeout, "timed out"}, {backend.ErrorConflict, "resource is currently in use"}, {backend.ErrorInvalidInput, "invalid input"}} {
		if backend.HasErrorCode(e, x.c) {
			return x.t
		}
	}
	return fmt.Sprintf("operation failed: %v", e)
}
