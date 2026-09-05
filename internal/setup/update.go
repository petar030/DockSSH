package setup

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/serverconfig"
)

// New creates a Model loaded with cfg and ready to edit at the first screen.
func New(configPath string, cfg serverconfig.Config, store Store) Model {
	if store == nil {
		store = defaultStore{}
	}
	m := Model{
		configPath:      configPath,
		original:        cfg,
		working:         cfg,
		activeScreen:    screenServer,
		removeKeyIndex:  -1,
		composeSelected: -1,
		store:           store,
	}

	// Populate server fields from config.
	m.serverFields[fieldAddress] = fieldModel{
		label: "Listen address (host:port) [default: " + serverconfig.DefaultListenAddress + "]",
		value: cfg.Server.Address,
	}
	m.serverFields[fieldHostKey] = fieldModel{
		label: "Host-key path [default: " + serverconfig.DefaultHostKeyPath + "]",
		value: cfg.Server.HostKeyPath,
	}

	// Populate docker fields.
	m.dockerFields[fieldDockerEndpoint] = fieldModel{
		label: "Docker endpoint (leave empty for defaults)",
		value: cfg.Docker.Endpoint,
	}

	// Copy compose roots.
	m.composeRoots = make([]string, len(cfg.Compose.Roots))
	copy(m.composeRoots, cfg.Compose.Roots)

	return m
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global: ctrl+c quits without saving.
	if key == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.activeScreen {
	case screenServer:
		return m.updateServer(key)
	case screenAuth:
		return m.updateAuth(key)
	case screenDocker:
		return m.updateDocker(key)
	case screenCompose:
		return m.updateCompose(key)
	case screenReview:
		return m.updateReview(key)
	}
	return m, nil
}

// --- Server screen ---

func (m Model) updateServer(key string) (Model, tea.Cmd) {
	switch key {
	case "tab", "down":
		m.activeField = (m.activeField + 1) % serverFieldCount
	case "shift+tab", "up":
		m.activeField = (m.activeField - 1 + serverFieldCount) % serverFieldCount
	case "enter":
		m = m.applyServerFields()
		if !m.serverHasErrors() {
			m.activeScreen = screenAuth
			m.activeField = 0
		}
	case "esc":
		return m, tea.Quit
	case "backspace":
		f := &m.serverFields[m.activeField]
		if f.cursor > 0 {
			runes := []rune(f.value)
			f.value = string(runes[:f.cursor-1]) + string(runes[f.cursor:])
			f.cursor--
		}
	case "left":
		if m.serverFields[m.activeField].cursor > 0 {
			m.serverFields[m.activeField].cursor--
		}
	case "right":
		f := m.serverFields[m.activeField]
		if f.cursor < len([]rune(f.value)) {
			m.serverFields[m.activeField].cursor++
		}
	default:
		if isPrintable(key) {
			f := &m.serverFields[m.activeField]
			runes := []rune(f.value)
			f.value = string(runes[:f.cursor]) + key + string(runes[f.cursor:])
			f.cursor++
		}
	}
	return m, nil
}

func (m Model) applyServerFields() Model {
	addr := strings.TrimSpace(m.serverFields[fieldAddress].value)
	m.working.Server.Address = addr
	if addr != "" {
		_, _, err := netSplitHostPort(addr)
		if err != nil {
			m.serverFields[fieldAddress].err = "must be host:port (e.g. 127.0.0.1:23234)"
		} else {
			m.serverFields[fieldAddress].err = ""
		}
	} else {
		m.serverFields[fieldAddress].err = ""
	}

	m.working.Server.HostKeyPath = strings.TrimSpace(m.serverFields[fieldHostKey].value)
	m.serverFields[fieldHostKey].err = ""
	return m
}

func (m Model) serverHasErrors() bool {
	for _, f := range m.serverFields {
		if f.err != "" {
			return true
		}
	}
	return false
}

// --- Auth screen ---

func (m Model) updateAuth(key string) (Model, tea.Cmd) {
	switch m.authAction {
	case authActionSetPassword:
		return m.updateAuthSetPassword(key)
	case authActionAddKey:
		return m.updateAuthAddKey(key)
	case authActionRemoveKey:
		return m.updateAuthRemoveKey(key)
	default:
		return m.updateAuthMenu(key)
	}
}

