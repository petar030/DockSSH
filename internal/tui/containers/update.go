package containers

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendcontainers "github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
)

type commandKind string

const (
	commandStart   commandKind = "start"
	commandStop    commandKind = "stop"
	commandRestart commandKind = "restart"
	commandPause   commandKind = "pause"
	commandUnpause commandKind = "unpause"
	commandKill    commandKind = "kill"
	commandRename  commandKind = "rename"
	commandRemove  commandKind = "remove"
)

var actions = []commandKind{
	commandStart, commandStop, commandRestart, commandPause,
	commandUnpause, commandKill, commandRename, commandRemove,
}

type subscriptionReadyMsg struct {
	generation   uint64
	subscription backend.Subscription
	err          error
}

type requestFinishedMsg struct {
	generation   uint64
	selectionGen uint64
	target       backend.RefreshKey
	err          error
}

type eventReceivedMsg struct {
	generation uint64
	event      backend.EventEnvelope
	open       bool
}

type commandFinishedMsg struct {
	generation uint64
	id         string
	operation  commandKind
	result     backend.CommandResult
	err        error
}

func subscribe(ctx context.Context, common Backend, generation uint64) tea.Cmd {
	return func() tea.Msg {
		if common == nil {
			return subscriptionReadyMsg{generation: generation, err: invalidUIError("subscribe to Containers")}
		}
		subscription, err := common.Subscribe(ctx, backend.PageContainers, backend.EventFilter{Types: []backend.EventType{
			backendcontainers.EventListUpdated,
			backendcontainers.EventDetailsUpdated,
			backendcontainers.EventProcessesUpdated,
			backend.EventRefreshFailed,
			backend.EventSubscriberOverflow,
		}})
		return subscriptionReadyMsg{generation: generation, subscription: subscription, err: err}
	}
}

func requestBase(common Backend, generation uint64) tea.Cmd {
	return func() tea.Msg {
		var err error
		if common == nil {
			err = invalidUIError("request Containers refresh")
		} else {
			err = common.RequestRefresh(backend.PageContainers)
		}
		return requestFinishedMsg{generation: generation, target: backend.RefreshKey{Kind: backendcontainers.RefreshKindList}, err: err}
	}
}

func requestTarget(api API, generation, selectionGen uint64, key backend.RefreshKey) tea.Cmd {
	return func() tea.Msg {
		var err error
		if api == nil {
			err = invalidUIError("request container data")
		} else if key.Kind == backendcontainers.RefreshKindDetails {
			err = api.RequestDetails(key.ID)
		} else {
			err = api.RequestProcesses(key.ID)
		}
		return requestFinishedMsg{generation: generation, selectionGen: selectionGen, target: key, err: err}
	}
}

func waitForEvent(subscription backend.Subscription, generation uint64) tea.Cmd {
	return func() tea.Msg {
		event, open := <-subscription.Events()
		return eventReceivedMsg{generation: generation, event: event, open: open}
	}
}

func (model Model) Update(message tea.Msg) (Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.KeyPressMsg:
		return model.handleKey(message)
	case subscriptionReadyMsg:
		if !model.current(message.generation) {
			if message.subscription != nil {
				_ = message.subscription.Close()
			}
			return model, nil
		}
		if message.err != nil {
			model.loading, model.stale, model.err = false, model.hasData, message.err
			return model, nil
		}
		model.subscription = message.subscription
		return model, tea.Batch(
			waitForEvent(model.subscription, model.generation),
			requestBase(model.backend, model.generation),
			model.spinner.Tick,
		)
	case requestFinishedMsg:
		if !model.current(message.generation) {
			return model, nil
		}
		if message.target.ID != "" && message.selectionGen != model.selectionGen {
			return model, nil
		}
		if message.err != nil {
			model.applyRequestError(message.target, message.err)
		}
		return model, nil
	case eventReceivedMsg:
		if !model.current(message.generation) {
			return model, nil
		}
		if !message.open {
			model.subscription = nil
			model.loading = false
			model.stale = model.hasData
			if model.pageCtx == nil || model.pageCtx.Err() == nil {
				model.err = &backend.AppError{Code: backend.ErrorStreamClosed, Operation: "receive Containers events"}
			}
			return model, nil
		}
		model, command := model.handleEvent(message.event)
		return model, tea.Batch(waitForEvent(model.subscription, model.generation), command)
	case commandFinishedMsg:
		if !model.current(message.generation) || model.pendingOperation != string(message.operation) {
			return model, nil
		}
		model.pendingOperation = ""
		if message.err != nil {
			model.notice = commandErrorText(message.operation, message.err)
			model.stale = model.hasData
			if backend.HasErrorCode(message.err, backend.ErrorNotFound) && message.id == model.selectedID {
				model.clearSelection()
				return model, requestBase(model.backend, model.generation)
			}
			return model, nil
		}
		model.notice = fmt.Sprintf("%s accepted; waiting for authoritative refresh", message.operation)
		return model, nil
	case logOpenedMsg, logValueMsg, logDoneMsg, statsOpenedMsg, statsValueMsg, statsDoneMsg, statsTickMsg:
		return model.handleStreamMessage(message)
	default:
		if model.loading {
			var command tea.Cmd
			model.spinner, command = model.spinner.Update(message)
			return model, command
		}
		return model, nil
	}
}

