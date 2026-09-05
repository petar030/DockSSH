package events

import (
	"charm.land/lipgloss/v2"
	"fmt"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
	"sort"
	"strings"
)

func (m Model) View() string {
	if !m.hasData {
		v := "Loading Docker events…"
		if m.err != nil {
			v = "Events unavailable\n\n" + ui.ErrorNotice(m.err.Error(), max(m.width-6, 1)) + "\n\nPress r to retry."
		}
		return fit(lipgloss.NewStyle().Padding(2, 3).Render(v), m.width, m.height)
	}
	inner := max(m.width-4, 1)
	lines := []string{fmt.Sprintf("Filters: resource=%s  id=%s  action=%s  project=%s", blank(m.filters[0]), blank(m.filters[1]), blank(m.filters[2]), blank(m.filters[3])), "", lipgloss.NewStyle().Foreground(ui.Primary).Render(cell("TIME", 20) + cell("RESOURCE", 13) + cell("ACTION", 16) + cell("ID / PROJECT", 24) + "ATTRIBUTES")}
	end := min(m.scroll+m.visibleRows(), len(m.rows))
	for _, r := range m.rows[m.scroll:end] {
		identity := safe(r.id)
		if r.project != "" {
			identity += " / " + safe(r.project)
		}
		lines = append(lines, cell(r.occurred.Format("2006-01-02 15:04:05"), 20)+cell(r.resource, 13)+cell(r.action, 16)+cell(identity, 24)+attributes(r.attributes))
	}
	if len(m.rows) == 0 {
		lines = append(lines, "", "No Docker events match this session filter.")
	}
	out := panel("◉  DOCKER EVENTS", strings.Join(lines, "\n"), m.width)
	if m.filtering {
		out = ui.OverlayCentered(fit(out, m.width, m.height), m.filterView(), m.width, m.height)
	}
	_ = inner
	return fit(out, m.width, m.height)
}
func (m Model) filterView() string {
	w := max(min(m.width-12, 76), 46)
	names := []string{"Resource type", "Resource ID", "Action", "Compose project"}
	rows := make([]string, 4)
	for i := range names {
		mark := " "
		if i == m.field {
			mark = ">"
		}
		rows[i] = mark + " " + names[i] + ": " + safe(m.edits[i]) + "_"
	}
	return panel("EVENT FILTERS", strings.Join(rows, "\n")+"\n\nApplying replaces the backend subscription safely.\ntab field   enter apply   esc cancel", w)
}
func attributes(v map[string]string) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, safe(k)+"="+safe(v[k]))
	}
	return strings.Join(parts, " ")
}
func blank(v string) string {
	if strings.TrimSpace(v) == "" {
		return "*"
	}
	return safe(v)
}
func safe(v string) string { return ui.SanitizeLine(v) }
func panel(t, b string, w int) string {
	i := max(w-4, 1)
	l := strings.Split(b, "\n")
	for n := range l {
		l[n] = ui.Truncate(l[n], i)
	}
	return lipgloss.NewStyle().Width(max(w, 1)).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(ui.Border).Render(lipgloss.NewStyle().Bold(true).Foreground(ui.Primary).Render(ui.Truncate(t, i)) + "\n" + strings.Join(l, "\n"))
}
func fit(v string, w, h int) string {
	l := strings.Split(v, "\n")
	if h > 0 && len(l) > h {
		l = l[:h]
	}
	for i := range l {
		l[i] = ui.Truncate(l[i], max(w, 1))
	}
	return strings.Join(l, "\n")
}
func cell(v string, w int) string {
	v = ui.Truncate(safe(v), max(w, 1))
	return v + strings.Repeat(" ", max(w-lipgloss.Width(v), 0))
}