func (m Model) updateAuthMenu(key string) (Model, tea.Cmd) {
	switch key {
	case "p":
		m.authAction = authActionSetPassword
		m.passwordInput = ""
		m.passwordConfirm = ""
		m.passwordCursor = false
		m.authError = ""
	case "d":
		m.working.Auth.PasswordHash = ""
		m.authError = ""
	case "a":
		m.authAction = authActionAddKey
		m.newKeyInput = ""
		m.authError = ""
	case "r":
		if len(m.working.Auth.AuthorizedKeys) > 0 {
			m.authAction = authActionRemoveKey
			m.removeKeyIndex = 0
			m.authError = ""
		}
	case "enter":
		m.activeScreen = screenDocker
		m.activeField = 0
		m.authError = ""
	case "esc":
		m.activeScreen = screenServer
		m.authError = ""
	}
	return m, nil
}

func (m Model) updateAuthSetPassword(key string) (Model, tea.Cmd) {
	switch key {
	case "tab":
		m.passwordCursor = !m.passwordCursor
	case "enter":
		if !m.passwordCursor {
			// Move to confirm field.
			m.passwordCursor = true
			return m, nil
		}
		// Both fields filled; validate.
		if m.passwordInput == "" {
			m.authError = "password must not be empty"
			m.passwordInput = ""
			m.passwordConfirm = ""
			m.passwordCursor = false
			return m, nil
		}
		if m.passwordInput != m.passwordConfirm {
			m.authError = "passwords do not match"
			m.passwordInput = ""
			m.passwordConfirm = ""
			m.passwordCursor = false
			return m, nil
		}
		if err := serverconfig.ValidatePassword(m.passwordInput); err != nil {
			m.authError = err.Error()
			m.passwordInput = ""
			m.passwordConfirm = ""
			m.passwordCursor = false
			return m, nil
		}
		hash, err := serverconfig.HashPassword(m.passwordInput)
		if err != nil {
			m.authError = "could not hash password: " + err.Error()
		} else {
			m.working.Auth.PasswordHash = hash
			m.authError = ""
		}
		// Always clear plaintext from model immediately.
		m.passwordInput = ""
		m.passwordConfirm = ""
		m.passwordCursor = false
		m.authAction = authActionNone
	case "esc":
		m.passwordInput = ""
		m.passwordConfirm = ""
		m.passwordCursor = false
		m.authAction = authActionNone
		m.authError = ""
	case "backspace":
		if !m.passwordCursor {
			if len(m.passwordInput) > 0 {
				m.passwordInput = m.passwordInput[:len(m.passwordInput)-1]
			}
		} else {
			if len(m.passwordConfirm) > 0 {
				m.passwordConfirm = m.passwordConfirm[:len(m.passwordConfirm)-1]
			}
		}
	default:
		if isPrintable(key) {
			if !m.passwordCursor {
				m.passwordInput += key
			} else {
				m.passwordConfirm += key
			}
		}
	}
	return m, nil
}

func (m Model) updateAuthAddKey(key string) (Model, tea.Cmd) {
	switch key {
	case "enter":
		canonical, err := serverconfig.ParseAuthorizedKey(m.newKeyInput)
		if err != nil {
			m.authError = "invalid key: " + err.Error()
			return m, nil
		}
		m.working.Auth.AuthorizedKeys = append(m.working.Auth.AuthorizedKeys, canonical)
		m.newKeyInput = ""
		m.authAction = authActionNone
		m.authError = ""
	case "esc":
		m.newKeyInput = ""
		m.authAction = authActionNone
		m.authError = ""
	case "backspace":
		if len(m.newKeyInput) > 0 {
			m.newKeyInput = m.newKeyInput[:len(m.newKeyInput)-1]
		}
	default:
		if isPrintable(key) {
			m.newKeyInput += key
		}
	}
	return m, nil
}

func (m Model) updateAuthRemoveKey(key string) (Model, tea.Cmd) {
	n := len(m.working.Auth.AuthorizedKeys)
	switch key {
	case "up":
		if m.removeKeyIndex > 0 {
			m.removeKeyIndex--
		}
	case "down":
		if m.removeKeyIndex < n-1 {
			m.removeKeyIndex++
		}
	case "enter", "d":
		if m.removeKeyIndex >= 0 && m.removeKeyIndex < n {
			keys := m.working.Auth.AuthorizedKeys
			m.working.Auth.AuthorizedKeys = append(append([]string{}, keys[:m.removeKeyIndex]...), keys[m.removeKeyIndex+1:]...)
			if m.removeKeyIndex >= len(m.working.Auth.AuthorizedKeys) {
				m.removeKeyIndex = len(m.working.Auth.AuthorizedKeys) - 1
			}
		}
		m.authAction = authActionNone
	case "esc":
		m.removeKeyIndex = -1
		m.authAction = authActionNone
	}
	return m, nil
}

