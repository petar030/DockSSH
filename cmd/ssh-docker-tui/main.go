// Command ssh-docker-tui is the entry point for the SSH-native Docker TUI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
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
)

func main() {
	watch := flag.Duration("watch", 10*time.Second, "how long to print live page updates after the initial data")
	endpoint := flag.String("docker-host", "", "optional Docker daemon endpoint; defaults to Docker environment settings")
	page := flag.String("page", "containers", "demo page: containers, compose, images, volumes, networks, events, system, or all")
	containerID := flag.String("container-id", "", "container ID/name to inspect; defaults to the first listed container")
	containerAction := flag.String("container-action", "", "optional command: start, stop, restart, pause, unpause, kill, rename, or remove")
	containerName := flag.String("container-name", "", "new name for -container-action=rename")
	composeProject := flag.String("compose-project", "", "Compose project name; defaults to the first active project")
	composeFile := flag.String("compose-file", "", "Compose config file used by up, pull, build, and scale")
	composeAction := flag.String("compose-action", "", "optional action: start, stop, restart, pause, unpause, scale, up, down, pull, build, or logs")
	composeService := flag.String("compose-service", "", "optional Compose service selected by an action")
	composeReplicas := flag.Int("compose-replicas", 1, "replica count for -compose-action=scale")
	imageID := flag.String("image-id", "", "image ID/reference to inspect or mutate; defaults to the first listed image")
	imageAction := flag.String("image-action", "", "optional action: details, history, tag, remove, or pull")
	imageReference := flag.String("image-reference", "", "destination tag or pull reference for the selected image action")
	imagePlatform := flag.String("image-platform", "", "optional pull platform as os/architecture[/variant]")
	volumeName := flag.String("volume-name", "", "volume name to inspect, create, or remove; defaults to the first listed volume")
	volumeAction := flag.String("volume-action", "", "optional action: details, attachments, create, or remove")
	volumeDriver := flag.String("volume-driver", "", "optional driver for -volume-action=create")
	networkID := flag.String("network-id", "", "network ID/name to inspect or mutate; defaults to the first listed network")
	networkName := flag.String("network-name", "", "name required by -network-action=create")
	networkAction := flag.String("network-action", "", "optional action: details, connections, create, remove, connect, or disconnect")
	networkContainerID := flag.String("network-container-id", "", "container ID/name required by network connect/disconnect")
	eventResource := flag.String("event-resource", "", "optional comma-separated Docker event resource filter")
	eventAction := flag.String("event-action", "", "optional comma-separated Docker event action filter")
	eventProject := flag.String("event-project", "", "optional comma-separated Compose project event filter")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runSelectedDemo(
		ctx, *endpoint, *watch, *page, *containerID, *containerAction, *containerName,
		*composeProject, *composeFile, *composeAction, *composeService, *composeReplicas,
		*imageID, *imageAction, *imageReference, *imagePlatform,
		*volumeName, *volumeAction, *volumeDriver,
		*networkID, *networkName, *networkAction, *networkContainerID,
		*eventResource, *eventAction, *eventProject,
	); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "ssh-docker-tui:", err)
		os.Exit(1)
	}
}

func runSelectedDemo(
	ctx context.Context,
	endpoint string,
	watch time.Duration,
	page, requestedID, action, newName string,
	composeProject, composeFile, composeAction, composeService string,
	composeReplicas int,
	imageID, imageAction, imageReference, imagePlatform string,
	volumeName, volumeAction, volumeDriver string,
	networkID, networkName, networkAction, networkContainerID string,
	eventResource, eventAction, eventProject string,
) error {
	switch strings.ToLower(strings.TrimSpace(page)) {
	case "containers":
		return runContainerDemo(ctx, endpoint, watch, requestedID, action, newName)
	case "all":
		return runAllDemo(ctx, endpoint, watch, requestedID, action, newName)
	case "compose":
		return runComposeDemo(ctx, endpoint, watch, composeProject, composeFile, composeAction, composeService, composeReplicas)
	case "images":
		return runImagesDemo(ctx, endpoint, imageID, imageAction, imageReference, imagePlatform)
	case "volumes":
		return runVolumesDemo(ctx, endpoint, volumeName, volumeAction, volumeDriver)
	case "networks":
		return runNetworksDemo(ctx, endpoint, networkID, networkName, networkAction, networkContainerID)
	case "events":
		return runEventsDemo(ctx, endpoint, watch, eventResource, eventAction, eventProject)
	case "system":
		return runSystemDemo(ctx, endpoint)
	default:
		return fmt.Errorf("unknown -page %q (choose containers, compose, images, volumes, networks, events, system, or all)", page)
	}
}

