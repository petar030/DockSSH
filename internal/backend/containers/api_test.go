package containers

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/netip"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	containertypes "github.com/moby/moby/api/types/container"
	networktypes "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func TestAPIReadsContainerListDetailsAndProcesses(t *testing.T) {
	created := "2026-08-29T10:11:12.123456789Z"
	docker := &fakeDockerClient{
		list: client.ContainerListResult{Items: []containertypes.Summary{{
			ID: "container-one", Names: []string{"/demo"}, Image: "alpine:latest",
			Created: 1_700_000_000, State: containertypes.StateRunning, Status: "Up 2 seconds",
			Ports: []containertypes.PortSummary{{
				IP: netip.MustParseAddr("127.0.0.1"), PrivatePort: 80, PublicPort: 8080, Type: "tcp",
			}},
		}}},
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
	api := newTestAPI(t, docker)

	listPayload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindList})
	if err != nil {
		t.Fatalf("read list: %v", err)
	}
	list := listPayload.(ListUpdated)
	if len(list.Containers) != 1 || list.Containers[0].Names[0] != "demo" || list.Containers[0].Ports[0].IP != "127.0.0.1" {
		t.Fatalf("container list = %#v", list)
	}
	if !docker.listOptions.All {
		t.Fatal("container list did not include stopped containers")
	}

	detailsPayload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindDetails, ID: "container-one"})
	if err != nil {
		t.Fatalf("read details: %v", err)
	}
	details := detailsPayload.(DetailsUpdated).Container
	if details.Name != "demo" || details.Hostname != "demo-host" || details.State.PID != 42 || details.Networks[0].Name != "alpha" {
		t.Fatalf("container details = %#v", details)
	}

	processPayload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindProcesses, ID: "container-one"})
	if err != nil {
		t.Fatalf("read processes: %v", err)
	}
	processes := processPayload.(ProcessesUpdated)
	if processes.ContainerID != "container-one" || processes.Rows[0][1] != "sleep" {
		t.Fatalf("container processes = %#v", processes)
	}
}

func TestAPIRejectsUnsupportedOrMalformedRefreshKeys(t *testing.T) {
	api := newTestAPI(t, &fakeDockerClient{})
	for _, key := range []backend.RefreshKey{
		{Kind: "unknown"},
		{Kind: RefreshKindList, ID: "unexpected"},
		{Kind: RefreshKindDetails},
	} {
		if _, err := api.ReadRefresh(context.Background(), key); err == nil {
			t.Fatalf("ReadRefresh(%#v) succeeded", key)
		}
	}
}

func TestAPICommandsSubmitDockerFunctionsAndAffectedRefreshKeys(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		run       func(*API) (backend.CommandResult, error)
	}{
		{"start", "start", func(api *API) (backend.CommandResult, error) { return api.Start(context.Background(), "abc") }},
		{"stop", "stop", func(api *API) (backend.CommandResult, error) {
			return api.Stop(context.Background(), "abc", StopOptions{})
		}},
		{"restart", "restart", func(api *API) (backend.CommandResult, error) {
			return api.Restart(context.Background(), "abc", RestartOptions{})
		}},
		{"pause", "pause", func(api *API) (backend.CommandResult, error) { return api.Pause(context.Background(), "abc") }},
		{"unpause", "unpause", func(api *API) (backend.CommandResult, error) { return api.Unpause(context.Background(), "abc") }},
		{"kill", "kill", func(api *API) (backend.CommandResult, error) {
			return api.Kill(context.Background(), "abc", KillOptions{Signal: "SIGTERM"})
		}},
		{"rename", "rename", func(api *API) (backend.CommandResult, error) {
			return api.Rename(context.Background(), "abc", RenameOptions{Name: "renamed"})
		}},
		{"remove", "remove", func(api *API) (backend.CommandResult, error) {
			return api.Remove(context.Background(), "abc", RemoveOptions{Force: true})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			docker := &fakeDockerClient{}
			runner := &immediateCommandRunner{}
			api := newTestAPIWithDependencies(t, docker, runner, &recordingRefreshRequester{})
			result, err := test.run(api)
			if err != nil {
				t.Fatalf("command: %v", err)
			}
			if !reflect.DeepEqual(docker.commandCalls, []string{test.operation}) {
				t.Fatalf("Docker calls = %v", docker.commandCalls)
			}
			if len(runner.requests) != 1 || len(runner.requests[0].RefreshKeys) != 2 {
				t.Fatalf("command request = %#v", runner.requests)
			}
			if len(result.Affected) != 1 || result.Affected[0].ID != "abc" {
				t.Fatalf("command result = %#v", result)
			}
		})
	}
}

func TestAPIClassifiesDockerCommandError(t *testing.T) {
	docker := &fakeDockerClient{commandErr: errdefs.ErrNotFound}
	api := newTestAPI(t, docker)
	_, err := api.Start(context.Background(), "missing")
	if !backend.HasErrorCode(err, backend.ErrorNotFound) {
		t.Fatalf("command error = %v", err)
	}
}

