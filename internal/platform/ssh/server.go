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
	"github.com/petar030/ssh-native-docker-tui/internal/serverconfig"
	"github.com/petar030/ssh-native-docker-tui/internal/tui"
)

const (
	DefaultAddress     = serverconfig.DefaultListenAddress
	DefaultHostKeyPath = serverconfig.DefaultHostKeyPath
)

// Config holds the SSH server configuration.
//
// Auth carries the validated authentication settings loaded from
// serverconfig.Config.  When Auth.HasAnyAuth() is false the server may only
// bind to a loopback address (first-run development behavior).
type Config struct {
	Address     string
	HostKeyPath string
	Backend     tui.Application
	Auth        serverconfig.AuthConfig
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
	if strings.TrimSpace(config.HostKeyPath) == "" {
		config.HostKeyPath = DefaultHostKeyPath
	}

	// Validate listener/auth combination before touching the filesystem.
	if err := requireLoopbackOrAuth(config.Address, config.Auth); err != nil {
		return nil, err
	}

	if err := ensureKeyDirectory(config.HostKeyPath); err != nil {
		return nil, err
	}

	opts := []ssh.Option{
		wish.WithAddress(config.Address),
		wish.WithHostKeyPath(config.HostKeyPath),
		wish.WithMiddleware(
			bubbletea.Middleware(func(session ssh.Session) (tea.Model, []tea.ProgramOption) {
				return tui.NewWithSSHAddress(session.Context(), config.Backend, config.Address), nil
			}),
			activeterm.Middleware(),
		),
	}

	// Install authentication handlers only when those mechanisms are configured.
	if config.Auth.HasPasswordAuth() {
		hash := config.Auth.PasswordHash // capture for closure
		opts = append(opts, wish.WithPasswordAuth(func(_ ssh.Context, password string) bool {
			return serverconfig.VerifyPassword(hash, password)
		}))
	}
	if config.Auth.HasKeyAuth() {
		keys := config.Auth.AuthorizedKeys // capture for closure
		opts = append(opts, wish.WithPublicKeyAuth(func(_ ssh.Context, key ssh.PublicKey) bool {
			for _, stored := range keys {
				if serverconfig.AuthorizedKeyMatches(stored, key) {
					return true
				}
			}
			return false
		}))
	}

	server, err := wish.NewServer(opts...)
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

// requireLoopbackOrAuth enforces the compatibility policy:
//   - loopback address with or without auth: accepted
//   - non-loopback address with at least one auth method: accepted
//   - non-loopback address with no auth: rejected
func requireLoopbackOrAuth(address string, auth serverconfig.AuthConfig) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("validate SSH listen address %q: %w", address, err)
	}
	if isLoopbackHost(host) {
		return nil
	}
	if !auth.HasAnyAuth() {
		return fmt.Errorf(
			"validate SSH listen address %q: a non-loopback address requires at least one authentication method",
			address)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
