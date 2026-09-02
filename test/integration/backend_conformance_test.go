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
	eventpage "github.com/petar030/ssh-native-docker-tui/internal/backend/events"
	imagepage "github.com/petar030/ssh-native-docker-tui/internal/backend/images"
	networkpage "github.com/petar030/ssh-native-docker-tui/internal/backend/networks"
	systempage "github.com/petar030/ssh-native-docker-tui/internal/backend/system"
	volumepage "github.com/petar030/ssh-native-docker-tui/internal/backend/volumes"
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

func TestImagesSliceAgainstDocker(t *testing.T) {
	fixture := dockerfixture.New(t)
	environment := fixture.Environment()
	ctx, cancel := context.WithTimeout(context.Background(), environment.Timeout)
	defer cancel()
	sourceReference, err := fixture.CreateImageTag(ctx, "slice-four-source")
	if err != nil {
		t.Fatalf("arrange image tag: %v", err)
	}
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{Endpoint: environment.DockerEndpoint})
	if err != nil {
		t.Fatalf("construct production backend: %v", err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })

	listSubscription, err := application.Subscribe(ctx, backend.PageImages, backend.EventFilter{Types: []backend.EventType{imagepage.EventListUpdated}})
	if err != nil {
		t.Fatalf("subscribe to Images list: %v", err)
	}
	detailsSubscription, err := application.Subscribe(ctx, backend.PageImages, backend.EventFilter{
		Types: []backend.EventType{imagepage.EventDetailsUpdated, imagepage.EventHistoryUpdated},
	})
	if err != nil {
		t.Fatalf("subscribe to Image details: %v", err)
	}
	dashboardSubscription, err := application.Subscribe(ctx, backend.PageDashboard, backend.EventFilter{Types: []backend.EventType{dashboard.EventSummaryUpdated}})
	if err != nil {
		t.Fatalf("subscribe to Dashboard: %v", err)
	}

	if err := application.RequestRefresh(backend.PageImages); err != nil {
		t.Fatalf("refresh Images: %v", err)
	}
	initial := waitForImageTag(t, ctx, listSubscription.Events(), sourceReference, true)
	image := findImageByTag(initial.Images, sourceReference)
	if image.ID == "" {
		t.Fatalf("source image not found: %#v", initial)
	}
	eventReference, err := fixture.CreateImageTag(ctx, "slice-four-event")
	if err != nil {
		t.Fatalf("create event image tag: %v", err)
	}
	for {
		event := receiveIntegrationEvent(t, ctx, listSubscription.Events())
		update, ok := event.Payload.(imagepage.ListUpdated)
		if ok && event.Reason == backend.RefreshDockerEvent && findImageByTag(update.Images, eventReference).ID != "" {
			break
		}
	}
	if err := application.Images().RequestDetails(image.ID); err != nil {
		t.Fatalf("request image details: %v", err)
	}
	detailsEvent := waitForEventType(t, ctx, detailsSubscription.Events(), imagepage.EventDetailsUpdated)
	details := detailsEvent.Payload.(imagepage.DetailsUpdated).Image
	if details.ID != image.ID || details.OS == "" || details.Architecture == "" {
		t.Fatalf("image details = %#v", details)
	}
	if err := application.Images().RequestHistory(image.ID); err != nil {
		t.Fatalf("request image history: %v", err)
	}
	historyEvent := waitForEventType(t, ctx, detailsSubscription.Events(), imagepage.EventHistoryUpdated)
	history := historyEvent.Payload.(imagepage.HistoryUpdated)
	if history.ImageID != image.ID || len(history.Entries) == 0 {
		t.Fatalf("image history = %#v", history)
	}

	targetReference := fixture.Name("slice-four-copy") + ":test"
	fixture.TrackImage(targetReference)
	result, err := application.Images().Tag(ctx, image.ID, imagepage.TagOptions{Reference: targetReference})
	if err != nil || result.OperationID != "image.tag" {
		t.Fatalf("tag image: result=%#v err=%v", result, err)
	}
	waitForImageTag(t, ctx, listSubscription.Events(), targetReference, true)
	waitForEventType(t, ctx, dashboardSubscription.Events(), dashboard.EventSummaryUpdated)

	result, err = application.Images().Remove(ctx, targetReference, imagepage.RemoveOptions{})
	if err != nil || result.OperationID != "image.remove" {
		t.Fatalf("remove image tag: result=%#v err=%v", result, err)
	}
	waitForImageTag(t, ctx, listSubscription.Events(), targetReference, false)
}

