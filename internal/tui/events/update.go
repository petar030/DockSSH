package events

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendevents "github.com/petar030/ssh-native-docker-tui/internal/backend/events"
	"unicode/utf8"
)

type subscriptionReadyMsg struct {
	generation, subscriptionGen uint64
	subscription                backend.Subscription
	replacement                 bool
	err                         error
}
type requestFinishedMsg struct {
	generation, subscriptionGen uint64
	err                         error
}
type eventReceivedMsg struct {
	generation, subscriptionGen uint64
	event                       backend.EventEnvelope
	open                        bool
}

func subscribe(ctx context.Context, b Backend, g, sg uint64, f backend.EventFilter, replacement bool) tea.Cmd {
	return func() tea.Msg {
		if b == nil {
			return subscriptionReadyMsg{generation: g, subscriptionGen: sg, replacement: replacement, err: &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "subscribe to Events"}}
		}
		s, e := b.Subscribe(ctx, backend.PageEvents, f)
		return subscriptionReadyMsg{g, sg, s, replacement, e}
	}
}
func requestRecent(b Backend, g, sg uint64) tea.Cmd {
	return func() tea.Msg { return requestFinishedMsg{g, sg, b.RequestRefresh(backend.PageEvents)} }
}
func waitEvent(s backend.Subscription, g, sg uint64) tea.Cmd {
	return func() tea.Msg { e, ok := <-s.Events(); return eventReceivedMsg{g, sg, e, ok} }
}
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch x := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(x)
	case subscriptionReadyMsg:
		if !m.current(x.generation, x.subscriptionGen) {
			if x.subscription != nil {
				_ = x.subscription.Close()
			}
			return m, nil
		}
		if x.err != nil {
			if x.subscription != nil && x.subscription != m.subscription {
				_ = x.subscription.Close()
			}
			m.loading, m.stale, m.err = false, m.hasData, x.err
			// A failed filter handover leaves the previous subscription usable.
			// Restore its generation so its already outstanding receive continues.
			if x.replacement && m.subscription != nil && m.subscriptionGen > 0 {
				m.subscriptionGen--
			}
			return m, nil
		}
		old := m.subscription
		m.subscription = x.subscription
		if x.replacement && old != nil {
			_ = old.Close()
		}
		return m, tea.Batch(waitEvent(m.subscription, m.generation, m.subscriptionGen), requestRecent(m.backend, m.generation, m.subscriptionGen), m.spinner.Tick)
	case requestFinishedMsg:
		if !m.current(x.generation, x.subscriptionGen) {
			return m, nil
		}
		if x.err != nil {
			m.loading, m.stale, m.err = false, m.hasData, x.err
		}
	case eventReceivedMsg:
		if !m.current(x.generation, x.subscriptionGen) {
			return m, nil
		}
		if !x.open {
			m.subscription = nil
			m.loading, m.stale = false, m.hasData
			if m.pageCtx == nil || m.pageCtx.Err() == nil {
				m.err = &backend.AppError{Code: backend.ErrorInternal, Operation: "receive Events"}
			}
			return m, nil
		}
		m = m.apply(x.event)
		if _, overflow := x.event.Payload.(backend.SubscriberOverflow); overflow {
			return m, tea.Batch(waitEvent(m.subscription, m.generation, m.subscriptionGen), requestRecent(m.backend, m.generation, m.subscriptionGen), m.spinner.Tick)
		}
		return m, waitEvent(m.subscription, m.generation, m.subscriptionGen)
	default:
		if m.loading {
			var c tea.Cmd
			m.spinner, c = m.spinner.Update(msg)
			return m, c
		}
	}
	return m, nil
}
func (m Model) apply(e backend.EventEnvelope) Model {
	switch p := e.Payload.(type) {
	case backendevents.RecentUpdated:
		v := make([]row, 0, len(p.Events))
		for _, x := range p.Events {
			v = append(v, row{x.Sequence, x.ReceivedAt, x.OccurredAt, x.Resource, x.ResourceID, x.Project, x.Action, clone(x.Attributes)})
		}
		m.merge(v)
		m.hasData, m.loading, m.stale, m.err, m.updatedAt = true, false, false, nil, e.Time
	case backend.DockerEventObserved:
		m.merge([]row{{e.Sequence, e.Time, p.OccurredAt, p.Resource, p.ResourceID, p.Project, p.Action, clone(p.Attributes)}})
		m.hasData, m.loading, m.stale, m.err, m.updatedAt = true, false, false, nil, e.Time
	case backend.RefreshFailed:
		m.loading, m.stale, m.err = false, m.hasData, p.Err
	case backend.SubscriberOverflow:
		m.loading, m.stale = true, m.hasData
	}
	return m
}
func (m Model) handleKey(k tea.KeyPressMsg) (Model, tea.Cmd) {
	key := k.String()
	if m.filtering {
		if key == "esc" {
			m.filtering = false
			m.notice = ""
			return m, nil
		}
		if key == "tab" {
			m.field = (m.field + 1) % 4
			return m, nil
		}
		if key == "enter" {
			m.filters = m.edits
			m.filtering = false
			m.loading = true
			m.subscriptionGen++
			return m, tea.Batch(subscribe(m.pageCtx, m.backend, m.generation, m.subscriptionGen, m.eventFilter(), true), m.spinner.Tick)
		}
		if key == "backspace" {
			m.edits[m.field] = trim(m.edits[m.field])
			return m, nil
		}
		if k.Key().Text != "" {
			m.edits[m.field] += k.Key().Text
		}
		return m, nil
	}
	switch key {
	case "f":
		m.edits = m.filters
		m.field = 0
		m.filtering = true
	case "c":
		m.rows = nil
		m.scroll = 0
		m.hasData = true
		m.notice = "Cleared this session's displayed events"
	case "down", "j":
		m.scroll = min(m.scroll+1, max(len(m.rows)-m.visibleRows(), 0))
	case "up", "k":
		m.scroll = max(m.scroll-1, 0)
	case "pgdown":
		m.scroll = min(m.scroll+m.visibleRows(), max(len(m.rows)-m.visibleRows(), 0))
	case "pgup":
		m.scroll = max(m.scroll-m.visibleRows(), 0)
	}
	return m, nil
}
func (m Model) current(g, sg uint64) bool {
	return m.active && m.generation == g && m.subscriptionGen == sg
}
func trim(v string) string {
	_, n := utf8.DecodeLastRuneInString(v)
	if n == 0 {
		return v
	}
	return v[:len(v)-n]
}
func clone(v map[string]string) map[string]string {
	if v == nil {
		return nil
	}
	o := make(map[string]string, len(v))
	for k, x := range v {
		o[k] = x
	}
	return o
}
