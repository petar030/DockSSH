package tui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

func renderTopBar(width int, connection, activity, sshAddress string) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(ui.Primary).Render("🐳  DOCKER TUI")
	endpoint := ""
	if sshAddress != "" {
		endpoint = lipgloss.NewStyle().Foreground(ui.Muted).Render("SSH: " + ui.SanitizeLine(sshAddress))
	}
	status := connection
	if activity != "" {
		status = activity + "  " + status
	}
	status = lipgloss.NewStyle().Foreground(ui.Muted).Render(status)
	left := title
	if endpoint != "" {
		left += "  " + endpoint
	}
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(status), 1)
	return ui.Truncate(left+strings.Repeat(" ", gap)+status, width)
}

func renderHeader(width, active int, connection, activity, sshAddress string) string {
	contentWidth := max(width-4, 1)
	body := padHeaderLine(renderTopBar(contentWidth, connection, activity, sshAddress), contentWidth) + "\n" +
		padHeaderLine(renderTabs(contentWidth, active), contentWidth)
	return lipgloss.NewStyle().Padding(0, 1).
		Border(lipgloss.RoundedBorder()).BorderForeground(ui.Border).Render(body)
}

func padHeaderLine(value string, width int) string {
	value = ui.Truncate(value, width)
	return value + strings.Repeat(" ", max(width-lipgloss.Width(value), 0))
}

func renderTabs(width, active int) string {
	values := make([]string, 0, len(tabs))
	for index, item := range tabs {
		label := " " + item.label + " "
		style := lipgloss.NewStyle().Foreground(ui.Muted)
		if index == active {
			style = style.Bold(true).Foreground(lipgloss.Color("#EAF6FF")).Background(lipgloss.Color("#10365C"))
		}
		values = append(values, style.Render(label))
	}
	return ui.Truncate(strings.Join(values, "│"), width)
}

func renderFooter(width int, status, pageHelp, globalHelp string) string {
	inner := max(width-4, 1)
	pageLines := strings.Split(pageHelp, "\n")
	for index := range pageLines {
		pageLines[index] = ui.Truncate(pageLines[index], inner)
	}
	pageHelp = strings.Join(pageLines, "\n")
	if pageHelp == "" {
		pageHelp = ui.Truncate(ui.SanitizeLine(status), inner)
	}
	globalHelp = ui.Truncate(globalHelp, inner)
	body := lipgloss.NewStyle().Foreground(ui.Primary).Render(pageHelp) + "\n" +
		lipgloss.NewStyle().Foreground(ui.Muted).Render(globalHelp)
	return lipgloss.NewStyle().Width(max(width-2, 1)).Padding(0, 1).
		Border(lipgloss.RoundedBorder()).BorderForeground(ui.Border).Render(body)
}

func renderTooSmall(width, height int) string {
	message := "Terminal too small\nMinimum size: 80x24\nCurrent size: " +
		strconv.Itoa(width) + "x" + strconv.Itoa(height) + "\n\n? help   q quit"
	return lipgloss.NewStyle().Padding(1, 2).Foreground(ui.Warning).Render(message)
}

func renderSmallHelp(width, height int) string {
	message := "KEYBOARD HELP\n\n? close help\nq / ctrl+c quit\n\n" +
		"Resize to at least 80x24 for the application.\nCurrent size: " +
		strconv.Itoa(width) + "x" + strconv.Itoa(height)
	return lipgloss.NewStyle().Padding(1, 2).Render(message)
}
