package containers

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

func TestServiceContainerCommandsCallDockerAndRefreshAffectedViews(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		run       func(*Service) (backend.CommandResult, error)
	}{
		{"start", "start", func(service *Service) (backend.CommandResult, error) {
			return service.Start(context.Background(), "abc")
		}},
		{"stop", "stop", func(service *Service) (backend.CommandResult, error) {
			return service.Stop(context.Background(), "abc", StopOptions{})
		}},
		{"restart", "restart", func(service *Service) (backend.CommandResult, error) {
			return service.Restart(context.Background(), "abc", RestartOptions{})
		}},
		{"pause", "pause", func(service *Service) (backend.CommandResult, error) {
			return service.Pause(context.Background(), "abc")
		}},
		{"unpause", "unpause", func(service *Service) (backend.CommandResult, error) {
			return service.Unpause(context.Background(), "abc")
		}},
		{"kill", "kill", func(service *Service) (backend.CommandResult, error) {
			return service.Kill(context.Background(), "abc", KillOptions{Signal: "SIGTERM"})
		}},
		{"rename", "rename", func(service *Service) (backend.CommandResult, error) {
			return service.Rename(context.Background(), "abc", RenameOptions{Name: "renamed"})
		}},
		{"remove", "remove", func(service *Service) (backend.CommandResult, error) {
			return service.Remove(context.Background(), "abc", RemoveOptions{Force: true})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			docker := &fakeServiceClient{}
			refreshes := &recordingRefresher{}
			requests := &recordingPageRequester{}
			service := newTestService(t, docker, refreshes, requests)
			result, err := test.run(service)
			if err != nil {
				t.Fatalf("command: %v", err)
			}
			if !reflect.DeepEqual(docker.calls, []string{test.operation}) {
				t.Fatalf("Docker calls = %v", docker.calls)
			}
			if len(result.Affected) != 1 || result.Affected[0].ID != "abc" || len(result.RefreshKeys) != 2 {
				t.Fatalf("command result = %#v", result)
			}
			if len(refreshes.calls) != 0 {
				t.Fatalf("command performed synchronous refreshes = %#v", refreshes.calls)
			}
			wantRequests := []pageRequest{
				{backend.PageContainers, backend.RefreshCommand},
				{backend.PageDashboard, backend.RefreshCommand},
			}
			if !reflect.DeepEqual(requests.calls, wantRequests) {
				t.Fatalf("page refresh requests = %#v, want %#v", requests.calls, wantRequests)
			}
		})
	}
}

func TestServiceNormalizesContainerIDForCommandAndRefresh(t *testing.T) {
	docker := &fakeServiceClient{}
	refreshes := &recordingRefresher{}
	service := newTestService(t, docker, refreshes, &recordingPageRequester{})
	result, err := service.Start(context.Background(), " abc ")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.Affected[0].ID != "abc" {
		t.Fatalf("normalized command result = %#v", result)
	}
}

func TestServiceDoesNotRefreshAfterFailedCommand(t *testing.T) {
	docker := &fakeServiceClient{commandErr: errdefs.ErrNotFound}
	refreshes := &recordingRefresher{}
	requests := &recordingPageRequester{}
	service := newTestService(t, docker, refreshes, requests)

	_, err := service.Start(context.Background(), "missing")
	var applicationError *backend.AppError
	if !errors.As(err, &applicationError) || applicationError.Code != backend.ErrorNotFound {
		t.Fatalf("error = %#v", err)
	}
	if len(refreshes.calls) != 0 {
		t.Fatalf("refreshes after failed command = %#v", refreshes.calls)
	}
	if len(requests.calls) != 0 {
		t.Fatalf("page refresh requests after failed command = %#v", requests.calls)
	}
}

func TestSuccessfulCommandRecordsSharedRefreshAfterSessionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	docker := &fakeServiceClient{commandHook: cancel}
	requests := &recordingPageRequester{}
	service := newTestService(t, docker, &recordingRefresher{}, requests)

	if _, err := service.Start(ctx, "abc"); err != nil {
		t.Fatalf("start after Docker success: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("test session context was not canceled")
	}
	want := []pageRequest{
		{backend.PageContainers, backend.RefreshCommand},
		{backend.PageDashboard, backend.RefreshCommand},
	}
	if !reflect.DeepEqual(requests.calls, want) {
		t.Fatalf("backend-owned refresh requests = %#v, want %#v", requests.calls, want)
	}
}

func TestServiceTargetedRefreshesUseContainerID(t *testing.T) {
	refreshes := &recordingRefresher{}
	service := newTestService(t, &fakeServiceClient{}, refreshes, &recordingPageRequester{})
	if err := service.RefreshDetails(context.Background(), " abc "); err != nil {
		t.Fatalf("refresh details: %v", err)
	}
	if err := service.RefreshProcesses(context.Background(), "abc"); err != nil {
		t.Fatalf("refresh processes: %v", err)
	}
	want := []refreshCall{
		{backend.RefreshKey{Kind: RefreshKindDetails, ID: "abc"}, backend.RefreshManual},
		{backend.RefreshKey{Kind: RefreshKindProcesses, ID: "abc"}, backend.RefreshManual},
	}
	if !reflect.DeepEqual(refreshes.calls, want) {
		t.Fatalf("refresh calls = %#v, want %#v", refreshes.calls, want)
	}
}