func TestVolumesSliceAgainstDocker(t *testing.T) {
	fixture := dockerfixture.New(t)
	environment := fixture.Environment()
	ctx, cancel := context.WithTimeout(context.Background(), environment.Timeout)
	defer cancel()
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{Endpoint: environment.DockerEndpoint})
	if err != nil {
		t.Fatalf("construct production backend: %v", err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })

	listSubscription, err := application.Subscribe(ctx, backend.PageVolumes, backend.EventFilter{Types: []backend.EventType{volumepage.EventListUpdated}})
	if err != nil {
		t.Fatalf("subscribe to Volumes list: %v", err)
	}
	detailsSubscription, err := application.Subscribe(ctx, backend.PageVolumes, backend.EventFilter{
		Types: []backend.EventType{volumepage.EventDetailsUpdated, volumepage.EventAttachmentsUpdated},
	})
	if err != nil {
		t.Fatalf("subscribe to Volume details: %v", err)
	}
	dashboardSubscription, err := application.Subscribe(ctx, backend.PageDashboard, backend.EventFilter{Types: []backend.EventType{dashboard.EventSummaryUpdated}})
	if err != nil {
		t.Fatalf("subscribe to Dashboard: %v", err)
	}
	eventVolume, err := fixture.CreateVolume(ctx, "slice-five-event")
	if err != nil {
		t.Fatalf("create event volume: %v", err)
	}
	for {
		event := receiveIntegrationEvent(t, ctx, listSubscription.Events())
		update, ok := event.Payload.(volumepage.ListUpdated)
		if ok && event.Reason == backend.RefreshDockerEvent && findVolume(update.Volumes, eventVolume).Name != "" {
			break
		}
	}
	waitForEventType(t, ctx, dashboardSubscription.Events(), dashboard.EventSummaryUpdated)

	volumeName := fixture.Name("slice-five")
	fixture.TrackVolume(volumeName)
	result, err := application.Volumes().Create(ctx, volumepage.CreateOptions{Name: volumeName, Labels: fixture.Labels(nil)})
	if err != nil || result.OperationID != "volume.create" {
		t.Fatalf("create volume: result=%#v err=%v", result, err)
	}
	waitForVolume(t, ctx, listSubscription.Events(), volumeName, true)
	waitForEventType(t, ctx, dashboardSubscription.Events(), dashboard.EventSummaryUpdated)

	if err := application.Volumes().RequestDetails(volumeName); err != nil {
		t.Fatalf("request volume details: %v", err)
	}
	detailsEvent := waitForEventType(t, ctx, detailsSubscription.Events(), volumepage.EventDetailsUpdated)
	details := detailsEvent.Payload.(volumepage.DetailsUpdated).Volume
	if details.Name != volumeName || details.Driver == "" || details.Scope == "" {
		t.Fatalf("volume details = %#v", details)
	}

	containerID, err := fixture.CreateContainerWithVolume(ctx, "slice-five-attached", []string{"sh", "-c", "sleep 30"}, volumeName, "/data")
	if err != nil {
		t.Fatalf("create attached container: %v", err)
	}
	if err := application.Volumes().RequestAttachments(volumeName); err != nil {
		t.Fatalf("request volume attachments: %v", err)
	}
	attachmentsEvent := waitForEventType(t, ctx, detailsSubscription.Events(), volumepage.EventAttachmentsUpdated)
	attachments := attachmentsEvent.Payload.(volumepage.AttachmentsUpdated)
	if len(attachments.Attachments) != 1 || attachments.Attachments[0].ContainerID != containerID || attachments.Attachments[0].Destination != "/data" {
		t.Fatalf("volume attachments = %#v", attachments)
	}
	if _, err := application.Volumes().Remove(ctx, volumeName, volumepage.RemoveOptions{}); !backend.HasErrorCode(err, backend.ErrorConflict) {
		t.Fatalf("remove in-use volume error = %v", err)
	}
	if _, err := application.Containers().Remove(ctx, containerID, containers.RemoveOptions{Force: true}); err != nil {
		t.Fatalf("remove attached container: %v", err)
	}
	result, err = application.Volumes().Remove(ctx, volumeName, volumepage.RemoveOptions{})
	if err != nil || result.OperationID != "volume.remove" {
		t.Fatalf("remove volume: result=%#v err=%v", result, err)
	}
	waitForVolume(t, ctx, listSubscription.Events(), volumeName, false)

	pruneTarget := fixture.Name("slice-five-prune")
	sentinel := fixture.Name("slice-five-sentinel")
	fixture.TrackVolume(pruneTarget)
	fixture.TrackVolume(sentinel)
	pruneLabel := "ssh-docker-tui.integration-prune"
	if _, err := application.Volumes().Create(ctx, volumepage.CreateOptions{
		Name: pruneTarget, Labels: fixture.Labels(map[string]string{pruneLabel: "target"}),
	}); err != nil {
		t.Fatalf("create prune target: %v", err)
	}
	if _, err := application.Volumes().Create(ctx, volumepage.CreateOptions{
		Name: sentinel, Labels: fixture.Labels(map[string]string{pruneLabel: "sentinel"}),
	}); err != nil {
		t.Fatalf("create prune sentinel: %v", err)
	}
	waitForVolume(t, ctx, listSubscription.Events(), sentinel, true)
	if _, err := application.Volumes().Prune(ctx, volumepage.PruneOptions{All: true, Labels: map[string]string{pruneLabel: "target"}}); err != nil {
		t.Fatalf("prune labeled volume: %v", err)
	}
	update := waitForVolume(t, ctx, listSubscription.Events(), pruneTarget, false)
	if findVolume(update.Volumes, sentinel).Name != sentinel {
		t.Fatalf("filtered prune removed sentinel: %#v", update)
	}
}

