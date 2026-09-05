package volumes

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendvolumes "github.com/petar030/ssh-native-docker-tui/internal/backend/volumes"
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

func subscribe(ctx context.Context, common Backend, g uint64) tea.Cmd {
	return func() tea.Msg {
		if common == nil {
			return subscriptionReadyMsg{generation: g, err: invalid("subscribe to Volumes")}
		}
		s, e := common.Subscribe(ctx, backend.PageVolumes, backend.EventFilter{Types: []backend.EventType{backendvolumes.EventListUpdated, backendvolumes.EventDetailsUpdated, backendvolumes.EventAttachmentsUpdated, backend.EventRefreshFailed, backend.EventSubscriberOverflow}})
		return subscriptionReadyMsg{g, s, e}
	}
}
func requestBase(common Backend, g uint64) tea.Cmd {
	return func() tea.Msg {
		return requestFinishedMsg{generation: g, key: backend.RefreshKey{Kind: backendvolumes.RefreshKindList}, err: common.RequestRefresh(backend.PageVolumes)}
	}
}
func requestTarget(api API, g, sg uint64, key backend.RefreshKey) tea.Cmd {
	return func() tea.Msg {
		var e error
		if api == nil {
			e = invalid("request volume data")
		} else if key.Kind == backendvolumes.RefreshKindAttachments {
			e = api.RequestAttachments(key.ID)
		} else {
			e = api.RequestDetails(key.ID)
		}
		return requestFinishedMsg{g, sg, key, e}
	}
}
func waitEvent(s backend.Subscription, g uint64) tea.Cmd {
	return func() tea.Msg { e, ok := <-s.Events(); return eventReceivedMsg{g, e, ok} }
}

func (m Model) Update(message tea.Msg) (Model, tea.Cmd) {
	switch x := message.(type) {
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
				m.err = invalid("receive Volumes events")
			}
			return m, nil
		}
		var cmd tea.Cmd
		m, cmd = m.handleEvent(x.event)
		return m, tea.Batch(waitEvent(m.subscription, m.generation), cmd)
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
				m.clearSelection()
				return m, requestBase(m.backend, m.generation)
			}
		} else {
			m.notice = ""
		}
	default:
		if m.loading || m.pending {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(message)
			return m, cmd
		}
	}
	return m, nil
}