func TestAPITargetedRefreshRequestsUseNormalizedID(t *testing.T) {
	refreshes := &recordingRefreshRequester{}
	api := newTestAPIWithDependencies(t, &fakeDockerClient{}, &immediateCommandRunner{}, refreshes)
	if err := api.RequestDetails(" abc "); err != nil {
		t.Fatalf("request details: %v", err)
	}
	if err := api.RequestProcesses("abc"); err != nil {
		t.Fatalf("request processes: %v", err)
	}
	want := []refreshCall{
		{backend.RefreshKey{Kind: RefreshKindDetails, ID: "abc"}, backend.RefreshManual},
		{backend.RefreshKey{Kind: RefreshKindProcesses, ID: "abc"}, backend.RefreshManual},
	}
	if !reflect.DeepEqual(refreshes.calls, want) {
		t.Fatalf("refresh requests = %#v, want %#v", refreshes.calls, want)
	}
}

func TestLogsDecodesDockerMultiplexedOutput(t *testing.T) {
	docker := &fakeDockerClient{
		inspect: client.ContainerInspectResult{Container: containertypes.InspectResponse{Config: &containertypes.Config{Tty: false}}},
		logs:    io.NopCloser(bytes.NewReader(multiplexedOutput(t, "out\n", "err\n"))),
	}
	api := newTestAPI(t, docker)
	stream, err := api.Logs(context.Background(), "abc", LogsOptions{Tail: 10})
	if err != nil {
		t.Fatalf("open logs: %v", err)
	}
	entries := collectStream(t, stream.Values(), stream.Done())
	if len(entries) != 2 || entries[0] != (LogEntry{Source: LogStdout, Data: "out\n"}) ||
		entries[1] != (LogEntry{Source: LogStderr, Data: "err\n"}) {
		t.Fatalf("log entries = %#v", entries)
	}
}

func TestStatsTransformsOneShotDockerSample(t *testing.T) {
	dockerSample := containertypes.StatsResponse{
		ID: "abc", Name: "/demo", Read: time.Unix(1_700_000_000, 0),
		PreCPUStats: containertypes.CPUStats{CPUUsage: containertypes.CPUUsage{TotalUsage: 100}, SystemUsage: 500},
		CPUStats:    containertypes.CPUStats{CPUUsage: containertypes.CPUUsage{TotalUsage: 200}, SystemUsage: 1_000, OnlineCPUs: 2},
		MemoryStats: containertypes.MemoryStats{Usage: 256, Limit: 1_024},
		Networks: map[string]containertypes.NetworkStats{
			"eth0": {RxBytes: 10, TxBytes: 20}, "eth1": {RxBytes: 30, TxBytes: 40},
		},
		BlkioStats: containertypes.BlkioStats{IoServiceBytesRecursive: []containertypes.BlkioStatEntry{
			{Op: "Read", Value: 11}, {Op: "Write", Value: 22},
		}},
		PidsStats: containertypes.PidsStats{Current: 3},
	}
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(dockerSample); err != nil {
		t.Fatalf("encode stats: %v", err)
	}
	docker := &fakeDockerClient{stats: io.NopCloser(bytes.NewReader(encoded.Bytes()))}
	api := newTestAPI(t, docker)
	stream, err := api.Stats(context.Background(), "abc", StatsOptions{OneShot: true})
	if err != nil {
		t.Fatalf("open stats: %v", err)
	}
	samples := collectStream(t, stream.Values(), stream.Done())
	if len(samples) != 1 {
		t.Fatalf("stats samples = %#v", samples)
	}
	sample := samples[0]
	if sample.ContainerID != "abc" || sample.Name != "demo" || sample.CPUPercent != 40 ||
		sample.MemoryPercent != 25 || sample.NetworkRx != 40 || sample.NetworkTx != 60 ||
		sample.BlockRead != 11 || sample.BlockWrite != 22 || sample.PIDs != 3 {
		t.Fatalf("stats sample = %#v", sample)
	}
}

