package containers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func TestLogsDecodesDockerMultiplexedOutput(t *testing.T) {
	docker := &fakeServiceClient{
		inspect: client.ContainerInspectResult{Container: containertypes.InspectResponse{Config: &containertypes.Config{Tty: false}}},
		logs:    io.NopCloser(bytes.NewReader(multiplexedOutput(t, "out\n", "err\n"))),
	}
	service := newTestService(t, docker, &recordingRefresher{}, &recordingPageRequester{})
	stream, err := service.Logs(context.Background(), "abc", LogsOptions{Tail: 10})
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
	sampleTime := time.Unix(1_700_000_000, 0)
	dockerSample := containertypes.StatsResponse{
		ID: "abc", Name: "/demo", Read: sampleTime,
		PreCPUStats: containertypes.CPUStats{
			CPUUsage: containertypes.CPUUsage{TotalUsage: 100}, SystemUsage: 500,
		},
		CPUStats: containertypes.CPUStats{
			CPUUsage: containertypes.CPUUsage{TotalUsage: 200}, SystemUsage: 1_000, OnlineCPUs: 2,
		},
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
	docker := &fakeServiceClient{stats: io.NopCloser(bytes.NewReader(encoded.Bytes()))}
	service := newTestService(t, docker, &recordingRefresher{}, &recordingPageRequester{})
	stream, err := service.Stats(context.Background(), "abc", StatsOptions{OneShot: true})
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

func TestServiceCloseUnblocksAndFinishesActiveStream(t *testing.T) {
	reader, _ := io.Pipe()
	docker := &fakeServiceClient{
		inspect: client.ContainerInspectResult{Container: containertypes.InspectResponse{Config: &containertypes.Config{}}},
		logs:    reader,
	}
	service, err := NewService(docker, &recordingRefresher{}, &recordingPageRequester{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	stream, err := service.Logs(context.Background(), "abc", LogsOptions{Follow: true})
	if err != nil {
		t.Fatalf("open logs: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = service.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("service Close did not unblock the active reader")
	}
	select {
	case err := <-stream.Done():
		if err != nil {
			t.Fatalf("stream shutdown error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not finish after service Close")
	}
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
