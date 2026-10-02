package setup

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/serverconfig"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

// colour palette reused from the main TUI.
var (
	colorPrimary = lipgloss.Color("#249DFF")
	colorMuted   = lipgloss.Color("#A7AFBA")
	colorSuccess = lipgloss.Color("#20D65A")
	colorWarning = lipgloss.Color("#FFD21F")
	colorDanger  = lipgloss.Color("#FF4D4D")
	colorBorder  = lipgloss.Color("#168BFF")
)

var (
	stylePrimary = lipgloss.NewStyle().Foreground(colorPrimary)
	styleMuted   = lipgloss.NewStyle().Foreground(colorMuted)
	styleSuccess = lipgloss.NewStyle().Foreground(colorSuccess)
	styleDanger  = lipgloss.NewStyle().Foreground(colorDanger)
	styleWarning = lipgloss.NewStyle().Foreground(colorWarning)
	styleBold    = lipgloss.NewStyle().Bold(true)
	styleBox     = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(1, 2)
)

// View implements tea.Model.
func (m Model) View() tea.View {
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n\n")

	switch m.activeScreen {
	case screenServer:
		b.WriteString(m.viewServer())
	case screenAuth:
		b.WriteString(m.viewAuth())
	case screenDocker:
		b.WriteString(m.viewDocker())
	case screenCompose:
		b.WriteString(m.viewCompose())
	case screenReview:
		b.WriteString(m.viewReview())
	}
	view := tea.NewView(b.String())
	view.AltScreen = true
	view.WindowTitle = "SSH Docker TUI Setup"
	return view
}

func (m Model) renderHeader() string {
	title := stylePrimary.Bold(true).Render("⚙  SSH Docker TUI — Setup")
	tabs := make([]string, screenCount)
	for i := screen(0); i < screenCount; i++ {
		label := " " + i.title() + " "
		if i == m.activeScreen {
			tabs[i] = styleBold.Foreground(lipgloss.Color("#EAF6FF")).Background(lipgloss.Color("#10365C")).Render(label)
		} else {
			tabs[i] = styleMuted.Render(label)
		}
	}
	return title + "\n\n" + wrapStyled(tabs, styleMuted.Render("│"), m.contentWidth())
}

// --- Server screen ---

