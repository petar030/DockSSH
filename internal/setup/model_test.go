package setup

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/serverconfig"
)

// --- Store stub ---

type stubStore struct {
	called   bool
	lastPath string
	lastCfg  serverconfig.Config
	err      error
}

func (s *stubStore) Save(path string, cfg serverconfig.Config) error {
	s.called = true
	s.lastPath = path
	s.lastCfg = cfg
	return s.err
}

// --- Test helpers ---

const (
	testConfigPath = "/tmp/test-config.json"
	testPassword   = "hunter2!"
	testPubKey     = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl test-key"
)

func newTestModel(cfg serverconfig.Config) (Model, *stubStore) {
	store := &stubStore{}
	m := New(testConfigPath, cfg, store)
	return m, store
}

// --- Tests ---

func TestNewLoadsExistingConfig(t *testing.T) {
	hash := mustHashPassword(t, "secret")
	cfg := serverconfig.DefaultConfig()
	cfg.Server.Address = "127.0.0.1:9999"
	cfg.Auth.PasswordHash = hash
	m, _ := newTestModel(cfg)

	if m.serverFields[fieldAddress].value != "127.0.0.1:9999" {
		t.Errorf("address field = %q, want %q", m.serverFields[fieldAddress].value, "127.0.0.1:9999")
	}
	if m.working.Auth.PasswordHash != hash {
		t.Error("working config should carry loaded password hash")
	}
}

func TestCancelOnServerScreenDoesNotWrite(t *testing.T) {
	m, store := newTestModel(serverconfig.DefaultConfig())
	m = sendKeys(m, "esc")
	if store.called {
		t.Fatal("Esc on server screen must not call Save")
	}
}

func TestNavigationToAuthScreen(t *testing.T) {
	m, _ := newTestModel(serverconfig.DefaultConfig())
	m = sendKeys(m, "enter") // advance from server → auth
	if m.activeScreen != screenAuth {
		t.Errorf("screen = %v, want screenAuth", m.activeScreen)
	}
}

func TestAuthMenuNavigatesToDocker(t *testing.T) {
	m, _ := newTestModel(serverconfig.DefaultConfig())
	m = sendKeys(m, "enter") // server → auth
	m = sendKeys(m, "enter") // auth → docker
	if m.activeScreen != screenDocker {
		t.Errorf("screen = %v, want screenDocker", m.activeScreen)
	}
}

func TestPasswordInputIsMaskedInView(t *testing.T) {
	m, _ := newTestModel(serverconfig.DefaultConfig())
	m = sendKeys(m, "enter") // → auth
	m = sendKeys(m, "p")     // open password entry
	// Type a character
	m = sendKeys(m, "x")
	view := m.View().Content
	if strings.Contains(view, "x") {
		t.Fatal("plaintext character must not appear in view during password entry")
	}
	if !strings.Contains(view, "•") {
		t.Fatal("masked character indicator '•' should appear in view")
	}
}

func TestPasswordMismatchCannotSave(t *testing.T) {
	m, store := newTestModel(serverconfig.DefaultConfig())
	m = sendKeys(m, "enter") // → auth
	m = sendKeys(m, "p")     // start password entry
	// Type "abc" for password
	for _, ch := range "abc" {
		m = sendKeys(m, string(ch))
	}
	m = sendKeys(m, "tab") // switch to confirm
	// Type "xyz" for confirm
	for _, ch := range "xyz" {
		m = sendKeys(m, string(ch))
	}
	m = sendKeys(m, "enter") // attempt to confirm

	if m.authError == "" || !strings.Contains(m.authError, "do not match") {
		t.Errorf("expected mismatch error, got %q", m.authError)
	}
	// Password must not have been stored.
	if m.working.Auth.HasPasswordAuth() {
		t.Fatal("mismatched password should not set PasswordHash")
	}
	// Plaintext must have been cleared.
	if m.passwordInput != "" || m.passwordConfirm != "" {
		t.Fatal("plaintext password inputs must be cleared after failed confirmation")
	}
	if store.called {
		t.Fatal("Save must not be called after password mismatch")
	}
}

func TestPasswordMatchSetsHash(t *testing.T) {
	m, _ := newTestModel(serverconfig.DefaultConfig())
	m = sendKeys(m, "enter") // → auth
	m = sendKeys(m, "p")     // start password entry

	password := "superSecret99!"
	for _, ch := range password {
		m = sendKeys(m, string(ch))
	}
	m = sendKeys(m, "tab") // switch to confirm
	for _, ch := range password {
		m = sendKeys(m, string(ch))
	}
	m = sendKeys(m, "enter") // confirm

	if !m.working.Auth.HasPasswordAuth() {
		t.Fatal("matching password should set PasswordHash")
	}
	if m.passwordInput != "" || m.passwordConfirm != "" {
		t.Fatal("plaintext password fields must be cleared after successful hash")
	}
	if strings.Contains(m.working.Auth.PasswordHash, password) {
		t.Fatal("plaintext password must not appear in stored hash")
	}
}

