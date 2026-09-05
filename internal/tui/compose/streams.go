package compose

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendcompose "github.com/petar030/ssh-native-docker-tui/internal/backend/compose"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

type logOpenedMsg struct {
	generation, streamGen uint64
	stream                backend.Stream[backendcompose.LogEntry]
	err                   error
}
type logBatchMsg struct {
	generation, streamGen uint64
	values                []backendcompose.LogEntry
	open                  bool
}
type logDoneMsg struct {
	generation, streamGen uint64
	err                   error
	open                  bool
}

func (m Model) openLogs() (Model, tea.Cmd) {
	m = m.closeLogs()
	m.overlay, m.logFollowing, m.logScroll, m.logErr = logsOverlay, true, 0, nil
	m.streamGen++
	m.logLines = nil
	m.logFragments = make(map[logFragmentKey]string)
	m.logFilter, m.logFilterEdit, m.logFiltering = "", "", false
	streamCtx, cancel := context.WithCancel(m.pageCtx)
	m.streamCancel = cancel
	generation, streamGeneration, project, api := m.generation, m.streamGen, m.selected, m.api
	return m, func() tea.Msg {
		if api == nil {
			return logOpenedMsg{generation: generation, streamGen: streamGeneration, err: invalid("open Compose logs")}
		}
		stream, err := api.Logs(streamCtx, project, backendcompose.LogsOptions{Follow: true, Tail: 200, Timestamps: true})
		return logOpenedMsg{generation: generation, streamGen: streamGeneration, stream: stream, err: err}
	}
}

func (m Model) handleLogMessage(message tea.Msg) (Model, tea.Cmd) {
	switch message := message.(type) {
	case logOpenedMsg:
		if !m.currentLog(message.generation, message.streamGen) {
			if message.stream != nil {
				_ = message.stream.Close()
			}
			return m, nil
		}
		if message.err != nil {
			m.logErr = message.err
			return m, nil
		}
		if message.stream == nil {
			m.logErr = invalid("open Compose logs")
			return m, nil
		}
		m.logStream = message.stream
		return m, tea.Batch(waitLogBatch(message.stream, m.generation, m.streamGen), waitLogDone(message.stream, m.generation, m.streamGen))
	case logBatchMsg:
		if !m.currentLog(message.generation, message.streamGen) {
			return m, nil
		}
		for _, value := range message.values {
			m.appendLog(value)
		}
		if !message.open {
			return m, nil
		}
		return m, waitLogBatch(m.logStream, m.generation, m.streamGen)
	case logDoneMsg:
		if !m.currentLog(message.generation, message.streamGen) {
			return m, nil
		}
		m.flushLogFragments()
		m.logStream, m.logErr = nil, message.err
	}
	return m, nil
}

func waitLogBatch(stream backend.Stream[backendcompose.LogEntry], generation, streamGen uint64) tea.Cmd {
	return func() tea.Msg {
		value, open := <-stream.Values()
		if !open {
			return logBatchMsg{generation: generation, streamGen: streamGen, open: false}
		}
		values := []backendcompose.LogEntry{value}
		timer := time.NewTimer(40 * time.Millisecond)
		defer timer.Stop()
		for len(values) < 256 {
			select {
			case value, open = <-stream.Values():
				if !open {
					return logBatchMsg{generation: generation, streamGen: streamGen, values: values, open: false}
				}
				values = append(values, value)
			case <-timer.C:
				return logBatchMsg{generation: generation, streamGen: streamGen, values: values, open: true}
			}
		}
		return logBatchMsg{generation: generation, streamGen: streamGen, values: values, open: true}
	}
}

func waitLogDone(stream backend.Stream[backendcompose.LogEntry], generation, streamGen uint64) tea.Cmd {
	return func() tea.Msg {
		err, open := <-stream.Done()
		return logDoneMsg{generation: generation, streamGen: streamGen, err: err, open: open}
	}
}

