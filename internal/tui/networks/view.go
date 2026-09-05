package networks

import (
	"charm.land/lipgloss/v2"
	"fmt"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
	"sort"
	"strings"
)

func (m Model) View() string {
	if !m.hasData {
		v := "Loading networks…"
		if m.err != nil {
			v = "Networks unavailable\n\n" + ui.ErrorNotice(m.err.Error(), max(m.width-6, 1)) + "\n\nPress r to retry."
		}
		return fit(lipgloss.NewStyle().Padding(2, 3).Render(v), m.width, m.height)
	}
	var out string
	if m.width >= 110 {
		l := m.listView(max(m.width*3/5, 64), m.height)
		r := m.detailView(max(m.width-lipgloss.Width(l)-1, 38))
		out = lipgloss.JoinHorizontal(lipgloss.Top, l, " ", r)
	} else {
		h := max(m.height/2, 8)
		out = lipgloss.JoinVertical(lipgloss.Left, fit(m.listView(m.width, h), m.width, h), m.detailView(m.width))
	}
	if m.overlay != noOverlay {
		out = ui.OverlayCentered(fit(out, m.width, m.height), m.overlayView(), m.width, m.height)
	}
	return fit(out, m.width, m.height)
}
func (m Model) listView(w, h int) string {
	v := m.visible()
	inner := max(w-4, 1)
	rows := []string{"Filter: " + cell(m.filter, max(inner-10, 8)), "", lipgloss.NewStyle().Foreground(ui.Primary).Render("  " + cell("NAME", max(inner*34/100, 14)) + cell("ID", 13) + cell("DRIVER", 11) + cell("SCOPE", 10) + "FLAGS")}
	capacity := max(h-6, 1)
	start := max(0, min(m.listStart, max(len(v)-capacity, 0)))
	end := min(start+capacity, len(v))
	for _, n := range v[start:end] {
		mark := " "
		if n.ID == m.selected {
			mark = ">"
		}
		flags := ""
		if n.Internal {
			flags += "internal "
		}
		if n.Attachable {
			flags += "attachable"
		}
		line := mark + " " + cell(n.Name, max(inner*34/100, 14)) + cell(short(n.ID), 13) + cell(n.Driver, 11) + cell(n.Scope, 10) + safe(flags)
		if n.ID == m.selected {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("#EAF6FF")).Background(lipgloss.Color("#10365C")).Render(line)
		}
		rows = append(rows, line)
	}
	if len(v) == 0 {
		rows = append(rows, "", "No networks match the filter.")
	}
	return panel("⌘  NETWORKS", strings.Join(rows, "\n"), w)
}
func (m Model) detailView(w int) string {
	if m.selected == "" {
		return panel("NETWORK DETAILS", "No network selected.", w)
	}
	if m.targetLoading {
		return panel("NETWORK DETAILS", "Loading selected network…", w)
	}
	if m.targetErr != nil {
		return panel("NETWORK DETAILS", ui.ErrorNotice(m.targetErr.Error(), w-4), w)
	}
	if m.showConnections {
		rows := []string{"CONTAINER       MAC                IPv4                 IPv6"}
		for _, c := range m.connections.Connections {
			rows = append(rows, cell(first(c.ContainerName, short(c.ContainerID)), 16)+cell(unknown(c.MACAddress), 19)+cell(unknown(c.IPv4Address), 21)+unknown(c.IPv6Address))
		}
		if len(m.connections.Connections) == 0 {
			rows = append(rows, "No connected containers.")
		}
		return panel("CONNECTIONS", strings.Join(rows, "\n"), w)
	}
	if m.detailsID != m.selected {
		return panel("NETWORK DETAILS", "Waiting for details…", w)
	}
	d := m.details
	lines := []string{"Name:        " + safe(d.Name), "ID:          " + short(d.ID), "Driver:      " + unknown(d.Driver), "Scope:       " + unknown(d.Scope), fmt.Sprintf("IPv4:        %t", d.EnableIPv4), fmt.Sprintf("IPv6:        %t", d.EnableIPv6), fmt.Sprintf("Internal:    %t", d.Internal), fmt.Sprintf("Attachable:  %t", d.Attachable), "IPAM driver: " + unknown(d.IPAMDriver)}
	for i, c := range d.IPAM {
		lines = append(lines, fmt.Sprintf("IPAM %d:      subnet=%s range=%s gateway=%s", i+1, unknown(c.Subnet), unknown(c.IPRange), unknown(c.Gateway)))
		if len(c.AuxAddresses) > 0 {
			lines = append(lines, mapLines(c.AuxAddresses)...)
		}
	}
	if len(d.Options) > 0 {
		lines = append(lines, "", "Options:")
		lines = append(lines, mapLines(d.Options)...)
	}
	return panel("ⓘ  NETWORK DETAILS", strings.Join(lines, "\n"), w)
}
func (m Model) overlayView() string {
	w := max(min(m.width-12, 82), 48)
	switch m.overlay {
	case filterOverlay:
		return panel("FILTER NETWORKS", "> "+safe(m.filterEdit)+"_\n\nenter apply   esc cancel", w)
	case removeOverlay:
		return panel("REMOVE NETWORK", "Remove "+safe(selectedName(m))+"? Active endpoints must be disconnected first.\n\ny/enter confirm   n/esc cancel", w)
	case createOverlay:
		return panel("CREATE NETWORK", fields([]string{"Name", "Driver", "Scope", "Label key", "Label value", "Option key", "Subnet", "Gateway"}, m.fields[:], m.field)+fmt.Sprintf("\n[I] internal: %t   [A] attachable: %t", m.createInternal, m.createAttachable)+"\n\ntab field   enter create   esc cancel\n"+notice(m.notice, w), w)
	case connectOverlay:
		return panel("CONNECT CONTAINER", fields([]string{"Container ID", "IPv4", "IPv6", "Aliases (comma)", "Gateway priority"}, m.fields[:5], m.field)+"\n\ntab field   enter connect   esc cancel\n"+notice(m.notice, w), w)
	case disconnectOverlay:
		return panel("DISCONNECT CONTAINER", fields([]string{"Container ID"}, m.fields[:1], m.field)+fmt.Sprintf("\n[f] force: %t", m.disconnectForce)+"\n\ntab field   enter disconnect   esc cancel\n"+notice(m.notice, w), w)
	case pruneOverlay:
		return panel("PRUNE NETWORKS", "Until and/or a label is required.\n\n"+fields([]string{"Until", "Label key", "Label value"}, m.fields[:3], m.field)+"\n\ntab field   enter prune   esc cancel\n"+notice(m.notice, w), w)
	}
	return ""
}
func selectedName(m Model) string {
	for _, n := range m.values {
		if n.ID == m.selected {
			return n.Name
		}
	}
	return m.selected
}
func fields(n, v []string, a int) string {
	o := make([]string, len(n))
	for i := range n {
		mark := " "
		if i == a {
			mark = ">"
		}
		o[i] = mark + " " + n[i] + ": " + safe(v[i]) + "_"
	}
	return strings.Join(o, "\n")
}
func mapLines(v map[string]string) []string {
	k := make([]string, 0, len(v))
	for x := range v {
		k = append(k, x)
	}
	sort.Strings(k)
	o := make([]string, 0, len(k))
	for _, x := range k {
		o = append(o, "  "+safe(x)+"="+safe(v[x]))
	}
	return o
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
func unknown(v string) string {
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
