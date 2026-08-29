//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
	dockerplatform "github.com/petar030/ssh-native-docker-tui/internal/platform/docker"
	"github.com/petar030/ssh-native-docker-tui/test/backendtest"
	"github.com/petar030/ssh-native-docker-tui/test/dockerfixture"
)

func TestProductionBackendConformance(t *testing.T) {
	fixture := dockerfixture.New(t)
	backendtest.RunBackendConformance(t, productionBackendFactory, fixture.Environment())
}

func TestProductionBootstrapSharesOneMobyClient(t *testing.T) {
	fixture := dockerfixture.New(t)
	application, err := dockerplatform.NewBackend(context.Background(), dockerplatform.BackendConfig{
		Endpoint: fixture.Environment().DockerEndpoint,
	})
	if err != nil {
		t.Fatalf("construct production backend: %v", err)
	}
	t.Cleanup(func() {
		if err := application.Close(context.Background()); err != nil {
			t.Errorf("close production backend: %v", err)
		}
	})
	if !application.UsesSharedMobyClient() {
		t.Fatal("Docker CLI does not use the application-owned Moby client")
	}
}

func TestDockerEventRefreshesDashboard(t *testing.T) {
	fixture := dockerfixture.New(t)
	environment := fixture.Environment()
	application, err := dockerplatform.NewBackend(context.Background(), dockerplatform.BackendConfig{
		Endpoint: environment.DockerEndpoint,
	})
	if err != nil {
		t.Fatalf("construct production backend: %v", err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), environment.Timeout)
	defer cancel()
	eventsSubscription, err := application.Subscribe(ctx, backend.PageEvents, backend.EventFilter{
		Types: []backend.EventType{backend.EventDockerObserved},
	})
	if err != nil {
		t.Fatalf("subscribe to Events: %v", err)
	}
	dashboardSubscription, err := application.Subscribe(ctx, backend.PageDashboard, backend.EventFilter{
		Types: []backend.EventType{dashboard.EventSummaryUpdated},
	})
	if err != nil {
		t.Fatalf("subscribe to Dashboard: %v", err)
	}

	volumeName, err := fixture.CreateVolume(ctx, "event-refresh")
	if err != nil {
		t.Fatalf("create event test volume: %v", err)
	}
	observed := waitForDockerEvent(t, ctx, eventsSubscription.Events(), "volume", volumeName, "create")
	if observed.Project != "" || observed.OccurredAt.IsZero() {
		t.Fatalf("normalized volume event = %#v", observed)
	}

	for {
		event := receiveIntegrationEvent(t, ctx, dashboardSubscription.Events())
		if event.Reason != backend.RefreshDockerEvent {
			continue
		}
		summary, ok := event.Payload.(dashboard.SummaryUpdated)
		if !ok {
			t.Fatalf("event-triggered Dashboard payload = %T", event.Payload)
		}
		if summary.Resources.Volumes < 1 {
			t.Fatalf("event-triggered volume count = %d", summary.Resources.Volumes)
		}
		break
	}
}

func waitForDockerEvent(
	t *testing.T,
	ctx context.Context,
	events <-chan backend.EventEnvelope,
	resource, id, action string,
) backend.DockerEventObserved {
	t.Helper()
	for {
		event := receiveIntegrationEvent(t, ctx, events)
		observed, ok := event.Payload.(backend.DockerEventObserved)
		if ok && observed.Resource == resource && observed.ResourceID == id && observed.Action == action {
			return observed
		}
	}
}

func receiveIntegrationEvent(t *testing.T, ctx context.Context, events <-chan backend.EventEnvelope) backend.EventEnvelope {
	t.Helper()
	select {
	case event, open := <-events:
		if !open {
			t.Fatal("event subscription closed")
		}
		return event
	case <-ctx.Done():
		t.Fatalf("wait for integration event: %v", ctx.Err())
		return backend.EventEnvelope{}
	}
}

func productionBackendFactory(
	ctx context.Context,
	environment backendtest.IntegrationEnvironment,
) (backend.Backend, error) {
	return dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{
		Endpoint: environment.DockerEndpoint,
	})
}
