package containers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
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

func TestLogFilterMatchesRetainedLinesCaseInsensitively(t *testing.T) {
	model := Model{logLines: []string{"[stdout] INFO ready", "[stderr] DEBUG retry", "[stdout] debug cache"}, logFilter: "debug"}
	lines := model.logDisplayLines()
	if len(lines) != 2 || !strings.Contains(lines[0], "DEBUG") || !strings.Contains(lines[1], "debug") {
		t.Fatalf("filtered lines = %#v", lines)
	}
}

func TestWaitLogBatchCoalescesBufferedEntries(t *testing.T) {
	stream := newFakeStream[backendcontainers.LogEntry]()
	stream.values <- backendcontainers.LogEntry{Data: "one\n"}
	stream.values <- backendcontainers.LogEntry{Data: "two\n"}
	stream.values <- backendcontainers.LogEntry{Data: "three\n"}
	close(stream.values)

	message := waitLogBatch(stream, 4, 7)().(logBatchMsg)
	if message.generation != 4 || message.streamGen != 7 {
		t.Fatalf("batch identity = generation %d stream %d", message.generation, message.streamGen)
	}
	if message.open {
		t.Fatal("closed values channel was reported as open")
	}
	if len(message.values) != 3 {
		t.Fatalf("batch length = %d, want 3", len(message.values))
	}
}

func TestFilteredScrollOnlyAccountsForMatchingEvictedLines(t *testing.T) {
	model := Model{logFilter: "keep", scroll: 7}
	for index := range maxLogLines {
		model.logLines = append(model.logLines, fmt.Sprintf("keep-%d", index))
	}
	model.logLines = append([]string{"discarded", "keep-old"}, model.logLines...)

	model.appendLog(backendcontainers.LogEntry{})

	if model.scroll != 6 {
		t.Fatalf("filtered scroll = %d, want 6 after one matching retained line was evicted", model.scroll)
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
	model, _ = model.handleStreamMessage(logBatchMsg{generation: 2, streamGen: current, open: true, values: []backendcontainers.LogEntry{{Source: backendcontainers.LogStdout, Data: "ok\n"}}})
	if len(model.logLines) != 1 {
		t.Fatal("current log value was not applied")
	}
	model = model.closeStream()
	select {
	case <-logs.closed:
	default:
		t.Fatal("stream was not closed")
	}
	model, _ = model.handleStreamMessage(logBatchMsg{generation: 2, streamGen: current, open: true, values: []backendcontainers.LogEntry{{Data: "late\n"}}})
	if len(model.logLines) != 1 {
		t.Fatal("late stream value was applied")
	}
}

func TestLogsOpenAsScrollableFloatingOverlay(t *testing.T) {
	model := New(context.Background(), &fakeBackend{}, &fakeAPI{}).SetSize(140, 28)
	model.active, model.hasData, model.generation, model.pageCtx = true, true, 1, context.Background()
	model.selectedID = "abc"
	model.containers = []backendcontainers.Summary{{ID: "abc", Names: []string{"demo"}}}
	model.detailsID = "abc"
	model, _ = model.openLogs()
	if model.overlay != logsOverlay || model.mode != logsView || !model.logFollowing {
		t.Fatalf("logs did not open as following overlay: overlay=%d mode=%d follow=%v", model.overlay, model.mode, model.logFollowing)
	}
	for index := range 60 {
		model.appendLog(backendcontainers.LogEntry{Source: backendcontainers.LogStdout, Data: fmt.Sprintf("line-%02d\n", index)})
	}
	bottom := model.scroll
	model, _ = model.handleKey(keyPress("k"))
	if model.scroll != bottom-1 || model.logFollowing {
		t.Fatalf("k did not scroll away from follow mode: scroll=%d bottom=%d follow=%v", model.scroll, bottom, model.logFollowing)
	}
	model.appendLog(backendcontainers.LogEntry{Source: backendcontainers.LogStdout, Data: "new-line\n"})
	if model.scroll != bottom-1 {
		t.Fatalf("new log forced a scrolled viewer to bottom: scroll=%d", model.scroll)
	}
	view := model.View()
	for _, want := range []string{"CONTAINERS", "LOGS — demo", "j/k scroll", "PgUp/PgDn", "esc close"} {
		if !strings.Contains(view, want) {
			t.Fatalf("floating logs missing %q:\n%s", want, view)
		}
	}
	if width, height := lipgloss.Width(view), lipgloss.Height(view); width > 140 || height > 28 {
		t.Fatalf("floating logs are %dx%d, want at most 140x28", width, height)
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
