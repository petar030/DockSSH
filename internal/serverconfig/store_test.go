package serverconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// secureDir creates a temporary directory with 0700 permissions suitable for
// use with Load (which checks parent directory permissions).
func secureDir(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	dir := filepath.Join(base, "secure")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	dir := secureDir(t)
	path := filepath.Join(dir, "config.json")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if cfg.Version != schemaVersion {
		t.Fatalf("got version %d, want %d", cfg.Version, schemaVersion)
	}
	if cfg.Auth.HasAnyAuth() {
		t.Fatal("default config must not have auth")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := secureDir(t)
	path := filepath.Join(dir, "config.json")

	hash, err := HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	composeDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Server.Address = "127.0.0.1:23234"
	cfg.Auth.PasswordHash = hash
	cfg.Compose.Roots = []string{composeDir}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Server.Address != cfg.Server.Address {
		t.Errorf("Address: got %q, want %q", loaded.Server.Address, cfg.Server.Address)
	}
	if loaded.Auth.PasswordHash != cfg.Auth.PasswordHash {
		t.Error("PasswordHash mismatch after round-trip")
	}
	if len(loaded.Compose.Roots) != 1 {
		t.Errorf("Compose.Roots length: got %d, want 1", len(loaded.Compose.Roots))
	}
}

func TestSaveFilePermissions(t *testing.T) {
	dir := secureDir(t)
	path := filepath.Join(dir, "config.json")
	if err := Save(path, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("file permissions = %o, want 0600 (no group/other access)", info.Mode().Perm())
	}
}

func TestLoadRejectsInsecureFilePermissions(t *testing.T) {
	dir := secureDir(t)
	path := filepath.Join(dir, "config.json")
	if err := Save(path, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	// Make it world-readable
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "insecure") {
		t.Fatalf("expected insecure permissions error, got %v", err)
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	dir := secureDir(t)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{bad json}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "malformed JSON") {
		t.Fatalf("expected malformed JSON error, got %v", err)
	}
}

func TestSaveCreatesParentDirectory(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "subdir", "config.json")
	if err := Save(path, DefaultConfig()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	// Check directory permissions
	dirInfo, err := os.Stat(filepath.Join(base, "subdir"))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("directory permissions = %o, want 0700", dirInfo.Mode().Perm())
	}
}

func TestSaveValidatesBeforeWriting(t *testing.T) {
	dir := secureDir(t)
	path := filepath.Join(dir, "config.json")
	cfg := DefaultConfig()
	cfg.Auth.PasswordHash = "not-a-hash"
	if err := Save(path, cfg); err == nil {
		t.Fatal("expected validation error on Save, got nil")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("config file should not have been created after validation failure")
	}
}

func TestLoadInvalidConfigAfterSave(t *testing.T) {
	// Write invalid content into a properly-permissioned file to test Load validation failure.
	dir := secureDir(t)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"authentication":{"passwordHash":"notahash"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error loading invalid config, got nil")
	}
}

func TestSaveSetsVersionOnWrite(t *testing.T) {
	dir := secureDir(t)
	path := filepath.Join(dir, "config.json")
	cfg := DefaultConfig()
	cfg.Version = 0 // intentionally wrong; Save should override
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != schemaVersion {
		t.Fatalf("loaded version = %d, want %d", loaded.Version, schemaVersion)
	}
}
