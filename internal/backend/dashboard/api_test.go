package dashboard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/eventhub"
)

func TestAPIReturnsCompleteDashboardSummary(t *testing.T) {
	history := eventhub.NewHistory(4)
	eventTime := time.Unix(1_700_000_000, 0)
	history.Add(backend.EventEnvelope{
		Time: eventTime,
		Payload: backend.DockerEventObserved{
			Resource: "container", ResourceID: "abc", Project: "demo", Action: "start",
		},
	})
	docker := &fakeDockerReader{
		ping: client.PingResult{APIVersion: "1.55"},
		info: client.SystemInfoResult{Info: system.Info{
			Name: "dev-host", ServerVersion: "29.0.0", OperatingSystem: "Linux",
			Architecture: "x86_64", NCPU: 8, MemTotal: 16 << 30,
			Containers: 4, ContainersRunning: 2, ContainersPaused: 1,
			ContainersStopped: 1, Images: 7, SystemTime: "2026-08-23T12:00:00Z",
		}},
		volumes:  client.VolumeListResult{Items: make([]volume.Volume, 3)},
		networks: client.NetworkListResult{Items: make([]network.Summary, 5)},
		disk: client.DiskUsageResult{
			Containers: client.ContainersDiskUsage{TotalCount: 4, ActiveCount: 3, TotalSize: 100, Reclaimable: 20},
			Images:     client.ImagesDiskUsage{TotalCount: 7, ActiveCount: 2, TotalSize: 200, Reclaimable: 80},
			Volumes:    client.VolumesDiskUsage{TotalCount: 3, ActiveCount: 1, TotalSize: 300, Reclaimable: 120},
			BuildCache: client.BuildCacheDiskUsage{TotalCount: 6, ActiveCount: 2, TotalSize: 400, Reclaimable: 150},
		},
	}
	handler, err := NewRefreshHandler(docker, history)
	if err != nil {
		t.Fatalf("new API: %v", err)
	}

	payload, err := handler.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindSummary})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	summary, ok := payload.(SummaryUpdated)
	if !ok {
		t.Fatalf("payload = %T", payload)
	}
	if !summary.Engine.Available || summary.Engine.Name != "dev-host" || summary.Engine.APIVersion != "1.55" {
		t.Fatalf("Engine summary = %#v", summary.Engine)
	}
	if summary.Resources.ContainersRunning != 2 || summary.Resources.Volumes != 3 || summary.Resources.Networks != 5 {
		t.Fatalf("resource counts = %#v", summary.Resources)
	}
	if summary.DiskUsage.Images.TotalBytes != 200 || summary.DiskUsage.Images.ReclaimableBytes != 80 {
		t.Fatalf("disk usage = %#v", summary.DiskUsage)
	}
	if len(summary.RecentEvents) != 1 || summary.RecentEvents[0].Time != eventTime || summary.RecentEvents[0].Project != "demo" {
		t.Fatalf("recent events = %#v", summary.RecentEvents)
	}
}

func TestAPIReturnsDockerFailure(t *testing.T) {
	docker := &fakeDockerReader{pingErr: errors.New("daemon unavailable")}
	handler, err := NewRefreshHandler(docker, eventhub.NewHistory(1))
	if err != nil {
		t.Fatalf("new API: %v", err)
	}
	if _, err := handler.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindSummary}); err == nil {
		t.Fatal("load succeeded with a failed Docker ping")
	}
}

func TestSummaryPayloadIsCopiedForEachSubscriber(t *testing.T) {
	bus := eventhub.NewBus(eventhub.BusConfig{})
	first, err := bus.Subscribe(context.Background(), backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe first: %v", err)
	}
	second, err := bus.Subscribe(context.Background(), backend.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe second: %v", err)
	}
	if _, err := bus.Publish(backend.EventEnvelope{Payload: SummaryUpdated{
		RecentEvents: []RecentEvent{{ResourceID: "original"}},
	}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	firstSummary := (<-first.Events()).Payload.(SummaryUpdated)
	firstSummary.RecentEvents[0].ResourceID = "mutated"
	secondSummary := (<-second.Events()).Payload.(SummaryUpdated)
	if secondSummary.RecentEvents[0].ResourceID != "original" {
		t.Fatalf("second subscriber observed mutation: %#v", secondSummary.RecentEvents)
	}
}

type fakeDockerReader struct {
	ping     client.PingResult
	pingErr  error
	info     client.SystemInfoResult
	volumes  client.VolumeListResult
	networks client.NetworkListResult
	disk     client.DiskUsageResult
}

func (fake *fakeDockerReader) Ping(context.Context, client.PingOptions) (client.PingResult, error) {
	return fake.ping, fake.pingErr
}

func (fake *fakeDockerReader) Info(context.Context, client.InfoOptions) (client.SystemInfoResult, error) {
	return fake.info, nil
}

func (fake *fakeDockerReader) VolumeList(context.Context, client.VolumeListOptions) (client.VolumeListResult, error) {
	return fake.volumes, nil
}

func (fake *fakeDockerReader) NetworkList(context.Context, client.NetworkListOptions) (client.NetworkListResult, error) {
	return fake.networks, nil
}

func (fake *fakeDockerReader) DiskUsage(context.Context, client.DiskUsageOptions) (client.DiskUsageResult, error) {
	return fake.disk, nil
}