func (m Model) handleEvent(event backend.EventEnvelope) (Model, tea.Cmd) {
	switch p := event.Payload.(type) {
	case backendvolumes.ListUpdated:
		m.values = append([]backendvolumes.Volume(nil), p.Volumes...)
		m.warnings = append([]string(nil), p.Warnings...)
		m.hasData, m.loading, m.stale, m.err, m.updatedAt = true, false, false, nil, event.Time
		if m.selected != "" && !m.exists(m.selected) {
			m.clearSelection()
		}
		m.chooseExisting()
		if m.selected != "" {
			m.targetLoading = true
			return m, requestTarget(m.api, m.generation, m.selectionGen, backend.RefreshKey{Kind: backendvolumes.RefreshKindDetails, ID: m.selected})
		}
	case backendvolumes.DetailsUpdated:
		if event.Key.Kind == backendvolumes.RefreshKindDetails && event.Key.ID == m.selected && p.Volume.Name == m.selected {
			m.details, m.detailsName, m.targetLoading, m.targetErr = p.Volume, p.Volume.Name, false, nil
		}
	case backendvolumes.AttachmentsUpdated:
		if event.Key.Kind == backendvolumes.RefreshKindAttachments && event.Key.ID == m.selected && p.VolumeName == m.selected {
			m.attachments, m.targetLoading, m.targetErr = p, false, nil
		}
	case backend.RefreshFailed:
		m.applyError(event.Key, p.Err)
	case backend.SubscriberOverflow:
		m.loading, m.stale = true, m.hasData
		cmds := []tea.Cmd{requestBase(m.backend, m.generation), m.spinner.Tick}
		if m.selected != "" {
			kind := backendvolumes.RefreshKindDetails
			if m.showAttachments {
				kind = backendvolumes.RefreshKindAttachments
			}
			cmds = append(cmds, requestTarget(m.api, m.generation, m.selectionGen, backend.RefreshKey{Kind: kind, ID: m.selected}))
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.pending {
		return m, nil
	}
	if m.overlay != noOverlay {
		return m.handleOverlay(k)
	}
	switch k.String() {
	case "up", "k":
		if m.showAttachments {
			m.attachmentsStart = max(0, m.attachmentsStart-1)
			return m, nil
		}
		return m.move(-1)
	case "down", "j":
		if m.showAttachments {
			m.attachmentsStart = min(m.attachmentsStart+1, max(len(m.attachments.Attachments)-m.attachmentRows(), 0))
			return m, nil
		}
		return m.move(1)
	case "f":
		m.filterEdit, m.overlay = m.filter, filterOverlay
	case "i", "enter":
		m.showAttachments = false
		return m.load(backendvolumes.RefreshKindDetails)
	case "a":
		m.showAttachments, m.attachmentsStart = true, 0
		return m.load(backendvolumes.RefreshKindAttachments)
	case "c":
		if !m.pending {
			m.overlay, m.field, m.fields = createOverlay, 0, [6]string{}
		}
	case "d":
		if m.selected != "" && !m.pending {
			m.overlay, m.removeForce = removeOverlay, false
		}
	case "p":
		if !m.pending {
			m.overlay, m.field, m.fields, m.pruneAll = pruneOverlay, 0, [6]string{}, false
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
		switch key {
		case "f":
			m.removeForce = !m.removeForce
		case "n":
			m.overlay = noOverlay
		case "y", "enter":
			m.overlay = noOverlay
			return m.run(m.selected, removeOverlay, func(ctx context.Context) (backend.CommandResult, error) {
				return m.api.Remove(ctx, m.selected, backendvolumes.RemoveOptions{Force: m.removeForce})
			})
		}
		return m, nil
	}
	if m.overlay == pruneOverlay && key == "a" {
		m.pruneAll = !m.pruneAll
		return m, nil
	}
	if key == "tab" {
		limit := 1
		if m.overlay == createOverlay {
			limit = 6
		} else if m.overlay == pruneOverlay {
			limit = 2
		}
		m.field = (m.field + 1) % limit
		return m, nil
	}
	if key == "enter" {
		switch m.overlay {
		case filterOverlay:
			m.filter = strings.TrimSpace(m.filterEdit)
			m.overlay = noOverlay
			m.chooseExisting()
			return m.load(backendvolumes.RefreshKindDetails)
		case createOverlay:
			if strings.TrimSpace(m.fields[0]) == "" {
				m.notice = "Volume name is required"
				return m, nil
			}
			opts := backendvolumes.CreateOptions{Name: strings.TrimSpace(m.fields[0]), Driver: strings.TrimSpace(m.fields[1]), Labels: pair(m.fields[2], m.fields[3]), DriverOptions: pair(m.fields[4], m.fields[5])}
			m.overlay = noOverlay
			return m.run("", createOverlay, func(ctx context.Context) (backend.CommandResult, error) { return m.api.Create(ctx, opts) })
		case pruneOverlay:
			labels := pair(m.fields[0], m.fields[1])
			if len(labels) == 0 {
				m.notice = "A label filter is required; All never removes this scope"
				return m, nil
			}
			opts := backendvolumes.PruneOptions{All: m.pruneAll, Labels: labels}
			m.overlay = noOverlay
			return m.run("", pruneOverlay, func(ctx context.Context) (backend.CommandResult, error) { return m.api.Prune(ctx, opts) })
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

func (m Model) move(delta int) (Model, tea.Cmd) {
	v := m.visible()
	if len(v) == 0 {
		return m, nil
	}
	i := 0
	for n := range v {
		if v[n].Name == m.selected {
			i = n
			break
		}
	}
	i = max(0, min(len(v)-1, i+delta))
	if v[i].Name == m.selected {
		return m, nil
	}
	m.selected = v[i].Name
	m.selectionGen++
	m.showAttachments = false
	m.attachmentsStart = 0
	m.targetLoading = true
	m.ensureVisible()
	return m, requestTarget(m.api, m.generation, m.selectionGen, backend.RefreshKey{Kind: backendvolumes.RefreshKindDetails, ID: m.selected})
}

func (m Model) attachmentRows() int {
	height := m.height
	if m.width < 110 {
		height = max(height-max(height/2, 8), 1)
	}
	return max(height-5, 1)
}
func (m Model) load(kind backend.RefreshKind) (Model, tea.Cmd) {
	if m.selected == "" {
		return m, nil
	}
	m.targetLoading = true
	m.targetErr = nil
	return m, requestTarget(m.api, m.generation, m.selectionGen, backend.RefreshKey{Kind: kind, ID: m.selected})
}
func (m Model) run(selected string, reopen overlayMode, fn func(context.Context) (backend.CommandResult, error)) (Model, tea.Cmd) {
	if m.pending || m.api == nil {
		return m, nil
	}
	m.pending = true
	m.notice = ""
	g, ctx := m.generation, m.pageCtx
	return m, tea.Batch(func() tea.Msg { _, e := fn(ctx); return commandFinishedMsg{g, selected, reopen, e} }, m.spinner.Tick)
}
func (m Model) current(g uint64) bool { return m.active && m.generation == g }
func (m *Model) applyError(key backend.RefreshKey, e error) {
	if key.Kind == backendvolumes.RefreshKindList {
		m.loading, m.stale, m.err = false, m.hasData, e
	} else if key.ID == m.selected {
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
func invalid(op string) error {
	return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: op}
}
func errorText(e error) string {
	for _, x := range []struct {
		c backend.ErrorCode
		t string
	}{{backend.ErrorConflict, "volume is in use; refresh and detach it before retrying"}, {backend.ErrorPermissionDenied, "permission denied"}, {backend.ErrorDaemonUnavailable, "Docker unavailable"}, {backend.ErrorTimeout, "timed out"}, {backend.ErrorNotFound, "volume no longer exists"}, {backend.ErrorInvalidInput, "invalid input"}} {
		if backend.HasErrorCode(e, x.c) {
			return x.t
		}
	}
	return fmt.Sprintf("operation failed: %v", e)
}
