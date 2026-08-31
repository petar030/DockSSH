//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	composepage "github.com/petar030/ssh-native-docker-tui/internal/backend/compose"
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
	if err := application.RequestRefresh(backend.PageContainers); err != nil {
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
	if err := application.Containers().RequestDetails(containerID); err != nil {
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
	if err := application.Containers().RequestProcesses(containerID); err != nil {
		t.Fatalf("refresh processes: %v", err)
	}
	processEvent := waitForEventType(t, ctx, detailsSubscription.Events(), containers.EventProcessesUpdated)
	processes := processEvent.Payload.(containers.ProcessesUpdated)
	if processes.ContainerID != containerID || len(processes.Titles) == 0 || len(processes.Rows) == 0 {
		t.Fatalf("container processes = %#v", processes)
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

func TestComposeSliceAgainstDocker(t *testing.T) {
	fixture := dockerfixture.New(t)
	environment := fixture.Environment()
	projectName, configPath, err := fixture.CreateComposeConfig("slice-three", []string{
		"sh", "-c", "while true; do echo slice3-log; sleep 1; done",
	})
	if err != nil {
		t.Fatalf("create Compose fixture: %v", err)
	}
	fixture.TrackComposeProject(projectName)

	ctx, cancel := context.WithTimeout(context.Background(), 3*environment.Timeout)
	defer cancel()
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{
		Endpoint: environment.DockerEndpoint, ComposeRoots: []string{environment.ComposeRoot},
	})
	if err != nil {
		t.Fatalf("construct production backend: %v", err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })

	listSubscription, err := application.Subscribe(ctx, backend.PageCompose, backend.EventFilter{
		Types: []backend.EventType{composepage.EventProjectsUpdated},
	})
	if err != nil {
		t.Fatalf("subscribe to Compose list: %v", err)
	}
	detailsSubscription, err := application.Subscribe(ctx, backend.PageCompose, backend.EventFilter{
		Types: []backend.EventType{composepage.EventProjectUpdated},
	})
	if err != nil {
		t.Fatalf("subscribe to Compose details: %v", err)
	}

	spec := composepage.ProjectSpec{Name: projectName, ConfigFiles: []string{configPath}}
	upJob, err := application.Compose().Up(ctx, spec, composepage.UpOptions{})
	if err != nil {
		t.Fatalf("start Compose up: %v", err)
	}
	if _, err := upJob.Wait(ctx); err != nil {
		t.Fatalf("wait for Compose up: %v", err)
	}
	waitForComposeProject(t, ctx, listSubscription.Events(), projectName, true)

	if err := application.Compose().RequestDetails(projectName); err != nil {
		t.Fatalf("request Compose details: %v", err)
	}
	detailsEvent := waitForEventType(t, ctx, detailsSubscription.Events(), composepage.EventProjectUpdated)
	details := detailsEvent.Payload.(composepage.ProjectUpdated).Project
	if details.Name != projectName || len(details.Services) != 1 || details.Services[0].Name != "web" || len(details.Containers) != 1 {
		t.Fatalf("Compose details = %#v", details)
	}

	logsCtx, cancelLogs := context.WithTimeout(ctx, 5*time.Second)
	logs, err := application.Compose().Logs(logsCtx, projectName, composepage.LogsOptions{Follow: true, Tail: 10})
	if err != nil {
		cancelLogs()
		t.Fatalf("open Compose logs: %v", err)
	}
	select {
	case entry := <-logs.Values():
		if !strings.Contains(entry.Data, "slice3-log") {
			t.Fatalf("Compose log entry = %#v", entry)
		}
	case <-logsCtx.Done():
		t.Fatalf("wait for Compose logs: %v", logsCtx.Err())
	}
	_ = logs.Close()
	cancelLogs()
	waitForStreamDone(t, ctx, logs.Done())

	result, commandErr := application.Compose().Pause(ctx, projectName, composepage.ServiceOptions{})
	assertComposeCommand(t, result, commandErr)
	result, commandErr = application.Compose().Unpause(ctx, projectName, composepage.ServiceOptions{})
	assertComposeCommand(t, result, commandErr)
	result, commandErr = application.Compose().Restart(ctx, projectName, composepage.RestartOptions{})
	assertComposeCommand(t, result, commandErr)
	result, commandErr = application.Compose().Stop(ctx, projectName, composepage.StopOptions{})
	assertComposeCommand(t, result, commandErr)
	result, commandErr = application.Compose().Start(ctx, projectName, composepage.ServiceOptions{})
	assertComposeCommand(t, result, commandErr)
	result, commandErr = application.Compose().Scale(ctx, spec, composepage.ScaleOptions{Service: "web", Replicas: 1})
	assertComposeCommand(t, result, commandErr)

	downJob, err := application.Compose().Down(ctx, projectName, composepage.DownOptions{RemoveOrphans: true, Volumes: true})
	if err != nil {
		t.Fatalf("start Compose down: %v", err)
	}
	if _, err := downJob.Wait(ctx); err != nil {
		t.Fatalf("wait for Compose down: %v", err)
	}
	waitForComposeProject(t, ctx, listSubscription.Events(), projectName, false)
}

func assertComposeCommand(t *testing.T, result backend.CommandResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Compose command: %v", err)
	}
	if !strings.HasPrefix(result.OperationID, "compose.") || len(result.Affected) != 1 {
		t.Fatalf("Compose command result = %#v", result)
	}
}

func waitForComposeProject(
	t *testing.T,
	ctx context.Context,
	events <-chan backend.EventEnvelope,
	projectName string,
	present bool,
) composepage.ProjectsUpdated {
	t.Helper()
	for {
		event := receiveIntegrationEvent(t, ctx, events)
		update, ok := event.Payload.(composepage.ProjectsUpdated)
		if !ok {
			continue
		}
		found := false
		for _, project := range update.Projects {
			found = found || project.Name == projectName
		}
		if found == present {
			return update
		}
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
		Endpoint: environment.DockerEndpoint, ComposeRoots: []string{environment.ComposeRoot},
	})
}
