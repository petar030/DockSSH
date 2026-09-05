package images

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendimages "github.com/petar030/ssh-native-docker-tui/internal/backend/images"
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
	id         string
	reopen     overlayMode
	err        error
}
type pullStartedMsg struct {
	generation uint64
	job        backend.Job
	err        error
}

func subscribe(ctx context.Context, common Backend, generation uint64) tea.Cmd {
	return func() tea.Msg {
		if common == nil {
			return subscriptionReadyMsg{generation: generation, err: invalid("subscribe to Images")}
		}
		sub, err := common.Subscribe(ctx, backend.PageImages, backend.EventFilter{Types: []backend.EventType{
			backendimages.EventListUpdated, backendimages.EventDetailsUpdated, backendimages.EventHistoryUpdated,
			backend.EventRefreshFailed, backend.EventSubscriberOverflow, backend.EventJobProgressed, backend.EventJobFinished,
		}})
		return subscriptionReadyMsg{generation: generation, subscription: sub, err: err}
	}
}
func requestBase(common Backend, generation uint64) tea.Cmd {
	return func() tea.Msg {
		return requestFinishedMsg{generation: generation, key: backend.RefreshKey{Kind: backendimages.RefreshKindList}, err: common.RequestRefresh(backend.PageImages)}
	}
}
func requestTarget(api API, generation, selectionGen uint64, key backend.RefreshKey) tea.Cmd {
	return func() tea.Msg {
		var err error
		if api == nil {
			err = invalid("request image data")
		} else if key.Kind == backendimages.RefreshKindHistory {
			err = api.RequestHistory(key.ID)
		} else {
			err = api.RequestDetails(key.ID)
		}
		return requestFinishedMsg{generation: generation, selectionGen: selectionGen, key: key, err: err}
	}
}
func waitEvent(sub backend.Subscription, generation uint64) tea.Cmd {
	return func() tea.Msg {
		event, open := <-sub.Events()
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
		return model, tea.Batch(waitEvent(model.subscription, model.generation), requestBase(model.backend, model.generation), model.spinner.Tick)
	case requestFinishedMsg:
		if !model.current(message.generation) || message.key.ID != "" && message.selectionGen != model.selectionGen {
			return model, nil
		}
		if message.err != nil {
			model.requestError(message.key, message.err)
		}
	case eventReceivedMsg:
		if !model.current(message.generation) {
			return model, nil
		}
		if !message.open {
			model.subscription = nil
			model.loading, model.stale = false, model.hasData
			if model.pageCtx == nil || model.pageCtx.Err() == nil {
				model.err = invalid("receive Images events")
			}
			return model, nil
		}
		model, command := model.handleEvent(message.event)
		return model, tea.Batch(waitEvent(model.subscription, model.generation), command)
	case commandFinishedMsg:
		if !model.current(message.generation) {
			return model, nil
		}
		model.pending = false
		if message.err != nil {
			model.notice = commandErrorText(message.reopen, message.err)
			if backend.HasErrorCode(message.err, backend.ErrorInvalidInput) {
				model.overlay = message.reopen
			}
			if backend.HasErrorCode(message.err, backend.ErrorNotFound) && message.id == model.selected {
				model.clearSelection()
				return model, requestBase(model.backend, model.generation)
			}
		} else {
			model.notice = ""
		}
	case pullStartedMsg:
		if !model.current(message.generation) {
			return model, nil
		}
		model.pending = false
		if message.err != nil {
			model.notice = errorText(message.err)
			model.overlay = pullOverlay
			return model, nil
		}
		model.notice, model.overlay = "", noOverlay
		if model.jobs != nil {
			return model, model.jobs.Register(message.job, "pull image")
		}
	default:
		if model.loading || model.pending {
			var command tea.Cmd
			model.spinner, command = model.spinner.Update(message)
			return model, command
		}
	}
	return model, nil
}

