package serverconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- DefaultConfig ---

func TestDefaultConfigVersion(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Version != schemaVersion {
		t.Fatalf("DefaultConfig().Version = %d, want %d", cfg.Version, schemaVersion)
	}
}

func TestDefaultConfigEmptyFields(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Server.Address != "" || cfg.Server.HostKeyPath != "" {
		t.Fatalf("expected empty server fields, got %+v", cfg.Server)
	}
	if cfg.Auth.HasAnyAuth() {
		t.Fatal("DefaultConfig should have no auth configured")
	}
}

// --- Validate: address ---

func TestValidateAcceptsLoopbackWithoutAuth(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:2222", "localhost:2222", "[::1]:2222"} {
		cfg := DefaultConfig()
		cfg.Server.Address = addr
		if err := Validate(cfg); err != nil {
			t.Errorf("Validate loopback %q: unexpected error: %v", addr, err)
		}
	}
}

func TestValidateRejectsNonLoopbackWithoutAuth(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Address = "0.0.0.0:2222"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "authentication method") {
		t.Fatalf("expected non-loopback/no-auth error, got %v", err)
	}
}

func TestValidateAcceptsNonLoopbackWithAuth(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Server.Address = "0.0.0.0:2222"
	cfg.Auth.PasswordHash = hash
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate non-loopback with auth: %v", err)
	}
}

func TestValidateRejectsBadAddress(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Address = "notanaddress"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for bad address, got nil")
	}
}

func TestValidateEmptyAddressIsAccepted(t *testing.T) {
	cfg := DefaultConfig()
	// Empty address means "use default"; Validate should not require it.
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate empty address: %v", err)
	}
}

// --- Validate: compose roots ---

func TestValidateRejectsDuplicateRoots(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Compose.Roots = []string{dir, dir}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate root error, got %v", err)
	}
}

func TestValidateRejectsNonExistentRoot(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Compose.Roots = []string{"/this/path/does/not/exist/xyz123"}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for non-existent root, got nil")
	}
}

func TestValidateAcceptsExistingRoot(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Compose.Roots = []string{dir}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate with existing root: %v", err)
	}
}

// --- Validate: auth ---

func TestValidateRejectsInvalidBcryptHash(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.PasswordHash = "not-a-bcrypt-hash"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "passwordHash") {
		t.Fatalf("expected passwordHash error, got %v", err)
	}
}

func TestValidateRejectsInvalidAuthorizedKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.AuthorizedKeys = []string{"this is not a valid ssh key"}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "authorizedKeys") {
		t.Fatalf("expected authorizedKeys error, got %v", err)
	}
}

// --- AuthConfig helpers ---

func TestAuthConfigHasPasswordAuth(t *testing.T) {
	a := AuthConfig{PasswordHash: "somehash"}
	if !a.HasPasswordAuth() {
		t.Fatal("expected HasPasswordAuth true")
	}
	a.PasswordHash = ""
	if a.HasPasswordAuth() {
		t.Fatal("expected HasPasswordAuth false when empty")
	}
}

func TestAuthConfigHasKeyAuth(t *testing.T) {
	a := AuthConfig{AuthorizedKeys: []string{"key1"}}
	if !a.HasKeyAuth() {
		t.Fatal("expected HasKeyAuth true")
	}
	a.AuthorizedKeys = nil
	if a.HasKeyAuth() {
		t.Fatal("expected HasKeyAuth false when empty")
	}
}

// --- JSON round-trip ---

func TestJSONRoundTrip(t *testing.T) {
	hash, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	orig := Config{
		Version: schemaVersion,
		Server: ServerConfig{
			Address:     "127.0.0.1:2222",
			HostKeyPath: "/tmp/host_key",
		},
		Docker:  DockerConfig{Endpoint: "unix:///var/run/docker.sock"},
		Compose: ComposeConfig{Roots: []string{dir}},
		Auth:    AuthConfig{PasswordHash: hash},
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}

	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Version != orig.Version {
		t.Errorf("Version mismatch: %d != %d", loaded.Version, orig.Version)
	}
	if loaded.Server.Address != orig.Server.Address {
		t.Errorf("Address mismatch: %q != %q", loaded.Server.Address, orig.Server.Address)
	}
	if loaded.Auth.PasswordHash != orig.Auth.PasswordHash {
		t.Error("PasswordHash mismatch")
	}
	// Plaintext password must not appear in serialized bytes
	if strings.Contains(string(data), "s3cret") {
		t.Fatal("plaintext password leaked into JSON")
	}
}