func (model Model) handleEvent(event backend.EventEnvelope) (Model, tea.Cmd) {
	switch payload := event.Payload.(type) {
	case backendcontainers.ListUpdated:
		model.containers = append([]backendcontainers.Summary(nil), payload.Containers...)
		model.hasData, model.loading, model.stale, model.err = true, false, false, nil
		model.updatedAt = event.Time
		if !model.selectionExists() {
			model.clearSelection()
		}
		model.ensureVisibleSelection()
		return model, model.refreshVisibleTarget()
	case backendcontainers.DetailsUpdated:
		if event.Key.Kind != backendcontainers.RefreshKindDetails || event.Key.ID != model.selectedID || payload.Container.ID != model.selectedID {
			return model, nil
		}
		model.details, model.detailsID = payload.Container, payload.Container.ID
		model.detailsState = panelState{}
	case backendcontainers.ProcessesUpdated:
		if event.Key.Kind != backendcontainers.RefreshKindProcesses || event.Key.ID != model.selectedID || payload.ContainerID != model.selectedID {
			return model, nil
		}
		model.processes = payload
		model.processState = panelState{}
	case backend.RefreshFailed:
		model.applyRefreshFailure(event.Key, payload.Err)
	case backend.SubscriberOverflow:
		model.loading, model.stale = true, model.hasData
		commands := []tea.Cmd{requestBase(model.backend, model.generation), model.spinner.Tick}
		if target := model.visibleTargetKey(); target.Valid() {
			commands = append(commands, requestTarget(model.api, model.generation, model.selectionGen, target))
		}
		return model, tea.Batch(commands...)
	}
	return model, nil
}

func (model Model) handleKey(message tea.KeyPressMsg) (Model, tea.Cmd) {
	key := message.String()
	if model.overlay == filterOverlay || model.overlay == renameOverlay {
		return model.handleEditorKey(message)
	}
	if model.overlay == actionsOverlay {
		switch key {
		case "esc":
			model.overlay = noOverlay
		case "up", "k":
			model.action = (model.action - 1 + len(actions)) % len(actions)
		case "down", "j":
			model.action = (model.action + 1) % len(actions)
		case "enter":
			return model.chooseAction(actions[model.action])
		}
		return model, nil
	}
	if model.overlay == confirmOverlay {
		switch key {
		case "esc", "n":
			model.overlay = noOverlay
		case "f":
			if model.confirm == commandRemove {
				model.force = !model.force
			}
		case "v":
			if model.confirm == commandRemove {
				model.volumes = !model.volumes
			}
		case "y", "enter":
			model.overlay = noOverlay
			return model.startCommand(model.confirm, "")
		}
		return model, nil
	}

	switch key {
	case "up", "k":
		if model.mode == listView {
			model.moveSelection(-1)
		} else {
			model.scroll = max(model.scroll-1, 0)
		}
	case "down", "j":
		if model.mode == listView {
			model.moveSelection(1)
		} else {
			model.scroll++
		}
	case "enter":
		if model.selectedID != "" {
			return model.openDetails()
		}
	case "esc":
		if model.mode != listView {
			model = model.closeStream()
			model.mode = listView
		}
	case "f":
		model.filterEdit, model.overlay = model.filter, filterOverlay
	case "o":
		model.sort = (model.sort + 1) % 3
		model.ensureVisibleSelection()
	case "p":
		if model.selectedID != "" {
			model = model.closeStream()
			model.mode = processesView
			model.scroll = 0
			model.selectionGen++
			model.processState = panelState{loading: true}
			return model, requestTarget(model.api, model.generation, model.selectionGen, backend.RefreshKey{Kind: backendcontainers.RefreshKindProcesses, ID: model.selectedID})
		}
	case "l":
		if model.selectedID != "" {
			return model.openLogs()
		}
	case "s":
		if model.selectedID != "" {
			return model.openStats()
		}
	case "a":
		if model.selectedID != "" && model.pendingOperation == "" {
			model.overlay, model.action = actionsOverlay, 0
		}
	}
	return model, nil
}