func (model Model) handleEvent(event backend.EventEnvelope) (Model, tea.Cmd) {
	switch payload := event.Payload.(type) {
	case backendimages.ListUpdated:
		model.images = append([]backendimages.Summary(nil), payload.Images...)
		model.hasData, model.loading, model.stale, model.err, model.updatedAt = true, false, false, nil, event.Time
		if !model.selectionExists() {
			model.clearSelection()
		}
		if model.selected == "" && len(model.visible()) > 0 {
			model.selected = model.visible()[0].ID
			model.selectionGen++
		}
		model.ensureSelectionVisible()
		if model.selected != "" {
			model.detailsState = panelState{loading: true}
			return model, requestTarget(model.api, model.generation, model.selectionGen, backend.RefreshKey{Kind: backendimages.RefreshKindDetails, ID: model.selected})
		}
	case backendimages.DetailsUpdated:
		if event.Key.Kind == backendimages.RefreshKindDetails && event.Key.ID == model.selected && payload.Image.ID == model.selected {
			model.details, model.detailsID, model.detailsState = payload.Image, payload.Image.ID, panelState{}
		}
	case backendimages.HistoryUpdated:
		if event.Key.Kind == backendimages.RefreshKindHistory && event.Key.ID == model.selected && payload.ImageID == model.selected {
			model.history, model.historyState = payload, panelState{}
		}
	case backend.RefreshFailed:
		model.requestError(event.Key, payload.Err)
	case backend.SubscriberOverflow:
		model.loading, model.stale = true, model.hasData
		commands := []tea.Cmd{requestBase(model.backend, model.generation), model.spinner.Tick}
		if model.selected != "" {
			kind := backendimages.RefreshKindDetails
			if model.panel == historyPanel {
				kind = backendimages.RefreshKindHistory
			}
			commands = append(commands, requestTarget(model.api, model.generation, model.selectionGen, backend.RefreshKey{Kind: kind, ID: model.selected}))
		}
		return model, tea.Batch(commands...)
	case backend.JobProgressed:
		if model.jobs == nil || !model.jobs.Has(payload.JobID) {
			model.remoteJob = payload
		}
	case backend.JobFinished:
		if model.jobs == nil || !model.jobs.Has(payload.Result.JobID) {
			if payload.Err != nil {
				model.notice = errorText(payload.Err)
			} else {
				model.remoteJob = backend.JobProgressed{}
			}
		}
	}
	return model, nil
}

func (model Model) handleKey(message tea.KeyPressMsg) (Model, tea.Cmd) {
	key := message.String()
	if model.pending {
		return model, nil
	}
	if model.overlay != noOverlay {
		return model.handleOverlay(message)
	}
	switch key {
	case "up", "k":
		return model.move(-1)
	case "down", "j":
		return model.move(1)
	case "f":
		model.filterEdit, model.overlay = model.filter, filterOverlay
	case "o":
		model.sort = (model.sort + 1) % 3
		model.ensureSelection()
		return model.loadDetails()
	case "v":
		model.dangling++
		if model.dangling > 1 {
			model.dangling = -1
		}
		model.ensureSelection()
		return model.loadDetails()
	case "i", "enter":
		return model.loadDetails()
	case "h":
		if model.selected != "" {
			model.panel, model.historyState = historyPanel, panelState{loading: true}
			return model, requestTarget(model.api, model.generation, model.selectionGen, backend.RefreshKey{Kind: backendimages.RefreshKindHistory, ID: model.selected})
		}
	case "t":
		if model.selected != "" && !model.pending {
			model.overlay, model.editPrimary = tagOverlay, ""
		}
	case "d":
		if model.selected != "" && !model.pending {
			model.overlay, model.removeForce, model.removeParents = removeOverlay, false, false
		}
	case "p":
		if !model.pending {
			model.overlay, model.field, model.editPrimary, model.editSecondary = pruneOverlay, 0, "", ""
		}
	case "u":
		if !model.pending {
			model.overlay, model.field, model.editPrimary, model.editSecondary = pullOverlay, 0, "", ""
		}
	case "esc":
		model.panel = detailsPanel
	}
	return model, nil
}