func TestNetworksSliceAgainstDocker(t *testing.T) {
	fixture := dockerfixture.New(t)
	environment := fixture.Environment()
	ctx, cancel := context.WithTimeout(context.Background(), environment.Timeout)
	defer cancel()
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{Endpoint: environment.DockerEndpoint})
	if err != nil {
		t.Fatalf("construct production backend: %v", err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })

	listSubscription, err := application.Subscribe(ctx, backend.PageNetworks, backend.EventFilter{Types: []backend.EventType{networkpage.EventListUpdated}})
	if err != nil {
		t.Fatalf("subscribe to Networks list: %v", err)
	}
	detailsSubscription, err := application.Subscribe(ctx, backend.PageNetworks, backend.EventFilter{
		Types: []backend.EventType{networkpage.EventDetailsUpdated, networkpage.EventConnectionsUpdated},
	})
	if err != nil {
		t.Fatalf("subscribe to Network details: %v", err)
	}
	dashboardSubscription, err := application.Subscribe(ctx, backend.PageDashboard, backend.EventFilter{Types: []backend.EventType{dashboard.EventSummaryUpdated}})
	if err != nil {
		t.Fatalf("subscribe to Dashboard: %v", err)
	}

	networkName := fixture.Name("slice-six")
	fixture.TrackNetwork(networkName)
	result, err := application.Networks().Create(ctx, networkpage.CreateOptions{
		Name: networkName, Driver: "bridge", Labels: fixture.Labels(nil),
		IPAM: []networkpage.CreateIPAMConfig{{Subnet: "172.30.240.0/24", Gateway: "172.30.240.1"}},
	})
	if err != nil || result.OperationID != "network.create" {
		t.Fatalf("create network: result=%#v err=%v", result, err)
	}
	created := waitForNetwork(t, ctx, listSubscription.Events(), networkName, true)
	networkID := findNetwork(created.Networks, networkName).ID
	if networkID == "" {
		t.Fatalf("created network missing ID: %#v", created)
	}
	waitForEventType(t, ctx, dashboardSubscription.Events(), dashboard.EventSummaryUpdated)

	if err := application.Networks().RequestDetails(networkID); err != nil {
		t.Fatalf("request network details: %v", err)
	}
	detailsEvent := waitForEventType(t, ctx, detailsSubscription.Events(), networkpage.EventDetailsUpdated)
	details := detailsEvent.Payload.(networkpage.DetailsUpdated).Network
	if details.ID != networkID || details.Driver != "bridge" || len(details.IPAM) != 1 || details.IPAM[0].Subnet != "172.30.240.0/24" {
		t.Fatalf("network details = %#v", details)
	}

	containerID, err := fixture.CreateContainer(ctx, "slice-six-attached", []string{"sh", "-c", "sleep 30"})
	if err != nil {
		t.Fatalf("create network test container: %v", err)
	}
	if _, err := application.Containers().Start(ctx, containerID); err != nil {
		t.Fatalf("start network test container: %v", err)
	}
	if _, err := application.Networks().Connect(ctx, networkID, networkpage.ConnectOptions{
		ContainerID: containerID, IPv4Address: "172.30.240.8", Aliases: []string{"slice-six-alias"},
	}); err != nil {
		t.Fatalf("connect container: %v", err)
	}
	if err := application.Networks().RequestConnections(networkID); err != nil {
		t.Fatalf("request network connections: %v", err)
	}
	connectionsEvent := waitForEventType(t, ctx, detailsSubscription.Events(), networkpage.EventConnectionsUpdated)
	connections := connectionsEvent.Payload.(networkpage.ConnectionsUpdated)
	connection := findNetworkConnection(connections.Connections, containerID)
	if connection.ContainerID != containerID || connection.IPv4Address != "172.30.240.8/24" || connection.EndpointID == "" {
		t.Fatalf("network connection = %#v", connection)
	}
	if _, err := application.Networks().Remove(ctx, networkID, networkpage.RemoveOptions{}); !backend.HasErrorCode(err, backend.ErrorConflict) {
		t.Fatalf("remove in-use network error = %v", err)
	}
	if _, err := application.Networks().Disconnect(ctx, networkID, networkpage.DisconnectOptions{ContainerID: containerID}); err != nil {
		t.Fatalf("disconnect container: %v", err)
	}
	if _, err := application.Networks().Remove(ctx, networkID, networkpage.RemoveOptions{}); err != nil {
		t.Fatalf("remove network: %v", err)
	}
	waitForNetwork(t, ctx, listSubscription.Events(), networkName, false)

	pruneLabel := "ssh-docker-tui.integration-network-prune"
	pruneTarget := fixture.Name("slice-six-prune")
	sentinel := fixture.Name("slice-six-sentinel")
	fixture.TrackNetwork(pruneTarget)
	fixture.TrackNetwork(sentinel)
	if _, err := application.Networks().Create(ctx, networkpage.CreateOptions{
		Name: pruneTarget, Labels: fixture.Labels(map[string]string{pruneLabel: "target"}),
	}); err != nil {
		t.Fatalf("create network prune target: %v", err)
	}
	if _, err := application.Networks().Create(ctx, networkpage.CreateOptions{
		Name: sentinel, Labels: fixture.Labels(map[string]string{pruneLabel: "sentinel"}),
	}); err != nil {
		t.Fatalf("create network prune sentinel: %v", err)
	}
	waitForNetwork(t, ctx, listSubscription.Events(), sentinel, true)
	if _, err := application.Networks().Prune(ctx, networkpage.PruneOptions{Labels: map[string]string{pruneLabel: "target"}}); err != nil {
		t.Fatalf("prune labeled network: %v", err)
	}
	update := waitForNetwork(t, ctx, listSubscription.Events(), pruneTarget, false)
	if findNetwork(update.Networks, sentinel).Name != sentinel {
		t.Fatalf("filtered network prune removed sentinel: %#v", update)
	}
}

