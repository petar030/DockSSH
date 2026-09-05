package ssh

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendcompose "github.com/petar030/ssh-native-docker-tui/internal/backend/compose"
	backendcontainers "github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	backendimages "github.com/petar030/ssh-native-docker-tui/internal/backend/images"
	backendnetworks "github.com/petar030/ssh-native-docker-tui/internal/backend/networks"
	backendsystem "github.com/petar030/ssh-native-docker-tui/internal/backend/system"
	backendvolumes "github.com/petar030/ssh-native-docker-tui/internal/backend/volumes"
	"github.com/petar030/ssh-native-docker-tui/internal/serverconfig"
)

type testBackend struct{}

func (testBackend) RequestRefresh(backend.Page) error  { return nil }
func (testBackend) Containers() *backendcontainers.API { return nil }
func (testBackend) Compose() *backendcompose.API       { return nil }
func (testBackend) Images() *backendimages.API         { return nil }
func (testBackend) Volumes() *backendvolumes.API       { return nil }
func (testBackend) Networks() *backendnetworks.API     { return nil }
func (testBackend) System() *backendsystem.API         { return nil }
func (testBackend) Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error) {
	return testSubscription{events: make(chan backend.EventEnvelope)}, nil
}

type testSubscription struct {
	events chan backend.EventEnvelope
}

func (subscription testSubscription) Events() <-chan backend.EventEnvelope {
	return subscription.events
}
func (testSubscription) Close() error { return nil }

func validHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := serverconfig.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

// --- Basic construction ---

func TestNewRequiresBackend(t *testing.T) {
	if _, err := New(Config{}); err == nil || !strings.Contains(err.Error(), "backend is required") {
		t.Fatalf("missing backend error = %v", err)
	}
}

func TestNewRejectsPublicAddressWithoutAuth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host-key")
	if _, err := New(Config{
		Address:     "0.0.0.0:2222",
		HostKeyPath: path,
		Backend:     testBackend{},
	}); err == nil || !strings.Contains(err.Error(), "authentication method") {
		t.Fatalf("expected auth-required error, got %v", err)
	}
	// Host key must not have been created for a rejected config.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("host key was created for rejected configuration")
	}
}

func TestNewAcceptsPublicAddressWithPasswordAuth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host-key")
	server, err := New(Config{
		Address:     "127.0.0.1:0",
		HostKeyPath: path,
		Backend:     testBackend{},
		Auth:        serverconfig.AuthConfig{PasswordHash: validHash(t, "pass")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = server.Close()
}

func TestNewAcceptsPublicAddressWithKeyAuth(t *testing.T) {
	const pubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl test"
	path := filepath.Join(t.TempDir(), "host-key")
	server, err := New(Config{
		Address:     "127.0.0.1:0",
		HostKeyPath: path,
		Backend:     testBackend{},
		Auth:        serverconfig.AuthConfig{AuthorizedKeys: []string{pubKey}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = server.Close()
}

// --- Loopback with no auth (first-run development behavior) ---

func TestNewCreatesPersistentHostKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "host_ed25519")
	server, err := New(Config{Address: "127.0.0.1:2222", HostKeyPath: path, Backend: testBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	if server.Address() != "127.0.0.1:2222" {
		t.Fatalf("address = %q", server.Address())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("host key was not created: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("host key permissions = %o, want no group/other access", info.Mode().Perm())
	}

	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Address: "localhost:2223", HostKeyPath: path, Backend: testBackend{}}); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("existing persistent host key was replaced")
	}
}

// --- requireLoopbackOrAuth ---

func TestRequireLoopbackOrAuthAcceptsLoopback(t *testing.T) {
	noAuth := serverconfig.AuthConfig{}
	for _, addr := range []string{"localhost:22", "127.0.0.1:22", "[::1]:22"} {
		if err := requireLoopbackOrAuth(addr, noAuth); err != nil {
			t.Fatalf("requireLoopbackOrAuth(%q) with no auth: %v", addr, err)
		}
	}
}

func TestRequireLoopbackOrAuthRejectsNonLoopbackWithoutAuth(t *testing.T) {
	noAuth := serverconfig.AuthConfig{}
	for _, addr := range []string{"0.0.0.0:22", "192.0.2.1:22"} {
		if err := requireLoopbackOrAuth(addr, noAuth); err == nil {
			t.Fatalf("requireLoopbackOrAuth(%q) with no auth should fail", addr)
		}
	}
}

func TestRequireLoopbackOrAuthAcceptsNonLoopbackWithAuth(t *testing.T) {
	auth := serverconfig.AuthConfig{PasswordHash: validHash(t, "pw")}
	if err := requireLoopbackOrAuth("0.0.0.0:22", auth); err != nil {
		t.Fatalf("requireLoopbackOrAuth with auth: %v", err)
	}
}

func TestRequireLoopbackOrAuthRejectsBadAddress(t *testing.T) {
	if err := requireLoopbackOrAuth("bad", serverconfig.AuthConfig{}); err == nil {
		t.Fatal("expected error for bad address")
	}
	if err := requireLoopbackOrAuth(":22", serverconfig.AuthConfig{}); err == nil {
		t.Fatal("expected error for empty host")
	}
}