func (m Model) viewServer() string {
	var b strings.Builder
	b.WriteString(styleBold.Render("Server Settings") + "\n\n")
	b.WriteString(m.mutedText("Controls the SSH listening address and server identity. Leave fields empty for defaults.") + "\n\n")

	for i, f := range m.serverFields {
		active := i == m.activeField
		b.WriteString(renderField(f, active, m.contentWidth()))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(commandBar(m, "↑/↓/Tab Move │ Enter Next │ Esc Quit without saving"))
	return b.String()
}

// --- Auth screen ---

func (m Model) viewAuth() string {
	var b strings.Builder
	b.WriteString(styleBold.Render("Authentication") + "\n\n")
	b.WriteString(m.mutedText("Choose how SSH clients authenticate. Passwords are stored as hashes; keys use public keys.") + "\n\n")

	// Status
	if m.working.Auth.HasPasswordAuth() {
		b.WriteString(styleSuccess.Render("✓ Password authentication enabled") + "\n")
	} else {
		b.WriteString(styleMuted.Render("  Password authentication disabled") + "\n")
	}
	if m.working.Auth.HasKeyAuth() {
		b.WriteString(styleSuccess.Render(fmt.Sprintf("✓ %d authorized SSH key(s)", len(m.working.Auth.AuthorizedKeys))) + "\n")
		for i, key := range m.working.Auth.AuthorizedKeys {
			fp, _ := serverconfig.KeyFingerprint(key)
			prefix := "  "
			if m.authAction == authActionRemoveKey && i == m.removeKeyIndex {
				prefix = styleDanger.Render("▶ ")
			}
			b.WriteString(wrapStyledText(prefix, styleMuted.Render(fp), m.contentWidth()) + "\n")
		}
	} else {
		b.WriteString(styleMuted.Render("  No authorized keys configured") + "\n")
	}

	b.WriteString("\n")

	switch m.authAction {
	case authActionSetPassword:
		b.WriteString(stylePrimary.Render("Set password") + "\n")
		b.WriteString(m.mutedText(serverconfig.PasswordPolicyDescription()) + "\n")
		b.WriteString(renderPasswordField("Password:        ", strings.Repeat("•", len(m.passwordInput)), !m.passwordCursor, m.contentWidth()) + "\n")
		b.WriteString(renderPasswordField("Confirm password:", strings.Repeat("•", len(m.passwordConfirm)), m.passwordCursor, m.contentWidth()) + "\n")
		b.WriteString("\n" + commandLine(m, "Tab", "Switch field") + "\n")
		b.WriteString(commandLine(m, "Enter", "Confirm") + "\n")
		b.WriteString(commandLine(m, "Esc", "Cancel") + "\n")

	case authActionAddKey:
		b.WriteString(m.primaryText("Paste SSH public key (authorized_keys format):") + "\n")
		b.WriteString(renderInput("", m.newKeyInput, true, m.contentWidth()) + "\n")
		b.WriteString("\n" + commandLine(m, "Enter", "Add key") + "\n")
		b.WriteString(commandLine(m, "Esc", "Cancel") + "\n")

	case authActionRemoveKey:
		b.WriteString(stylePrimary.Render("Select key to remove (↑/↓ then Enter):") + "\n")
		b.WriteString("\n" + commandLine(m, "Enter", "Remove key") + "\n")
		b.WriteString(commandLine(m, "Esc", "Cancel") + "\n")

	default:
		b.WriteString(commandLine(m, "p", "Set/replace password") + "\n")
		b.WriteString(commandLine(m, "d", "Disable password") + "\n")
		b.WriteString(commandLine(m, "a", "Add key") + "\n")
		b.WriteString(commandLine(m, "r", "Remove key") + "\n")
		b.WriteString(commandLine(m, "Enter", "Continue") + "\n")
		b.WriteString(commandLine(m, "Esc", "Back") + "\n")
	}

	if m.authError != "" {
		b.WriteString("\n" + styleDanger.Render(wrapPlain("✕ "+m.authError, m.contentWidth())) + "\n")
	}
	return b.String()
}

func commandLine(m Model, key, description string) string {
	return "  " + ui.CommandBar(key+" "+description, m.contentWidth())
}

func commandBar(m Model, value string) string {
	width := m.contentWidth()
	items := strings.Split(value, "│")
	lines := make([]string, 0, len(items))
	line := ""
	for _, item := range items {
		item = strings.TrimSpace(item)
		candidate := item
		if line != "" {
			candidate = line + " │ " + item
		}
		if line != "" && lipgloss.Width(candidate) > width {
			lines = append(lines, ui.CommandBar(line, width))
			line = item
			continue
		}
		line = candidate
	}
	if line != "" {
		lines = append(lines, ui.CommandBar(line, width))
	}
	return strings.Join(lines, "\n")
}

// --- Docker screen ---

func (m Model) viewDocker() string {
	var b strings.Builder
	b.WriteString(styleBold.Render("Docker Settings") + "\n\n")
	b.WriteString(m.mutedText("Select the Docker daemon endpoint. Leave empty to use Docker's environment defaults.") + "\n\n")
	b.WriteString(renderField(m.dockerFields[fieldDockerEndpoint], true, m.contentWidth()))
	b.WriteString("\n\n")
	b.WriteString(commandBar(m, "Enter Next │ Esc Back"))
	return b.String()
}

// --- Compose screen ---

func (m Model) viewCompose() string {
	var b strings.Builder
	b.WriteString(styleBold.Render("Compose Roots") + "\n\n")
	b.WriteString(m.mutedText("Allowed folders for Compose files; the first root is the managed-project default.") + "\n\n")

	if len(m.composeRoots) == 0 {
		b.WriteString(styleWarning.Render("  (no roots configured)") + "\n")
	}
	for i, root := range m.composeRoots {
		prefix := "  "
		if i == m.composeSelected {
			prefix = stylePrimary.Render("▶ ")
		}
		b.WriteString(wrapStyledText(prefix, root, m.contentWidth()) + "\n")
		if i == 0 {
			indent := strings.Repeat(" ", lipgloss.Width(prefix))
			b.WriteString(wrapStyledText(indent, styleMuted.Render("← default managed-config root"), m.contentWidth()) + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(renderInput("Add root: ", m.composeInput, true, m.contentWidth()) + "\n")
	if m.composeError != "" {
		b.WriteString(styleDanger.Render(wrapPlain("✕ "+m.composeError, m.contentWidth())) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(commandBar(m, "Enter Next │ → Next │ Tab Select │ ↑/↓ Reorder │ ctrl+d Remove │ Esc Back"))
	return b.String()
}

// --- Review screen ---

func (m Model) viewReview() string {
	var b strings.Builder
	b.WriteString(styleBold.Render("Review & Save") + "\n\n")
	b.WriteString(m.mutedText("Check the redacted settings, then save them to the private server configuration file.") + "\n\n")

	b.WriteString(styleBox.Render(m.renderSummary(max(m.contentWidth()-6, 20))))
	b.WriteString("\n\n")

	if m.saveSuccess {
		b.WriteString(styleSuccess.Render(wrapPlain("✓ Configuration saved to "+m.configPath, m.contentWidth())) + "\n\n")
		b.WriteString(commandBar(m, "q Quit │ Esc Back"))
	} else {
		if m.saveError != "" {
			b.WriteString(styleDanger.Render(wrapPlain("✕ "+m.saveError, m.contentWidth())) + "\n\n")
		}
		b.WriteString(commandBar(m, "s Save │ Esc Back │ q Quit without saving"))
	}
	return b.String()
}

func (m Model) renderSummary(width int) string {
	var b strings.Builder

	addr := m.working.Server.Address
	if addr == "" {
		addr = "(default)"
	}
	b.WriteString(summaryLine("Listen address:  ", addr, width) + "\n")

	hkp := m.working.Server.HostKeyPath
	if hkp == "" {
		hkp = "(default)"
	}
	b.WriteString(summaryLine("Host-key path:   ", hkp, width) + "\n")

	ep := m.working.Docker.Endpoint
	if ep == "" {
		ep = "(Docker defaults)"
	}
	b.WriteString(summaryLine("Docker endpoint: ", ep, width) + "\n")

	b.WriteString("\nAuthentication:\n")
	if m.working.Auth.HasPasswordAuth() {
		b.WriteString("  " + styleSuccess.Render("✓ Password: [hash stored, not shown]") + "\n")
	} else {
		b.WriteString("  " + styleMuted.Render("  Password: disabled") + "\n")
	}
	if m.working.Auth.HasKeyAuth() {
		b.WriteString(fmt.Sprintf("  "+styleSuccess.Render("✓ Authorized keys: %d"), len(m.working.Auth.AuthorizedKeys)) + "\n")
		for _, key := range m.working.Auth.AuthorizedKeys {
			fp, _ := serverconfig.KeyFingerprint(key)
			b.WriteString(wrapStyledText("      ", styleMuted.Render(fp), width) + "\n")
		}
	} else {
		b.WriteString("  " + styleMuted.Render("  Authorized keys: none") + "\n")
	}

	b.WriteString("\nCompose roots:\n")
	if len(m.working.Compose.Roots) == 0 {
		b.WriteString("  " + styleWarning.Render("(none configured)") + "\n")
	}
	for i, root := range m.working.Compose.Roots {
		marker := fmt.Sprintf("[%d]", i+1)
		if i == 0 {
			marker = stylePrimary.Render("[1*]")
		}
		b.WriteString(wrapStyledText("  "+marker+" ", root, width) + "\n")
	}
	return b.String()
}

// --- Shared field rendering ---

func renderField(f fieldModel, active bool, width int) string {
	labelStyle := styleMuted
	if active {
		labelStyle = stylePrimary
	}

	line := "  " + labelStyle.Render(wrapPlain(f.label+":", max(width-2, 1))) + "\n" + renderInput("    ", f.value, active, width)
	if f.err != "" {
		line += "\n    " + styleDanger.Render(wrapPlain("✕ "+f.err, max(width-4, 1)))
	}
	return line
}

func renderPasswordField(label, masked string, active bool, width int) string {
	cursor := ""
	if active {
		cursor = stylePrimary.Render("█")
	}
	labelStr := styleMuted.Render(label)
	if active {
		labelStr = stylePrimary.Render(label)
	}
	return wrapStyledText("  "+labelStr+" ", masked+cursor, width)
}

func (m Model) contentWidth() int {
	if m.width <= 0 {
		return 80
	}
	return max(m.width, 20)
}

func (m Model) mutedText(value string) string {
	return styleMuted.Render(wrapPlain(value, m.contentWidth()))
}

func (m Model) primaryText(value string) string {
	return stylePrimary.Render(wrapPlain(value, m.contentWidth()))
}

func wrapStyled(items []string, separator string, width int) string {
	lines := make([]string, 0, len(items))
	line := ""
	for _, item := range items {
		candidate := item
		if line != "" {
			candidate = line + separator + item
		}
		if line != "" && lipgloss.Width(candidate) > width {
			lines = append(lines, line)
			line = item
			continue
		}
		line = candidate
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func wrapStyledText(prefix, value string, width int) string {
	available := max(width-lipgloss.Width(prefix), 1)
	lines := strings.Split(wrapPlain(value, available), "\n")
	for index := range lines {
		if index == 0 {
			lines[index] = prefix + lines[index]
		} else {
			lines[index] = strings.Repeat(" ", lipgloss.Width(prefix)) + lines[index]
		}
	}
	return strings.Join(lines, "\n")
}

func renderInput(prefix, value string, active bool, width int) string {
	if !active && value == "" {
		return prefix + styleMuted.Render("(empty)")
	}
	if !active {
		return wrapStyledText(prefix, value, width)
	}
	runes := []rune(value)
	lineWidth := 0
	var b strings.Builder
	b.WriteString(prefix)
	lineWidth = lipgloss.Width(prefix)
	write := func(value string) {
		if lineWidth+lipgloss.Width(value) > width && lineWidth > lipgloss.Width(prefix) {
			b.WriteString("\n" + strings.Repeat(" ", lipgloss.Width(prefix)))
			lineWidth = lipgloss.Width(prefix)
		}
		b.WriteString(value)
		lineWidth += lipgloss.Width(value)
	}
	for _, r := range runes {
		write(string(r))
	}
	write(stylePrimary.Render("█"))
	return b.String()
}

func summaryLine(label, value string, width int) string {
	return wrapStyledText(label, value, width)
}

func wrapPlain(value string, width int) string {
	if width <= 0 {
		return ""
	}
	paragraphs := strings.Split(value, "\n")
	for index, paragraph := range paragraphs {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			continue
		}
		lines := make([]string, 0, len(words))
		line := ""
		for _, word := range words {
			for lipgloss.Width(word) > width {
				part, rest := splitToWidth(word, width)
				if line != "" {
					lines = append(lines, line)
					line = ""
				}
				lines = append(lines, part)
				word = rest
			}
			if line == "" {
				line = word
			} else if lipgloss.Width(line)+1+lipgloss.Width(word) <= width {
				line += " " + word
			} else {
				lines = append(lines, line)
				line = word
			}
		}
		if line != "" {
			lines = append(lines, line)
		}
		paragraphs[index] = strings.Join(lines, "\n")
	}
	return strings.Join(paragraphs, "\n")
}

func splitToWidth(value string, width int) (string, string) {
	runes := []rune(value)
	end := 0
	for end < len(runes) && lipgloss.Width(string(runes[:end+1])) <= width {
		end++
	}
	return string(runes[:end]), string(runes[end:])
}
