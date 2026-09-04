// Package ui contains small presentation helpers shared by the root TUI and
// page views. It has no backend or Bubble Tea state.
package ui

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	MinimumWidth  = 80
	MinimumHeight = 24
)

var (
	Primary = lipgloss.Color("#7D9CFF")
	Muted   = lipgloss.Color("#7C8496")
	Success = lipgloss.Color("#7BD88F")
	Warning = lipgloss.Color("#F2C66D")
	Danger  = lipgloss.Color("#FF7A90")
	Border  = lipgloss.Color("#485267")
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