func (model Model) handleEditorKey(message tea.KeyPressMsg) (Model, tea.Cmd) {
	key := message.String()
	if key == "esc" {
		model.overlay = noOverlay
		return model, nil
	}
	if key == "enter" {
		if model.overlay == filterOverlay {
			model.filter, model.overlay = strings.TrimSpace(model.filterEdit), noOverlay
			model.ensureVisibleSelection()
			return model, nil
		}
		if strings.TrimSpace(model.renameEdit) == "" {
			model.notice = "Container name cannot be blank"
			return model, nil
		}
		name := model.renameEdit
		model.overlay = noOverlay
		return model.startCommand(commandRename, name)
	}
	if key == "backspace" {
		if model.overlay == filterOverlay {
			model.filterEdit = trimLastRune(model.filterEdit)
		} else {
			model.renameEdit = trimLastRune(model.renameEdit)
		}
		return model, nil
	}
	if message.Key().Text != "" {
		if model.overlay == filterOverlay {
			model.filterEdit += message.Key().Text
		} else {
			model.renameEdit += message.Key().Text
		}
	}
	return model, nil
}

func (model Model) chooseAction(action commandKind) (Model, tea.Cmd) {
	model.overlay = noOverlay
	switch action {
	case commandRename:
		model.renameEdit, model.overlay = "", renameOverlay
		return model, nil
	case commandKill, commandRemove:
		model.confirm, model.overlay = action, confirmOverlay
		model.force, model.volumes = false, false
		return model, nil
	default:
		return model.startCommand(action, "")
	}
}

func (model Model) startCommand(operation commandKind, rename string) (Model, tea.Cmd) {
	if model.api == nil || model.selectedID == "" || model.pendingOperation != "" {
		return model, nil
	}
	model.pendingOperation = string(operation)
	model.notice = ""
	id, generation, ctx := model.selectedID, model.generation, model.pageCtx
	force, volumes := model.force, model.volumes
	return model, func() tea.Msg {
		var result backend.CommandResult
		var err error
		switch operation {
		case commandStart:
			result, err = model.api.Start(ctx, id)
		case commandStop:
			result, err = model.api.Stop(ctx, id, backendcontainers.StopOptions{})
		case commandRestart:
			result, err = model.api.Restart(ctx, id, backendcontainers.RestartOptions{})
		case commandPause:
			result, err = model.api.Pause(ctx, id)
		case commandUnpause:
			result, err = model.api.Unpause(ctx, id)
		case commandKill:
			result, err = model.api.Kill(ctx, id, backendcontainers.KillOptions{})
		case commandRename:
			result, err = model.api.Rename(ctx, id, backendcontainers.RenameOptions{Name: rename})
		case commandRemove:
			result, err = model.api.Remove(ctx, id, backendcontainers.RemoveOptions{Force: force, RemoveVolumes: volumes})
		}
		return commandFinishedMsg{generation: generation, id: id, operation: operation, result: result, err: err}
	}
}

func (model Model) openDetails() (Model, tea.Cmd) {
	model = model.closeStream()
	model.mode = detailsView
	model.scroll = 0
	model.selectionGen++
	model.detailsState = panelState{loading: true, stale: model.detailsID == model.selectedID}
	return model, requestTarget(model.api, model.generation, model.selectionGen, backend.RefreshKey{Kind: backendcontainers.RefreshKindDetails, ID: model.selectedID})
}

