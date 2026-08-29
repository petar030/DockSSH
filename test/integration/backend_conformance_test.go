//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
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

func TestContainersSliceAgainstDocker(t *testing.T) {
	fixture := dockerfixture.New(t)
	environment := fixture.Environment()
	ctx, cancel := context.WithTimeout(context.Background(), environment.Timeout)
	defer cancel()
	containerID, err := fixture.CreateContainer(ctx, "slice-two", []string{
		"sh", "-c", "while true; do echo slice2-log; sleep 1; done",
	})
	if err != nil {
		t.Fatalf("arrange container (set BACKEND_TEST_CONTAINER_IMAGE to an existing Linux image with sh if needed): %v", err)
	}
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{Endpoint: environment.DockerEndpoint})
	if err != nil {
		t.Fatalf("construct production backend: %v", err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })

	listSubscription, err := application.Subscribe(ctx, backend.PageContainers, backend.EventFilter{
		Types: []backend.EventType{containers.EventListUpdated},
	})
	if err != nil {
		t.Fatalf("subscribe to Containers: %v", err)
	}
	if err := application.Refresh(ctx, backend.PageContainers); err != nil {
		t.Fatalf("refresh Containers: %v", err)
	}
	initial := waitForContainerList(t, ctx, listSubscription.Events(), backend.RefreshManual, containerID)
	if findContainer(initial.Containers, containerID).State != "created" {
		t.Fatalf("initial container = %#v", findContainer(initial.Containers, containerID))
	}

	detailsSubscription, err := application.Subscribe(ctx, backend.PageContainers, backend.EventFilter{
		Types: []backend.EventType{containers.EventDetailsUpdated, containers.EventProcessesUpdated},
	})
	if err != nil {
		t.Fatalf("subscribe to Container details: %v", err)
	}
	if err := application.Containers().RefreshDetails(ctx, containerID); err != nil {
		t.Fatalf("refresh details: %v", err)
	}
	detailsEvent := receiveIntegrationEvent(t, ctx, detailsSubscription.Events())
	details := detailsEvent.Payload.(containers.DetailsUpdated).Container
	if details.ID != containerID || details.Name == "" || details.State.Status != "created" {
		t.Fatalf("container details = %#v", details)
	}

	result, err := application.Containers().Start(ctx, containerID)
	assertContainerCommand(t, "start", result, err)
	running := waitForContainerList(t, ctx, listSubscription.Events(), "", containerID)
	if findContainer(running.Containers, containerID).State != "running" {
		t.Fatalf("container did not become running: %#v", findContainer(running.Containers, containerID))
	}
	if err := application.Containers().RefreshProcesses(ctx, containerID); err != nil {
		t.Fatalf("refresh processes: %v", err)
	}
	processEvent := waitForEventType(t, ctx, detailsSubscription.Events(), containers.EventProcessesUpdated)
	processes := processEvent.Payload.(containers.ProcessesUpdated)
	if processes.ContainerID != containerID || len(processes.Titles) == 0 || len(processes.Rows) == 0 {
		t.Fatalf("container processes = %#v", processes)
	}

	execResult, err := application.Containers().Exec(ctx, containerID, containers.ExecOptions{
		Command: []string{"sh", "-c", "printf exec-out; printf exec-err >&2; exit 7"},
	})
	if err != nil {
		t.Fatalf("one-shot exec: %v", err)
	}
	if execResult.ExitCode != 7 || execResult.Stdout != "exec-out" || execResult.Stderr != "exec-err" {
		t.Fatalf("exec result = %#v", execResult)
	}

	logsContext, cancelLogs := context.WithTimeout(ctx, 5*time.Second)
	logStream, err := application.Containers().Logs(logsContext, containerID, containers.LogsOptions{Follow: true, Tail: 10})
	if err != nil {
		cancelLogs()
		t.Fatalf("open logs: %v", err)
	}
	select {
	case entry := <-logStream.Values():
		if !strings.Contains(entry.Data, "slice2-log") {
			t.Fatalf("log entry = %#v", entry)
		}
	case <-logsContext.Done():
		t.Fatalf("wait for logs: %v", logsContext.Err())
	}
	_ = logStream.Close()
	cancelLogs()
	waitForStreamDone(t, ctx, logStream.Done())

	statsStream, err := application.Containers().Stats(ctx, containerID, containers.StatsOptions{OneShot: true})
	if err != nil {
		t.Fatalf("open stats: %v", err)
	}
	select {
	case sample := <-statsStream.Values():
		if sample.ContainerID != containerID || sample.Name == "" || sample.MemoryLimit == 0 {
			t.Fatalf("stats sample = %#v", sample)
		}
	case <-ctx.Done():
		t.Fatalf("wait for stats: %v", ctx.Err())
	}
	waitForStreamDone(t, ctx, statsStream.Done())

	result, err = application.Containers().Pause(ctx, containerID)
	assertContainerCommand(t, "pause", result, err)
	result, err = application.Containers().Unpause(ctx, containerID)
	assertContainerCommand(t, "unpause", result, err)
	shortTimeout := 1
	result, err = application.Containers().Restart(ctx, containerID, containers.RestartOptions{TimeoutSeconds: &shortTimeout})
	assertContainerCommand(t, "restart", result, err)
	result, err = application.Containers().Stop(ctx, containerID, containers.StopOptions{TimeoutSeconds: &shortTimeout})
	assertContainerCommand(t, "stop", result, err)
	result, err = application.Containers().Rename(ctx, containerID, containers.RenameOptions{Name: fixture.Name("renamed")})
	assertContainerCommand(t, "rename", result, err)
	result, err = application.Containers().Start(ctx, containerID)
	assertContainerCommand(t, "start", result, err)
	result, err = application.Containers().Kill(ctx, containerID, containers.KillOptions{Signal: "SIGKILL"})
	assertContainerCommand(t, "kill", result, err)
	result, err = application.Containers().Remove(ctx, containerID, containers.RemoveOptions{Force: true, RemoveVolumes: true})
	assertContainerCommand(t, "remove", result, err)
}

