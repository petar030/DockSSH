package containers

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendcontainers "github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

type logOpenedMsg struct {
	generation uint64
	streamGen  uint64
	stream     backend.Stream[backendcontainers.LogEntry]
	err        error
}

type logValueMsg struct {
	generation uint64
	streamGen  uint64
	value      backendcontainers.LogEntry
	open       bool
}

type logDoneMsg struct {
	generation uint64
	streamGen  uint64
	err        error
	open       bool
}

type statsOpenedMsg struct {
	generation uint64
	streamGen  uint64
	stream     backend.Stream[backendcontainers.StatsSample]
	err        error
}

type statsValueMsg struct {
	generation uint64
	streamGen  uint64
	value      backendcontainers.StatsSample
	open       bool
}

type statsDoneMsg struct {
	generation uint64
	streamGen  uint64
	err        error
	open       bool
}

type statsTickMsg struct {
	generation uint64
	streamGen  uint64
	time       time.Time
}

func (model Model) openLogs() (Model, tea.Cmd) {
	model = model.closeStream()
	model.mode = logsView
	model.overlay = logsOverlay
	model.scroll = 0
	model.logFollowing = true
	model.streamGen++
	model.logLines = nil
	model.logFragments = make(map[backendcontainers.LogSource]string)
	model.logErr = nil
	model.logFilter, model.logFilterEdit, model.logFiltering = "", "", false
	streamCtx, cancel := context.WithCancel(model.pageCtx)
	model.streamCancel = cancel
	generation, streamGeneration, id, api := model.generation, model.streamGen, model.selectedID, model.api
	return model, func() tea.Msg {
		if api == nil {
			return logOpenedMsg{generation: generation, streamGen: streamGeneration, err: invalidUIError("open container logs")}
		}
		stream, err := api.Logs(streamCtx, id, backendcontainers.LogsOptions{Follow: true, Tail: 200, Timestamps: true})
		return logOpenedMsg{generation: generation, streamGen: streamGeneration, stream: stream, err: err}
	}
}

func (model Model) openStats() (Model, tea.Cmd) {
	model = model.closeStream()
	model.mode = statsView
	model.scroll = 0
	model.streamGen++
	model.statsErr, model.hasStats, model.statsDirty = nil, false, false
	streamCtx, cancel := context.WithCancel(model.pageCtx)
	model.streamCancel = cancel
	generation, streamGeneration, id, api := model.generation, model.streamGen, model.selectedID, model.api
	return model, func() tea.Msg {
		if api == nil {
			return statsOpenedMsg{generation: generation, streamGen: streamGeneration, err: invalidUIError("open container stats")}
		}
		stream, err := api.Stats(streamCtx, id, backendcontainers.StatsOptions{})
		return statsOpenedMsg{generation: generation, streamGen: streamGeneration, stream: stream, err: err}
	}
}

func (model Model) handleStreamMessage(message tea.Msg) (Model, tea.Cmd) {
	switch message := message.(type) {
	case logOpenedMsg:
		if !model.currentStream(message.generation, message.streamGen, logsView) {
			if message.stream != nil {
				_ = message.stream.Close()
			}
			return model, nil
		}
		if message.err != nil {
			model.logErr = message.err
			return model, nil
		}
		if message.stream == nil {
			model.logErr = invalidUIError("open container logs")
			return model, nil
		}
		model.logStream = message.stream
		return model, tea.Batch(waitLogValue(message.stream, model.generation, model.streamGen), waitLogDone(message.stream, model.generation, model.streamGen))
	case logValueMsg:
		if !model.currentStream(message.generation, message.streamGen, logsView) {
			return model, nil
		}
		if !message.open {
			return model, nil
		}
		model.appendLog(message.value)
		if model.logStream != nil {
			return model, waitLogValue(model.logStream, model.generation, model.streamGen)
		}
		return model, nil
	case logDoneMsg:
		if !model.currentStream(message.generation, message.streamGen, logsView) {
			return model, nil
		}
		model.flushLogFragments()
		model.logErr = message.err
		model.logStream = nil
		return model, nil
	case statsOpenedMsg:
		if !model.currentStream(message.generation, message.streamGen, statsView) {
			if message.stream != nil {
				_ = message.stream.Close()
			}
			return model, nil
		}
		if message.err != nil {
			model.statsErr = message.err
			return model, nil
		}
		if message.stream == nil {
			model.statsErr = invalidUIError("open container stats")
			return model, nil
		}
		model.statsStream = message.stream
		return model, tea.Batch(waitStatsValue(message.stream, model.generation, model.streamGen), waitStatsDone(message.stream, model.generation, model.streamGen), statsTick(model.generation, model.streamGen))
	case statsValueMsg:
		if !model.currentStream(message.generation, message.streamGen, statsView) || !message.open {
			return model, nil
		}
		model.pendingStats, model.statsDirty = message.value, true
		if model.statsStream != nil {
			return model, waitStatsValue(model.statsStream, model.generation, model.streamGen)
		}
		return model, nil
	case statsDoneMsg:
		if !model.currentStream(message.generation, message.streamGen, statsView) {
			return model, nil
		}
		model.statsErr = message.err
		model.statsStream = nil
		return model, nil
	case statsTickMsg:
		if !model.currentStream(message.generation, message.streamGen, statsView) {
			return model, nil
		}
		if model.statsDirty {
			model.stats, model.hasStats, model.statsDirty = model.pendingStats, true, false
		}
		if model.statsStream != nil {
			return model, statsTick(model.generation, model.streamGen)
		}
	}
	return model, nil
}

