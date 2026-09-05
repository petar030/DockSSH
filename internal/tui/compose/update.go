package compose

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendcompose "github.com/petar030/ssh-native-docker-tui/internal/backend/compose"
)

type subscriptionReadyMsg struct {
	generation   uint64
	subscription backend.Subscription
	err          error
}
type requestFinishedMsg struct {
	generation, selectionGen uint64
	key                      backend.RefreshKey
	err                      error
}
type eventReceivedMsg struct {
	generation uint64
	event      backend.EventEnvelope
	open       bool
}
type commandFinishedMsg struct {
	generation uint64
	operation  overlayMode
	err        error
}
type jobStartedMsg struct {
	generation uint64
	operation  overlayMode
	job        backend.Job
	err        error
}

func subscribe(ctx context.Context, common Backend, generation uint64) tea.Cmd {
	return func() tea.Msg {
		if common == nil {
			return subscriptionReadyMsg{generation: generation, err: invalid("subscribe to Compose")}
		}
		subscription, err := common.Subscribe(ctx, backend.PageCompose, backend.EventFilter{Types: []backend.EventType{
			backendcompose.EventProjectsUpdated, backendcompose.EventProjectUpdated,
			backend.EventRefreshFailed, backend.EventSubscriberOverflow,
			backend.EventJobProgressed, backend.EventJobFinished,
		}})
		return subscriptionReadyMsg{generation: generation, subscription: subscription, err: err}
	}
}

func requestBase(common Backend, generation uint64) tea.Cmd {
	return func() tea.Msg {
		var err error
		if common == nil {
			err = invalid("request Compose refresh")
		} else {
			err = common.RequestRefresh(backend.PageCompose)
		}
		return requestFinishedMsg{generation: generation, key: backend.RefreshKey{Kind: backendcompose.RefreshKindList}, err: err}
	}
}

func requestDetails(api API, generation, selectionGen uint64, name string) tea.Cmd {
	return func() tea.Msg {
		var err error
		if api == nil {
			err = invalid("request Compose project details")
		} else {
			err = api.RequestDetails(name)
		}
		return requestFinishedMsg{generation: generation, selectionGen: selectionGen, key: backend.RefreshKey{Kind: backendcompose.RefreshKindDetails, ID: name}, err: err}
	}
}

func waitEvent(subscription backend.Subscription, generation uint64) tea.Cmd {
	return func() tea.Msg {
		event, open := <-subscription.Events()
		return eventReceivedMsg{generation: generation, event: event, open: open}
	}
}

func (m Model) Update(message tea.Msg) (Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(message)
	case subscriptionReadyMsg:
		if !m.current(message.generation) {
			if message.subscription != nil {
				_ = message.subscription.Close()
			}
			return m, nil
		}
		if message.err != nil {
			m.loading, m.stale, m.err = false, m.hasData, message.err
			return m, nil
		}
		m.subscription = message.subscription
		return m, tea.Batch(waitEvent(m.subscription, m.generation), requestBase(m.backend, m.generation), m.spinner.Tick)
	case requestFinishedMsg:
		if !m.current(message.generation) || message.key.ID != "" && message.selectionGen != m.selectionGen {
			return m, nil
		}
		if message.err != nil {
			m.applyRequestError(message.key, message.err)
		}
	case eventReceivedMsg:
		if !m.current(message.generation) {
			return m, nil
		}
		if !message.open {
			m.subscription = nil
			m.loading, m.stale = false, m.hasData
			if m.pageCtx == nil || m.pageCtx.Err() == nil {
				m.err = invalid("receive Compose events")
			}
			return m, nil
		}
		updated, command := m.handleEvent(message.event)
		return updated, tea.Batch(waitEvent(m.subscription, m.generation), command)
	case commandFinishedMsg:
		if !m.current(message.generation) {
			return m, nil
		}
		m.pending = false
		if message.err != nil {
			m.notice = errorText(message.err)
			m.overlay = message.operation
		} else {
			m.notice = ""
			m.overlay = noOverlay
		}
	case jobStartedMsg:
		if !m.current(message.generation) {
			// A job accepted just before a tab switch remains backend-owned. Keep
			// its handle observable in the session-wide tracker even though this
			// page generation is no longer active.
			if message.err == nil && message.job != nil && m.jobs != nil {
				return m, m.jobs.Register(message.job, jobName(message.operation))
			}
			return m, nil
		}
		m.pending = false
		if message.err != nil {
			m.notice = errorText(message.err)
			m.overlay = message.operation
			return m, nil
		}
		if message.job == nil {
			m.notice = "Compose did not return a job handle"
			m.overlay = message.operation
			return m, nil
		}
		m.notice, m.overlay = "", noOverlay
		if m.jobs != nil {
			return m, m.jobs.Register(message.job, jobName(message.operation))
		}
	case logOpenedMsg, logValueMsg, logDoneMsg:
		return m.handleLogMessage(message)
	default:
		if m.loading || m.pending {
			var command tea.Cmd
			m.spinner, command = m.spinner.Update(message)
			return m, command
		}
	}
	return m, nil
}

