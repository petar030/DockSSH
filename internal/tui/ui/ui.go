// Package ui contains small presentation helpers shared by the root TUI and
// page views. It has no backend or Bubble Tea state.
package ui

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// NewSpinner returns the shared, primary-colour activity indicator.
func NewSpinner() spinner.Model {
	return spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(Primary)),
	)
}

const (
	MinimumWidth  = 80
	MinimumHeight = 24
)

var (
	Primary = lipgloss.Color("#249DFF")
	Muted   = lipgloss.Color("#A7AFBA")
	Success = lipgloss.Color("#20D65A")
	Warning = lipgloss.Color("#FFD21F")
	Danger  = lipgloss.Color("#FF4D4D")
	Border  = lipgloss.Color("#168BFF")
)

// SanitizeLine makes Docker-controlled text safe to render on one terminal
// line. Control characters, including escape sequences, are replaced rather
// than emitted to the user's terminal.
func SanitizeLine(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
}

func Truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	return ansi.Truncate(value, width, "…")
}

// CommandBar renders a compact keyboard-help row. Each item uses the first
// token as its key and the remaining text as its description, with a muted dot
// between items. Page models supply plain text separated by │ so the same
// presentation is used throughout the application frame.
func CommandBar(value string, width int) string {
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		items := strings.Split(line, "│")
		rendered := make([]string, 0, len(items))
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			rendered = append(rendered, commandItem(item))
		}
		lines[index] = Truncate(strings.Join(rendered, lipgloss.NewStyle().Foreground(Muted).Render(" · ")), width)
	}
	return strings.Join(lines, "\n")
}

func commandItem(item string) string {
	fields := strings.Fields(item)
	if len(fields) == 0 {
		return ""
	}
	key, description := fields[0], strings.Join(fields[1:], " ")
	if strings.HasPrefix(item, "[ / ] ") {
		key = "[ / ]"
		description = strings.TrimSpace(strings.TrimPrefix(item, "[ / ]"))
	}
	renderedKey := lipgloss.NewStyle().Bold(true).Foreground(Primary).Render(key)
	if description == "" {
		return renderedKey
	}
	return renderedKey + " " + lipgloss.NewStyle().Foreground(Muted).Render(description)
}

// ErrorNotice is the standard presentation for recoverable page, command and
// stream errors. It sanitizes backend-controlled text and always fits one row.
func ErrorNotice(value string, width int) string {
	return notice("✕", value, width, lipgloss.NewStyle().Foreground(Danger))
}

// WarningNotice is the standard presentation for stale or degraded data that
// remains safe to display.
func WarningNotice(value string, width int) string {
	return notice("!", value, width, lipgloss.NewStyle().Foreground(Warning))
}

// SuccessNotice presents a completed user action without implying an error.
func SuccessNotice(value string, width int) string {
	return notice("✓", value, width, lipgloss.NewStyle().Foreground(Success))
}

func notice(symbol, value string, width int, style lipgloss.Style) string {
	value = symbol + " " + strings.TrimSpace(SanitizeLine(value))
	return style.Render(Truncate(value, width))
}

// OverlayCentered draws foreground over background without changing the
// surrounding page. Both inputs may contain ANSI styling.
func OverlayCentered(background, foreground string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	backgroundLines := strings.Split(background, "\n")
	foregroundLines := strings.Split(foreground, "\n")
	foregroundWidth := min(lipgloss.Width(foreground), width)
	foregroundHeight := min(len(foregroundLines), height)
	left := max((width-foregroundWidth)/2, 0)
	top := max((height-foregroundHeight)/2, 0)

	result := make([]string, height)
	for row := range height {
		backgroundLine := ""
		if row < len(backgroundLines) {
			backgroundLine = Truncate(backgroundLines[row], width)
		}
		backgroundLine += strings.Repeat(" ", max(width-lipgloss.Width(backgroundLine), 0))
		if row < top || row >= top+foregroundHeight {
			result[row] = backgroundLine
			continue
		}

		foregroundLine := Truncate(foregroundLines[row-top], foregroundWidth)
		foregroundLine += strings.Repeat(" ", max(foregroundWidth-lipgloss.Width(foregroundLine), 0))
		prefix := ansi.Cut(backgroundLine, 0, left)
		suffix := ansi.Cut(backgroundLine, left+foregroundWidth, width)
		result[row] = prefix + foregroundLine + suffix
	}
	return strings.Join(result, "\n")
}

func FormatBytes(value int64) string {
	const unit = int64(1024)
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor, exponent := unit, 0
	for reduced := value / unit; reduced >= unit && exponent < 5; reduced /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(divisor), "KMGTPE"[exponent])
}

// UsageBar draws a compact primary-colour proportion bar. The caller supplies
// the total represented by the bar; it may be total Docker-managed disk space
// rather than host filesystem capacity.
func UsageBar(used, total int64, width int) string {
	width = max(width, 4)
	if total <= 0 || used <= 0 {
		return lipgloss.NewStyle().Foreground(Muted).Render(strings.Repeat("░", width))
	}
	filled := int((used*int64(width) + total - 1) / total)
	filled = min(max(filled, 1), width)
	return lipgloss.NewStyle().Foreground(Primary).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(Muted).Render(strings.Repeat("░", width-filled))
}

// Percent returns a clamped whole-number proportion suitable for a compact UI.
func Percent(used, total int64) int {
	if total <= 0 || used <= 0 {
		return 0
	}
	return min(int(used*100/total), 100)
}
