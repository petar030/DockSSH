// Package ssh adapts Wish sessions to independent Bubble Tea programs that
// share the process-wide backend.
package ssh

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/ssh"
	"charm.land/wish/v2"
	"charm.land/wish/v2/activeterm"
	"charm.land/wish/v2/bubbletea"
	"github.com/petar030/ssh-native-docker-tui/internal/tui"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/dashboard"
)

const (
	DefaultAddress     = "127.0.0.1:23234"
	DefaultHostKeyPath = ".ssh-docker-tui/host_ed25519"
)

type Config struct {
	Address     string
	HostKeyPath string
	Backend     dashboard.Backend
}

type Server struct {
	server  *ssh.Server
	address string
}

func New(config Config) (*Server, error) {
	if config.Backend == nil {
		return nil, fmt.Errorf("create SSH server: backend is required")
	}
	if strings.TrimSpace(config.Address) == "" {
		config.Address = DefaultAddress
	}
	if err := requireLoopback(config.Address); err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.HostKeyPath) == "" {
		config.HostKeyPath = DefaultHostKeyPath
	}
	if err := ensureKeyDirectory(config.HostKeyPath); err != nil {
		return nil, err
	}

	server, err := wish.NewServer(
		wish.WithAddress(config.Address),
		wish.WithHostKeyPath(config.HostKeyPath),
		wish.WithMiddleware(
			bubbletea.Middleware(func(session ssh.Session) (tea.Model, []tea.ProgramOption) {
				return tui.New(session.Context(), config.Backend), nil
			}),
			activeterm.Middleware(),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create SSH server: %w", err)
	}
	return &Server{server: server, address: config.Address}, nil
}

func (server *Server) Address() string { return server.address }

func (server *Server) ListenAndServe() error {
	if err := server.server.ListenAndServe(); err != nil && !isClosed(err) {
		return fmt.Errorf("serve SSH: %w", err)
	}
	return nil
}

// Close stops accepting connections and closes current sessions so their
// contexts release page subscriptions before the shared backend is closed.
func (server *Server) Close() error {
	if err := server.server.Close(); err != nil && !isClosed(err) {
		return fmt.Errorf("close SSH server: %w", err)
	}
	return nil
}

func requireLoopback(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("validate SSH listen address %q: %w", address, err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("validate SSH listen address %q: authentication is not implemented; use a loopback host", address)
	}
	return nil
}

func ensureKeyDirectory(path string) error {
	directory := filepath.Dir(path)
	if directory == "." {
		return nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create SSH host-key directory: %w", err)
	}
	return nil
}

func isClosed(err error) bool {
	return err == nil || errors.Is(err, ssh.ErrServerClosed)
}
