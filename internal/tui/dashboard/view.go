package dashboard

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

func (model Model) View() string {
	if !model.hasData {
		message := model.spinner.View() + " Loading Dashboard data…"
		if model.err != nil {
			message = "Dashboard unavailable\n\n" + ui.SanitizeLine(errorText(model.err)) + "\n\nPress r to retry."
		}
		return lipgloss.NewStyle().Padding(2, 3).Render(message)
	}

	contentWidth := max(model.width, ui.MinimumWidth)
	gap := 1
	columnWidth := max((contentWidth-gap)/2, 24)

	engine := model.enginePanel(columnWidth)
	resources := model.resourcesPanel(columnWidth)
	disk := model.diskPanel(columnWidth)
	recent := model.recentPanel(columnWidth)

	firstRow := lipgloss.JoinHorizontal(lipgloss.Top, engine, strings.Repeat(" ", gap), resources)
	secondRow := lipgloss.JoinHorizontal(lipgloss.Top, disk, strings.Repeat(" ", gap), recent)
	content := lipgloss.JoinVertical(lipgloss.Left, firstRow, secondRow)

	if model.stale || model.loading {
		state := model.spinner.View() + " refreshing"
		if model.stale && model.err != nil {
			state = "stale: " + ui.SanitizeLine(errorText(model.err))
		}
		content = stateStyle(model.stale).Render(state) + "\n" + content
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
	return panel("ENGINE", strings.Join(body, "\n"), width)
}

func (model Model) resourcesPanel(width int) string {
	resources := model.summary.Resources
	body := fmt.Sprintf(
		"Containers  %d\n  running   %d\n  paused    %d\n  stopped   %d\nImages      %d\nVolumes     %d\nNetworks    %d",
		resources.Containers, resources.ContainersRunning, resources.ContainersPaused,
		resources.ContainersStopped, resources.Images, resources.Volumes, resources.Networks,
	)
	return panel("RESOURCES", body, width)
}

func (model Model) diskPanel(width int) string {
	disk := model.summary.DiskUsage
	body := strings.Join([]string{
		"              total / reclaimable",
		diskLine("Containers", disk.Containers.TotalBytes, disk.Containers.ReclaimableBytes),
		diskLine("Images", disk.Images.TotalBytes, disk.Images.ReclaimableBytes),
		diskLine("Volumes", disk.Volumes.TotalBytes, disk.Volumes.ReclaimableBytes),
		diskLine("Build cache", disk.BuildCache.TotalBytes, disk.BuildCache.ReclaimableBytes),
	}, "\n")
	return panel("DISK USAGE", body, width)
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

func panel(title, body string, width int) string {
	return lipgloss.NewStyle().
		Width(max(width, 1)).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Border).
		Render(lipgloss.NewStyle().Bold(true).Foreground(ui.Primary).Render(title) + "\n" + body)
}

func diskLine(name string, total, reclaimable int64) string {
	return fmt.Sprintf("%-11s %9s / %9s", name, ui.FormatBytes(total), ui.FormatBytes(reclaimable))
}

func safeDash(value string) string {
	value = strings.TrimSpace(ui.SanitizeLine(value))
	if value == "" {
		return "—"
	}
	return value
}

func stateStyle(stale bool) lipgloss.Style {
	color := ui.Muted
	if stale {
		color = ui.Warning
	}
	return lipgloss.NewStyle().Foreground(color)
}