func runNetworksDemo(ctx context.Context, endpoint, requestedID, requestedName, action, containerID string) error {
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{Endpoint: endpoint})
	if err != nil {
		return err
	}
	defer closeApplication(application)
	listEvents, err := application.Subscribe(ctx, backend.PageNetworks, backend.EventFilter{Types: []backend.EventType{networkpage.EventListUpdated}})
	if err != nil {
		return err
	}
	defer listEvents.Close()
	detailEvents, err := application.Subscribe(ctx, backend.PageNetworks, backend.EventFilter{
		Types: []backend.EventType{networkpage.EventDetailsUpdated, networkpage.EventConnectionsUpdated, backend.EventRefreshFailed},
	})
	if err != nil {
		return err
	}
	defer detailEvents.Close()
	if err := application.RequestRefresh(backend.PageNetworks); err != nil {
		return err
	}
	event, err := waitForDemoEvent(ctx, listEvents.Events())
	if err != nil {
		return err
	}
	update := event.Payload.(networkpage.ListUpdated)
	fmt.Printf("Networks (%d):\n", len(update.Networks))
	for _, value := range update.Networks {
		fmt.Printf("  %-20s %-24s driver=%-10s scope=%s internal=%v\n", shortID(value.ID), value.Name, value.Driver, value.Scope, value.Internal)
	}

	action = strings.ToLower(strings.TrimSpace(action))
	if action == "create" {
		name := strings.TrimSpace(requestedName)
		if name == "" {
			return errors.New("create requires -network-name")
		}
		result, err := application.Networks().Create(ctx, networkpage.CreateOptions{Name: name, Driver: "bridge"})
		if err == nil {
			fmt.Printf("Network command completed: %s affected=%v\n", result.OperationID, result.Affected)
		}
		return err
	}
	id := strings.TrimSpace(requestedID)
	if id == "" && len(update.Networks) > 0 {
		id = update.Networks[0].ID
	}
	if id == "" {
		if action == "" {
			return nil
		}
		return errors.New("-network-id is required when no network exists")
	}
	if action == "" {
		action = "details"
	}
	switch action {
	case "details":
		err = application.Networks().RequestDetails(id)
	case "connections":
		err = application.Networks().RequestConnections(id)
	case "remove":
		result, commandErr := application.Networks().Remove(ctx, id, networkpage.RemoveOptions{})
		if commandErr == nil {
			fmt.Printf("Network command completed: %s affected=%v\n", result.OperationID, result.Affected)
		}
		return commandErr
	case "connect":
		if strings.TrimSpace(containerID) == "" {
			return errors.New("connect requires -network-container-id")
		}
		result, commandErr := application.Networks().Connect(ctx, id, networkpage.ConnectOptions{ContainerID: containerID})
		if commandErr == nil {
			fmt.Printf("Network command completed: %s affected=%v\n", result.OperationID, result.Affected)
		}
		return commandErr
	case "disconnect":
		if strings.TrimSpace(containerID) == "" {
			return errors.New("disconnect requires -network-container-id")
		}
		result, commandErr := application.Networks().Disconnect(ctx, id, networkpage.DisconnectOptions{ContainerID: containerID})
		if commandErr == nil {
			fmt.Printf("Network command completed: %s affected=%v\n", result.OperationID, result.Affected)
		}
		return commandErr
	default:
		return fmt.Errorf("unknown network action %q", action)
	}
	if err != nil {
		return err
	}
	detailEvent, err := waitForDemoEvent(ctx, detailEvents.Events())
	if err != nil {
		return err
	}
	if failure, ok := detailEvent.Payload.(backend.RefreshFailed); ok {
		return failure.Err
	}
	switch payload := detailEvent.Payload.(type) {
	case networkpage.DetailsUpdated:
		value := payload.Network
		fmt.Printf("\nNetwork %s (%s): driver=%s scope=%s IPv4=%v IPv6=%v\n", value.Name, value.ID, value.Driver, value.Scope, value.EnableIPv4, value.EnableIPv6)
		for _, ipam := range value.IPAM {
			fmt.Printf("  subnet=%s range=%s gateway=%s\n", ipam.Subnet, ipam.IPRange, ipam.Gateway)
		}
	case networkpage.ConnectionsUpdated:
		fmt.Printf("\nNetwork connections (%d):\n", len(payload.Connections))
		for _, value := range payload.Connections {
			fmt.Printf("  %-20s %-12s IPv4=%-20s IPv6=%s\n", value.ContainerName, shortID(value.ContainerID), value.IPv4Address, value.IPv6Address)
		}
	}
	return nil
}

