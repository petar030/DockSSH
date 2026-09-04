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
			content = "Containers unavailable\n\n" + ui.ErrorNotice(model.err.Error(), max(model.width-6, 1)) + "\n\nPress r to retry."
		}
		content = lipgloss.NewStyle().Padding(2, 3).Render(content)
	} else if model.width >= 110 {
		left := model.listPanel(max(model.width*2/3, 70))
		rightWidth := max(model.width-lipgloss.Width(left)-1, 36)
		right := model.secondaryPanel(rightWidth)
		content = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	} else {
		listHeight := max(model.height/2, 8)
		list := fitView(model.listPanel(max(model.width, 1)), model.width, listHeight)
		secondary := model.secondaryPanel(max(model.width, 1))
		content = lipgloss.JoinVertical(lipgloss.Left, list, secondary)
	}
	if model.overlay != noOverlay {
		content = model.overlayView()
	}
	return fitView(content, model.width, model.height)
}

func (model Model) listPanel(width int) string {
	visible := model.visible()
	inner := max(width-4, 1)
	nameWidth := max(inner*17/100, 10)
	imageWidth := max(inner*21/100, 12)
	stateWidth := 10
	healthWidth := 10
	projectWidth := max(inner*13/100, 8)
	portsWidth := max(inner-3-nameWidth-imageWidth-stateWidth-healthWidth-projectWidth-6, 8)
	row := func(marker, name, image, state, health, ports, project string) string {
		return marker + " " + cell(name, nameWidth) + " " + cell(image, imageWidth) + " " +
			cell(state, stateWidth) + " " + cell(health, healthWidth) + " " +
			cell(ports, portsWidth) + " " + cell(project, projectWidth)
	}
	rows := make([]string, 0, 5)
	if feedback := model.feedback(); feedback != "" {
		rows = append(rows, ui.ErrorNotice(feedback, inner), "")
	}
	rows = append(rows,
		"Search: "+cell(emptyDash(model.filter), max(inner-25, 8))+"  Sort: "+model.sortName(),
		"",
		lipgloss.NewStyle().Foreground(ui.Primary).Render(row(" ", "NAME", "IMAGE", "STATE", "HEALTH", "PORTS", "PROJECT")),
	)
	if len(visible) == 0 {
		rows = append(rows, "", "No containers match the current filter.")
	} else {
		limit := max(model.height-8, 1)
		for index, value := range visible {
			if index >= limit {
				rows = append(rows, fmt.Sprintf("… %d more", len(visible)-index))
				break
			}
			marker := stateMarker(value.State)
			if value.ID == model.selectedID {
				marker = ">"
			}
			ports := make([]string, 0, len(value.Ports))
			for _, port := range value.Ports {
				ports = append(ports, formatPort(port))
			}
			line := row(marker, safe(displayName(value)), safe(value.Image), safe(value.State),
				safe(emptyDash(value.Health)), safe(strings.Join(ports, ", ")), safe(composeProject(value.Labels)))
			if value.ID == model.selectedID {
				line = lipgloss.NewStyle().Foreground(lipgloss.Color("#EAF6FF")).Background(lipgloss.Color("#10365C")).Render(line)
			} else {
				line = semanticColor(value.State, value.Health).Render(marker) + " " +
					cell(displayName(value), nameWidth) + " " + cell(value.Image, imageWidth) + " " +
					semanticColor(value.State, "").Render(cell(value.State, stateWidth)) + " " +
					semanticColor("", value.Health).Render(cell(emptyDash(value.Health), healthWidth)) + " " +
					cell(strings.Join(ports, ", "), portsWidth) + " " + cell(composeProject(value.Labels), projectWidth)
			}
			rows = append(rows, ui.Truncate(line, inner))
		}
	}
	title := "▣  CONTAINERS"
	count := fmt.Sprintf("%d shown / %d total", len(visible), len(model.containers))
	header := title + strings.Repeat(" ", max(inner-lipgloss.Width(title)-lipgloss.Width(count), 1)) + count
	return pagePanel(header, strings.Join(rows, "\n"), width)
}