func (m Model) handleEvent(event backend.EventEnvelope) (Model, tea.Cmd) {
	switch payload := event.Payload.(type) {
	case backendcompose.ProjectsUpdated:
		m.projects = append([]backendcompose.ProjectSummary(nil), payload.Projects...)
		m.hasData, m.loading, m.stale, m.err, m.updatedAt = true, false, false, nil, event.Time
		m.choose()
		if m.selected != "" {
			m.targetLoading = true
			return m, requestDetails(m.api, m.generation, m.selectionGen, m.selected)
		}
	case backendcompose.ProjectUpdated:
		if event.Key.Kind == backendcompose.RefreshKindDetails && event.Key.ID == m.selected && payload.Project.Name == m.selected {
			m.details, m.detailsName, m.targetLoading, m.targetErr, m.detailScroll = payload.Project, payload.Project.Name, false, nil, 0
		}
	case backend.RefreshFailed:
		m.applyRequestError(event.Key, payload.Err)
	case backend.SubscriberOverflow:
		m.loading, m.stale = true, m.hasData
		commands := []tea.Cmd{requestBase(m.backend, m.generation), m.spinner.Tick}
		if m.selected != "" {
			commands = append(commands, requestDetails(m.api, m.generation, m.selectionGen, m.selected))
		}
		return m, tea.Batch(commands...)
	case backend.JobProgressed:
		if m.jobs == nil || !m.jobs.Has(payload.JobID) {
			m.notice = jobProgressText(payload)
		}
	case backend.JobFinished:
		if m.jobs == nil || !m.jobs.Has(payload.Result.JobID) {
			if payload.Err != nil {
				m.notice = errorText(payload.Err)
			} else {
				m.notice = ""
			}
		}
	}
	return m, nil
}

func (m Model) handleKey(key tea.KeyPressMsg) (Model, tea.Cmd) {
	value := key.String()
	if m.pending {
		return m, nil
	}
	if m.overlay == logsOverlay {
		return m.handleLogKey(value)
	}
	if m.overlay != noOverlay {
		return m.handleOverlay(key)
	}
	switch value {
	case "up", "k":
		return m.move(-1)
	case "down", "j":
		return m.move(1)
	case "pgup":
		m.detailScroll = max(m.detailScroll-m.detailRows(), 0)
	case "pgdown":
		m.detailScroll = min(m.detailScroll+m.detailRows(), m.detailMaxScroll())
	case "f":
		m.filterEdit, m.overlay = m.filter, filterOverlay
	case "i", "enter":
		return m.loadDetails()
	case "s":
		return m.runCommand(noOverlay, func(ctx context.Context) (backend.CommandResult, error) {
			return m.api.Start(ctx, m.selected, backendcompose.ServiceOptions{})
		})
	case "p":
		return m.runCommand(noOverlay, func(ctx context.Context) (backend.CommandResult, error) {
			return m.api.Pause(ctx, m.selected, backendcompose.ServiceOptions{})
		})
	case "P":
		return m.runCommand(noOverlay, func(ctx context.Context) (backend.CommandResult, error) {
			return m.api.Unpause(ctx, m.selected, backendcompose.ServiceOptions{})
		})
	case "x":
		m.openOverlay(stopOverlay)
	case "e":
		m.openOverlay(restartOverlay)
	case "c":
		m.openOverlay(scaleOverlay)
	case "u":
		m.openOverlay(upOverlay)
	case "d":
		m.openOverlay(downOverlay)
	case "o":
		m.openOverlay(pullOverlay)
	case "b":
		m.openOverlay(buildOverlay)
	case "l":
		if m.selected != "" {
			return m.openLogs()
		}
	}
	return m, nil
}

func (m *Model) openOverlay(mode overlayMode) {
	if m.selected == "" {
		return
	}
	m.overlay, m.field, m.fields, m.optionA, m.optionB, m.notice = mode, 0, [4]string{}, false, false, ""
	if mode == upOverlay || mode == pullOverlay || mode == buildOverlay || mode == scaleOverlay {
		m.fields[0] = strings.Join(m.projectConfigFiles(), ",")
	}
}

