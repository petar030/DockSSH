// Package setup implements the local host-side Bubble Tea configuration TUI
// for the ssh-docker-tui server.  It is not exposed over SSH and has no
// connection to the Docker management TUI pages.
package setup

import (
	"github.com/petar030/ssh-native-docker-tui/internal/serverconfig"
)

// screen identifies the current setup screen.
type screen int

const (
	screenServer screen = iota
	screenAuth
	screenDocker
	screenCompose
	screenReview
	screenCount
)

func (s screen) title() string {
	switch s {
	case screenServer:
		return "Server"
	case screenAuth:
		return "Authentication"
	case screenDocker:
		return "Docker"
	case screenCompose:
		return "Compose Roots"
	case screenReview:
		return "Review & Save"
	default:
		return ""
	}
}

// field indices for the server screen.
const (
	fieldAddress  = iota
	fieldHostKey
	serverFieldCount
)

// field indices for the docker screen.
const (
	fieldDockerEndpoint = iota
	dockerFieldCount
)

// authAction describes the active auth sub-screen.
type authAction int

const (
	authActionNone authAction = iota
	authActionSetPassword
	authActionRemovePassword
	authActionAddKey
	authActionRemoveKey
)

// Model holds all setup state.  It is the sole Bubble Tea model for the
// setup program.
type Model struct {
	// configPath is the path where the configuration file will be written.
	configPath string
	// original is the configuration loaded at startup.
	original serverconfig.Config
	// working is the in-progress edited configuration.
	working serverconfig.Config
	// store is the persistence layer; injected for testability.
	store Store

	// current screen
	activeScreen screen

	// server screen fields and active field index
	serverFields [serverFieldCount]fieldModel
	activeField  int

	// auth screen state
	authAction      authAction
	passwordInput   string // masked during display; cleared after hashing
	passwordConfirm string // masked; used only to confirm match
	passwordCursor  bool   // false = typing in passwordInput, true = passwordConfirm
	newKeyInput     string
	authError       string
	removeKeyIndex  int // index of key selected for removal (-1 = none)

	// docker screen fields
	dockerFields [dockerFieldCount]fieldModel

	// compose screen
	composeRoots    []string
	composeInput    string
	composeError    string
	composeSelected int // index of selected root for reorder/remove (-1 = none)

	// review screen
	saveError   string
	saveSuccess bool

	// terminal dimensions
	width  int
	height int
}

// fieldModel is a simple editable text field with label, value, inline cursor
// position, and an optional validation error message.
type fieldModel struct {
	label  string
	value  string
	cursor int
	err    string
}

// Store is the narrow interface the setup model calls to persist configuration.
// It matches serverconfig.Save's signature so tests can inject a stub.
type Store interface {
	Save(path string, cfg serverconfig.Config) error
}

// defaultStore delegates to the real serverconfig.Save.
type defaultStore struct{}

func (defaultStore) Save(path string, cfg serverconfig.Config) error {
	return serverconfig.Save(path, cfg)
}
