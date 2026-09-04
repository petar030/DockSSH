package containers

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	backendcontainers "github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

func (model Model) View() string {
	var content string
	if !model.hasData {
		content = model.spinner.View() + " Loading containers…"
		if model.err != nil {
			content = "Containers unavailable\n\n" + ui.SanitizeLine(model.err.Error()) + "\n\nPress r to retry."
		}
		content = lipgloss.NewStyle().Padding(2, 3).Render(content)
	} else if model.width >= 120 && model.mode != logsView && model.mode != statsView {
		left := model.listPanel(max(model.width*2/5, 42))
		right := model.secondaryPanel(max(model.width-lipgloss.Width(left)-1, 40))
		content = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	} else if model.mode == listView {
		content = model.listPanel(max(model.width, 1))
	} else {
		content = model.secondaryPanel(max(model.width, 1))
	}
	if model.overlay != noOverlay {
		content = model.overlayView()
	}
	return fitView(content, model.width, model.height)
}

func (model Model) listPanel(width int) string {
	visible := model.visible()
	rows := []string{"NAME             ID            IMAGE                 STATE / STATUS"}
	if len(visible) == 0 {
		rows = append(rows, "", "No containers match the current filter.")
	} else {
		limit := max(model.height-5, 1)
		for index, value := range visible {
			if index >= limit {
				rows = append(rows, fmt.Sprintf("… %d more", len(visible)-index))
				break
			}
			marker := " "
			if value.ID == model.selectedID {
				marker = ">"
			}
			state := strings.TrimSpace(value.State + " " + value.Status)
			if value.Health != "" {
				state += " [" + value.Health + "]"
			}
			row := fmt.Sprintf("%s %-15s %-12s %-21s %s", marker,
				ui.Truncate(safe(displayName(value)), 15), safe(shortID(value.ID)),
				ui.Truncate(safe(value.Image), 21), safe(state))
			rows = append(rows, ui.Truncate(row, max(width-4, 1)))
		}
	}
	header := fmt.Sprintf("CONTAINERS (%d/%d)  filter: %s  sort: %s", len(visible), len(model.containers), emptyDash(model.filter), model.sortName())
	return pagePanel(header, strings.Join(rows, "\n"), width)
}

func (model Model) secondaryPanel(width int) string {
	switch model.mode {
	case processesView:
		return model.processesPanel(width)
	case logsView:
		return model.logsPanel(width)
	case statsView:
		return model.statsPanel(width)
	default:
		return model.detailsPanel(width)
	}
}

func (model Model) detailsPanel(width int) string {
	if model.selectedID == "" {
		return pagePanel("DETAILS", "Select a container and press enter.", width)
	}
	if model.detailsState.loading && model.detailsID != model.selectedID {
		return pagePanel("DETAILS", model.spinner.View()+" Loading details…", width)
	}
	if model.detailsState.err != nil && model.detailsID != model.selectedID {
		return pagePanel("DETAILS", "Unavailable: "+safe(model.detailsState.err.Error())+"\nPress enter to retry.", width)
	}
	details := model.details
	if model.detailsID != model.selectedID {
		return pagePanel("DETAILS", "Press enter to load selected container details.", width)
	}
	lines := []string{
		fmt.Sprintf("%s  %s", safe(emptyDash(details.Name)), safe(shortID(details.ID))),
		fmt.Sprintf("State: %s  running=%t paused=%t exit=%d pid=%d", safe(emptyDash(details.State.Status)), details.State.Running, details.State.Paused, details.State.ExitCode, details.State.PID),
		"Image: " + safe(emptyDash(details.Image)),
		"Command: " + safe(strings.TrimSpace(details.Path+" "+strings.Join(details.Args, " "))),
		fmt.Sprintf("Platform: %s  Driver: %s  Restarts: %d", safe(emptyDash(details.Platform)), safe(emptyDash(details.Driver)), details.RestartCount),
		fmt.Sprintf("Host: %s  User: %s  Workdir: %s", safe(emptyDash(details.Hostname)), safe(emptyDash(details.User)), safe(emptyDash(details.WorkingDir))),
		fmt.Sprintf("Mounts: %d  Networks: %d", len(details.Mounts), len(details.Networks)),
	}
	if len(details.Labels) > 0 {
		lines = append(lines, "Labels:")
		keys := make([]string, 0, len(details.Labels))
		for key := range details.Labels {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, "  "+safe(key)+"="+safe(details.Labels[key]))
		}
	}
	if len(details.Environment) > 0 {
		lines = append(lines, "Environment:")
		for _, value := range details.Environment {
			lines = append(lines, "  "+safe(value))
		}
	}
	return pagePanel("DETAILS", model.windowBody(strings.Join(lines, "\n")), width)
}

func (model Model) processesPanel(width int) string {
	if model.processState.loading {
		return pagePanel("PROCESSES", model.spinner.View()+" Loading processes…", width)
	}
	if model.processState.unavailable {
		return pagePanel("PROCESSES", "Unavailable for this container state.\nA stopped container has no process table.\n\nEsc returns to the list.", width)
	}
	if model.processState.err != nil {
		return pagePanel("PROCESSES", "Unavailable: "+safe(model.processState.err.Error()), width)
	}
	if len(model.processes.Rows) == 0 {
		return pagePanel("PROCESSES", "No processes reported.", width)
	}
	rows := []string{strings.Join(model.processes.Titles, "  ")}
	for _, row := range model.processes.Rows {
		rows = append(rows, strings.Join(row, "  "))
	}
	return pagePanel("PROCESSES", model.windowBody(strings.Join(rows, "\n")), width)
}

