package ssh

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendcontainers "github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	backendimages "github.com/petar030/ssh-native-docker-tui/internal/backend/images"
	backendnetworks "github.com/petar030/ssh-native-docker-tui/internal/backend/networks"
	backendsystem "github.com/petar030/ssh-native-docker-tui/internal/backend/system"
	backendvolumes "github.com/petar030/ssh-native-docker-tui/internal/backend/volumes"
)

type testBackend struct{}

func (testBackend) RequestRefresh(backend.Page) error  { return nil }
func (testBackend) Containers() *backendcontainers.API { return nil }
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

func TestNewRequiresBackendAndLoopbackAddress(t *testing.T) {
	if _, err := New(Config{}); err == nil || !strings.Contains(err.Error(), "backend is required") {
		t.Fatalf("missing backend error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "host-key")
	if _, err := New(Config{Address: "0.0.0.0:2222", HostKeyPath: path, Backend: testBackend{}}); err == nil ||
		!strings.Contains(err.Error(), "authentication is not implemented") {
		t.Fatalf("public address error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("host key was created for rejected configuration: %v", err)
	}
}

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

func TestRequireLoopback(t *testing.T) {
	for _, address := range []string{"localhost:22", "127.0.0.1:22", "[::1]:22"} {
		if err := requireLoopback(address); err != nil {
			t.Fatalf("requireLoopback(%q): %v", address, err)
		}
	}
	for _, address := range []string{"bad", ":22", "192.0.2.1:22"} {
		if err := requireLoopback(address); err == nil {
			t.Fatalf("requireLoopback(%q) accepted non-loopback address", address)
		}
	}
}
