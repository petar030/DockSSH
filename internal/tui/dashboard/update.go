package dashboard

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backenddashboard "github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

type subscriptionReadyMsg struct {
	generation   uint64
	subscription backend.Subscription
	err          error
}

type refreshRequestedMsg struct {
	generation uint64
	err        error
}

type eventReceivedMsg struct {
	generation uint64
	event      backend.EventEnvelope
	open       bool
}

func subscribe(ctx context.Context, application Backend, generation uint64) tea.Cmd {
	return func() tea.Msg {
		if application == nil {
			return subscriptionReadyMsg{generation: generation, err: &backend.AppError{
				Code: backend.ErrorInvalidInput, Operation: "subscribe to Dashboard",
			}}
		}
		subscription, err := application.Subscribe(ctx, backend.PageDashboard, backend.EventFilter{
			Types: []backend.EventType{
				backenddashboard.EventSummaryUpdated,
				backend.EventRefreshFailed,
				backend.EventSubscriberOverflow,
			},
		})
		return subscriptionReadyMsg{generation: generation, subscription: subscription, err: err}
	}
}

func requestRefresh(application Backend, generation uint64) tea.Cmd {
	return func() tea.Msg {
		return refreshRequestedMsg{
			generation: generation,
			err:        application.RequestRefresh(backend.PageDashboard),
		}
	}
}

func waitForEvent(subscription backend.Subscription, generation uint64) tea.Cmd {
	return func() tea.Msg {
		event, open := <-subscription.Events()
		return eventReceivedMsg{generation: generation, event: event, open: open}
	}
}

func (model Model) Update(message tea.Msg) (Model, tea.Cmd) {
	switch message := message.(type) {
	case subscriptionReadyMsg:
		if !model.current(message.generation) {
			if message.subscription != nil {
				_ = message.subscription.Close()
			}
			return model, nil
		}
		if message.err != nil {
			model.loading = false
			model.stale = model.hasData
			model.err = message.err
			return model, nil
		}
		model.subscription = message.subscription
		return model, tea.Batch(
			requestRefresh(model.backend, model.generation),
			model.spinner.Tick,
		)

	case refreshRequestedMsg:
		if !model.current(message.generation) {
			return model, nil
		}
		if message.err != nil {
			model.loading = false
			model.stale = model.hasData
			model.err = message.err
		}
		if model.subscription == nil {
			return model, nil
		}
		return model, waitForEvent(model.subscription, model.generation)

	case eventReceivedMsg:
		if !model.current(message.generation) {
			return model, nil
		}
		if !message.open {
			model.subscription = nil
			model.loading = false
			model.stale = model.hasData
			if model.pageCtx == nil || model.pageCtx.Err() == nil {
				model.err = closedSubscriptionError()
			}
			return model, nil
		}
		return model.handleEvent(message.event)

	default:
		return model.updateSpinner(message)
	}
}

func (model Model) handleEvent(event backend.EventEnvelope) (Model, tea.Cmd) {
	next := waitForEvent(model.subscription, model.generation)
	switch payload := event.Payload.(type) {
	case backenddashboard.SummaryUpdated:
		model.summary = payload
		model.hasData = true
		model.loading = false
		model.stale = false
		model.err = nil
		model.lastUpdated = event.Time
		model.lastReason = event.Reason
		return model, next
	case *backenddashboard.SummaryUpdated:
		if payload != nil {
			model.summary = *payload
			model.hasData = true
			model.loading = false
			model.stale = false
			model.err = nil
			model.lastUpdated = event.Time
			model.lastReason = event.Reason
		}
		return model, next
	case backend.RefreshFailed:
		model.loading = false
		model.stale = model.hasData
		model.err = payload.Err
		return model, next
	case backend.SubscriberOverflow:
		model.loading = true
		model.stale = model.hasData
		model.err = nil
		return model, tea.Batch(
			next,
			requestRefresh(model.backend, model.generation),
			model.spinner.Tick,
		)
	default:
		return model, next
	}
}

func (model Model) current(generation uint64) bool {
	return model.active && generation == model.generation
}