func (model Model) handleOverlay(message tea.KeyPressMsg) (Model, tea.Cmd) {
	key := message.String()
	if key == "esc" {
		model.overlay, model.notice = noOverlay, ""
		return model, nil
	}
	if model.overlay == removeOverlay {
		switch key {
		case "f":
			model.removeForce = !model.removeForce
		case "c":
			model.removeParents = !model.removeParents
		case "n":
			model.overlay = noOverlay
		case "y", "enter":
			model.overlay = noOverlay
			return model.runCommand(model.selected, removeOverlay, func(ctx context.Context) (backend.CommandResult, error) {
				return model.api.Remove(ctx, model.selected, backendimages.RemoveOptions{Force: model.removeForce, PruneChildren: model.removeParents})
			})
		}
		return model, nil
	}
	if key == "tab" {
		if model.overlay == pullOverlay || model.overlay == pruneOverlay {
			model.field = (model.field + 1) % 2
		}
		return model, nil
	}
	if key == "enter" {
		switch model.overlay {
		case filterOverlay:
			model.filter = strings.TrimSpace(model.filterEdit)
			model.overlay = noOverlay
			model.ensureSelection()
			return model.loadDetails()
		case tagOverlay:
			if strings.TrimSpace(model.editPrimary) == "" {
				model.notice = "Tag reference is required"
				return model, nil
			}
			ref := model.editPrimary
			model.overlay = noOverlay
			return model.runCommand(model.selected, tagOverlay, func(ctx context.Context) (backend.CommandResult, error) {
				return model.api.Tag(ctx, model.selected, backendimages.TagOptions{Reference: ref})
			})
		case pruneOverlay:
			labels, err := pruneLabel(model.editSecondary)
			if err != nil {
				model.notice = err.Error()
				return model, nil
			}
			dangling := true
			opts := backendimages.PruneOptions{Dangling: &dangling, Until: strings.TrimSpace(model.editPrimary), Labels: labels}
			model.overlay = noOverlay
			return model.runCommand("", pruneOverlay, func(ctx context.Context) (backend.CommandResult, error) { return model.api.Prune(ctx, opts) })
		case pullOverlay:
			if strings.TrimSpace(model.editPrimary) == "" {
				model.notice = "Image reference is required"
				return model, nil
			}
			if !validPlatform(model.editSecondary) {
				model.notice = "Platform must be os/arch[/variant]"
				return model, nil
			}
			ref, platform := strings.TrimSpace(model.editPrimary), strings.TrimSpace(model.editSecondary)
			model.pending = true
			model.notice = ""
			generation, ctx, api := model.generation, model.pageCtx, model.api
			return model, tea.Batch(func() tea.Msg {
				job, err := api.Pull(ctx, ref, backendimages.PullOptions{Platform: platform})
				return pullStartedMsg{generation: generation, job: job, err: err}
			}, model.spinner.Tick)
		}
	}
	if key == "backspace" {
		model.setEdit(trimRune(model.currentEdit()))
		return model, nil
	}
	if message.Key().Text != "" {
		model.setEdit(model.currentEdit() + message.Key().Text)
	}
	return model, nil
}