func (m *Model) appendLog(entry backendcompose.LogEntry) {
	if m.logFragments == nil {
		m.logFragments = make(map[logFragmentKey]string)
	}
	key := logFragmentKey{container: entry.Container, source: entry.Source}
	prefix := "[" + ui.SanitizeLine(entry.Container) + "] "
	if entry.Source != "" {
		prefix += "[" + ui.SanitizeLine(string(entry.Source)) + "] "
	}
	value := strings.ReplaceAll(entry.Data, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	parts := strings.Split(m.logFragments[key]+value, "\n")
	m.logFragments[key] = parts[len(parts)-1]
	for _, line := range parts[:len(parts)-1] {
		m.logLines = append(m.logLines, prefix+ui.SanitizeLine(line))
	}
	m.boundLogs()
}

func (m *Model) flushLogFragments() {
	for key, value := range m.logFragments {
		if value == "" {
			continue
		}
		prefix := "[" + ui.SanitizeLine(key.container) + "] [" + ui.SanitizeLine(string(key.source)) + "] "
		m.logLines = append(m.logLines, prefix+ui.SanitizeLine(value))
	}
	m.logFragments = nil
	m.boundLogs()
}

func (m *Model) boundLogs() {
	if excess := len(m.logLines) - maxLogLines; excess > 0 {
		droppedVisible := excess
		if filter := strings.ToLower(strings.TrimSpace(m.logFilter)); filter != "" {
			droppedVisible = 0
			for _, line := range m.logLines[:excess] {
				if strings.Contains(strings.ToLower(line), filter) {
					droppedVisible++
				}
			}
		}
		m.logLines = append([]string(nil), m.logLines[excess:]...)
		if !m.logFollowing {
			m.logScroll = max(m.logScroll-droppedVisible, 0)
		}
	}
	if m.logFollowing {
		m.logScroll = m.logMaxScroll()
	}
}

func (m Model) handleLogKey(message tea.KeyPressMsg) (Model, tea.Cmd) {
	key := message.String()
	if m.logFiltering {
		switch key {
		case "esc":
			m.logFiltering = false
		case "enter":
			m.logFilter = strings.TrimSpace(m.logFilterEdit)
			m.logFiltering = false
			m.logScroll, m.logFollowing = m.logMaxScroll(), true
		case "backspace":
			m.logFilterEdit = trim(m.logFilterEdit)
		default:
			if text := message.Key().Text; text != "" {
				m.logFilterEdit += text
			}
		}
		return m, nil
	}
	switch key {
	case "f":
		m.logFilterEdit, m.logFiltering = m.logFilter, true
	case "up", "k":
		m.scrollLogs(-1)
	case "down", "j":
		m.scrollLogs(1)
	case "pgup":
		m.scrollLogs(-m.logViewportRows())
	case "pgdown":
		m.scrollLogs(m.logViewportRows())
	case "g":
		m.logScroll, m.logFollowing = 0, false
	case "G":
		m.logScroll, m.logFollowing = m.logMaxScroll(), true
	case "esc", "l":
		m = m.closeLogs()
		m.overlay = noOverlay
	}
	return m, nil
}

func (m Model) closeLogs() Model {
	m.streamGen++
	if m.streamCancel != nil {
		m.streamCancel()
	}
	if m.logStream != nil {
		_ = m.logStream.Close()
	}
	m.streamCancel, m.logStream = nil, nil
	return m
}

func (m Model) currentLog(generation, streamGen uint64) bool {
	return m.current(generation) && m.streamGen == streamGen && m.overlay == logsOverlay
}
func (m Model) logViewportRows() int { return max(min(m.height-9, 28), 5) }
func (m Model) logMaxScroll() int    { return max(len(m.filteredLogLines())-m.logViewportRows(), 0) }
func (m *Model) scrollLogs(delta int) {
	m.logScroll = max(0, min(m.logMaxScroll(), m.logScroll+delta))
	m.logFollowing = m.logScroll == m.logMaxScroll()
}
