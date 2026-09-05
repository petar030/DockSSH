package networks

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendnetworks "github.com/petar030/ssh-native-docker-tui/internal/backend/networks"
	"strconv"
	"strings"
	"unicode/utf8"
)

type subscriptionReadyMsg struct {
	generation   uint64
	subscription backend.Subscription
	err          error
}
type requestFinishedMsg struct {
	generation, selectionGen uint64
	key                      backend.RefreshKey
	err                      error
}
type eventReceivedMsg struct {
	generation uint64
	event      backend.EventEnvelope
	open       bool
}
type commandFinishedMsg struct {
	generation uint64
	selected   string
	reopen     overlayMode
	err        error
}

func subscribe(ctx context.Context, b Backend, g uint64) tea.Cmd {
	return func() tea.Msg {
		if b == nil {
			return subscriptionReadyMsg{generation: g, err: invalid("subscribe to Networks")}
		}
		s, e := b.Subscribe(ctx, backend.PageNetworks, backend.EventFilter{Types: []backend.EventType{backendnetworks.EventListUpdated, backendnetworks.EventDetailsUpdated, backendnetworks.EventConnectionsUpdated, backend.EventRefreshFailed, backend.EventSubscriberOverflow}})
		return subscriptionReadyMsg{g, s, e}
	}
}
func requestBase(b Backend, g uint64) tea.Cmd {
	return func() tea.Msg {
		return requestFinishedMsg{generation: g, key: backend.RefreshKey{Kind: backendnetworks.RefreshKindList}, err: b.RequestRefresh(backend.PageNetworks)}
	}
}
func requestTarget(a API, g, sg uint64, k backend.RefreshKey) tea.Cmd {
	return func() tea.Msg {
		var e error
		if a == nil {
			e = invalid("request network data")
		} else if k.Kind == backendnetworks.RefreshKindConnections {
			e = a.RequestConnections(k.ID)
		} else {
			e = a.RequestDetails(k.ID)
		}
		return requestFinishedMsg{g, sg, k, e}
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
			m.loading, m.stale, m.err = false, m.hasData, x.err
			return m, nil
		}
		m.subscription = x.subscription
		return m, tea.Batch(waitEvent(m.subscription, m.generation), requestBase(m.backend, m.generation), m.spinner.Tick)
	case requestFinishedMsg:
		if !m.current(x.generation) || x.key.ID != "" && x.selectionGen != m.selectionGen {
			return m, nil
		}
		if x.err != nil {
			m.applyError(x.key, x.err)
		}
	case eventReceivedMsg:
		if !m.current(x.generation) {
			return m, nil
		}
		if !x.open {
			m.subscription = nil
			m.loading, m.stale = false, m.hasData
			if m.pageCtx == nil || m.pageCtx.Err() == nil {
				m.err = invalid("receive Networks events")
			}
			return m, nil
		}
		var c tea.Cmd
		m, c = m.handleEvent(x.event)
		return m, tea.Batch(waitEvent(m.subscription, m.generation), c)
	case commandFinishedMsg:
		if !m.current(x.generation) {
			return m, nil
		}
		m.pending = false
		if x.err != nil {
			m.notice = errorText(x.err)
			if backend.HasErrorCode(x.err, backend.ErrorInvalidInput) {
				m.overlay = x.reopen
			}
			if backend.HasErrorCode(x.err, backend.ErrorNotFound) && x.selected == m.selected {
				m.clear()
				return m, requestBase(m.backend, m.generation)
			}
		} else {
			m.notice = ""
		}
	default:
		if m.loading {
			var c tea.Cmd
			m.spinner, c = m.spinner.Update(msg)
			return m, c
		}
	}
	return m, nil
}
func (m Model) handleEvent(e backend.EventEnvelope) (Model, tea.Cmd) {
	switch p := e.Payload.(type) {
	case backendnetworks.ListUpdated:
		m.values = append([]backendnetworks.Summary(nil), p.Networks...)
		m.hasData, m.loading, m.stale, m.err, m.updatedAt = true, false, false, nil, e.Time
		if m.selected != "" && !m.exists(m.selected) {
			m.clear()
		}
		m.choose()
		if m.selected != "" {
			m.targetLoading = true
			return m, requestTarget(m.api, m.generation, m.selectionGen, backend.RefreshKey{Kind: backendnetworks.RefreshKindDetails, ID: m.selected})
		}
	case backendnetworks.DetailsUpdated:
		if e.Key.Kind == backendnetworks.RefreshKindDetails && e.Key.ID == m.selected && p.Network.ID == m.selected {
			m.details, m.detailsID, m.targetLoading, m.targetErr = p.Network, p.Network.ID, false, nil
		}
	case backendnetworks.ConnectionsUpdated:
		if e.Key.Kind == backendnetworks.RefreshKindConnections && e.Key.ID == m.selected && p.NetworkID == m.selected {
			m.connections, m.targetLoading, m.targetErr = p, false, nil
		}
	case backend.RefreshFailed:
		m.applyError(e.Key, p.Err)
	case backend.SubscriberOverflow:
		m.loading, m.stale = true, m.hasData
		cs := []tea.Cmd{requestBase(m.backend, m.generation), m.spinner.Tick}
		if m.selected != "" {
			kind := backendnetworks.RefreshKindDetails
			if m.showConnections {
				kind = backendnetworks.RefreshKindConnections
			}
			cs = append(cs, requestTarget(m.api, m.generation, m.selectionGen, backend.RefreshKey{Kind: kind, ID: m.selected}))
		}
		return m, tea.Batch(cs...)
	}
	return m, nil
}
func (m Model) handleKey(k tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.overlay != noOverlay {
		return m.handleOverlay(k)
	}
	switch k.String() {
	case "up", "k":
		return m.move(-1)
	case "down", "j":
		return m.move(1)
	case "f":
		m.filterEdit, m.overlay = m.filter, filterOverlay
	case "i", "enter":
		m.showConnections = false
		return m.load(backendnetworks.RefreshKindDetails)
	case "a":
		m.showConnections = true
		return m.load(backendnetworks.RefreshKindConnections)
	case "c":
		if !m.pending {
			m.overlay, m.field, m.fields = createOverlay, 0, [8]string{}
		}
	case "n":
		if m.selected != "" && !m.pending {
			m.overlay, m.field, m.fields = connectOverlay, 0, [8]string{}
		}
	case "x":
		if m.selected != "" && !m.pending {
			m.overlay, m.field, m.fields, m.disconnectForce = disconnectOverlay, 0, [8]string{}, false
		}
	case "d":
		if m.selected != "" && !m.pending {
			m.overlay = removeOverlay
		}
	case "p":
		if !m.pending {
			m.overlay, m.field, m.fields = pruneOverlay, 0, [8]string{}
		}
	}
	return m, nil
}
func (m Model) handleOverlay(k tea.KeyPressMsg) (Model, tea.Cmd) {
	key := k.String()
	if key == "esc" {
		m.overlay, m.notice = noOverlay, ""
		return m, nil
	}
	if m.overlay == removeOverlay {
		if key == "n" {
			m.overlay = noOverlay
		}
		if key == "y" || key == "enter" {
			m.overlay = noOverlay
			return m.run(m.selected, removeOverlay, func(ctx context.Context) (backend.CommandResult, error) {
				return m.api.Remove(ctx, m.selected, backendnetworks.RemoveOptions{})
			})
		}
		return m, nil
	}
	if m.overlay == disconnectOverlay && key == "f" {
		m.disconnectForce = !m.disconnectForce
		return m, nil
	}
	if m.overlay == createOverlay {
		if key == "I" {
			m.createInternal = !m.createInternal
			return m, nil
		}
		if key == "A" {
			m.createAttachable = !m.createAttachable
			return m, nil
		}
	}
	if key == "tab" {
		limit := 1
		switch m.overlay {
		case createOverlay:
			limit = 8
		case connectOverlay:
			limit = 5
		case disconnectOverlay:
			limit = 1
		case pruneOverlay:
			limit = 3
		}
		m.field = (m.field + 1) % limit
		return m, nil
	}
	if key == "enter" {
		switch m.overlay {
		case filterOverlay:
			m.filter = strings.TrimSpace(m.filterEdit)
			m.overlay = noOverlay
			m.choose()
			return m.load(backendnetworks.RefreshKindDetails)
		case createOverlay:
			if strings.TrimSpace(m.fields[0]) == "" {
				m.notice = "Network name is required"
				return m, nil
			}
			ipam := []backendnetworks.CreateIPAMConfig{}
			if strings.TrimSpace(m.fields[6]) != "" || strings.TrimSpace(m.fields[7]) != "" {
				ipam = append(ipam, backendnetworks.CreateIPAMConfig{Subnet: strings.TrimSpace(m.fields[6]), Gateway: strings.TrimSpace(m.fields[7])})
			}
			o := backendnetworks.CreateOptions{Name: strings.TrimSpace(m.fields[0]), Driver: strings.TrimSpace(m.fields[1]), Scope: strings.TrimSpace(m.fields[2]), Labels: pair(m.fields[3], m.fields[4]), Options: pair(m.fields[5], "true"), IPAM: ipam, Internal: m.createInternal, Attachable: m.createAttachable}
			m.overlay = noOverlay
			return m.run("", createOverlay, func(ctx context.Context) (backend.CommandResult, error) { return m.api.Create(ctx, o) })
		case connectOverlay:
			if strings.TrimSpace(m.fields[0]) == "" {
				m.notice = "Container ID is required"
				return m, nil
			}
			o := backendnetworks.ConnectOptions{ContainerID: strings.TrimSpace(m.fields[0]), IPv4Address: strings.TrimSpace(m.fields[1]), IPv6Address: strings.TrimSpace(m.fields[2]), Aliases: csv(m.fields[3])}
			if strings.TrimSpace(m.fields[4]) != "" {
				priority, e := strconv.Atoi(strings.TrimSpace(m.fields[4]))
				if e != nil {
					m.notice = "Gateway priority must be a number"
					return m, nil
				}
				o.GwPriority = priority
			}
			m.overlay = noOverlay
			return m.run(m.selected, connectOverlay, func(ctx context.Context) (backend.CommandResult, error) { return m.api.Connect(ctx, m.selected, o) })
		case disconnectOverlay:
			if strings.TrimSpace(m.fields[0]) == "" {
				m.notice = "Container ID is required"
				return m, nil
			}
			o := backendnetworks.DisconnectOptions{ContainerID: strings.TrimSpace(m.fields[0]), Force: m.disconnectForce}
			m.overlay = noOverlay
			return m.run(m.selected, disconnectOverlay, func(ctx context.Context) (backend.CommandResult, error) { return m.api.Disconnect(ctx, m.selected, o) })
		case pruneOverlay:
			labels := pair(m.fields[1], m.fields[2])
			if strings.TrimSpace(m.fields[0]) == "" && len(labels) == 0 {
				m.notice = "Until or a label filter is required"
				return m, nil
			}
			o := backendnetworks.PruneOptions{Until: strings.TrimSpace(m.fields[0]), Labels: labels}
			m.overlay = noOverlay
			return m.run("", pruneOverlay, func(ctx context.Context) (backend.CommandResult, error) { return m.api.Prune(ctx, o) })
		}
	}
	if key == "backspace" {
		m.setField(trim(m.edit()))
		return m, nil
	}
	if k.Key().Text != "" {
		m.setField(m.edit() + k.Key().Text)
	}
	return m, nil
}
func (m Model) move(d int) (Model, tea.Cmd) {
	v := m.visible()
	if len(v) == 0 {
		return m, nil
	}
	i := 0
	for n := range v {
		if v[n].ID == m.selected {
			i = n
			break
		}
	}
	i = max(0, min(len(v)-1, i+d))
	if v[i].ID == m.selected {
		return m, nil
	}
	m.selected = v[i].ID
	m.selectionGen++
	m.showConnections = false
	m.targetLoading = true
	m.ensureVisible()
	return m, requestTarget(m.api, m.generation, m.selectionGen, backend.RefreshKey{Kind: backendnetworks.RefreshKindDetails, ID: m.selected})
}
func (m Model) load(k backend.RefreshKind) (Model, tea.Cmd) {
	if m.selected == "" {
		return m, nil
	}
	m.targetLoading = true
	m.targetErr = nil
	return m, requestTarget(m.api, m.generation, m.selectionGen, backend.RefreshKey{Kind: k, ID: m.selected})
}
func (m Model) run(id string, reopen overlayMode, fn func(context.Context) (backend.CommandResult, error)) (Model, tea.Cmd) {
	if m.pending || m.api == nil {
		return m, nil
	}
	m.pending = true
	m.notice = ""
	g, ctx := m.generation, m.pageCtx
	return m, func() tea.Msg { _, e := fn(ctx); return commandFinishedMsg{g, id, reopen, e} }
}
func (m Model) current(g uint64) bool { return m.active && m.generation == g }
func (m *Model) applyError(k backend.RefreshKey, e error) {
	if k.Kind == backendnetworks.RefreshKindList {
		m.loading, m.stale, m.err = false, m.hasData, e
	} else if k.ID == m.selected {
		m.targetLoading = false
		m.targetErr = e
	}
}
func (m Model) edit() string {
	if m.overlay == filterOverlay {
		return m.filterEdit
	}
	return m.fields[m.field]
}
func (m *Model) setField(v string) {
	if m.overlay == filterOverlay {
		m.filterEdit = v
	} else {
		m.fields[m.field] = v
	}
}
func trim(v string) string {
	_, n := utf8.DecodeLastRuneInString(v)
	if n == 0 {
		return v
	}
	return v[:len(v)-n]
}
func pair(k, v string) map[string]string {
	k = strings.TrimSpace(k)
	if k == "" {
		return nil
	}
	return map[string]string{k: strings.TrimSpace(v)}
}
func csv(v string) []string {
	var o []string
	for _, x := range strings.Split(v, ",") {
		if x = strings.TrimSpace(x); x != "" {
			o = append(o, x)
		}
	}
	return o
}
func invalid(o string) error { return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: o} }
func errorText(e error) string {
	for _, x := range []struct {
		c backend.ErrorCode
		t string
	}{{backend.ErrorConflict, "network has active endpoints; disconnect them before retrying"}, {backend.ErrorPermissionDenied, "permission denied"}, {backend.ErrorDaemonUnavailable, "Docker unavailable"}, {backend.ErrorTimeout, "timed out"}, {backend.ErrorNotFound, "network no longer exists"}, {backend.ErrorInvalidInput, "invalid input"}} {
		if backend.HasErrorCode(e, x.c) {
			return x.t
		}
	}
	return fmt.Sprintf("operation failed: %v", e)
}