func (model Model) feedback() string {
	return model.notice
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
		return pagePanel("CONTAINER DETAILS", "No container selected.", width)
	}
	if model.detailsState.loading && model.detailsID != model.selectedID {
		return pagePanel("CONTAINER DETAILS", model.spinner.View()+" Loading details…", width)
	}
	if model.detailsState.err != nil && model.detailsID != model.selectedID {
		return pagePanel("CONTAINER DETAILS", ui.ErrorNotice("Unavailable: "+model.detailsState.err.Error(), max(width-4, 1))+"\nPress i to retry.", width)
	}
	details := model.details
	if model.detailsID != model.selectedID {
		return pagePanel("CONTAINER DETAILS", model.spinner.View()+" Loading selected container…", width)
	}
	selected := model.selectedSummary()
	lines := []string{
		fmt.Sprintf("Name:       %s", safe(emptyDash(details.Name))),
		fmt.Sprintf("ID:         %s", safe(shortID(details.ID))),
		"Image:      " + safe(emptyDash(details.Image)),
		"Status:     " + safe(emptyDash(details.State.Status)),
		"Health:     " + safe(emptyDash(details.State.Health)),
		"Project:    " + safe(composeProject(selected.Labels)),
		"Service:    " + safe(composeService(selected.Labels)),
		"Ports:      " + safe(summaryPorts(selected)),
		fmt.Sprintf("Restarts:   %d", details.RestartCount),
	}
	if len(details.Networks) > 0 {
		networks := make([]string, 0, len(details.Networks))
		for _, network := range details.Networks {
			value := network.Name
			if network.IPAddress != "" {
				value += " (" + network.IPAddress + ")"
			}
			networks = append(networks, value)
		}
		lines = append(lines, "Networks:   "+safe(strings.Join(networks, ", ")))
	}
	if len(details.Mounts) > 0 {
		mounts := make([]string, 0, len(details.Mounts))
		for _, mount := range details.Mounts {
			mounts = append(mounts, mount.Destination)
		}
		lines = append(lines, "Volumes:    "+safe(strings.Join(mounts, ", ")))
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
	return pagePanel("ⓘ  CONTAINER DETAILS", model.windowBody(strings.Join(lines, "\n")), width)
}

func (model Model) processesPanel(width int) string {
	if model.processState.loading {
		return pagePanel("PROCESSES", model.spinner.View()+" Loading processes…", width)
	}
	if model.processState.unavailable {
		return pagePanel("PROCESSES", "Unavailable for this container state.\nA stopped container has no process table.\n\nEsc returns to the list.", width)
	}
	if model.processState.err != nil {
		return pagePanel("PROCESSES", ui.ErrorNotice("Unavailable: "+model.processState.err.Error(), max(width-4, 1)), width)
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
		lines = append(lines, "", ui.ErrorNotice("Stream ended: "+model.logErr.Error(), max(width-4, 1)))
	}
	return pagePanel("LOGS — "+shortID(model.selectedID), model.windowBody(strings.Join(lines, "\n")), width)
}

func (model Model) statsPanel(width int) string {
	if model.statsErr != nil {
		message := "Stats unavailable: " + safe(model.statsErr.Error())
		if strings.Contains(strings.ToLower(model.statsErr.Error()), "conflict") {
			message = "Stats unavailable for this container state."
		}
		return pagePanel("STATS", ui.ErrorNotice(message, max(width-4, 1))+"\n\nEsc returns to details.", width)
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

func cell(value string, width int) string {
	value = ui.Truncate(safe(value), max(width, 1))
	return value + strings.Repeat(" ", max(width-lipgloss.Width(value), 0))
}

func composeProject(labels map[string]string) string {
	if project := strings.TrimSpace(labels["com.docker.compose.project"]); project != "" {
		return project
	}
	return "—"
}

func composeService(labels map[string]string) string {
	if service := strings.TrimSpace(labels["com.docker.compose.service"]); service != "" {
		return service
	}
	return "—"
}

func formatPort(port backendcontainers.Port) string {
	protocol := port.Protocol
	if protocol == "" {
		protocol = "tcp"
	}
	if port.PublicPort == 0 {
		return fmt.Sprintf("%d/%s", port.PrivatePort, protocol)
	}
	host := port.IP
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = ""
	} else {
		host += ":"
	}
	return fmt.Sprintf("%s%d→%d/%s", host, port.PublicPort, port.PrivatePort, protocol)
}

func summaryPorts(value backendcontainers.Summary) string {
	ports := make([]string, 0, len(value.Ports))
	for _, port := range value.Ports {
		ports = append(ports, formatPort(port))
	}
	if len(ports) == 0 {
		return "—"
	}
	return strings.Join(ports, ", ")
}

func stateMarker(state string) string {
	switch strings.ToLower(state) {
	case "running":
		return "●"
	case "paused", "restarting":
		return "●"
	default:
		return "○"
	}
}

func semanticColor(state, health string) lipgloss.Style {
	color := ui.Muted
	if strings.EqualFold(state, "running") {
		color = ui.Success
	}
	if strings.EqualFold(state, "paused") || strings.EqualFold(state, "restarting") || strings.EqualFold(health, "unhealthy") {
		color = ui.Warning
	}
	if strings.EqualFold(state, "dead") {
		color = ui.Danger
	}
	return lipgloss.NewStyle().Foreground(color)
}

func safe(value string) string { return ui.SanitizeLine(value) }

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}
