package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestSanitizeLineRemovesTerminalControls(t *testing.T) {
	value := SanitizeLine("safe\x1b[31mred\nnext\x07")
	if strings.ContainsAny(value, "\x1b\n\x07") {
		t.Fatalf("control characters survived sanitization: %q", value)
	}
	if !strings.Contains(value, "safe [31mred next ") {
		t.Fatalf("unexpected sanitized value: %q", value)
	}
}

func TestTruncatePreservesANSIBoundariesAndWidth(t *testing.T) {
	styled := lipgloss.NewStyle().Bold(true).Render("abcdefgh")
	result := Truncate(styled, 5)
	if got := lipgloss.Width(result); got > 5 {
		t.Fatalf("truncated width = %d, want <= 5", got)
	}
	if !strings.Contains(result, "…") {
		t.Fatalf("truncated result %q has no ellipsis", result)
	}
}

func TestFormatBytes(t *testing.T) {
	for _, test := range []struct {
		value int64
		want  string
	}{
		{value: 0, want: "0 B"},
		{value: 1024, want: "1.0 KiB"},
		{value: 1024 * 1024, want: "1.0 MiB"},
	} {
		if got := FormatBytes(test.value); got != test.want {
			t.Fatalf("FormatBytes(%d) = %q, want %q", test.value, got, test.want)
		}
	}
}
