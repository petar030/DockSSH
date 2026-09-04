package containers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendcontainers "github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
)

type fakeStream[T any] struct {
	values chan T
	done   chan error
	closed chan struct{}
	once   sync.Once
}

func newFakeStream[T any]() *fakeStream[T] {
	return &fakeStream[T]{values: make(chan T, 8), done: make(chan error, 1), closed: make(chan struct{})}
}
func (stream *fakeStream[T]) Values() <-chan T   { return stream.values }
func (stream *fakeStream[T]) Done() <-chan error { return stream.done }
func (stream *fakeStream[T]) Close() error {
	stream.once.Do(func() { close(stream.closed) })
	return nil
}

func TestLogChunksAreAssembledSanitizedAndBounded(t *testing.T) {
	model := Model{logFragments: make(map[backendcontainers.LogSource]string)}
	model.appendLog(backendcontainers.LogEntry{Source: backendcontainers.LogStdout, Data: "hel"})
	model.appendLog(backendcontainers.LogEntry{Source: backendcontainers.LogStdout, Data: "lo\x1b[2J\nnext"})
	if len(model.logLines) != 1 || model.logLines[0] != "[stdout] hello [2J" || model.logFragments[backendcontainers.LogStdout] != "next" {
		t.Fatalf("assembled logs = %#v fragment=%q", model.logLines, model.logFragments[backendcontainers.LogStdout])
	}
	for range maxLogLines + 10 {
		model.appendLog(backendcontainers.LogEntry{Source: backendcontainers.LogStderr, Data: "line\n"})
	}
	if len(model.logLines) != maxLogLines {
		t.Fatalf("log ring length = %d", len(model.logLines))
	}
	if strings.Contains(strings.Join(model.logLines, "\n"), "\x1b") {
		t.Fatal("terminal control reached log history")
	}
}

func TestStreamMessagesAndCloseRespectGeneration(t *testing.T) {
	logs := newFakeStream[backendcontainers.LogEntry]()
	api := &fakeAPI{logs: logs}
	model := New(context.Background(), &fakeBackend{}, api)
	model.active, model.generation, model.pageCtx, model.selectedID = true, 2, context.Background(), "abc"
	model, command := model.openLogs()
	opened := command().(logOpenedMsg)
	model, _ = model.handleStreamMessage(opened)
	current := model.streamGen
	model, _ = model.handleStreamMessage(logValueMsg{generation: 2, streamGen: current, open: true, value: backendcontainers.LogEntry{Source: backendcontainers.LogStdout, Data: "ok\n"}})
	if len(model.logLines) != 1 {
		t.Fatal("current log value was not applied")
	}
	model = model.closeStream()
	select {
	case <-logs.closed:
	default:
		t.Fatal("stream was not closed")
	}
	model, _ = model.handleStreamMessage(logValueMsg{generation: 2, streamGen: current, open: true, value: backendcontainers.LogEntry{Data: "late\n"}})
	if len(model.logLines) != 1 {
		t.Fatal("late stream value was applied")
	}
}

func TestStatsRetainsLatestOnlyAtRenderTick(t *testing.T) {
	model := Model{active: true, generation: 1, streamGen: 5, mode: statsView, statsStream: newFakeStream[backendcontainers.StatsSample]()}
	first := backendcontainers.StatsSample{ContainerID: "a", CPUPercent: 1}
	second := backendcontainers.StatsSample{ContainerID: "a", CPUPercent: 9}
	model, _ = model.handleStreamMessage(statsValueMsg{generation: 1, streamGen: 5, value: first, open: true})
	model, _ = model.handleStreamMessage(statsValueMsg{generation: 1, streamGen: 5, value: second, open: true})
	if model.hasStats {
		t.Fatal("stats rendered before bounded tick")
	}
	model, _ = model.handleStreamMessage(statsTickMsg{generation: 1, streamGen: 5, time: time.Now()})
	if !model.hasStats || model.stats.CPUPercent != 9 {
		t.Fatalf("latest stats not retained: %#v", model.stats)
	}

	conflict := &backend.AppError{Code: backend.ErrorConflict, Operation: "stats"}
	model, _ = model.handleStreamMessage(statsDoneMsg{generation: 1, streamGen: 5, err: conflict, open: true})
	if !errors.Is(model.statsErr, conflict) {
		t.Fatal("stats terminal error not retained")
	}
}
