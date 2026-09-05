package compose

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

func (m Model) View() string {
	var content string
	if !m.hasData {
		message := "Loading Compose projects…"
		if m.err != nil {
			message = "Compose unavailable\n\n" + ui.ErrorNotice(m.err.Error(), max(m.width-6, 1)) + "\n\nPress r to retry."
		}
		content = lipgloss.NewStyle().Padding(2, 3).Render(message)
	} else if m.width >= 110 {
		left := m.listView(max(m.width*2/5, 52), m.height)
		right := m.detailView(max(m.width-lipgloss.Width(left)-1, 54), m.height)
		content = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	} else {
		height := max(m.height/2, 8)
		content = lipgloss.JoinVertical(lipgloss.Left, fit(m.listView(m.width, height), m.width, height), m.detailView(m.width, max(m.height-height, 1)))
	}
	if m.overlay != noOverlay {
		content = ui.OverlayCentered(fit(content, m.width, m.height), m.overlayView(), m.width, m.height)
	}
	return fit(content, m.width, m.height)
}

func (m Model) listView(width, height int) string {
	values := m.visible()
	inner := max(width-4, 1)
	rows := make([]string, 0, 7)
	if m.notice != "" {
		rows = append(rows, ui.ErrorNotice(m.notice, inner), "")
	}
	rows = append(rows, "Filter: "+cell(m.filter, max(inner-10, 8)), "",
		lipgloss.NewStyle().Foreground(ui.Primary).Render("  "+cell("PROJECT", max(inner*45/100, 16))+cell("STATUS", 16)+"CONFIG"))
	capacity := max(height-len(rows)-3, 1)
	start := max(0, min(m.listStart, max(len(values)-capacity, 0)))
	end := min(start+capacity, len(values))
	for _, value := range values[start:end] {
		marker := " "
		if value.Name == m.selected {
			marker = ">"
		}
		config := "—"
		if len(value.ConfigFiles) > 0 {
			config = value.ConfigFiles[0]
		}
		line := marker + " " + cell(value.Name, max(inner*45/100, 16)) + cell(dash(value.Status), 16) + safe(config)
		if value.Name == m.selected {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("#EAF6FF")).Background(lipgloss.Color("#10365C")).Render(line)
		}
		rows = append(rows, line)
	}
	if len(values) == 0 {
		rows = append(rows, "", "No Compose projects match the filter.")
	}
	return panel("▦  COMPOSE PROJECTS", strings.Join(rows, "\n"), width)
}

func (m Model) detailView(width, height int) string {
	if m.selected == "" {
		return panel("COMPOSE DETAILS", "No project selected.", width)
	}
	if m.targetLoading && m.detailsName != m.selected {
		return panel("COMPOSE DETAILS", "Loading selected project…", width)
	}
	if m.targetErr != nil && m.detailsName != m.selected {
		return panel("COMPOSE DETAILS", ui.ErrorNotice(m.targetErr.Error(), width-4), width)
	}
	if m.detailsName != m.selected {
		return panel("COMPOSE DETAILS", "Waiting for project details…", width)
	}
	lines := m.detailLines()
	start := max(0, min(m.detailScroll, max(len(lines)-m.detailRows(), 0)))
	end := min(start+max(height-3, 1), len(lines))
	return panel("ⓘ  COMPOSE DETAILS", strings.Join(lines[start:end], "\n"), width)
}

func (m Model) detailLines() []string {
	details := m.details
	lines := []string{
		"Name:         " + safe(details.Name),
		"Status:       " + dash(details.Status),
		"Working dir:  " + dash(details.WorkingDir),
		"Config files: " + dash(strings.Join(details.ConfigFiles, ", ")),
		"",
		"SERVICES",
		"NAME              IMAGE                 RUNNING/DESIRED  PROFILES",
	}
	for _, service := range details.Services {
		lines = append(lines, cell(service.Name, 18)+cell(dash(service.Image), 22)+cell(fmt.Sprintf("%d/%d", service.Replicas, service.Desired), 17)+dash(strings.Join(service.Profiles, ",")))
		if len(service.DependsOn) > 0 {
			lines = append(lines, "  depends on: "+safe(strings.Join(service.DependsOn, ", ")))
		}
	}
	if len(details.Services) == 0 {
		lines = append(lines, "No services reported.")
	}
	lines = append(lines, "", "CONTAINERS", "NAME              SERVICE           STATE       HEALTH      EXIT  STATUS")
	for _, container := range details.Containers {
		lines = append(lines, cell(container.Name, 18)+cell(container.Service, 18)+cell(dash(container.State), 12)+cell(dash(container.Health), 12)+cell(fmt.Sprint(container.ExitCode), 6)+safe(container.Status))
	}
	if len(details.Containers) == 0 {
		lines = append(lines, "No project containers reported.")
	}
	return lines
}

