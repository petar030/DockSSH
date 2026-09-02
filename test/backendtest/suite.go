package backendtest

import (
	"context"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
	systempage "github.com/petar030/ssh-native-docker-tui/internal/backend/system"
)

// RunBackendConformance executes behavior shared by every production backend.
func RunBackendConformance(t *testing.T, factory BackendFactory, env IntegrationEnvironment) {
	t.Helper()
	if factory == nil {
		t.Fatal("backendtest: nil BackendFactory")
	}
	if env.Timeout <= 0 {
		env.Timeout = 30 * time.Second
	}

	t.Run("complete facade exposes every domain API", func(t *testing.T) {
		instance := openBackend(t, factory, env)
		if instance.Containers() == nil || instance.Compose() == nil || instance.Images() == nil ||
			instance.Volumes() == nil || instance.Networks() == nil || instance.System() == nil {
			t.Fatal("production Backend facade contains a nil domain API")
		}
	})

	t.Run("one refresh broadcasts a typed result to two observers", func(t *testing.T) {
		instance := openBackend(t, factory, env)
		key := backend.RefreshKey{Kind: systempage.RefreshKindInfo}
		filter := backend.EventFilter{
			Types: []backend.EventType{systempage.EventInfoUpdated},
			Keys:  []backend.RefreshKey{key},
		}

		first, err := instance.Subscribe(context.Background(), backend.PageSystem, filter)
		if err != nil {
			t.Fatalf("subscribe first observer: %v", err)
		}
		second, err := instance.Subscribe(context.Background(), backend.PageSystem, filter)
		if err != nil {
			t.Fatalf("subscribe second observer: %v", err)
		}
		otherPage, err := instance.Subscribe(context.Background(), backend.PageContainers, filter)
		if err != nil {
			t.Fatalf("subscribe observer on another page: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), env.Timeout)
		defer cancel()
		err = instance.RequestRefresh(backend.PageSystem)
		if err != nil {
			t.Fatalf("refresh backend status: %v", err)
		}
		firstEvent := receiveEvent(t, ctx, first.Events())
		secondEvent := receiveEvent(t, ctx, second.Events())
		for position, event := range []backend.EventEnvelope{firstEvent, secondEvent} {
			status, ok := event.Payload.(systempage.InfoUpdated)
			if !ok {
				t.Fatalf("observer %d payload = %T", position+1, event.Payload)
			}
			if status.Engine.APIVersion == "" {
				t.Fatalf("observer %d received empty Docker API version", position+1)
			}
			if event.Key != key || event.Reason != backend.RefreshManual || event.Sequence == 0 {
				t.Fatalf("observer %d event = %#v", position+1, event)
			}
		}
		select {
		case event := <-otherPage.Events():
			t.Fatalf("System update leaked to Containers page: %#v", event)
		default:
		}

		if err := first.Close(); err != nil {
			t.Fatalf("close first observer: %v", err)
		}
		if err := instance.RequestRefresh(backend.PageSystem); err != nil {
			t.Fatalf("refresh after first observer closed: %v", err)
		}
		if event := receiveEvent(t, ctx, second.Events()); event.Payload.EventType() != systempage.EventInfoUpdated {
			t.Fatalf("remaining observer event = %#v", event)
		}
	})

	t.Run("subscription cancellation closes only that observer", func(t *testing.T) {
		instance := openBackend(t, factory, env)
		subscriptionContext, cancelSubscription := context.WithCancel(context.Background())
		subscription, err := instance.Subscribe(subscriptionContext, backend.PageSystem, backend.EventFilter{})
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		cancelSubscription()

		ctx, cancel := context.WithTimeout(context.Background(), env.Timeout)
		defer cancel()
		select {
		case _, open := <-subscription.Events():
			if open {
				t.Fatal("subscription delivered an event after cancellation")
			}
		case <-ctx.Done():
			t.Fatal("subscription channel remained open after cancellation")
		}

		if err := subscription.Close(); err != nil {
			t.Fatalf("idempotent subscription close: %v", err)
		}
	})

	t.Run("shutdown closes active subscriptions and rejects new work", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), env.Timeout)
		defer cancel()
		instance, err := factory(ctx, env)
		if err != nil {
			t.Fatalf("construct backend: %v", err)
		}
		subscription, err := instance.Subscribe(context.Background(), backend.PageSystem, backend.EventFilter{})
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		if err := instance.Close(ctx); err != nil {
			t.Fatalf("close backend: %v", err)
		}
		select {
		case _, open := <-subscription.Events():
			if open {
				t.Fatal("subscription remained open after backend shutdown")
			}
		case <-ctx.Done():
			t.Fatal("subscription did not close during backend shutdown")
		}
		if err := instance.RequestRefresh(backend.PageSystem); !backend.HasErrorCode(err, backend.ErrorStreamClosed) {
			t.Fatalf("refresh after shutdown = %v", err)
		}
		if _, err := instance.Subscribe(context.Background(), backend.PageSystem, backend.EventFilter{}); !backend.HasErrorCode(err, backend.ErrorStreamClosed) {
			t.Fatalf("subscription after shutdown = %v", err)
		}
	})

	t.Run("Dashboard refresh publishes a complete real-Docker summary", func(t *testing.T) {
		instance := openBackend(t, factory, env)
		subscription, err := instance.Subscribe(context.Background(), backend.PageDashboard, backend.EventFilter{
			Types: []backend.EventType{dashboard.EventSummaryUpdated},
		})
		if err != nil {
			t.Fatalf("subscribe to Dashboard: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), env.Timeout)
		defer cancel()
		if err := instance.RequestRefresh(backend.PageDashboard); err != nil {
			t.Fatalf("refresh Dashboard: %v", err)
		}
		event := receiveEvent(t, ctx, subscription.Events())
		summary, ok := event.Payload.(dashboard.SummaryUpdated)
		if !ok {
			t.Fatalf("Dashboard payload = %T", event.Payload)
		}
		if !summary.Engine.Available || summary.Engine.ServerVersion == "" || summary.Engine.APIVersion == "" {
			t.Fatalf("Dashboard Engine summary = %#v", summary.Engine)
		}
		if event.Reason != backend.RefreshManual {
			t.Fatalf("Dashboard refresh reason = %q", event.Reason)
		}
	})
}

func receiveEvent(t *testing.T, ctx context.Context, events <-chan backend.EventEnvelope) backend.EventEnvelope {
	t.Helper()
	select {
	case event, open := <-events:
		if !open {
			t.Fatal("subscription closed before delivering an event")
		}
		return event
	case <-ctx.Done():
		t.Fatalf("wait for event: %v", ctx.Err())
		return backend.EventEnvelope{}
	}
}

func openBackend(t *testing.T, factory BackendFactory, env IntegrationEnvironment) ConformanceBackend {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), env.Timeout)
	defer cancel()

	instance, err := factory(ctx, env)
	if err != nil {
		t.Fatalf("construct backend: %v", err)
	}
	if instance == nil {
		t.Fatal("factory returned a nil backend without an error")
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), env.Timeout)
		defer closeCancel()
		if err := instance.Close(closeCtx); err != nil {
			t.Errorf("close backend: %v", err)
		}
	})
	return instance
}