func TestEventsSliceAgainstDocker(t *testing.T) {
	fixture := dockerfixture.New(t)
	environment := fixture.Environment()
	ctx, cancel := context.WithTimeout(context.Background(), environment.Timeout)
	defer cancel()
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{Endpoint: environment.DockerEndpoint})
	if err != nil {
		t.Fatalf("construct production backend: %v", err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })

	targetName := fixture.Name("slice-seven-target")
	live, err := application.Subscribe(ctx, backend.PageEvents, backend.EventFilter{
		Types: []backend.EventType{backend.EventDockerObserved}, DockerResources: []string{"volume"},
		DockerResourceIDs: []string{targetName}, DockerActions: []string{"create"},
	})
	if err != nil {
		t.Fatalf("subscribe to filtered live Events: %v", err)
	}
	recent, err := application.Subscribe(ctx, backend.PageEvents, backend.EventFilter{Types: []backend.EventType{eventpage.EventRecentUpdated}})
	if err != nil {
		t.Fatalf("subscribe to recent Events: %v", err)
	}
	if _, err := fixture.CreateVolume(ctx, "slice-seven-other"); err != nil {
		t.Fatalf("create non-matching event volume: %v", err)
	}
	createdName, err := fixture.CreateVolume(ctx, "slice-seven-target")
	if err != nil {
		t.Fatalf("create matching event volume: %v", err)
	}
	observed := waitForDockerEvent(t, ctx, live.Events(), "volume", createdName, "create")
	if observed.ResourceID != targetName || observed.OccurredAt.IsZero() {
		t.Fatalf("filtered live observation = %#v", observed)
	}

	if err := application.RequestRefresh(backend.PageEvents); err != nil {
		t.Fatalf("request recent Events: %v", err)
	}
	recentEvent := waitForEventType(t, ctx, recent.Events(), eventpage.EventRecentUpdated)
	window := recentEvent.Payload.(eventpage.RecentUpdated)
	found := false
	for _, value := range window.Events {
		found = found || (value.Resource == "volume" && value.ResourceID == targetName && value.Action == "create")
	}
	if !found {
		t.Fatalf("recent Events window does not contain matching event: %#v", window)
	}
}

