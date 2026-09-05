package serverconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// defaultDirName is the application state directory name under the user home.
	defaultDirName = ".ssh-docker-tui"
	// configFileName is the configuration file name within the state directory.
	configFileName = "config.json"
	// dirMode is the required permissions for the state directory.
	dirMode = 0o700
	// fileMode is the required permissions for the configuration file.
	fileMode = 0o600
)

// DefaultConfigDir returns the platform-appropriate private state directory
// for the application.  It uses os.UserHomeDir rather than hardcoding a user
// name.
func DefaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, defaultDirName), nil
}

// DefaultConfigPath returns the full path of the default configuration file.
func DefaultConfigPath() (string, error) {
	dir, err := DefaultConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

// Load reads and validates the configuration file at path.  If the file does
// not exist, Load returns a default zero-value Config and no error, preserving
// first-run development behavior.
//
// Load returns an error when:
//   - the file exists but has insecure permissions;
//   - the parent directory has insecure permissions;
//   - the JSON is malformed;
//   - validation fails.
func Load(path string) (Config, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("load config %q: %w", path, err)
	}

	if err := checkPermissions(path); err != nil {
		return Config{}, fmt.Errorf("load config %q: %w", path, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("load config %q: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("load config %q: malformed JSON: %w", path, err)
	}

	if err := Validate(cfg); err != nil {
		return Config{}, fmt.Errorf("load config %q: %w", path, err)
	}

	return cfg, nil
}

// Save validates cfg and atomically writes it to path as JSON.  The parent
// directory is created with mode 0700 if absent.  The file is written through
// a same-directory temporary file, fsynced, and renamed to prevent partial
// writes.  File permissions are 0600.
func Save(path string, cfg Config) error {
	if err := Validate(cfg); err != nil {
		return fmt.Errorf("save config %q: validation failed: %w", path, err)
	}

	cfg.Version = schemaVersion

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("save config %q: create directory: %w", path, err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("save config %q: marshal JSON: %w", path, err)
	}
	data = append(data, '\n')

	// Write to a temporary file in the same directory so that rename is atomic
	// on the same filesystem.
	tmp, err := os.CreateTemp(dir, ".config-*.json.tmp")
	if err != nil {
		return fmt.Errorf("save config %q: create temp file: %w", path, err)
	}
	tmpPath := tmp.Name()

	// Clean up temp file on any error path.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save config %q: write temp file: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save config %q: fsync temp file: %w", path, err)
	}
	if err := tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save config %q: set permissions: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save config %q: close temp file: %w", path, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("save config %q: atomic rename: %w", path, err)
	}
	success = true
	return nil
}

// checkPermissions returns an error when the config file or its parent
// directory has group- or world-readable/writable/executable bits set.
func checkPermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf(
			"insecure file permissions %o on %q (want 0600)", info.Mode().Perm(), path)
	}

	dir := filepath.Dir(path)
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat directory: %w", err)
	}
	if dirInfo.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf(
			"insecure directory permissions %o on %q (want 0700)", dirInfo.Mode().Perm(), dir)
	}
	return nil
}