func TestServiceExecCapturesStdoutStderrAndExitCode(t *testing.T) {
	docker := &fakeServiceClient{execExitCode: 7}
	docker.execOutput = multiplexedOutput(t, "standard output", "standard error")
	service := newTestService(t, docker, &recordingRefresher{}, &recordingPageRequester{})

	result, err := service.Exec(context.Background(), "abc", ExecOptions{Command: []string{"sh", "-c", "demo"}})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if result.ExecID != "exec-one" || result.ExitCode != 7 || result.Stdout != "standard output" || result.Stderr != "standard error" {
		t.Fatalf("exec result = %#v", result)
	}
	if !reflect.DeepEqual(docker.execOptions.Cmd, []string{"sh", "-c", "demo"}) {
		t.Fatalf("exec options = %#v", docker.execOptions)
	}
}

func newTestService(
	t *testing.T,
	docker serviceClient,
	refreshes refresher,
	requests backend.PageRefreshRequester,
) *Service {
	t.Helper()
	service, err := NewService(docker, refreshes, requests)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
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

type refreshCall struct {
	key    backend.RefreshKey
	reason backend.RefreshReason
}

type recordingRefresher struct{ calls []refreshCall }

func (refresher *recordingRefresher) Refresh(_ context.Context, key backend.RefreshKey, reason backend.RefreshReason) (backend.RefreshResult, error) {
	refresher.calls = append(refresher.calls, refreshCall{key, reason})
	return backend.RefreshResult{Key: key}, nil
}

type pageRequest struct {
	page   backend.Page
	reason backend.RefreshReason
}

type recordingPageRequester struct{ calls []pageRequest }

func (requester *recordingPageRequester) RequestPage(page backend.Page, reason backend.RefreshReason) {
	requester.calls = append(requester.calls, pageRequest{page, reason})
}

type fakeServiceClient struct {
	calls        []string
	commandErr   error
	commandHook  func()
	execOutput   []byte
	execExitCode int
	execOptions  client.ExecCreateOptions
	inspect      client.ContainerInspectResult
	logs         io.ReadCloser
	stats        io.ReadCloser
}

func (fake *fakeServiceClient) command(name string) error {
	fake.calls = append(fake.calls, name)
	if fake.commandHook != nil {
		fake.commandHook()
	}
	return fake.commandErr
}

func (fake *fakeServiceClient) ContainerStart(context.Context, string, client.ContainerStartOptions) (client.ContainerStartResult, error) {
	return client.ContainerStartResult{}, fake.command("start")
}
func (fake *fakeServiceClient) ContainerStop(context.Context, string, client.ContainerStopOptions) (client.ContainerStopResult, error) {
	return client.ContainerStopResult{}, fake.command("stop")
}
func (fake *fakeServiceClient) ContainerRestart(context.Context, string, client.ContainerRestartOptions) (client.ContainerRestartResult, error) {
	return client.ContainerRestartResult{}, fake.command("restart")
}
func (fake *fakeServiceClient) ContainerPause(context.Context, string, client.ContainerPauseOptions) (client.ContainerPauseResult, error) {
	return client.ContainerPauseResult{}, fake.command("pause")
}
func (fake *fakeServiceClient) ContainerUnpause(context.Context, string, client.ContainerUnpauseOptions) (client.ContainerUnpauseResult, error) {
	return client.ContainerUnpauseResult{}, fake.command("unpause")
}
func (fake *fakeServiceClient) ContainerKill(context.Context, string, client.ContainerKillOptions) (client.ContainerKillResult, error) {
	return client.ContainerKillResult{}, fake.command("kill")
}
func (fake *fakeServiceClient) ContainerRename(context.Context, string, client.ContainerRenameOptions) (client.ContainerRenameResult, error) {
	return client.ContainerRenameResult{}, fake.command("rename")
}
func (fake *fakeServiceClient) ContainerRemove(context.Context, string, client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	return client.ContainerRemoveResult{}, fake.command("remove")
}
func (fake *fakeServiceClient) ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	return fake.inspect, nil
}
func (fake *fakeServiceClient) ContainerLogs(context.Context, string, client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
	return fake.logs, nil
}
func (fake *fakeServiceClient) ContainerStats(context.Context, string, client.ContainerStatsOptions) (client.ContainerStatsResult, error) {
	return client.ContainerStatsResult{Body: fake.stats}, nil
}
func (fake *fakeServiceClient) ExecCreate(_ context.Context, _ string, options client.ExecCreateOptions) (client.ExecCreateResult, error) {
	fake.execOptions = options
	return client.ExecCreateResult{ID: "exec-one"}, nil
}
func (fake *fakeServiceClient) ExecAttach(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error) {
	reader, writer := net.Pipe()
	go func() {
		_, _ = writer.Write(fake.execOutput)
		_ = writer.Close()
	}()
	return client.ExecAttachResult{HijackedResponse: client.NewHijackedResponse(reader, "application/vnd.docker.multiplexed-stream")}, nil
}
func (fake *fakeServiceClient) ExecInspect(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
	return client.ExecInspectResult{ID: "exec-one", ExitCode: fake.execExitCode}, nil
}

var _ serviceClient = (*fakeServiceClient)(nil)