func (model Model) logsPanel(width int) string {
	lines := append([]string(nil), model.logLines...)
	for _, source := range []backendcontainers.LogSource{backendcontainers.LogStdout, backendcontainers.LogStderr} {
		if fragment := model.logFragments[source]; fragment != "" {
			lines = append(lines, "["+string(source)+"] "+ui.SanitizeLine(fragment))
		}
	}
	if len(lines) == 0 {
		lines = []string{"Waiting for log output…"}
	}
	if model.logErr != nil {
		lines = append(lines, "", "Stream ended: "+safe(model.logErr.Error()))
	}
	return pagePanel("LOGS — "+shortID(model.selectedID), model.windowBody(strings.Join(lines, "\n")), width)
}

func (model Model) statsPanel(width int) string {
	if model.statsErr != nil {
		message := "Stats unavailable: " + safe(model.statsErr.Error())
		if strings.Contains(strings.ToLower(model.statsErr.Error()), "conflict") {
			message = "Stats unavailable for this container state."
		}
		return pagePanel("STATS", message+"\n\nEsc returns to the list.", width)
	}
	if !model.hasStats {
		return pagePanel("STATS", model.spinner.View()+" Waiting for statistics…", width)
	}
	s := model.stats
	body := fmt.Sprintf("%s  %s\n\nCPU: %.2f%%\nMemory: %s / %s (%.2f%%)\nNetwork: rx %s  tx %s\nBlock IO: read %s  write %s\nPIDs: %d\nRead: %s",
		safe(emptyDash(s.Name)), shortID(s.ContainerID), s.CPUPercent,
		ui.FormatBytes(int64(s.MemoryUsage)), ui.FormatBytes(int64(s.MemoryLimit)), s.MemoryPercent,
		ui.FormatBytes(int64(s.NetworkRx)), ui.FormatBytes(int64(s.NetworkTx)),
		ui.FormatBytes(int64(s.BlockRead)), ui.FormatBytes(int64(s.BlockWrite)), s.PIDs,
		s.ReadAt.Format("15:04:05"))
	return pagePanel("STATS", body, width)
}

func (model Model) overlayView() string {
	name := model.selectedName()
	switch model.overlay {
	case filterOverlay:
		return pagePanel("FILTER", "Search words; state:<value>; label:<key>=<value>\n\n> "+safe(model.filterEdit)+"_\n\nenter apply   esc cancel", max(min(model.width, 76), 1))
	case actionsOverlay:
		lines := []string{"Container: " + safe(name), ""}
		for index, action := range actions {
			marker := " "
			if index == model.action {
				marker = ">"
			}
			lines = append(lines, marker+" "+string(action))
		}
		lines = append(lines, "", "enter choose   esc cancel")
		return pagePanel("ACTIONS", strings.Join(lines, "\n"), max(min(model.width, 54), 1))
	case renameOverlay:
		return pagePanel("RENAME "+safe(name), "> "+safe(model.renameEdit)+"_\n\nenter submit   esc cancel", max(min(model.width, 60), 1))
	case confirmOverlay:
		lines := []string{"Container: " + safe(name), ""}
		if model.confirm == commandKill {
			lines = append(lines, "Kill this container immediately?")
		} else {
			lines = append(lines, "Remove this container?", fmt.Sprintf("[f] force: %t", model.force), fmt.Sprintf("[v] remove anonymous volumes: %t", model.volumes))
		}
		lines = append(lines, "", "y/enter confirm   n/esc cancel")
		return pagePanel("CONFIRM "+strings.ToUpper(string(model.confirm)), strings.Join(lines, "\n"), max(min(model.width, 66), 1))
	}
	return ""
}

func pagePanel(title, body string, width int) string {
	innerWidth := max(width-4, 1)
	lines := strings.Split(body, "\n")
	for index := range lines {
		lines[index] = ui.Truncate(lines[index], innerWidth)
	}
	return lipgloss.NewStyle().Width(max(width, 1)).Padding(0, 1).
		Border(lipgloss.RoundedBorder()).BorderForeground(ui.Border).
		Render(lipgloss.NewStyle().Bold(true).Foreground(ui.Primary).Render(ui.Truncate(title, innerWidth)) + "\n" + strings.Join(lines, "\n"))
}

func (model Model) windowBody(body string) string {
	lines := strings.Split(body, "\n")
	limit := max(model.height-3, 1)
	if len(lines) <= limit {
		return body
	}
	start := min(max(model.scroll, 0), len(lines)-limit)
	return strings.Join(lines[start:start+limit], "\n")
}

func fitView(value string, width, height int) string {
	lines := strings.Split(value, "\n")
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	for index := range lines {
		lines[index] = ui.Truncate(lines[index], max(width, 1))
	}
	return strings.Join(lines, "\n")
}

func (model Model) selectedName() string {
	for _, value := range model.containers {
		if value.ID == model.selectedID {
			return displayName(value)
		}
	}
	return shortID(model.selectedID)
}

func (model Model) sortName() string {
	return [...]string{"name", "state", "newest"}[model.sort]
}

func safe(value string) string { return ui.SanitizeLine(value) }

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}