func TestKeyAddAndRemove(t *testing.T) {
	m, _ := newTestModel(serverconfig.DefaultConfig())
	m = sendKeys(m, "enter") // → auth
	m = sendKeys(m, "a")     // add key action

	for _, ch := range testPubKey {
		m = sendKeys(m, string(ch))
	}
	m = sendKeys(m, "enter") // submit

	if len(m.working.Auth.AuthorizedKeys) != 1 {
		t.Fatalf("expected 1 key, got %d; error: %q", len(m.working.Auth.AuthorizedKeys), m.authError)
	}

	// Now remove it.
	m = sendKeys(m, "r")     // remove key action
	m = sendKeys(m, "enter") // confirm removal

	if len(m.working.Auth.AuthorizedKeys) != 0 {
		t.Fatalf("expected 0 keys after removal, got %d", len(m.working.Auth.AuthorizedKeys))
	}
}

func TestReviewSummaryIsRedacted(t *testing.T) {
	hash := mustHashPassword(t, "topsecret")
	cfg := serverconfig.DefaultConfig()
	cfg.Auth.PasswordHash = hash
	m, _ := newTestModel(cfg)

	// Navigate to review screen.
	m = sendKeys(m, "enter") // → auth
	m = sendKeys(m, "enter") // → docker
	m = sendKeys(m, "enter") // → compose
	m = sendKeys(m, "enter") // → review (empty input)

	if m.activeScreen != screenReview {
		t.Fatalf("expected screenReview, got %v", m.activeScreen)
	}
	view := m.View().Content
	if strings.Contains(view, hash) {
		t.Fatal("bcrypt hash must not appear in review view")
	}
	if strings.Contains(view, "topsecret") {
		t.Fatal("plaintext password must not appear in review view")
	}
}

func TestSetupViewsWrapAtNarrowTerminalWidth(t *testing.T) {
	cfg := serverconfig.DefaultConfig()
	cfg.Server.HostKeyPath = "/a/long/path/that/would/otherwise/run/past/the/edge/of/a/narrow/terminal/host_ed25519"
	cfg.Compose.Roots = []string{"/a/long/path/that/would/otherwise/run/past/the/edge/of/a/narrow/terminal/compose-projects"}
	m, _ := newTestModel(cfg)
	m.width = 48

	for _, activeScreen := range []screen{screenServer, screenAuth, screenDocker, screenCompose, screenReview} {
		m.activeScreen = activeScreen
		for _, line := range strings.Split(m.View().Content, "\n") {
			if width := lipgloss.Width(line); width > m.width {
				t.Fatalf("screen %v rendered %d columns at width %d: %q", activeScreen, width, m.width, line)
			}
		}
	}
}

func TestSaveCallsStoreOnce(t *testing.T) {
	m, store := newTestModel(serverconfig.DefaultConfig())
	m = sendKeys(m, "enter") // → auth
	m = sendKeys(m, "enter") // → docker
	m = sendKeys(m, "enter") // → compose
	m = sendKeys(m, "enter") // → review

	m = sendKeys(m, "s") // save

	if !store.called {
		t.Fatal("s key on review screen must call Store.Save")
	}
	if store.lastPath != testConfigPath {
		t.Errorf("Save path = %q, want %q", store.lastPath, testConfigPath)
	}
}

func TestCancelOnReviewDoesNotWrite(t *testing.T) {
	m, store := newTestModel(serverconfig.DefaultConfig())
	m = navigateToReview(m)
	m = sendKeys(m, "esc") // back to compose
	if store.called {
		t.Fatal("Esc on review must not write")
	}
}

func TestComposeRootAddAndRemove(t *testing.T) {
	dir := t.TempDir()
	m, _ := newTestModel(serverconfig.DefaultConfig())
	m = sendKeys(m, "enter") // → auth
	m = sendKeys(m, "enter") // → docker
	m = sendKeys(m, "enter") // → compose

	// Type the path
	for _, ch := range dir {
		m = sendKeys(m, string(ch))
	}
	m = sendKeys(m, "enter") // add

	if len(m.composeRoots) != 1 {
		t.Fatalf("expected 1 root, got %d; error: %q", len(m.composeRoots), m.composeError)
	}

	// Select and remove.
	m = sendKeys(m, "tab")    // select index 0
	m = sendKeys(m, "ctrl+d") // remove

	if len(m.composeRoots) != 0 {
		t.Fatalf("expected 0 roots after removal, got %d", len(m.composeRoots))
	}
}

// --- helpers ---

func mustHashPassword(t *testing.T, pw string) string {
	t.Helper()
	h, err := serverconfig.HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func sendKeys(m Model, keys ...string) Model {
	for _, key := range keys {
		result, _ := m.Update(keyMsgT{key})
		m = result.(Model)
	}
	return m
}

// keyMsgT is a minimal implementation of the tea.Msg interface that satisfies
// handleKey's type switch against tea.KeyMsg.
type keyMsgT struct{ s string }

func (k keyMsgT) String() string { return k.s }
func (k keyMsgT) Key() tea.Key   { return tea.Key{} }

func navigateToReview(m Model) Model {
	m = sendKeys(m, "enter") // server → auth
	m = sendKeys(m, "enter") // auth → docker
	m = sendKeys(m, "enter") // docker → compose
	m = sendKeys(m, "enter") // compose (empty) → review
	return m
}
