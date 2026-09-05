package setup

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/serverconfig"
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
	return title + "\n" + strings.Join(tabs, styleMuted.Render("│"))
}

// --- Server screen ---

func (m Model) viewServer() string {
	var b strings.Builder
	b.WriteString(styleBold.Render("Server Settings") + "\n\n")
	b.WriteString(styleMuted.Render("Controls the SSH listening address and server identity. Leave fields empty for defaults.") + "\n\n")

	for i, f := range m.serverFields {
		active := i == m.activeField
		b.WriteString(renderField(f, active))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(styleMuted.Render("↑/↓ or Tab — move   Enter — next   Esc — quit without saving"))
	return b.String()
}

// --- Auth screen ---

func (m Model) viewAuth() string {
	var b strings.Builder
	b.WriteString(styleBold.Render("Authentication") + "\n\n")
	b.WriteString(styleMuted.Render("Choose how SSH clients authenticate. Passwords are stored as hashes; keys use public keys.") + "\n\n")

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
			b.WriteString(prefix + styleMuted.Render(fp) + "\n")
		}
	} else {
		b.WriteString(styleMuted.Render("  No authorized keys configured") + "\n")
	}

	b.WriteString("\n")

	switch m.authAction {
	case authActionSetPassword:
		b.WriteString(stylePrimary.Render("Set password") + "\n")
		b.WriteString(styleMuted.Render(serverconfig.PasswordPolicyDescription()) + "\n")
		b.WriteString(renderPasswordField("Password:        ", strings.Repeat("•", len(m.passwordInput)), !m.passwordCursor) + "\n")
		b.WriteString(renderPasswordField("Confirm password:", strings.Repeat("•", len(m.passwordConfirm)), m.passwordCursor) + "\n")
		b.WriteString(styleMuted.Render("Tab — switch field   Enter — confirm   Esc — cancel") + "\n")

	case authActionAddKey:
		b.WriteString(stylePrimary.Render("Paste SSH public key (authorized_keys format):") + "\n")
		b.WriteString("> " + m.newKeyInput + "█\n")
		b.WriteString(styleMuted.Render("Enter — add   Esc — cancel") + "\n")

	case authActionRemoveKey:
		b.WriteString(stylePrimary.Render("Select key to remove (↑/↓ then Enter):") + "\n")
		b.WriteString(styleMuted.Render("Enter — remove   Esc — cancel") + "\n")

	default:
		b.WriteString(styleMuted.Render("p Set/replace password   d Disable password\n"))
		b.WriteString(styleMuted.Render("a Add key   r Remove key\n"))
		b.WriteString(styleMuted.Render("Enter Continue   Esc Back") + "\n")
	}

	if m.authError != "" {
		b.WriteString("\n" + styleDanger.Render("✕ "+m.authError) + "\n")
	}
	return b.String()
}

// --- Docker screen ---

func (m Model) viewDocker() string {
	var b strings.Builder
	b.WriteString(styleBold.Render("Docker Settings") + "\n\n")
	b.WriteString(styleMuted.Render("Select the Docker daemon endpoint. Leave empty to use Docker's environment defaults.") + "\n\n")
	b.WriteString(renderField(m.dockerFields[fieldDockerEndpoint], true))
	b.WriteString("\n\n")
	b.WriteString(styleMuted.Render("Enter — next   Esc — back"))
	return b.String()
}

// --- Compose screen ---

func (m Model) viewCompose() string {
	var b strings.Builder
	b.WriteString(styleBold.Render("Compose Roots") + "\n\n")
	b.WriteString(styleMuted.Render("Allowed folders for Compose files; the first root is the managed-project default.") + "\n\n")

	if len(m.composeRoots) == 0 {
		b.WriteString(styleWarning.Render("  (no roots configured)") + "\n")
	}
	for i, root := range m.composeRoots {
		prefix := "  "
		if i == m.composeSelected {
			prefix = stylePrimary.Render("▶ ")
		}
		label := root
		if i == 0 {
			label += styleMuted.Render("  ← default managed-config root")
		}
		b.WriteString(prefix + label + "\n")
	}

	b.WriteString("\n")
	b.WriteString(stylePrimary.Render("Add root: ") + m.composeInput + "█\n")
	if m.composeError != "" {
		b.WriteString(styleDanger.Render("✕ "+m.composeError) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("Enter (empty) or → — next   Tab — select   ↑/↓ — reorder   ctrl+d — remove   Esc — back"))
	return b.String()
}

// --- Review screen ---

func (m Model) viewReview() string {
	var b strings.Builder
	b.WriteString(styleBold.Render("Review & Save") + "\n\n")
	b.WriteString(styleMuted.Render("Check the redacted settings, then save them to the private server configuration file.") + "\n\n")

	b.WriteString(styleBox.Render(m.renderSummary()))
	b.WriteString("\n\n")

	if m.saveSuccess {
		b.WriteString(styleSuccess.Render("✓ Configuration saved to "+m.configPath) + "\n\n")
		b.WriteString(styleMuted.Render("q — quit   Esc — back"))
	} else {
		if m.saveError != "" {
			b.WriteString(styleDanger.Render("✕ "+m.saveError) + "\n\n")
		}
		b.WriteString(styleMuted.Render("s — save   Esc — back   q — quit without saving"))
	}
	return b.String()
}

func (m Model) renderSummary() string {
	var b strings.Builder

	addr := m.working.Server.Address
	if addr == "" {
		addr = styleMuted.Render("(default)")
	}
	b.WriteString("Listen address:  " + addr + "\n")

	hkp := m.working.Server.HostKeyPath
	if hkp == "" {
		hkp = styleMuted.Render("(default)")
	}
	b.WriteString("Host-key path:   " + hkp + "\n")

	ep := m.working.Docker.Endpoint
	if ep == "" {
		ep = styleMuted.Render("(Docker defaults)")
	}
	b.WriteString("Docker endpoint: " + ep + "\n")

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
			b.WriteString("      " + styleMuted.Render(fp) + "\n")
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
		b.WriteString("  " + marker + " " + root + "\n")
	}
	return b.String()
}

// --- Shared field rendering ---

func renderField(f fieldModel, active bool) string {
	labelStyle := styleMuted
	valueStyle := lipgloss.NewStyle()
	if active {
		labelStyle = stylePrimary
	}

	label := labelStyle.Render(f.label + ":")
	runes := []rune(f.value)
	cursor := f.cursor
	if cursor > len(runes) {
		cursor = len(runes)
	}
	before := string(runes[:cursor])
	after := string(runes[cursor:])

	var valueStr string
	if active {
		valueStr = valueStyle.Render(before) + stylePrimary.Render("█") + valueStyle.Render(after)
	} else {
		valueStr = valueStyle.Render(f.value)
		if f.value == "" {
			valueStr = styleMuted.Render("(empty)")
		}
	}

	line := "  " + label + " " + valueStr
	if f.err != "" {
		line += "  " + styleDanger.Render("✕ "+f.err)
	}
	return line
}

func renderPasswordField(label, masked string, active bool) string {
	cursor := ""
	if active {
		cursor = stylePrimary.Render("█")
	}
	labelStr := styleMuted.Render(label)
	if active {
		labelStr = stylePrimary.Render(label)
	}
	return "  " + labelStr + " " + masked + cursor
}