func (m Model) handleOverlay(key tea.KeyPressMsg) (Model, tea.Cmd) {
	value := key.String()
	if value == "esc" {
		m.overlay, m.notice = noOverlay, ""
		return m, nil
	}
	if m.overlay == filterOverlay {
		switch value {
		case "enter":
			m.filter, m.overlay = strings.TrimSpace(m.filterEdit), noOverlay
			m.choose()
			return m.loadDetails()
		case "backspace":
			m.filterEdit = trim(m.filterEdit)
		default:
			if key.Key().Text != "" {
				m.filterEdit += key.Key().Text
			}
		}
		return m, nil
	}
	if value == "tab" {
		m.field = (m.field + 1) % m.fieldCount()
		return m, nil
	}
	if value == "a" && (m.overlay == upOverlay || m.overlay == downOverlay || m.overlay == pullOverlay || m.overlay == buildOverlay) {
		m.optionA = !m.optionA
		return m, nil
	}
	if (value == "v" && m.overlay == downOverlay) ||
		(value == "n" && m.overlay == restartOverlay) ||
		(value == "c" && m.overlay == buildOverlay) {
		m.optionB = !m.optionB
		return m, nil
	}
	if value == "enter" {
		return m.submitOverlay()
	}
	if value == "backspace" {
		m.fields[m.field] = trim(m.fields[m.field])
		return m, nil
	}
	if key.Key().Text != "" {
		m.fields[m.field] += key.Key().Text
	}
	return m, nil
}

func (m Model) submitOverlay() (Model, tea.Cmd) {
	services := csv(m.fields[0])
	switch m.overlay {
	case stopOverlay:
		duration, err := duration(m.fields[1])
		if err != nil {
			return m.refuse(err.Error())
		}
		return m.runCommand(stopOverlay, func(ctx context.Context) (backend.CommandResult, error) {
			return m.api.Stop(ctx, m.selected, backendcompose.StopOptions{Services: services, Timeout: duration})
		})
	case restartOverlay:
		duration, err := duration(m.fields[1])
		if err != nil {
			return m.refuse(err.Error())
		}
		return m.runCommand(restartOverlay, func(ctx context.Context) (backend.CommandResult, error) {
			return m.api.Restart(ctx, m.selected, backendcompose.RestartOptions{Services: services, Timeout: duration, NoDeps: m.optionB})
		})
	case scaleOverlay:
		service := strings.TrimSpace(m.fields[1])
		replicas, err := strconv.Atoi(strings.TrimSpace(m.fields[2]))
		if service == "" || err != nil || replicas < 0 {
			return m.refuse("Service and a non-negative replica count are required")
		}
		spec, err := m.projectSpec(m.fields[0], "")
		if err != nil {
			return m.refuse(err.Error())
		}
		return m.runCommand(scaleOverlay, func(ctx context.Context) (backend.CommandResult, error) {
			return m.api.Scale(ctx, spec, backendcompose.ScaleOptions{Service: service, Replicas: replicas})
		})
	case upOverlay, pullOverlay, buildOverlay:
		spec, err := m.projectSpec(m.fields[0], m.fields[2])
		if err != nil {
			return m.refuse(err.Error())
		}
		services = csv(m.fields[1])
		mode := m.overlay
		return m.startJob(mode, func(ctx context.Context) (backend.Job, error) {
			switch mode {
			case upOverlay:
				return m.api.Up(ctx, spec, backendcompose.UpOptions{Services: services, RemoveOrphans: m.optionA})
			case pullOverlay:
				return m.api.Pull(ctx, spec, backendcompose.PullOptions{Services: services, IgnoreFailures: m.optionA})
			default:
				return m.api.Build(ctx, spec, backendcompose.BuildOptions{Services: services, Pull: m.optionA, NoCache: m.optionB})
			}
		})
	case downOverlay:
		duration, err := duration(m.fields[1])
		if err != nil {
			return m.refuse(err.Error())
		}
		return m.startJob(downOverlay, func(ctx context.Context) (backend.Job, error) {
			return m.api.Down(ctx, m.selected, backendcompose.DownOptions{Services: services, Timeout: duration, RemoveOrphans: m.optionA, Volumes: m.optionB})
		})
	}
	return m, nil
}

func (m Model) runCommand(reopen overlayMode, run func(context.Context) (backend.CommandResult, error)) (Model, tea.Cmd) {
	if m.selected == "" || m.api == nil {
		return m, nil
	}
	m.pending, m.notice, m.overlay = true, "", noOverlay
	generation, ctx := m.generation, m.pageCtx
	return m, tea.Batch(func() tea.Msg {
		_, err := run(ctx)
		return commandFinishedMsg{generation: generation, operation: reopen, err: err}
	}, m.spinner.Tick)
}

