// Command ssh-docker-tui is the entry point for the SSH-native Docker TUI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
	dockerplatform "github.com/petar030/ssh-native-docker-tui/internal/platform/docker"
)

func main() {
	watch := flag.Duration("watch", 10*time.Second, "how long to print live Docker events after the Dashboard summary")
	endpoint := flag.String("docker-host", "", "optional Docker daemon endpoint; defaults to Docker environment settings")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runDemo(ctx, *endpoint, *watch); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "ssh-docker-tui:", err)
		os.Exit(1)
	}
}

func runDemo(ctx context.Context, endpoint string, watch time.Duration) error {
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
	liveEvents, err := application.Subscribe(ctx, backend.PageEvents, backend.EventFilter{
		Types: []backend.EventType{backend.EventDockerObserved},
	})
	if err != nil {
		return err
	}
	defer liveEvents.Close()

	if err := application.Refresh(ctx, backend.PageDashboard); err != nil {
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
