package dashboard

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

const (
	summaryPanelBodyRows = 7
	diskLabelWidth       = 11
	diskValueWidth       = 10
)

func (model Model) View() string {
	if !model.hasData {
		message := "Loading Dashboard data…"
		if model.err != nil {
			message = "Dashboard unavailable\n\n" + ui.ErrorNotice(errorText(model.err), max(model.width-6, 1)) + "\n\nPress r to retry."
		}
		return lipgloss.NewStyle().Padding(2, 3).Render(message)
	}

	contentWidth := max(model.width, ui.MinimumWidth)
	gap := 1
	var content string
	if contentWidth >= 120 {
		columnWidth := max((contentWidth-2*gap)/3, 24)
		firstRow := lipgloss.JoinHorizontal(lipgloss.Top,
			model.enginePanel(columnWidth), strings.Repeat(" ", gap),
			model.resourcesPanel(columnWidth), strings.Repeat(" ", gap),
			model.diskPanel(contentWidth-2*columnWidth-2*gap))
		content = lipgloss.JoinVertical(lipgloss.Left, firstRow, model.recentPanel(contentWidth))
	} else {
		columnWidth := max((contentWidth-gap)/2, 24)
		firstRow := lipgloss.JoinHorizontal(lipgloss.Top, model.enginePanel(columnWidth), strings.Repeat(" ", gap), model.resourcesPanel(columnWidth))
		secondRow := lipgloss.JoinHorizontal(lipgloss.Top, model.diskPanel(columnWidth), strings.Repeat(" ", gap), model.recentPanel(columnWidth))
		content = lipgloss.JoinVertical(lipgloss.Left, firstRow, secondRow)
	}

	if model.stale && model.err != nil {
		content = ui.WarningNotice("stale: "+errorText(model.err), model.width) + "\n" + content
	}
	return content
}

func (model Model) enginePanel(width int) string {
	engine := model.summary.Engine
	availability := "available"
	if !engine.Available {
		availability = "unavailable"
	}
	body := []string{
		availability,
		"Docker " + safeDash(engine.ServerVersion),
		"API " + safeDash(engine.APIVersion),
		"Host " + safeDash(engine.Name),
		ui.Truncate(safeDash(engine.OperatingSystem)+" / "+safeDash(engine.Architecture), max(width-4, 1)),
		fmt.Sprintf("%d CPUs  %s memory", engine.CPUs, ui.FormatBytes(engine.MemoryBytes)),
	}
	return summaryPanel("ENGINE", strings.Join(body, "\n"), width)
}

func (model Model) resourcesPanel(width int) string {
	resources := model.summary.Resources
	body := fmt.Sprintf(
		"Containers  %d\n  running   %d\n  paused    %d\n  stopped   %d\nImages      %d\nVolumes     %d\nNetworks    %d",
		resources.Containers, resources.ContainersRunning, resources.ContainersPaused,
		resources.ContainersStopped, resources.Images, resources.Volumes, resources.Networks,
	)
	return summaryPanel("RESOURCES", body, width)
}

func (model Model) diskPanel(width int) string {
	disk := model.summary.DiskUsage
	total := disk.Containers.TotalBytes + disk.Images.TotalBytes + disk.Volumes.TotalBytes + disk.BuildCache.TotalBytes
	if width < 68 {
		body := strings.Join([]string{
			diskLineCompact("Containers", disk.Containers.TotalBytes, total, width),
			diskLineCompact("Images", disk.Images.TotalBytes, total, width),
			diskLineCompact("Volumes", disk.Volumes.TotalBytes, total, width),
			diskLineCompact("Build cache", disk.BuildCache.TotalBytes, total, width),
		}, "\n")
		return summaryPanel("DISK USAGE", body, width)
	}
	body := strings.Join([]string{
		"Relative Docker disk usage",
		diskLine("Containers", disk.Containers.TotalBytes, total, width),
		diskLine("Images", disk.Images.TotalBytes, total, width),
		diskLine("Volumes", disk.Volumes.TotalBytes, total, width),
		diskLine("Build cache", disk.BuildCache.TotalBytes, total, width),
		diskLine("Total", total, total, width),
	}, "\n")
	return summaryPanel("DISK USAGE", body, width)
}

func (model Model) recentPanel(width int) string {
	if len(model.summary.RecentEvents) == 0 {
		return panel("RECENT DOCKER EVENTS", "No events observed during this run.", width)
	}
	limit := min(len(model.summary.RecentEvents), 5)
	rows := make([]string, 0, limit)
	for _, event := range model.summary.RecentEvents[:limit] {
		row := fmt.Sprintf("%s  %s %s  %s",
			event.Time.Format("15:04:05"), safeDash(event.Resource),
			safeDash(event.Action), safeDash(event.ResourceID),
		)
		rows = append(rows, ui.Truncate(row, max(width-4, 1)))
	}
	return panel("RECENT DOCKER EVENTS", strings.Join(rows, "\n"), width)
}

func summaryPanel(title, body string, width int) string {
	rows := strings.Count(body, "\n") + 1
	if rows < summaryPanelBodyRows {
		body += strings.Repeat("\n", summaryPanelBodyRows-rows)
	}
	return panel(title, body, width)
}

func panel(title, body string, width int) string {
	style := lipgloss.NewStyle().
		Width(max(width, 1)).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Border)
	return style.Render(lipgloss.NewStyle().Bold(true).Foreground(ui.Primary).Render(title) + "\n" + body)
}

func diskLine(name string, used, total int64, width int) string {
	contentWidth := max(width-4, 1)
	barWidth := max(contentWidth-diskLabelWidth-diskValueWidth-7, 5)
	return fmt.Sprintf("%-*s %s %-*s %3d%%", diskLabelWidth, name, ui.UsageBar(used, total, barWidth), diskValueWidth, ui.Truncate(ui.FormatBytes(used), diskValueWidth), ui.Percent(used, total))
}

func diskLineCompact(name string, used, total int64, width int) string {
	contentWidth := max(width-4, 1)
	barWidth := max(contentWidth-diskLabelWidth-6, 4)
	return fmt.Sprintf("%-*s %s %3d%%", diskLabelWidth, name, ui.UsageBar(used, total, barWidth), ui.Percent(used, total))
}

func safeDash(value string) string {
	value = strings.TrimSpace(ui.SanitizeLine(value))
	if value == "" {
		return "—"
	}
	return value
}