func (model Model) refreshVisibleTarget() tea.Cmd {
	key := model.visibleTargetKey()
	if !key.Valid() {
		return nil
	}
	return requestTarget(model.api, model.generation, model.selectionGen, key)
}

func (model Model) visibleTargetKey() backend.RefreshKey {
	switch model.mode {
	case detailsView:
		return backend.RefreshKey{Kind: backendcontainers.RefreshKindDetails, ID: model.selectedID}
	case processesView:
		return backend.RefreshKey{Kind: backendcontainers.RefreshKindProcesses, ID: model.selectedID}
	default:
		return backend.RefreshKey{}
	}
}

func (model *Model) applyRequestError(key backend.RefreshKey, err error) {
	if key.Kind == backendcontainers.RefreshKindList {
		model.loading, model.stale, model.err = false, model.hasData, err
	} else if key.Kind == backendcontainers.RefreshKindDetails && key.ID == model.selectedID {
		model.detailsState.loading, model.detailsState.stale, model.detailsState.err = false, model.detailsID == key.ID, err
	} else if key.Kind == backendcontainers.RefreshKindProcesses && key.ID == model.selectedID {
		model.processState.loading, model.processState.stale, model.processState.err = false, len(model.processes.Rows) > 0, err
		model.processState.unavailable = backend.HasErrorCode(err, backend.ErrorConflict)
	}
}

func (model *Model) applyRefreshFailure(key backend.RefreshKey, err error) {
	model.applyRequestError(key, err)
}

func (model *Model) moveSelection(delta int) {
	visible := model.visible()
	if len(visible) == 0 {
		model.clearSelection()
		return
	}
	index := 0
	for i := range visible {
		if visible[i].ID == model.selectedID {
			index = i
			break
		}
	}
	index = max(0, min(len(visible)-1, index+delta))
	if model.selectedID != visible[index].ID {
		model.selectedID = visible[index].ID
		model.selectionGen++
		model.detailsState, model.processState = panelState{}, panelState{}
		model = model.closeStreamPointer()
	}
}

func (model *Model) ensureVisibleSelection() {
	visible := model.visible()
	for _, value := range visible {
		if value.ID == model.selectedID {
			return
		}
	}
	model.clearSelection()
	if len(visible) > 0 {
		model.selectedID = visible[0].ID
	}
}

func (model Model) selectionExists() bool {
	if model.selectedID == "" {
		return false
	}
	for _, value := range model.containers {
		if value.ID == model.selectedID {
			return true
		}
	}
	return false
}

func (model *Model) clearSelection() {
	model.closeStreamPointer()
	model.selectedID = ""
	model.selectionGen++
	model.mode = listView
	model.details, model.detailsID = backendcontainers.Details{}, ""
	model.processes = backendcontainers.ProcessesUpdated{}
	model.detailsState, model.processState = panelState{}, panelState{}
}

func (model Model) current(generation uint64) bool {
	return model.active && model.generation == generation
}

func invalidUIError(operation string) error {
	return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: operation}
}

func commandErrorText(operation commandKind, err error) string {
	suffix := "failed"
	for _, item := range []struct {
		code backend.ErrorCode
		text string
	}{
		{backend.ErrorPermissionDenied, "permission denied"},
		{backend.ErrorDaemonUnavailable, "Docker unavailable"},
		{backend.ErrorTimeout, "timed out; final state is uncertain"},
		{backend.ErrorConflict, "conflicts with current container state"},
		{backend.ErrorNotFound, "container no longer exists"},
		{backend.ErrorCanceled, "canceled while waiting; accepted work may continue"},
		{backend.ErrorUnsupported, "unsupported"},
		{backend.ErrorInvalidInput, "invalid input"},
	} {
		if backend.HasErrorCode(err, item.code) {
			suffix = item.text
			break
		}
	}
	return fmt.Sprintf("%s: %s", operation, suffix)
}

func trimLastRune(value string) string {
	_, size := utf8.DecodeLastRuneInString(value)
	if size == 0 {
		return value
	}
	return value[:len(value)-size]
}