func runEventsDemo(ctx context.Context, endpoint string, watch time.Duration, resources, actions, projects string) error {
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{Endpoint: endpoint})
	if err != nil {
		return err
	}
	defer closeApplication(application)
	recentEvents, err := application.Subscribe(ctx, backend.PageEvents, backend.EventFilter{Types: []backend.EventType{eventpage.EventRecentUpdated}})
	if err != nil {
		return err
	}
	defer recentEvents.Close()
	liveEvents, err := application.Subscribe(ctx, backend.PageEvents, backend.EventFilter{
		Types: []backend.EventType{backend.EventDockerObserved}, DockerResources: cleanCLIValues(resources),
		DockerActions: cleanCLIValues(actions), DockerProjects: cleanCLIValues(projects),
	})
	if err != nil {
		return err
	}
	defer liveEvents.Close()
	if err := application.RequestRefresh(backend.PageEvents); err != nil {
		return err
	}
	event, err := waitForDemoEvent(ctx, recentEvents.Events())
	if err != nil {
		return err
	}
	window := event.Payload.(eventpage.RecentUpdated)
	fmt.Printf("Recent Docker events (%d, newest first):\n", len(window.Events))
	for _, value := range window.Events {
		fmt.Printf("  %s %-10s %-12s %s\n", value.ReceivedAt.Format(time.TimeOnly), value.Resource, value.Action, value.ResourceID)
	}
	if watch <= 0 {
		return nil
	}
	fmt.Printf("\nWatching matching Docker events for %s...\n", watch)
	timer := time.NewTimer(watch)
	defer timer.Stop()
	for {
		select {
		case event, open := <-liveEvents.Events():
			if !open {
				return nil
			}
			if observed, ok := event.Payload.(backend.DockerEventObserved); ok {
				fmt.Printf("  %s %-10s %-12s %s project=%s\n", event.Time.Format(time.TimeOnly), observed.Resource, observed.Action, observed.ResourceID, observed.Project)
			}
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func runSystemDemo(ctx context.Context, endpoint string) error {
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{Endpoint: endpoint})
	if err != nil {
		return err
	}
	defer closeApplication(application)
	events, err := application.Subscribe(ctx, backend.PageSystem, backend.EventFilter{
		Types: []backend.EventType{systempage.EventInfoUpdated, systempage.EventDiskUsageUpdated, backend.EventRefreshFailed},
	})
	if err != nil {
		return err
	}
	defer events.Close()
	if err := application.RequestRefresh(backend.PageSystem); err != nil {
		return err
	}
	event, err := waitForDemoEvent(ctx, events.Events())
	if err != nil {
		return err
	}
	if failure, ok := event.Payload.(backend.RefreshFailed); ok {
		return failure.Err
	}
	info := event.Payload.(systempage.InfoUpdated)
	fmt.Printf("Docker %s (%s, API %s–%s)\n", info.Engine.Version, info.Engine.Platform, info.Engine.MinAPIVersion, info.Engine.APIVersion)
	fmt.Printf("Host %s: %s %s, kernel %s, %s, CPUs=%d memory=%s\n", info.Host.Name, info.Host.OperatingSystem, info.Host.OSVersion, info.Host.KernelVersion, info.Host.Architecture, info.Host.CPUs, formatBytes(info.Host.MemoryBytes))
	fmt.Printf("Storage=%s logging=%s cgroup=%s/%s runtime=%s\n", info.Host.StorageDriver, info.Host.LoggingDriver, info.Host.CgroupDriver, info.Host.CgroupVersion, info.Host.DefaultRuntime)
	if err := application.System().RequestDiskUsage(); err != nil {
		return err
	}
	event, err = waitForDemoEvent(ctx, events.Events())
	if err != nil {
		return err
	}
	if failure, ok := event.Payload.(backend.RefreshFailed); ok {
		return failure.Err
	}
	disk := event.Payload.(systempage.DiskUsageUpdated)
	fmt.Printf("Disk: containers %s (%d), images %s (%d), volumes %s (%d), build cache %s (%d)\n",
		formatBytes(disk.Containers.TotalBytes), disk.Containers.Count,
		formatBytes(disk.Images.TotalBytes), disk.Images.Count,
		formatBytes(disk.Volumes.TotalBytes), disk.Volumes.Count,
		formatBytes(disk.BuildCache.TotalBytes), disk.BuildCache.Count)
	fmt.Printf("Detailed items: containers=%d images=%d volumes=%d build-cache=%d\n",
		len(disk.ContainerItems), len(disk.ImageItems), len(disk.VolumeItems), len(disk.BuildCacheItems))
	return nil
}

func runImagesDemo(ctx context.Context, endpoint, requestedID, action, reference, platform string) error {
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{Endpoint: endpoint})
	if err != nil {
		return err
	}
	defer closeApplication(application)
	listEvents, err := application.Subscribe(ctx, backend.PageImages, backend.EventFilter{Types: []backend.EventType{imagepage.EventListUpdated}})
	if err != nil {
		return err
	}
	defer listEvents.Close()
	detailEvents, err := application.Subscribe(ctx, backend.PageImages, backend.EventFilter{
		Types: []backend.EventType{imagepage.EventDetailsUpdated, imagepage.EventHistoryUpdated, backend.EventRefreshFailed},
	})
	if err != nil {
		return err
	}
	defer detailEvents.Close()
	if err := application.RequestRefresh(backend.PageImages); err != nil {
		return err
	}
	event, err := waitForDemoEvent(ctx, listEvents.Events())
	if err != nil {
		return err
	}
	update := event.Payload.(imagepage.ListUpdated)
	fmt.Printf("Images (%d):\n", len(update.Images))
	for _, image := range update.Images {
		name := "<untagged>"
		if len(image.RepoTags) > 0 {
			name = strings.Join(image.RepoTags, ",")
		}
		fmt.Printf("  %-20s %-45s %d bytes\n", shortID(image.ID), name, image.Size)
	}
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "pull" {
		if strings.TrimSpace(reference) == "" {
			return errors.New("pull requires -image-reference")
		}
		job, err := application.Images().Pull(ctx, reference, imagepage.PullOptions{Platform: platform})
		if err != nil {
			return err
		}
		for progress := range job.Progress() {
			fmt.Printf("[%s] %-16s %d/%d %s\n", progress.Status, progress.Resource, progress.Current, progress.Total, progress.Message)
		}
		result, err := job.Wait(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("Image pull completed: %s\n", result.JobID)
		return nil
	}
	id := strings.TrimSpace(requestedID)
	if id == "" && len(update.Images) > 0 {
		id = update.Images[0].ID
	}
	if id == "" {
		if action == "" {
			return nil
		}
		return errors.New("-image-id is required when no image exists")
	}
	if action == "" {
		action = "details"
	}
	switch action {
	case "details":
		err = application.Images().RequestDetails(id)
	case "history":
		err = application.Images().RequestHistory(id)
	case "tag":
		if strings.TrimSpace(reference) == "" {
			return errors.New("tag requires -image-reference")
		}
		var result backend.CommandResult
		result, err = application.Images().Tag(ctx, id, imagepage.TagOptions{Reference: reference})
		if err == nil {
			fmt.Printf("Image command completed: %s affected=%v\n", result.OperationID, result.Affected)
		}
		return err
	case "remove":
		var result backend.CommandResult
		result, err = application.Images().Remove(ctx, id, imagepage.RemoveOptions{})
		if err == nil {
			fmt.Printf("Image command completed: %s affected=%v\n", result.OperationID, result.Affected)
		}
		return err
	default:
		return fmt.Errorf("unknown image action %q", action)
	}
	if err != nil {
		return err
	}
	detailEvent, err := waitForDemoEvent(ctx, detailEvents.Events())
	if err != nil {
		return err
	}
	if failure, ok := detailEvent.Payload.(backend.RefreshFailed); ok {
		return failure.Err
	}
	switch payload := detailEvent.Payload.(type) {
	case imagepage.DetailsUpdated:
		fmt.Printf("\nImage %s: %s/%s size=%d bytes tags=%s\n", shortID(payload.Image.ID), payload.Image.OS, payload.Image.Architecture, payload.Image.Size, strings.Join(payload.Image.RepoTags, ","))
	case imagepage.HistoryUpdated:
		fmt.Printf("\nImage history (%d):\n", len(payload.Entries))
		for _, entry := range payload.Entries {
			fmt.Printf("  %-20s %10d %s\n", shortID(entry.ID), entry.Size, entry.CreatedBy)
		}
	}
	return nil
}

func runVolumesDemo(ctx context.Context, endpoint, requestedName, action, driver string) error {
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{Endpoint: endpoint})
	if err != nil {
		return err
	}
	defer closeApplication(application)
	listEvents, err := application.Subscribe(ctx, backend.PageVolumes, backend.EventFilter{Types: []backend.EventType{volumepage.EventListUpdated}})
	if err != nil {
		return err
	}
	defer listEvents.Close()
	detailEvents, err := application.Subscribe(ctx, backend.PageVolumes, backend.EventFilter{
		Types: []backend.EventType{volumepage.EventDetailsUpdated, volumepage.EventAttachmentsUpdated, backend.EventRefreshFailed},
	})
	if err != nil {
		return err
	}
	defer detailEvents.Close()
	if err := application.RequestRefresh(backend.PageVolumes); err != nil {
		return err
	}
	event, err := waitForDemoEvent(ctx, listEvents.Events())
	if err != nil {
		return err
	}
	update := event.Payload.(volumepage.ListUpdated)
	fmt.Printf("Volumes (%d):\n", len(update.Volumes))
	for _, volume := range update.Volumes {
		fmt.Printf("  %-32s driver=%-10s scope=%s\n", volume.Name, volume.Driver, volume.Scope)
	}
	name := strings.TrimSpace(requestedName)
	if name == "" && len(update.Volumes) > 0 {
		name = update.Volumes[0].Name
	}
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "create" {
		if strings.TrimSpace(requestedName) == "" {
			return errors.New("create requires -volume-name")
		}
		result, err := application.Volumes().Create(ctx, volumepage.CreateOptions{Name: requestedName, Driver: driver})
		if err == nil {
			fmt.Printf("Volume command completed: %s affected=%v\n", result.OperationID, result.Affected)
		}
		return err
	}
	if name == "" {
		if action == "" {
			return nil
		}
		return errors.New("-volume-name is required when no volume exists")
	}
	if action == "" {
		action = "details"
	}
	switch action {
	case "details":
		err = application.Volumes().RequestDetails(name)
	case "attachments":
		err = application.Volumes().RequestAttachments(name)
	case "remove":
		result, removeErr := application.Volumes().Remove(ctx, name, volumepage.RemoveOptions{})
		if removeErr == nil {
			fmt.Printf("Volume command completed: %s affected=%v\n", result.OperationID, result.Affected)
		}
		return removeErr
	default:
		return fmt.Errorf("unknown volume action %q", action)
	}
	if err != nil {
		return err
	}
	detailEvent, err := waitForDemoEvent(ctx, detailEvents.Events())
	if err != nil {
		return err
	}
	if failure, ok := detailEvent.Payload.(backend.RefreshFailed); ok {
		return failure.Err
	}
	switch payload := detailEvent.Payload.(type) {
	case volumepage.DetailsUpdated:
		fmt.Printf("\nVolume %s: driver=%s scope=%s mountpoint=%s labels=%v\n", payload.Volume.Name, payload.Volume.Driver, payload.Volume.Scope, payload.Volume.Mountpoint, payload.Volume.Labels)
	case volumepage.AttachmentsUpdated:
		fmt.Printf("\nVolume attachments (%d):\n", len(payload.Attachments))
		for _, attachment := range payload.Attachments {
			fmt.Printf("  %-20s %-12s -> %s rw=%v\n", attachment.ContainerName, attachment.State, attachment.Destination, attachment.ReadWrite)
		}
	}
	return nil
}