func waitLogValue(stream backend.Stream[backendcontainers.LogEntry], generation, streamGen uint64) tea.Cmd {
	return func() tea.Msg {
		value, open := <-stream.Values()
		return logValueMsg{generation: generation, streamGen: streamGen, value: value, open: open}
	}
}

func waitLogDone(stream backend.Stream[backendcontainers.LogEntry], generation, streamGen uint64) tea.Cmd {
	return func() tea.Msg {
		err, open := <-stream.Done()
		return logDoneMsg{generation: generation, streamGen: streamGen, err: err, open: open}
	}
}

func waitStatsValue(stream backend.Stream[backendcontainers.StatsSample], generation, streamGen uint64) tea.Cmd {
	return func() tea.Msg {
		value, open := <-stream.Values()
		return statsValueMsg{generation: generation, streamGen: streamGen, value: value, open: open}
	}
}

func waitStatsDone(stream backend.Stream[backendcontainers.StatsSample], generation, streamGen uint64) tea.Cmd {
	return func() tea.Msg {
		err, open := <-stream.Done()
		return statsDoneMsg{generation: generation, streamGen: streamGen, err: err, open: open}
	}
}

func statsTick(generation, streamGen uint64) tea.Cmd {
	return tea.Tick(statsCadence, func(now time.Time) tea.Msg {
		return statsTickMsg{generation: generation, streamGen: streamGen, time: now}
	})
}

func (model *Model) appendLog(entry backendcontainers.LogEntry) {
	if model.logFragments == nil {
		model.logFragments = make(map[backendcontainers.LogSource]string)
	}
	value := strings.ReplaceAll(entry.Data, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	parts := strings.Split(model.logFragments[entry.Source]+value, "\n")
	model.logFragments[entry.Source] = parts[len(parts)-1]
	prefix := "[stdout] "
	if entry.Source == backendcontainers.LogStderr {
		prefix = "[stderr] "
	}
	for _, line := range parts[:len(parts)-1] {
		model.logLines = append(model.logLines, prefix+ui.SanitizeLine(line))
	}
	if excess := len(model.logLines) - maxLogLines; excess > 0 {
		model.logLines = append([]string(nil), model.logLines[excess:]...)
		if !model.logFollowing {
			model.scroll = max(model.scroll-excess, 0)
		}
	}
	if model.logFollowing {
		model.scroll = model.logMaxScroll()
	}
}

func (model *Model) flushLogFragments() {
	for _, source := range []backendcontainers.LogSource{backendcontainers.LogStdout, backendcontainers.LogStderr} {
		if value := model.logFragments[source]; value != "" {
			prefix := "[stdout] "
			if source == backendcontainers.LogStderr {
				prefix = "[stderr] "
			}
			model.logLines = append(model.logLines, prefix+ui.SanitizeLine(value))
		}
	}
	model.logFragments = nil
}

func (model Model) closeStream() Model {
	return *model.closeStreamPointer()
}

func (model *Model) closeStreamPointer() *Model {
	model.streamGen++
	if model.streamCancel != nil {
		model.streamCancel()
	}
	if model.logStream != nil {
		_ = model.logStream.Close()
	}
	if model.statsStream != nil {
		_ = model.statsStream.Close()
	}
	model.streamCancel, model.logStream, model.statsStream = nil, nil, nil
	return model
}

func (model Model) currentStream(generation, streamGen uint64, mode viewMode) bool {
	return model.current(generation) && model.streamGen == streamGen && model.mode == mode
}

func (model Model) logViewportRows() int {
	overlayHeight := max(min(model.height-4, 30), 10)
	return max(overlayHeight-5, 1)
}

func (model Model) logMaxScroll() int {
	return max(len(model.logDisplayLines())-model.logViewportRows(), 0)
}

func (model *Model) scrollLogs(delta int) {
	model.scroll = max(0, min(model.logMaxScroll(), model.scroll+delta))
	model.logFollowing = model.scroll == model.logMaxScroll()
}
