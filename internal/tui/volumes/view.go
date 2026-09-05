package volumes

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

func (m Model) View() string {
	if !m.hasData {
		message := "Loading volumes…"
		if m.err != nil {
			message = "Volumes unavailable\n\n" + ui.ErrorNotice(m.err.Error(), max(m.width-6, 1)) + "\n\nPress r to retry."
		}
		return fit(lipgloss.NewStyle().Padding(2, 3).Render(message), m.width, m.height)
	}
	var content string
	if m.width >= 110 {
		left := m.listView(max(m.width*3/5, 64), m.height)
		right := m.detailView(max(m.width-lipgloss.Width(left)-1, 38))
		content = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	} else {
		h := max(m.height/2, 8)
		content = lipgloss.JoinVertical(lipgloss.Left, fit(m.listView(m.width, h), m.width, h), m.detailView(m.width))
	}
	if m.overlay != noOverlay {
		content = ui.OverlayCentered(fit(content, m.width, m.height), m.overlayView(), m.width, m.height)
	}
	return fit(content, m.width, m.height)
}
func (m Model) listView(width, height int) string {
	values := m.visible()
	inner := max(width-4, 1)
	rows := []string{"Filter: " + cell(m.filter, max(inner-10, 8))}
	for _, warning := range m.warnings {
		rows = append(rows, ui.ErrorNotice("Docker warning: "+safe(warning), inner))
	}
	rows = append(rows, "", lipgloss.NewStyle().Foreground(ui.Primary).Render("  "+cell("NAME", max(inner*40/100, 16))+cell("DRIVER", 12)+cell("SIZE", 12)+"REFS"))
	capacity := max(height-len(rows)-3, 1)
	start := max(0, min(m.listStart, max(len(values)-capacity, 0)))
	end := min(start+capacity, len(values))
	for _, v := range values[start:end] {
		size := "—"
		if v.UsageKnown {
			size = ui.FormatBytes(v.Size)
		}
		marker := " "
		if v.Name == m.selected {
			marker = ">"
		}
		line := marker + " " + cell(v.Name, max(inner*40/100, 16)) + cell(v.Driver, 12) + cell(size, 12)
		if v.UsageKnown {
			line += fmt.Sprint(v.ReferenceCount)
		} else {
			line += "—"
		}
		if v.Name == m.selected {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("#EAF6FF")).Background(lipgloss.Color("#10365C")).Render(line)
		}
		rows = append(rows, line)
	}
	if len(values) == 0 {
		rows = append(rows, "", "No volumes match the filter.")
	}
	return panel("▣  VOLUMES", strings.Join(rows, "\n"), width)
}
func (m Model) detailView(width int) string {
	if m.selected == "" {
		return panel("VOLUME DETAILS", "No volume selected.", width)
	}
	if m.targetLoading {
		return panel("VOLUME DETAILS", "Loading selected volume…", width)
	}
	if m.targetErr != nil {
		return panel("VOLUME DETAILS", ui.ErrorNotice(m.targetErr.Error(), width-4), width)
	}
	if m.showAttachments {
		rows := []string{"CONTAINER       STATE       DESTINATION"}
		for _, a := range m.attachments.Attachments {
			rows = append(rows, cell(first(a.ContainerName, short(a.ContainerID)), 16)+cell(a.State, 12)+safe(a.Destination))
		}
		if len(m.attachments.Attachments) == 0 {
			rows = append(rows, "No attached containers.")
		}
		return panel("ATTACHMENTS", strings.Join(rows, "\n"), width)
	}
	if m.detailsName != m.selected {
		return panel("VOLUME DETAILS", "Waiting for details…", width)
	}
	v := m.details
	size := "unknown"
	refs := "unknown"
	if v.UsageKnown {
		size = ui.FormatBytes(v.Size)
		refs = fmt.Sprint(v.ReferenceCount)
	}
	lines := []string{"Name:        " + safe(v.Name), "Driver:      " + dash(v.Driver), "Scope:       " + dash(v.Scope), "Created:     " + v.Created.Format("2006-01-02 15:04"), "Mountpoint:  " + dash(v.Mountpoint), "Usage:       " + size, "References:  " + refs}
	if len(v.Labels) > 0 {
		lines = append(lines, "", "Labels:")
		lines = append(lines, mapLines(v.Labels)...)
	}
	if len(v.Options) > 0 {
		lines = append(lines, "", "Driver options:")
		lines = append(lines, mapLines(v.Options)...)
	}
	return panel("ⓘ  VOLUME DETAILS", strings.Join(lines, "\n"), width)
}
func (m Model) overlayView() string {
	w := max(min(m.width-12, 76), 46)
	switch m.overlay {
	case filterOverlay:
		return panel("FILTER VOLUMES", "> "+safe(m.filterEdit)+"_\n\nenter apply   esc cancel", w)
	case removeOverlay:
		return panel("REMOVE VOLUME", "Remove "+safe(m.selected)+"?\n\n[f] force: "+fmt.Sprint(m.removeForce)+"\n\ny/enter confirm   n/esc cancel", w)
	case createOverlay:
		names := []string{"Name", "Driver", "Label key", "Label value", "Driver option", "Option value"}
		return panel("CREATE VOLUME", fields(names, m.fields[:], m.field)+"\n\ntab field   enter create   esc cancel\n"+notice(m.notice, w), w)
	case pruneOverlay:
		return panel("PRUNE VOLUMES", "A label is always required. All only includes named volumes matching it.\n\n"+fields([]string{"Label key", "Label value"}, m.fields[:2], m.field)+"\n[a] all named: "+fmt.Sprint(m.pruneAll)+"\n\ntab field   enter prune   esc cancel\n"+notice(m.notice, w), w)
	}
	return ""
}
func fields(names, values []string, active int) string {
	rows := make([]string, len(names))
	for i := range names {
		mark := " "
		if i == active {
			mark = ">"
		}
		rows[i] = mark + " " + names[i] + ": " + safe(values[i]) + "_"
	}
	return strings.Join(rows, "\n")
}
func mapLines(v map[string]string) []string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, "  "+safe(k)+"="+safe(v[k]))
	}
	return rows
}
func first(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
func short(v string) string {
	if len(v) > 12 {
		return v[:12]
	}
	return v
}
func dash(v string) string {
	v = strings.TrimSpace(safe(v))
	if v == "" {
		return "—"
	}
	return v
}
func safe(v string) string { return ui.SanitizeLine(v) }
func notice(v string, w int) string {
	if v == "" {
		return ""
	}
	return ui.ErrorNotice(v, w-4)
}
func panel(title, body string, w int) string {
	inner := max(w-4, 1)
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = ui.Truncate(lines[i], inner)
	}
	return lipgloss.NewStyle().Width(max(w, 1)).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(ui.Border).Render(lipgloss.NewStyle().Bold(true).Foreground(ui.Primary).Render(ui.Truncate(title, inner)) + "\n" + strings.Join(lines, "\n"))
}
func fit(v string, w, h int) string {
	lines := strings.Split(v, "\n")
	if h > 0 && len(lines) > h {
		lines = lines[:h]
	}
	for i := range lines {
		lines[i] = ui.Truncate(lines[i], max(w, 1))
	}
	return strings.Join(lines, "\n")
}
func cell(v string, w int) string {
	v = ui.Truncate(safe(v), max(w, 1))
	return v + strings.Repeat(" ", max(w-lipgloss.Width(v), 0))
}