func shortID(value string) string {
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func runComposeDemo(
	ctx context.Context,
	endpoint string,
	watch time.Duration,
	requestedProject, configFile, action, service string,
	replicas int,
) error {
	var roots []string
	if strings.TrimSpace(configFile) != "" {
		absolute, err := filepath.Abs(configFile)
		if err != nil {
			return err
		}
		configFile = absolute
		roots = []string{filepath.Dir(absolute)}
	}
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{
		Endpoint: endpoint, ComposeRoots: roots,
	})
	if err != nil {
		return err
	}
	defer closeApplication(application)

	listEvents, err := application.Subscribe(ctx, backend.PageCompose, backend.EventFilter{
		Types: []backend.EventType{composepage.EventProjectsUpdated},
	})
	if err != nil {
		return err
	}
	defer listEvents.Close()
	detailsEvents, err := application.Subscribe(ctx, backend.PageCompose, backend.EventFilter{
		Types: []backend.EventType{composepage.EventProjectUpdated, backend.EventRefreshFailed},
	})
	if err != nil {
		return err
	}
	defer detailsEvents.Close()

	if err := application.RequestRefresh(backend.PageCompose); err != nil {
		return err
	}
	event, err := waitForDemoEvent(ctx, listEvents.Events())
	if err != nil {
		return err
	}
	update := event.Payload.(composepage.ProjectsUpdated)
	fmt.Printf("Compose projects (%d):\n", len(update.Projects))
	for _, project := range update.Projects {
		fmt.Printf("  %-24s %-12s %s\n", project.Name, project.Status, strings.Join(project.ConfigFiles, ","))
	}

	projectName := strings.TrimSpace(requestedProject)
	if projectName == "" && len(update.Projects) > 0 {
		projectName = update.Projects[0].Name
	}
	action = strings.ToLower(strings.TrimSpace(action))
	if projectName == "" && action != "" {
		return errors.New("-compose-project is required when no active Compose project exists")
	}
	services := cleanCLIValues(service)
	spec := composepage.ProjectSpec{Name: projectName}
	if configFile != "" {
		spec.ConfigFiles = []string{configFile}
	}

	if action == "" {
		if projectName == "" {
			return nil
		}
		if err := application.Compose().RequestDetails(projectName); err != nil {
			return err
		}
		detailsEvent, err := waitForDemoEvent(ctx, detailsEvents.Events())
		if err != nil {
			return err
		}
		if failure, ok := detailsEvent.Payload.(backend.RefreshFailed); ok {
			return failure.Err
		}
		printComposeDetails(detailsEvent.Payload.(composepage.ProjectUpdated).Project)
		return nil
	}

	var commandResult backend.CommandResult
	switch action {
	case "start":
		commandResult, err = application.Compose().Start(ctx, projectName, composepage.ServiceOptions{Services: services})
	case "stop":
		commandResult, err = application.Compose().Stop(ctx, projectName, composepage.StopOptions{Services: services})
	case "restart":
		commandResult, err = application.Compose().Restart(ctx, projectName, composepage.RestartOptions{Services: services})
	case "pause":
		commandResult, err = application.Compose().Pause(ctx, projectName, composepage.ServiceOptions{Services: services})
	case "unpause":
		commandResult, err = application.Compose().Unpause(ctx, projectName, composepage.ServiceOptions{Services: services})
	case "scale":
		if len(services) != 1 || configFile == "" {
			return errors.New("scale requires one -compose-service and -compose-file")
		}
		commandResult, err = application.Compose().Scale(ctx, spec, composepage.ScaleOptions{Service: services[0], Replicas: replicas})
	case "logs":
		return printComposeLogs(ctx, application, projectName, services, watch)
	case "up", "down", "pull", "build":
		return runComposeJob(ctx, application, action, spec, services)
	default:
		return fmt.Errorf("unknown Compose action %q", action)
	}
	if err != nil {
		return err
	}
	fmt.Printf("Compose command accepted: %s affected=%v\n", commandResult.OperationID, commandResult.Affected)
	return nil
}