func (m Model) startJob(mode overlayMode, start func(context.Context) (backend.Job, error)) (Model, tea.Cmd) {
	if m.api == nil {
		return m, nil
	}
	m.pending, m.notice = true, ""
	generation, ctx := m.generation, m.pageCtx
	return m, tea.Batch(func() tea.Msg {
		job, err := start(ctx)
		return jobStartedMsg{generation: generation, operation: mode, job: job, err: err}
	}, m.spinner.Tick)
}

func (m Model) move(delta int) (Model, tea.Cmd) {
	values := m.visible()
	if len(values) == 0 {
		return m, nil
	}
	index := 0
	for i, value := range values {
		if value.Name == m.selected {
			index = i
			break
		}
	}
	index = max(0, min(index+delta, len(values)-1))
	if values[index].Name == m.selected {
		return m, nil
	}
	m = m.closeLogs()
	m.selected = values[index].Name
	m.selectionGen++
	m.targetLoading, m.targetErr = true, nil
	m.detailScroll = 0
	m.ensureVisible()
	return m, requestDetails(m.api, m.generation, m.selectionGen, m.selected)
}

func (m Model) loadDetails() (Model, tea.Cmd) {
	if m.selected == "" {
		return m, nil
	}
	m.targetLoading, m.targetErr = true, nil
	return m, requestDetails(m.api, m.generation, m.selectionGen, m.selected)
}

func (m Model) projectConfigFiles() []string {
	if m.detailsName == m.selected && len(m.details.ConfigFiles) > 0 {
		return append([]string(nil), m.details.ConfigFiles...)
	}
	return append([]string(nil), m.selectedSummary().ConfigFiles...)
}

func (m Model) projectSpec(files, profiles string) (backendcompose.ProjectSpec, error) {
	configFiles := csv(files)
	if strings.TrimSpace(m.selected) == "" || len(configFiles) == 0 {
		return backendcompose.ProjectSpec{}, fmt.Errorf("Project and at least one Compose file are required")
	}
	return backendcompose.ProjectSpec{Name: m.selected, ConfigFiles: configFiles, Profiles: csv(profiles)}, nil
}

func (m Model) fieldCount() int {
	switch m.overlay {
	case stopOverlay, restartOverlay, downOverlay:
		return 2
	case scaleOverlay, upOverlay, pullOverlay, buildOverlay:
		return 3
	default:
		return 1
	}
}

func (m Model) refuse(value string) (Model, tea.Cmd) { m.notice = value; return m, nil }

func (m *Model) applyRequestError(key backend.RefreshKey, err error) {
	if key.Kind == backendcompose.RefreshKindList {
		m.loading, m.stale, m.err = false, m.hasData, err
	} else if key.Kind == backendcompose.RefreshKindDetails && key.ID == m.selected {
		m.targetLoading, m.targetErr = false, err
	}
}

func csv(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func duration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	result, err := time.ParseDuration(value)
	if err != nil || result < 0 {
		return 0, fmt.Errorf("Timeout must be a duration such as 10s")
	}
	return result, nil
}

func trim(value string) string {
	_, size := utf8.DecodeLastRuneInString(value)
	if size == 0 {
		return value
	}
	return value[:len(value)-size]
}

func invalid(operation string) error {
	return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: operation}
}

func errorText(err error) string {
	for _, item := range []struct {
		code backend.ErrorCode
		text string
	}{
		{backend.ErrorConflict, "Another operation is already running for this project"},
		{backend.ErrorPermissionDenied, "Compose file or operation is not permitted"},
		{backend.ErrorDaemonUnavailable, "Docker is unavailable"},
		{backend.ErrorTimeout, "Operation timed out"},
		{backend.ErrorNotFound, "Compose project or file was not found"},
		{backend.ErrorCanceled, "Operation was canceled"},
		{backend.ErrorInvalidInput, "Invalid Compose input"},
	} {
		if backend.HasErrorCode(err, item.code) {
			return item.text
		}
	}
	return fmt.Sprintf("Compose operation failed: %v", err)
}

func jobProgressText(progress backend.JobProgressed) string {
	text := strings.TrimSpace(progress.Progress.Status)
	if progress.Progress.Message != "" {
		text += " — " + progress.Progress.Message
	}
	return strings.TrimSpace(text)
}

func jobName(mode overlayMode) string {
	switch mode {
	case upOverlay:
		return "Compose up"
	case downOverlay:
		return "Compose down"
	case pullOverlay:
		return "Compose pull"
	case buildOverlay:
		return "Compose build"
	default:
		return "Compose job"
	}
}