func TestSystemSliceAgainstDocker(t *testing.T) {
	fixture := dockerfixture.New(t)
	environment := fixture.Environment()
	ctx, cancel := context.WithTimeout(context.Background(), environment.Timeout)
	defer cancel()
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{Endpoint: environment.DockerEndpoint})
	if err != nil {
		t.Fatalf("construct production backend: %v", err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })

	systemSubscription, err := application.Subscribe(ctx, backend.PageSystem, backend.EventFilter{
		Types: []backend.EventType{systempage.EventInfoUpdated, systempage.EventDiskUsageUpdated},
	})
	if err != nil {
		t.Fatalf("subscribe to System: %v", err)
	}
	if err := application.RequestRefresh(backend.PageSystem); err != nil {
		t.Fatalf("request System info: %v", err)
	}
	infoEvent := waitForEventType(t, ctx, systemSubscription.Events(), systempage.EventInfoUpdated)
	info := infoEvent.Payload.(systempage.InfoUpdated)
	if info.Engine.Version == "" || info.Engine.APIVersion == "" || info.Host.Name == "" || info.Host.OperatingSystem == "" {
		t.Fatalf("System info = %#v", info)
	}
	if err := application.System().RequestDiskUsage(); err != nil {
		t.Fatalf("request System disk usage: %v", err)
	}
	diskEvent := waitForEventType(t, ctx, systemSubscription.Events(), systempage.EventDiskUsageUpdated)
	disk := diskEvent.Payload.(systempage.DiskUsageUpdated)
	if disk.Images.Count < 0 || disk.Containers.Count < 0 || disk.Volumes.Count < 0 || disk.BuildCache.Count < 0 {
		t.Fatalf("System disk usage = %#v", disk)
	}

	containerSubscription, err := application.Subscribe(ctx, backend.PageContainers, backend.EventFilter{Types: []backend.EventType{containers.EventListUpdated}})
	if err != nil {
		t.Fatalf("subscribe to Containers: %v", err)
	}
	volumeSubscription, err := application.Subscribe(ctx, backend.PageVolumes, backend.EventFilter{Types: []backend.EventType{volumepage.EventListUpdated}})
	if err != nil {
		t.Fatalf("subscribe to Volumes: %v", err)
	}
	networkSubscription, err := application.Subscribe(ctx, backend.PageNetworks, backend.EventFilter{Types: []backend.EventType{networkpage.EventListUpdated}})
	if err != nil {
		t.Fatalf("subscribe to Networks: %v", err)
	}
	dashboardSubscription, err := application.Subscribe(ctx, backend.PageDashboard, backend.EventFilter{Types: []backend.EventType{dashboard.EventSummaryUpdated}})
	if err != nil {
		t.Fatalf("subscribe to Dashboard: %v", err)
	}

	pruneLabel := "ssh-docker-tui.integration-system-prune"
	containerTarget, err := fixture.CreateContainerWithLabels(ctx, "slice-eight-container-target", []string{"true"}, map[string]string{pruneLabel: "container-target"})
	if err != nil {
		t.Fatalf("create container prune target: %v", err)
	}
	containerSentinel, err := fixture.CreateContainerWithLabels(ctx, "slice-eight-container-sentinel", []string{"true"}, map[string]string{pruneLabel: "container-sentinel"})
	if err != nil {
		t.Fatalf("create container prune sentinel: %v", err)
	}
	containerResult, err := application.System().PruneContainers(ctx, systempage.ContainerPruneOptions{Labels: map[string]string{pruneLabel: "container-target"}})
	if err != nil || containerResult.Command.OperationID != "system.prune.containers" || !containsString(containerResult.Report.ContainersDeleted, containerTarget) {
		t.Fatalf("prune containers: result=%#v err=%v", containerResult, err)
	}
	containerUpdate := waitForContainerPresence(t, ctx, containerSubscription.Events(), containerTarget, false)
	if findContainer(containerUpdate.Containers, containerSentinel).ID != containerSentinel {
		t.Fatalf("container prune removed sentinel: %#v", containerUpdate)
	}

	volumeTarget := fixture.Name("slice-eight-volume-target")
	volumeSentinel := fixture.Name("slice-eight-volume-sentinel")
	fixture.TrackVolume(volumeTarget)
	fixture.TrackVolume(volumeSentinel)
	if _, err := application.Volumes().Create(ctx, volumepage.CreateOptions{Name: volumeTarget, Labels: fixture.Labels(map[string]string{pruneLabel: "volume-target"})}); err != nil {
		t.Fatalf("create volume prune target: %v", err)
	}
	if _, err := application.Volumes().Create(ctx, volumepage.CreateOptions{Name: volumeSentinel, Labels: fixture.Labels(map[string]string{pruneLabel: "volume-sentinel"})}); err != nil {
		t.Fatalf("create volume prune sentinel: %v", err)
	}
	waitForVolume(t, ctx, volumeSubscription.Events(), volumeSentinel, true)
	volumeResult, err := application.System().PruneVolumes(ctx, systempage.VolumePruneOptions{All: true, Labels: map[string]string{pruneLabel: "volume-target"}})
	if err != nil || volumeResult.Command.OperationID != "system.prune.volumes" || !containsString(volumeResult.Report.VolumesDeleted, volumeTarget) {
		t.Fatalf("prune volumes: result=%#v err=%v", volumeResult, err)
	}
	volumeUpdate := waitForVolume(t, ctx, volumeSubscription.Events(), volumeTarget, false)
	if findVolume(volumeUpdate.Volumes, volumeSentinel).Name != volumeSentinel {
		t.Fatalf("volume prune removed sentinel: %#v", volumeUpdate)
	}

	networkTarget := fixture.Name("slice-eight-network-target")
	networkSentinel := fixture.Name("slice-eight-network-sentinel")
	fixture.TrackNetwork(networkTarget)
	fixture.TrackNetwork(networkSentinel)
	if _, err := application.Networks().Create(ctx, networkpage.CreateOptions{Name: networkTarget, Labels: fixture.Labels(map[string]string{pruneLabel: "network-target"})}); err != nil {
		t.Fatalf("create network prune target: %v", err)
	}
	if _, err := application.Networks().Create(ctx, networkpage.CreateOptions{Name: networkSentinel, Labels: fixture.Labels(map[string]string{pruneLabel: "network-sentinel"})}); err != nil {
		t.Fatalf("create network prune sentinel: %v", err)
	}
	waitForNetwork(t, ctx, networkSubscription.Events(), networkSentinel, true)
	networkResult, err := application.System().PruneNetworks(ctx, systempage.NetworkPruneOptions{Labels: map[string]string{pruneLabel: "network-target"}})
	if err != nil || networkResult.Command.OperationID != "system.prune.networks" || !containsString(networkResult.Report.NetworksDeleted, networkTarget) {
		t.Fatalf("prune networks: result=%#v err=%v", networkResult, err)
	}
	networkUpdate := waitForNetwork(t, ctx, networkSubscription.Events(), networkTarget, false)
	if findNetwork(networkUpdate.Networks, networkSentinel).Name != networkSentinel {
		t.Fatalf("network prune removed sentinel: %#v", networkUpdate)
	}

	waitForEventType(t, ctx, systemSubscription.Events(), systempage.EventDiskUsageUpdated)
	waitForEventType(t, ctx, dashboardSubscription.Events(), dashboard.EventSummaryUpdated)
}