func TestDockerContainerEventRefreshesContainersPage(t *testing.T) {
	fixture := dockerfixture.New(t)
	environment := fixture.Environment()
	application, err := dockerplatform.NewBackend(context.Background(), dockerplatform.BackendConfig{Endpoint: environment.DockerEndpoint})
	if err != nil {
		t.Fatalf("construct production backend: %v", err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), environment.Timeout)
	defer cancel()
	subscription, err := application.Subscribe(ctx, backend.PageContainers, backend.EventFilter{
		Types: []backend.EventType{containers.EventListUpdated},
	})
	if err != nil {
		t.Fatalf("subscribe to Containers: %v", err)
	}
	containerID, err := fixture.CreateContainer(ctx, "event-refresh", []string{"sh", "-c", "sleep 30"})
	if err != nil {
		t.Fatalf("create event test container: %v", err)
	}
	update := waitForContainerList(t, ctx, subscription.Events(), backend.RefreshDockerEvent, containerID)
	if findContainer(update.Containers, containerID).ID != containerID {
		t.Fatalf("event-triggered list does not contain %q", containerID)
	}
}

func assertContainerCommand(t *testing.T, operation string, result backend.CommandResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s container: %v", operation, err)
	}
	if result.OperationID != "container."+operation || len(result.Affected) != 1 {
		t.Fatalf("%s result = %#v", operation, result)
	}
}

func waitForContainerList(
	t *testing.T,
	ctx context.Context,
	events <-chan backend.EventEnvelope,
	reason backend.RefreshReason,
	containerID string,
) containers.ListUpdated {
	t.Helper()
	for {
		event := receiveIntegrationEvent(t, ctx, events)
		if reason != "" && event.Reason != reason {
			continue
		}
		update, ok := event.Payload.(containers.ListUpdated)
		if ok && findContainer(update.Containers, containerID).ID == containerID {
			return update
		}
	}
}

func waitForEventType(t *testing.T, ctx context.Context, events <-chan backend.EventEnvelope, eventType backend.EventType) backend.EventEnvelope {
	t.Helper()
	for {
		event := receiveIntegrationEvent(t, ctx, events)
		if event.Payload.EventType() == eventType {
			return event
		}
	}
}

func findContainer(values []containers.Summary, id string) containers.Summary {
	for _, value := range values {
		if value.ID == id {
			return value
		}
	}
	return containers.Summary{}
}

func waitForStreamDone(t *testing.T, ctx context.Context, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stream completed with error: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("wait for stream completion: %v", ctx.Err())
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