func runComposeJob(ctx context.Context, application *dockerplatform.Application, action string, spec composepage.ProjectSpec, services []string) error {
	var (
		job backend.Job
		err error
	)
	switch action {
	case "up":
		if len(spec.ConfigFiles) == 0 {
			return errors.New("up requires -compose-file")
		}
		job, err = application.Compose().Up(ctx, spec, composepage.UpOptions{Services: services})
	case "down":
		job, err = application.Compose().Down(ctx, spec.Name, composepage.DownOptions{Services: services})
	case "pull":
		if len(spec.ConfigFiles) == 0 {
			return errors.New("pull requires -compose-file")
		}
		job, err = application.Compose().Pull(ctx, spec, composepage.PullOptions{Services: services})
	case "build":
		if len(spec.ConfigFiles) == 0 {
			return errors.New("build requires -compose-file")
		}
		job, err = application.Compose().Build(ctx, spec, composepage.BuildOptions{Services: services})
	}
	if err != nil {
		return err
	}
	for progress := range job.Progress() {
		fmt.Printf("[%s] %s\n", progress.Status, progress.Message)
	}
	result, err := job.Wait(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Compose job completed: %s\n", result.JobID)
	return nil
}

func printComposeLogs(ctx context.Context, application *dockerplatform.Application, projectName string, services []string, watch time.Duration) error {
	streamCtx := ctx
	cancel := func() {}
	if watch > 0 {
		streamCtx, cancel = context.WithTimeout(ctx, watch)
	}
	defer cancel()
	stream, err := application.Compose().Logs(streamCtx, projectName, composepage.LogsOptions{
		Services: services, Follow: watch > 0, Tail: 20,
	})
	if err != nil {
		return err
	}
	defer stream.Close()
	for entry := range stream.Values() {
		fmt.Printf("[%s] %-20s %s", entry.Source, entry.Container, entry.Data)
		if !strings.HasSuffix(entry.Data, "\n") {
			fmt.Println()
		}
	}
	return <-stream.Done()
}

func printComposeDetails(project composepage.ProjectDetails) {
	fmt.Printf("\nCompose project: %s (%s)\n", project.Name, project.Status)
	for _, service := range project.Services {
		fmt.Printf("  %-20s image=%-20s replicas=%d/%d containers=%d\n",
			service.Name, service.Image, service.Replicas, service.Desired, len(service.Containers))
	}
}

func cleanCLIValues(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func waitForDemoEvent(ctx context.Context, events <-chan backend.EventEnvelope) (backend.EventEnvelope, error) {
	select {
	case event, open := <-events:
		if !open {
			return backend.EventEnvelope{}, errors.New("page subscription closed")
		}
		return event, nil
	case <-ctx.Done():
		return backend.EventEnvelope{}, ctx.Err()
	}
}

// runContainerDemo is the isolated manual exercise for the Containers page.
// It intentionally does not subscribe to or refresh the Dashboard page.
func runContainerDemo(ctx context.Context, endpoint string, watch time.Duration, requestedID, action, newName string) error {
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{Endpoint: endpoint})
	if err != nil {
		return err
	}
	defer closeApplication(application)

	containerEvents, err := application.Subscribe(ctx, backend.PageContainers, backend.EventFilter{
		Types: []backend.EventType{containers.EventListUpdated},
	})
	if err != nil {
		return err
	}
	defer containerEvents.Close()
	detailsEvents, err := application.Subscribe(ctx, backend.PageContainers, backend.EventFilter{
		Types: []backend.EventType{
			containers.EventDetailsUpdated, containers.EventProcessesUpdated, backend.EventRefreshFailed,
		},
	})
	if err != nil {
		return err
	}
	defer detailsEvents.Close()
	liveEvents, err := application.Subscribe(ctx, backend.PageEvents, backend.EventFilter{
		Types: []backend.EventType{backend.EventDockerObserved},
	})
	if err != nil {
		return err
	}
	defer liveEvents.Close()

	if err := application.RequestRefresh(backend.PageContainers); err != nil {
		return err
	}
	select {
	case event, open := <-containerEvents.Events():
		if !open {
			return errors.New("Containers subscription closed before the initial update")
		}
		update, ok := event.Payload.(containers.ListUpdated)
		if !ok {
			return fmt.Errorf("unexpected Containers payload %T", event.Payload)
		}
		printContainers(update)
		if len(update.Containers) == 0 {
			return nil
		}
		id := requestedID
		if id == "" {
			id = update.Containers[0].ID
		}
		if err := inspectContainer(ctx, application, detailsEvents, id); err != nil {
			return err
		}
		if err := runContainerAction(ctx, application, id, action, newName); err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	if watch <= 0 {
		return nil
	}
	fmt.Printf("\nWatching Containers and Docker events for %s...\n", watch)
	timer := time.NewTimer(watch)
	defer timer.Stop()
	for {
		select {
		case event, open := <-containerEvents.Events():
			if !open {
				return nil
			}
			if update, ok := event.Payload.(containers.ListUpdated); ok {
				fmt.Printf("Containers page refreshed: %d containers\n", len(update.Containers))
			}
		case event, open := <-liveEvents.Events():
			if !open {
				return nil
			}
			if observed, ok := event.Payload.(backend.DockerEventObserved); ok {
				fmt.Printf("%s  %-10s %-12s %s\n", event.Time.Format(time.TimeOnly), observed.Resource, observed.Action, observed.ResourceID)
			}
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func closeApplication(application *dockerplatform.Application) {
	closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = application.Close(closeContext)
}

func runAllDemo(ctx context.Context, endpoint string, watch time.Duration, requestedID, action, newName string) error {
	application, err := dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{Endpoint: endpoint})
	if err != nil {
		return err
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = application.Close(closeContext)
	}()

	dashboardEvents, err := application.Subscribe(ctx, backend.PageDashboard, backend.EventFilter{
		Types: []backend.EventType{dashboard.EventSummaryUpdated},
	})
	if err != nil {
		return err
	}
	defer dashboardEvents.Close()
	containerEvents, err := application.Subscribe(ctx, backend.PageContainers, backend.EventFilter{
		Types: []backend.EventType{containers.EventListUpdated},
	})
	if err != nil {
		return err
	}
	defer containerEvents.Close()
	containerDetailsEvents, err := application.Subscribe(ctx, backend.PageContainers, backend.EventFilter{
		Types: []backend.EventType{
			containers.EventDetailsUpdated, containers.EventProcessesUpdated, backend.EventRefreshFailed,
		},
	})
	if err != nil {
		return err
	}
	defer containerDetailsEvents.Close()
	liveEvents, err := application.Subscribe(ctx, backend.PageEvents, backend.EventFilter{
		Types: []backend.EventType{backend.EventDockerObserved},
	})
	if err != nil {
		return err
	}
	defer liveEvents.Close()

	if err := application.RequestRefresh(backend.PageDashboard); err != nil {
		return err
	}
	select {
	case event, open := <-dashboardEvents.Events():
		if !open {
			return errors.New("Dashboard subscription closed before the initial update")
		}
		summary, ok := event.Payload.(dashboard.SummaryUpdated)
		if !ok {
			return fmt.Errorf("unexpected Dashboard payload %T", event.Payload)
		}
		printDashboard(summary)
	case <-ctx.Done():
		return ctx.Err()
	}

	if err := application.RequestRefresh(backend.PageContainers); err != nil {
		return err
	}
	select {
	case event, open := <-containerEvents.Events():
		if !open {
			return errors.New("Containers subscription closed before the initial update")
		}
		update, ok := event.Payload.(containers.ListUpdated)
		if !ok {
			return fmt.Errorf("unexpected Containers payload %T", event.Payload)
		}
		printContainers(update)
		if len(update.Containers) > 0 {
			id := requestedID
			if id == "" {
				id = update.Containers[0].ID
			}
			if err := inspectContainer(ctx, application, containerDetailsEvents, id); err != nil {
				return err
			}
			if err := runContainerAction(ctx, application, id, action, newName); err != nil {
				return err
			}
		} else if strings.TrimSpace(requestedID) != "" || strings.TrimSpace(action) != "" {
			return errors.New("no containers are available for the requested container operation")
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	if watch <= 0 {
		return nil
	}
	fmt.Printf("\nWatching Docker events for %s (try creating or starting a Docker resource)...\n", watch)
	timer := time.NewTimer(watch)
	defer timer.Stop()
	for {
		select {
		case event, open := <-dashboardEvents.Events():
			if !open {
				return nil
			}
			summary, ok := event.Payload.(dashboard.SummaryUpdated)
			if ok {
				fmt.Printf("Dashboard refreshed: %d containers, %d images, %d volumes, %d networks\n",
					summary.Resources.Containers, summary.Resources.Images,
					summary.Resources.Volumes, summary.Resources.Networks)
			}
		case event, open := <-containerEvents.Events():
			if !open {
				return nil
			}
			update, ok := event.Payload.(containers.ListUpdated)
			if ok {
				fmt.Printf("Containers page refreshed: %d containers\n", len(update.Containers))
			}
		case event, open := <-liveEvents.Events():
			if !open {
				return nil
			}
			observed, ok := event.Payload.(backend.DockerEventObserved)
			if ok {
				fmt.Printf("%s  %-10s %-12s %s\n", event.Time.Format(time.TimeOnly), observed.Resource, observed.Action, observed.ResourceID)
			}
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func inspectContainer(
	ctx context.Context,
	application *dockerplatform.Application,
	events backend.Subscription,
	id string,
) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if err := application.Containers().RequestDetails(id); err != nil {
		fmt.Printf("\nContainer details unavailable: %v\n", err)
		return nil
	}
	select {
	case event, open := <-events.Events():
		if !open {
			return errors.New("Container details subscription closed")
		}
		switch payload := event.Payload.(type) {
		case containers.DetailsUpdated:
			details := payload
			printContainerDetails(details.Container)
		case backend.RefreshFailed:
			fmt.Printf("\nContainer details unavailable: %v\n", payload.Err)
			return nil
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	if err := application.Containers().RequestProcesses(id); err != nil {
		fmt.Printf("Processes unavailable: %v\n", err)
	} else {
		select {
		case event, open := <-events.Events():
			if !open {
				return errors.New("Container processes subscription closed")
			}
			switch payload := event.Payload.(type) {
			case containers.ProcessesUpdated:
				processes := payload
				printContainerProcesses(processes)
			case backend.RefreshFailed:
				fmt.Printf("Processes unavailable: %v\n", payload.Err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	printContainerLogs(ctx, application, id)
	printContainerStats(ctx, application, id)
	return nil
}

func runContainerAction(
	ctx context.Context,
	application *dockerplatform.Application,
	id, action, newName string,
) error {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return nil
	}
	var (
		result backend.CommandResult
		err    error
	)
	switch action {
	case "start":
		result, err = application.Containers().Start(ctx, id)
	case "stop":
		result, err = application.Containers().Stop(ctx, id, containers.StopOptions{})
	case "restart":
		result, err = application.Containers().Restart(ctx, id, containers.RestartOptions{})
	case "pause":
		result, err = application.Containers().Pause(ctx, id)
	case "unpause":
		result, err = application.Containers().Unpause(ctx, id)
	case "kill":
		result, err = application.Containers().Kill(ctx, id, containers.KillOptions{Signal: "SIGKILL"})
	case "rename":
		if strings.TrimSpace(newName) == "" {
			return errors.New("-container-name is required with -container-action=rename")
		}
		result, err = application.Containers().Rename(ctx, id, containers.RenameOptions{Name: newName})
	case "remove":
		result, err = application.Containers().Remove(ctx, id, containers.RemoveOptions{Force: true, RemoveVolumes: true})
	default:
		return fmt.Errorf("unknown -container-action %q", action)
	}
	if err != nil {
		return fmt.Errorf("container %s: %w", action, err)
	}
	fmt.Printf("\nContainer command %q accepted (operation %s); shared refresh was requested.\n", action, result.OperationID)
	return nil
}

func printContainerDetails(details containers.Details) {
	fmt.Printf("\nContainer details: %s (%s)\n", details.Name, details.ID)
	fmt.Printf("  image: %s  path: %s  platform: %s\n", details.Image, details.Path, details.Platform)
	fmt.Printf("  state: %s  running=%t paused=%t pid=%d exit=%d\n",
		details.State.Status, details.State.Running, details.State.Paused, details.State.PID, details.State.ExitCode)
	fmt.Printf("  hostname: %s  workdir: %s  restart-count: %d\n",
		details.Hostname, details.WorkingDir, details.RestartCount)
	fmt.Printf("  mounts: %d  networks: %d  environment entries: %d\n",
		len(details.Mounts), len(details.Networks), len(details.Environment))
}

func printContainerProcesses(processes containers.ProcessesUpdated) {
	fmt.Printf("  processes (%d):", len(processes.Rows))
	if len(processes.Rows) == 0 {
		fmt.Println(" none")
		return
	}
	fmt.Printf(" %s\n", strings.Join(processes.Titles, " | "))
	for _, row := range processes.Rows {
		fmt.Printf("    %s\n", strings.Join(row, " | "))
	}
}

func printContainerLogs(ctx context.Context, application *dockerplatform.Application, id string) {
	logsContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	stream, err := application.Containers().Logs(logsContext, id, containers.LogsOptions{Tail: 20})
	if err != nil {
		fmt.Printf("  logs unavailable: %v\n", err)
		return
	}
	defer stream.Close()
	entries := 0
	for entry := range stream.Values() {
		if entries == 0 {
			fmt.Println("  recent logs:")
		}
		fmt.Printf("    [%s] %s", entry.Source, entry.Data)
		entries++
		if entries >= 20 {
			break
		}
	}
	_ = stream.Close()
	if err := waitStream(stream.Done(), logsContext); err != nil {
		fmt.Printf("  logs ended: %v\n", err)
	}
	if entries == 0 {
		fmt.Println("  recent logs: none")
	}
}

func printContainerStats(ctx context.Context, application *dockerplatform.Application, id string) {
	statsContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	stream, err := application.Containers().Stats(statsContext, id, containers.StatsOptions{OneShot: true})
	if err != nil {
		fmt.Printf("  stats unavailable: %v\n", err)
		return
	}
	defer stream.Close()
	select {
	case sample, open := <-stream.Values():
		if !open {
			fmt.Println("  stats: no sample")
			break
		}
		fmt.Printf("  stats: CPU %.2f%%  memory %d/%d (%.2f%%)  network rx=%d tx=%d  pids=%d\n",
			sample.CPUPercent, sample.MemoryUsage, sample.MemoryLimit, sample.MemoryPercent,
			sample.NetworkRx, sample.NetworkTx, sample.PIDs)
	case <-statsContext.Done():
		fmt.Printf("  stats unavailable: %v\n", statsContext.Err())
	}
	if err := waitStream(stream.Done(), statsContext); err != nil {
		fmt.Printf("  stats ended: %v\n", err)
	}
}

func waitStream(done <-chan error, ctx context.Context) error {
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func printContainers(update containers.ListUpdated) {
	fmt.Printf("\nContainers page (%d):\n", len(update.Containers))
	if len(update.Containers) == 0 {
		fmt.Println("  no containers")
		return
	}
	for _, container := range update.Containers {
		name := "<unnamed>"
		if len(container.Names) > 0 {
			name = container.Names[0]
		}
		id := container.ID
		if len(id) > 12 {
			id = id[:12]
		}
		fmt.Printf("  %-12s  %-20s  %-10s  %s\n", id, name, container.State, container.Image)
	}
}

func printDashboard(summary dashboard.SummaryUpdated) {
	fmt.Printf("Docker %s on %s (%s/%s, API %s)\n",
		summary.Engine.ServerVersion, summary.Engine.Name,
		summary.Engine.OperatingSystem, summary.Engine.Architecture, summary.Engine.APIVersion)
	fmt.Printf("Containers: %d total, %d running, %d paused, %d stopped\n",
		summary.Resources.Containers, summary.Resources.ContainersRunning,
		summary.Resources.ContainersPaused, summary.Resources.ContainersStopped)
	fmt.Printf("Images: %d  Volumes: %d  Networks: %d\n",
		summary.Resources.Images, summary.Resources.Volumes, summary.Resources.Networks)
	fmt.Printf("Disk usage: containers %s, images %s, volumes %s, build cache %s\n",
		formatBytes(summary.DiskUsage.Containers.TotalBytes),
		formatBytes(summary.DiskUsage.Images.TotalBytes),
		formatBytes(summary.DiskUsage.Volumes.TotalBytes),
		formatBytes(summary.DiskUsage.BuildCache.TotalBytes))
	fmt.Printf("Recent Docker events retained: %d\n", len(summary.RecentEvents))
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	divisor, exponent := int64(unit), 0
	for value := bytes / unit; value >= unit; value /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(divisor), "KMGTPE"[exponent])
}