// --- Docker screen ---

func (m Model) updateDocker(key string) (Model, tea.Cmd) {
	switch key {
	case "enter":
		m.working.Docker.Endpoint = strings.TrimSpace(m.dockerFields[fieldDockerEndpoint].value)
		m.activeScreen = screenCompose
		m.composeInput = ""
		m.composeError = ""
	case "esc":
		m.activeScreen = screenAuth
	case "backspace":
		f := &m.dockerFields[m.activeField]
		if f.cursor > 0 {
			runes := []rune(f.value)
			f.value = string(runes[:f.cursor-1]) + string(runes[f.cursor:])
			f.cursor--
		}
	case "left":
		if m.dockerFields[m.activeField].cursor > 0 {
			m.dockerFields[m.activeField].cursor--
		}
	case "right":
		f := m.dockerFields[m.activeField]
		if f.cursor < len([]rune(f.value)) {
			m.dockerFields[m.activeField].cursor++
		}
	default:
		if isPrintable(key) {
			f := &m.dockerFields[m.activeField]
			runes := []rune(f.value)
			f.value = string(runes[:f.cursor]) + key + string(runes[f.cursor:])
			f.cursor++
		}
	}
	return m, nil
}

// --- Compose screen ---

func (m Model) updateCompose(key string) (Model, tea.Cmd) {
	switch key {
	case "enter":
		if m.composeInput != "" {
			root := strings.TrimSpace(m.composeInput)
			canonical, err := canonicalizeRoot(root)
			if err != nil {
				m.composeError = err.Error()
			} else if isDuplicate(m.composeRoots, canonical) {
				m.composeError = "duplicate root"
			} else {
				m.composeRoots = append(m.composeRoots, canonical)
				m.composeInput = ""
				m.composeError = ""
				m.working.Compose.Roots = m.composeRoots
			}
		} else {
			// Empty input = advance to review.
			m.working.Compose.Roots = m.composeRoots
			m.activeScreen = screenReview
			m.saveError = ""
			m.saveSuccess = false
		}
	case "right":
		m.working.Compose.Roots = m.composeRoots
		m.activeScreen = screenReview
		m.saveError = ""
		m.saveSuccess = false
	case "ctrl+d":
		if m.composeSelected >= 0 && m.composeSelected < len(m.composeRoots) {
			m.composeRoots = append(append([]string{}, m.composeRoots[:m.composeSelected]...), m.composeRoots[m.composeSelected+1:]...)
			if m.composeSelected >= len(m.composeRoots) {
				m.composeSelected = len(m.composeRoots) - 1
			}
			m.working.Compose.Roots = m.composeRoots
		}
	case "up":
		if m.composeSelected > 0 {
			m.composeRoots[m.composeSelected], m.composeRoots[m.composeSelected-1] =
				m.composeRoots[m.composeSelected-1], m.composeRoots[m.composeSelected]
			m.composeSelected--
		}
	case "down":
		if m.composeSelected >= 0 && m.composeSelected < len(m.composeRoots)-1 {
			m.composeRoots[m.composeSelected], m.composeRoots[m.composeSelected+1] =
				m.composeRoots[m.composeSelected+1], m.composeRoots[m.composeSelected]
			m.composeSelected++
		}
	case "tab":
		if len(m.composeRoots) > 0 {
			next := m.composeSelected + 1
			if next >= len(m.composeRoots) {
				next = 0
			}
			m.composeSelected = next
		}
	case "esc":
		m.activeScreen = screenDocker
		m.composeInput = ""
		m.composeError = ""
	case "backspace":
		if len(m.composeInput) > 0 {
			m.composeInput = m.composeInput[:len(m.composeInput)-1]
		}
	default:
		if isPrintable(key) {
			m.composeInput += key
		}
	}
	return m, nil
}

// --- Review screen ---

func (m Model) updateReview(key string) (Model, tea.Cmd) {
	switch key {
	case "s":
		err := m.store.Save(m.configPath, m.working)
		if err != nil {
			m.saveError = err.Error()
			m.saveSuccess = false
		} else {
			m.saveSuccess = true
			m.saveError = ""
		}
	case "esc":
		m.activeScreen = screenCompose
		m.saveError = ""
		m.saveSuccess = false
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

// isPrintable reports whether the key string is a single printable ASCII character.
func isPrintable(key string) bool {
	if len(key) != 1 {
		return false
	}
	r := rune(key[0])
	return r >= 32 && r < 127
}