func (model Model) runCommand(id string, reopen overlayMode, run func(context.Context) (backend.CommandResult, error)) (Model, tea.Cmd) {
	if model.pending || model.api == nil {
		return model, nil
	}
	model.pending = true
	model.notice = ""
	generation, ctx := model.generation, model.pageCtx
	return model, tea.Batch(func() tea.Msg {
		_, err := run(ctx)
		return commandFinishedMsg{generation: generation, id: id, reopen: reopen, err: err}
	}, model.spinner.Tick)
}
func (model Model) move(delta int) (Model, tea.Cmd) {
	values := model.visible()
	if len(values) == 0 {
		return model, nil
	}
	index := 0
	for i := range values {
		if values[i].ID == model.selected {
			index = i
			break
		}
	}
	index = max(0, min(len(values)-1, index+delta))
	if values[index].ID == model.selected {
		return model, nil
	}
	model.selected = values[index].ID
	model.selectionGen++
	model.panel = detailsPanel
	model.detailsState = panelState{loading: true}
	model.ensureSelectionVisible()
	return model, requestTarget(model.api, model.generation, model.selectionGen, backend.RefreshKey{Kind: backendimages.RefreshKindDetails, ID: model.selected})
}
func (model *Model) ensureSelection() {
	values := model.visible()
	for _, v := range values {
		if v.ID == model.selected {
			model.ensureSelectionVisible()
			return
		}
	}
	model.clearSelection()
	if len(values) > 0 {
		model.selected = values[0].ID
		model.selectionGen++
	}
	model.ensureSelectionVisible()
}
func (model Model) loadDetails() (Model, tea.Cmd) {
	if model.selected == "" {
		return model, nil
	}
	model.panel = detailsPanel
	model.detailsState = panelState{loading: true, stale: model.detailsID == model.selected}
	return model, requestTarget(model.api, model.generation, model.selectionGen, backend.RefreshKey{Kind: backendimages.RefreshKindDetails, ID: model.selected})
}
func (model *Model) clearSelection() {
	model.selected = ""
	model.listStart = 0
	model.selectionGen++
	model.details = backendimages.Details{}
	model.detailsID = ""
	model.history = backendimages.HistoryUpdated{}
	model.detailsState = panelState{}
	model.historyState = panelState{}
	model.panel = detailsPanel
}
func (model Model) selectionExists() bool {
	for _, v := range model.images {
		if v.ID == model.selected {
			return true
		}
	}
	return false
}
func (model Model) current(g uint64) bool { return model.active && model.generation == g }
func (model *Model) requestError(key backend.RefreshKey, err error) {
	if key.Kind == backendimages.RefreshKindList {
		model.loading, model.stale, model.err = false, model.hasData, err
	} else if key.ID == model.selected && key.Kind == backendimages.RefreshKindDetails {
		model.detailsState = panelState{stale: model.detailsID == key.ID, err: err}
	} else if key.ID == model.selected && key.Kind == backendimages.RefreshKindHistory {
		model.historyState = panelState{stale: model.history.ImageID == key.ID, err: err}
	}
}
func (model Model) currentEdit() string {
	if model.overlay == filterOverlay {
		return model.filterEdit
	}
	if model.overlay == pruneOverlay {
		if model.field == 0 {
			return model.editPrimary
		}
		return model.editSecondary
	}
	if model.overlay == pullOverlay && model.field == 1 {
		return model.editSecondary
	}
	return model.editPrimary
}
func (model *Model) setEdit(v string) {
	if model.overlay == filterOverlay {
		model.filterEdit = v
	} else if model.overlay == pruneOverlay {
		if model.field == 0 {
			model.editPrimary = v
		} else {
			model.editSecondary = v
		}
	} else if model.overlay == pullOverlay && model.field == 1 {
		model.editSecondary = v
	} else {
		model.editPrimary = v
	}
}
func trimRune(v string) string {
	_, size := utf8.DecodeLastRuneInString(v)
	if size == 0 {
		return v
	}
	return v[:len(v)-size]
}
func pruneLabel(value string) (map[string]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	key, item, found := strings.Cut(value, "=")
	key = strings.TrimSpace(key)
	if !found || key == "" {
		return nil, fmt.Errorf("label must use key=value")
	}
	return map[string]string{key: strings.TrimSpace(item)}, nil
}
func validPlatform(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	parts := strings.Split(value, "/")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return false
		}
	}
	return true
}
func invalid(op string) error {
	return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: op}
}
func errorText(err error) string {
	for _, item := range []struct {
		code backend.ErrorCode
		text string
	}{{backend.ErrorPermissionDenied, "permission denied"}, {backend.ErrorDaemonUnavailable, "Docker unavailable"}, {backend.ErrorTimeout, "timed out"}, {backend.ErrorConflict, "operation conflicts with current state"}, {backend.ErrorNotFound, "image no longer exists"}, {backend.ErrorCanceled, "canceled while waiting"}, {backend.ErrorInvalidInput, "invalid input"}} {
		if backend.HasErrorCode(err, item.code) {
			return item.text
		}
	}
	return fmt.Sprintf("operation failed: %v", err)
}

func commandErrorText(operation overlayMode, err error) string {
	if operation == removeOverlay && backend.HasErrorCode(err, backend.ErrorConflict) {
		return "Image is still used by a container; stop/remove the container or retry with force"
	}
	return errorText(err)
}