func TestSystemPruneAgainstDedicatedDocker(t *testing.T) {
	fixture := dockerfixture.New(t)
	fixture.RequireDedicatedDaemon(t)
	environment := fixture.Environment()
	ctx, cancel := context.WithTimeout(context.Background(), environment.Timeout)
	defer cancel()
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{
		Endpoint: environment.DockerEndpoint, AllowSystemPrune: true,
	})
	if err != nil {
		t.Fatalf("construct production backend: %v", err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })
	result, err := application.System().PruneSystem(ctx, systempage.SystemPruneOptions{Confirmation: systempage.SystemPruneConfirmation})
	if err != nil || result.Command.OperationID != "system.prune" {
		t.Fatalf("prune dedicated Docker daemon: result=%#v err=%v", result, err)
	}
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

func waitForImageTag(t *testing.T, ctx context.Context, events <-chan backend.EventEnvelope, tag string, present bool) imagepage.ListUpdated {
	t.Helper()
	for {
		event := receiveIntegrationEvent(t, ctx, events)
		update, ok := event.Payload.(imagepage.ListUpdated)
		if ok && (findImageByTag(update.Images, tag).ID != "") == present {
			return update
		}
	}
}

func findImageByTag(values []imagepage.Summary, tag string) imagepage.Summary {
	for _, value := range values {
		for _, candidate := range value.RepoTags {
			if candidate == tag {
				return value
			}
		}
	}
	return imagepage.Summary{}
}

