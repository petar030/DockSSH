package containers

import (
	"context"
	"net/netip"
	"testing"
	"time"

	containertypes "github.com/moby/moby/api/types/container"
	networktypes "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func TestLoaderLoadsContainerList(t *testing.T) {
	docker := &fakeLoaderClient{list: client.ContainerListResult{Items: []containertypes.Summary{{
		ID: "container-one", Names: []string{"/demo"}, Image: "alpine:latest",
		Created: 1_700_000_000, State: containertypes.StateRunning, Status: "Up 2 seconds",
		Ports: []containertypes.PortSummary{{
			IP: netip.MustParseAddr("127.0.0.1"), PrivatePort: 80, PublicPort: 8080, Type: "tcp",
		}},
	}}}}
	loader := newTestLoader(t, docker)

	payload, err := loader.Load(context.Background(), backend.RefreshKey{Kind: RefreshKindList})
	if err != nil {
		t.Fatalf("load list: %v", err)
	}
	update, ok := payload.(ListUpdated)
	if !ok || len(update.Containers) != 1 {
		t.Fatalf("list payload = %#v", payload)
	}
	container := update.Containers[0]
	if container.ID != "container-one" || container.Names[0] != "demo" || container.State != "running" {
		t.Fatalf("container summary = %#v", container)
	}
	if len(container.Ports) != 1 || container.Ports[0].IP != "127.0.0.1" {
		t.Fatalf("container ports = %#v", container.Ports)
	}
	if !docker.listOptions.All {
		t.Fatal("container list did not include stopped containers")
	}
}

func TestLoaderLoadsDetailsAndProcesses(t *testing.T) {
	created := "2026-08-29T10:11:12.123456789Z"
	docker := &fakeLoaderClient{
		inspect: client.ContainerInspectResult{Container: containertypes.InspectResponse{
			ID: "container-one", Name: "/demo", Created: created, Image: "sha256:image",
			Config: &containertypes.Config{
				Hostname: "demo-host", User: "1000", WorkingDir: "/work",
				Env: []string{"MODE=test"}, Labels: map[string]string{"project": "demo"},
			},
			State: &containertypes.State{Status: containertypes.StateRunning, Running: true, Pid: 42},
			NetworkSettings: &containertypes.NetworkSettings{Networks: map[string]*networktypes.EndpointSettings{
				"zeta":  {IPAddress: netip.MustParseAddr("172.18.0.3")},
				"alpha": {IPAddress: netip.MustParseAddr("172.18.0.2")},
			}},
		}},
		top: client.ContainerTopResult{Titles: []string{"PID", "CMD"}, Processes: [][]string{{"42", "sleep"}}},
	}
	loader := newTestLoader(t, docker)

	detailsPayload, err := loader.Load(context.Background(), backend.RefreshKey{Kind: RefreshKindDetails, ID: "container-one"})
	if err != nil {
		t.Fatalf("load details: %v", err)
	}
	details := detailsPayload.(DetailsUpdated).Container
	if details.Name != "demo" || details.Hostname != "demo-host" || details.State.PID != 42 {
		t.Fatalf("container details = %#v", details)
	}
	wantCreated, _ := time.Parse(time.RFC3339Nano, created)
	if !details.Created.Equal(wantCreated) || len(details.Networks) != 2 || details.Networks[0].Name != "alpha" {
		t.Fatalf("container details metadata = %#v", details)
	}

	processPayload, err := loader.Load(context.Background(), backend.RefreshKey{Kind: RefreshKindProcesses, ID: "container-one"})
	if err != nil {
		t.Fatalf("load processes: %v", err)
	}
	processes := processPayload.(ProcessesUpdated)
	if processes.ContainerID != "container-one" || processes.Rows[0][1] != "sleep" {
		t.Fatalf("container processes = %#v", processes)
	}
}

func TestContainerPayloadsAreDeepCopied(t *testing.T) {
	list := ListUpdated{Containers: []Summary{{Names: []string{"original"}, Labels: map[string]string{"key": "value"}}}}
	clone := list.CloneEventPayload().(ListUpdated)
	list.Containers[0].Names[0] = "changed"
	list.Containers[0].Labels["key"] = "changed"
	if clone.Containers[0].Names[0] != "original" || clone.Containers[0].Labels["key"] != "value" {
		t.Fatalf("cloned payload changed = %#v", clone)
	}
}

func TestLoaderRejectsUnsupportedOrMalformedRefreshKeys(t *testing.T) {
	loader := newTestLoader(t, &fakeLoaderClient{})
	for _, key := range []backend.RefreshKey{
		{Kind: "unknown"},
		{Kind: RefreshKindList, ID: "unexpected"},
		{Kind: RefreshKindDetails},
	} {
		if _, err := loader.Load(context.Background(), key); err == nil {
			t.Fatalf("Load(%#v) succeeded", key)
		}
	}
}

func newTestLoader(t *testing.T, docker loaderClient) *Loader {
	t.Helper()
	loader, err := NewLoader(docker)
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	return loader
}

type fakeLoaderClient struct {
	list        client.ContainerListResult
	listOptions client.ContainerListOptions
	inspect     client.ContainerInspectResult
	top         client.ContainerTopResult
}

func (fake *fakeLoaderClient) ContainerList(_ context.Context, options client.ContainerListOptions) (client.ContainerListResult, error) {
	fake.listOptions = options
	return fake.list, nil
}

func (fake *fakeLoaderClient) ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	return fake.inspect, nil
}

func (fake *fakeLoaderClient) ContainerTop(context.Context, string, client.ContainerTopOptions) (client.ContainerTopResult, error) {
	return fake.top, nil
}