func (m Model) overlayView() string {
	if m.overlay == logsOverlay {
		return m.logsView()
	}
	width := max(min(m.width-12, 88), 52)
	switch m.overlay {
	case filterOverlay:
		return panel("FILTER COMPOSE PROJECTS", "> "+safe(m.filterEdit)+"_\n\nenter apply   esc cancel", width)
	case newConfigOverlay:
		return panel("NEW COMPOSE CONFIGURATION", "Project names use lowercase letters, digits, - or _.\n\n> Project name: "+safe(m.fields[0])+"_\n\nenter continue   esc cancel\n"+notice(m.notice, width), width)
	case configEditorOverlay:
		return m.configEditorView()
	case stopOverlay:
		return panel("STOP COMPOSE PROJECT", form([]string{"Services (comma, optional)", "Timeout (example: 10s)"}, m.fields[:2], m.field)+"\n\nenter stop   esc cancel\n"+notice(m.notice, width), width)
	case restartOverlay:
		return panel("RESTART COMPOSE PROJECT", form([]string{"Services (comma, optional)", "Timeout (example: 10s)"}, m.fields[:2], m.field)+fmt.Sprintf("\n[n] no dependencies: %t", m.optionB)+"\n\nenter restart   esc cancel\n"+notice(m.notice, width), width)
	case scaleOverlay:
		return panel("SCALE COMPOSE SERVICE", "Project: "+safe(m.formProject)+"\n\n"+form([]string{"Compose files (comma)", "Service", "Replicas"}, m.fields[:3], m.field)+"\n\nenter scale   esc cancel\n"+notice(m.notice, width), width)
	case upOverlay:
		return panel("COMPOSE UP", "Project: "+safe(m.formProject)+"\n\n"+form([]string{"Compose files (comma)", "Services (comma, optional)", "Profiles (comma, optional)"}, m.fields[:3], m.field)+fmt.Sprintf("\n[a] remove orphans: %t", m.optionA)+"\n\nenter start   esc cancel\n"+notice(m.notice, width), width)
	case downOverlay:
		return panel("CONFIRM COMPOSE DOWN", "This removes project containers and networks.\n\n"+form([]string{"Services (comma, optional)", "Timeout (example: 10s)"}, m.fields[:2], m.field)+fmt.Sprintf("\n[a] remove orphans: %t\n[v] remove volumes: %t", m.optionA, m.optionB)+"\n\nenter confirm down   esc cancel\n"+notice(m.notice, width), width)
	case pullOverlay:
		return panel("COMPOSE PULL", "Project: "+safe(m.formProject)+"\n\n"+form([]string{"Compose files (comma)", "Services (comma, optional)", "Profiles (comma, optional)"}, m.fields[:3], m.field)+fmt.Sprintf("\n[a] ignore failures: %t", m.optionA)+"\n\nenter start   esc cancel\n"+notice(m.notice, width), width)
	case buildOverlay:
		return panel("COMPOSE BUILD", "Project: "+safe(m.formProject)+"\n\n"+form([]string{"Compose files (comma)", "Services (comma, optional)", "Profiles (comma, optional)"}, m.fields[:3], m.field)+fmt.Sprintf("\n[a] pull: %t\n[c] no cache: %t", m.optionA, m.optionB)+"\n\nenter start   esc cancel\n"+notice(m.notice, width), width)
	}
	return ""
}

func (m Model) configEditorView() string {
	width := max(min(m.width-10, 120), 54)
	content := strings.Join([]string{
		"Project: " + safe(m.editorProject),
		"Managed file: " + safe(m.editorPath),
		"",
		m.editor.View(),
		"",
		notice(m.notice, width),
		"ctrl+s validate and save   esc cancel",
	}, "\n")
	return panel("COMPOSE CONFIGURATION", content, width)
}

func (m Model) logsView() string {
	width := max(min(m.width-10, 120), 54)
	rows := []string{}
	if m.logErr != nil {
		rows = append(rows, ui.ErrorNotice(m.logErr.Error(), width-4), "")
	}
	start := max(0, min(m.logScroll, m.logMaxScroll()))
	end := min(start+m.logViewportRows(), len(m.logLines))
	rows = append(rows, m.logLines[start:end]...)
	if len(m.logLines) == 0 && m.logErr == nil {
		rows = append(rows, "Waiting for Compose logs…")
	}
	rows = append(rows, "", "j/k scroll   pgup/pgdown   g/G top/bottom   esc close")
	return panel("COMPOSE LOGS — "+safe(m.selected), strings.Join(rows, "\n"), width)
}

func form(names, values []string, active int) string {
	rows := make([]string, len(names))
	for index := range names {
		marker := " "
		if index == active {
			marker = ">"
		}
		rows[index] = marker + " " + names[index] + ": " + safe(values[index]) + "_"
	}
	return strings.Join(rows, "\n")
}
func notice(value string, width int) string {
	if value == "" {
		return ""
	}
	return ui.ErrorNotice(value, width-4)
}
func safe(value string) string { return ui.SanitizeLine(value) }
func dash(value string) string {
	value = strings.TrimSpace(safe(value))
	if value == "" {
		return "—"
	}
	return value
}
func panel(title, body string, width int) string {
	inner := max(width-4, 1)
	lines := strings.Split(body, "\n")
	for index := range lines {
		lines[index] = ui.Truncate(lines[index], inner)
	}
	return lipgloss.NewStyle().Width(max(width, 1)).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(ui.Border).
		Render(lipgloss.NewStyle().Bold(true).Foreground(ui.Primary).Render(ui.Truncate(title, inner)) + "\n" + strings.Join(lines, "\n"))
}
func fit(value string, width, height int) string {
	lines := strings.Split(value, "\n")
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	for index := range lines {
		lines[index] = ui.Truncate(lines[index], max(width, 1))
	}
	return strings.Join(lines, "\n")
}
func cell(value string, width int) string {
	value = ui.Truncate(safe(value), max(width, 1))
	return value + strings.Repeat(" ", max(width-lipgloss.Width(value), 0))
}