func waitForVolume(t *testing.T, ctx context.Context, events <-chan backend.EventEnvelope, name string, present bool) volumepage.ListUpdated {
	t.Helper()
	for {
		event := receiveIntegrationEvent(t, ctx, events)
		update, ok := event.Payload.(volumepage.ListUpdated)
		if ok && (findVolume(update.Volumes, name).Name != "") == present {
			return update
		}
	}
}

func findVolume(values []volumepage.Volume, name string) volumepage.Volume {
	for _, value := range values {
		if value.Name == name {
			return value
		}
	}
	return volumepage.Volume{}
}

func waitForNetwork(t *testing.T, ctx context.Context, events <-chan backend.EventEnvelope, name string, present bool) networkpage.ListUpdated {
	t.Helper()
	for {
		event := receiveIntegrationEvent(t, ctx, events)
		update, ok := event.Payload.(networkpage.ListUpdated)
		if ok && (findNetwork(update.Networks, name).Name != "") == present {
			return update
		}
	}
}

func findNetwork(values []networkpage.Summary, name string) networkpage.Summary {
	for _, value := range values {
		if value.Name == name {
			return value
		}
	}
	return networkpage.Summary{}
}

func findNetworkConnection(values []networkpage.Connection, containerID string) networkpage.Connection {
	for _, value := range values {
		if value.ContainerID == containerID {
			return value
		}
	}
	return networkpage.Connection{}
}

func waitForRefreshReason(t *testing.T, ctx context.Context, events <-chan backend.EventEnvelope, reason backend.RefreshReason) backend.EventEnvelope {
	t.Helper()
	for {
		event := receiveIntegrationEvent(t, ctx, events)
		if event.Reason == reason {
			return event
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

func waitForContainerPresence(t *testing.T, ctx context.Context, events <-chan backend.EventEnvelope, containerID string, present bool) containers.ListUpdated {
	t.Helper()
	for {
		event := receiveIntegrationEvent(t, ctx, events)
		update, ok := event.Payload.(containers.ListUpdated)
		if ok && (findContainer(update.Containers, containerID).ID != "") == present {
			return update
		}
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
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
) (backendtest.ConformanceBackend, error) {
	return dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{
		Endpoint: environment.DockerEndpoint, ComposeRoots: []string{environment.ComposeRoot},
	})
}
