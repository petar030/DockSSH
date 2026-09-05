// Package serverconfig owns the persisted configuration schema, secure
// storage, and authentication helpers for the ssh-docker-tui server.
package serverconfig

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Default SSH values used when the corresponding saved setting is empty.
const (
	DefaultListenAddress = "127.0.0.1:23234"
	DefaultHostKeyPath   = ".ssh-docker-tui/host_ed25519"
)

// schemaVersion is the persisted JSON schema version.
const schemaVersion = 1

// Config is the complete, versioned configuration persisted to disk.
// All fields have safe zero values; a zero Config represents an
// unconfigured first-run state.
type Config struct {
	Version int           `json:"version"`
	Server  ServerConfig  `json:"server"`
	Docker  DockerConfig  `json:"docker"`
	Compose ComposeConfig `json:"compose"`
	Auth    AuthConfig    `json:"authentication"`
}

// ServerConfig contains SSH server network and key settings.
type ServerConfig struct {
	// Address is the SSH listen address in host:port form.
	// Empty means the default loopback address is used.
	Address string `json:"address"`
	// HostKeyPath is the path to the persistent SSH host private key.
	// Empty means the default path is used.
	HostKeyPath string `json:"hostKeyPath"`
}

// DockerConfig contains Docker daemon connection settings.
type DockerConfig struct {
	// Endpoint is the Docker daemon endpoint URL.
	// Empty means the Docker environment defaults are used.
	Endpoint string `json:"endpoint"`
}

// ComposeConfig holds the list of allowed Compose project root directories.
type ComposeConfig struct {
	// Roots is the ordered list of allowed Compose root directories.
	// The first entry is the default location for newly created managed
	// compose.yaml files.
	Roots []string `json:"roots"`
}

// AuthConfig holds authentication credentials.  Never store a plaintext
// password here or anywhere else in the codebase.
type AuthConfig struct {
	// PasswordHash is the bcrypt hash of the configured password, or empty
	// when password authentication is disabled.
	PasswordHash string `json:"passwordHash,omitempty"`
	// AuthorizedKeys contains canonical SSH public-key lines (one per element)
	// in authorized_keys format.  Empty means no key auth is configured.
	AuthorizedKeys []string `json:"authorizedKeys,omitempty"`
}

// HasPasswordAuth reports whether password authentication is configured.
func (a AuthConfig) HasPasswordAuth() bool { return a.PasswordHash != "" }

// HasKeyAuth reports whether at least one authorized public key is configured.
func (a AuthConfig) HasKeyAuth() bool { return len(a.AuthorizedKeys) > 0 }

// HasAnyAuth reports whether at least one authentication method is configured.
func (a AuthConfig) HasAnyAuth() bool { return a.HasPasswordAuth() || a.HasKeyAuth() }

// DefaultConfig returns a Config populated with the same defaults used when
// no configuration file exists.  Version is set to the current schema version.
func DefaultConfig() Config {
	return Config{Version: schemaVersion}
}

// Validate checks the configuration for constraint violations.  It is called
// before saving and after loading.
func Validate(cfg Config) error {
	var errs []error

	if addr := strings.TrimSpace(cfg.Server.Address); addr != "" {
		if err := validateAddress(addr); err != nil {
			errs = append(errs, err)
		} else if !isLoopback(addr) && !cfg.Auth.HasAnyAuth() {
			errs = append(errs, errors.New(
				"a non-loopback listen address requires at least one authentication method"))
		}
	}

	if path := strings.TrimSpace(cfg.Server.HostKeyPath); path != "" {
		if !filepath.IsAbs(path) {
			dir := filepath.Dir(path)
			if dir != "" && dir != "." {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					errs = append(errs, fmt.Errorf(
						"host-key parent directory %q is not creatable: %w", dir, err))
				}
			}
		}
	}

	seen := make(map[string]bool, len(cfg.Compose.Roots))
	for i, root := range cfg.Compose.Roots {
		canonical, err := canonicalizeRoot(root)
		if err != nil {
			errs = append(errs, fmt.Errorf("compose root[%d] %q: %w", i, root, err))
			continue
		}
		if seen[canonical] {
			errs = append(errs, fmt.Errorf("duplicate compose root %q (index %d)", root, i))
		}
		seen[canonical] = true
	}

	if cfg.Auth.PasswordHash != "" {
		if err := validateBcryptHash(cfg.Auth.PasswordHash); err != nil {
			errs = append(errs, fmt.Errorf("invalid passwordHash: %w", err))
		}
	}

	for i, key := range cfg.Auth.AuthorizedKeys {
		if _, err := ParseAuthorizedKey(key); err != nil {
			errs = append(errs, fmt.Errorf("authorizedKeys[%d]: %w", i, err))
		}
	}

	return errors.Join(errs...)
}

// validateAddress returns an error if address is not a valid host:port.
func validateAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", address, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("listen address %q: host must not be empty", address)
	}
	if strings.TrimSpace(port) == "" {
		return fmt.Errorf("listen address %q: port must not be empty", address)
	}
	return nil
}

// isLoopback reports whether the host part of address resolves to a loopback
// interface.
func isLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// canonicalizeRoot returns the cleaned absolute path for a Compose root.
// It returns an error when the path is empty, not absolute, or the directory
// does not exist and is not accessible.
func canonicalizeRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("must not be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("cannot access directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", abs)
	}
	return filepath.Clean(abs), nil
}