func TestSessionCancellationFinishesActiveStream(t *testing.T) {
	reader, _ := io.Pipe()
	docker := &fakeDockerClient{
		inspect: client.ContainerInspectResult{Container: containertypes.InspectResponse{Config: &containertypes.Config{}}},
		logs:    reader,
	}
	api := newTestAPI(t, docker)
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := api.Logs(ctx, "abc", LogsOptions{Follow: true})
	if err != nil {
		t.Fatalf("open logs: %v", err)
	}
	cancel()
	select {
	case err := <-stream.Done():
		if err != nil {
			t.Fatalf("stream shutdown error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("session cancellation did not finish stream")
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

func newTestAPI(t *testing.T, docker dockerClient) *API {
	t.Helper()
	return newTestAPIWithDependencies(t, docker, &immediateCommandRunner{}, &recordingRefreshRequester{})
}

func newTestAPIWithDependencies(
	t *testing.T,
	docker dockerClient,
	commands backend.CommandRunner,
	refreshes backend.RefreshRequester,
) *API {
	t.Helper()
	api, err := NewAPI(docker, commands, refreshes)
	if err != nil {
		t.Fatalf("new API: %v", err)
	}
	return api
}

type immediateCommandRunner struct {
	requests []backend.CommandRequest
}

func (runner *immediateCommandRunner) Run(_ context.Context, request backend.CommandRequest) (backend.CommandResult, error) {
	runner.requests = append(runner.requests, request)
	err := request.Run(context.Background())
	return backend.CommandResult{
		OperationID: request.OperationID,
		Affected:    append([]backend.AffectedResource(nil), request.Affected...),
		RefreshKeys: append([]backend.RefreshKey(nil), request.RefreshKeys...),
	}, err
}

type refreshCall struct {
	key    backend.RefreshKey
	reason backend.RefreshReason
}

type recordingRefreshRequester struct {
	mu    sync.Mutex
	calls []refreshCall
}

func (requester *recordingRefreshRequester) Request(key backend.RefreshKey, reason backend.RefreshReason) error {
	requester.mu.Lock()
	requester.calls = append(requester.calls, refreshCall{key: key, reason: reason})
	requester.mu.Unlock()
	return nil
}

type fakeDockerClient struct {
	list         client.ContainerListResult
	listOptions  client.ContainerListOptions
	inspect      client.ContainerInspectResult
	top          client.ContainerTopResult
	logs         io.ReadCloser
	stats        io.ReadCloser
	commandCalls []string
	commandErr   error
}

func (fake *fakeDockerClient) command(name string) error {
	fake.commandCalls = append(fake.commandCalls, name)
	return fake.commandErr
}

func (fake *fakeDockerClient) ContainerList(_ context.Context, options client.ContainerListOptions) (client.ContainerListResult, error) {
	fake.listOptions = options
	return fake.list, nil
}
func (fake *fakeDockerClient) ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	return fake.inspect, nil
}
func (fake *fakeDockerClient) ContainerTop(context.Context, string, client.ContainerTopOptions) (client.ContainerTopResult, error) {
	return fake.top, nil
}
func (fake *fakeDockerClient) ContainerStart(context.Context, string, client.ContainerStartOptions) (client.ContainerStartResult, error) {
	return client.ContainerStartResult{}, fake.command("start")
}
func (fake *fakeDockerClient) ContainerStop(context.Context, string, client.ContainerStopOptions) (client.ContainerStopResult, error) {
	return client.ContainerStopResult{}, fake.command("stop")
}
func (fake *fakeDockerClient) ContainerRestart(context.Context, string, client.ContainerRestartOptions) (client.ContainerRestartResult, error) {
	return client.ContainerRestartResult{}, fake.command("restart")
}
func (fake *fakeDockerClient) ContainerPause(context.Context, string, client.ContainerPauseOptions) (client.ContainerPauseResult, error) {
	return client.ContainerPauseResult{}, fake.command("pause")
}
func (fake *fakeDockerClient) ContainerUnpause(context.Context, string, client.ContainerUnpauseOptions) (client.ContainerUnpauseResult, error) {
	return client.ContainerUnpauseResult{}, fake.command("unpause")
}
func (fake *fakeDockerClient) ContainerKill(context.Context, string, client.ContainerKillOptions) (client.ContainerKillResult, error) {
	return client.ContainerKillResult{}, fake.command("kill")
}
func (fake *fakeDockerClient) ContainerRename(context.Context, string, client.ContainerRenameOptions) (client.ContainerRenameResult, error) {
	return client.ContainerRenameResult{}, fake.command("rename")
}
func (fake *fakeDockerClient) ContainerRemove(context.Context, string, client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	return client.ContainerRemoveResult{}, fake.command("remove")
}
func (fake *fakeDockerClient) ContainerLogs(context.Context, string, client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
	return fake.logs, nil
}
func (fake *fakeDockerClient) ContainerStats(context.Context, string, client.ContainerStatsOptions) (client.ContainerStatsResult, error) {
	return client.ContainerStatsResult{Body: fake.stats}, nil
}

func multiplexedOutput(t *testing.T, stdout, stderr string) []byte {
	t.Helper()
	var output bytes.Buffer
	for _, frame := range []struct {
		stream byte
		value  string
	}{{1, stdout}, {2, stderr}} {
		header := make([]byte, 8)
		header[0] = frame.stream
		binary.BigEndian.PutUint32(header[4:], uint32(len(frame.value)))
		if _, err := output.Write(append(header, []byte(frame.value)...)); err != nil {
			t.Fatalf("write stream frame: %v", err)
		}
	}
	return output.Bytes()
}

func collectStream[T any](t *testing.T, values <-chan T, done <-chan error) []T {
	t.Helper()
	var collected []T
	for value := range values {
		collected = append(collected, value)
	}
	err, open := <-done
	if !open {
		t.Fatal("Done closed without a terminal value")
	}
	if err != nil {
		t.Fatalf("stream terminal error: %v", err)
	}
	if _, open := <-done; open {
		t.Fatal("Done remained open after terminal value")
	}
	return collected
}
