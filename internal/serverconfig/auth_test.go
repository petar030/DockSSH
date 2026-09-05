package serverconfig

import (
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// --- HashPassword ---

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Fatal("expected error for empty password, got nil")
	}
}

func TestValidatePasswordPolicy(t *testing.T) {
	valid := "correct horse battery staple 9"
	if err := ValidatePassword(valid); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
	for _, password := range []string{
		"short",
		"12345678901234",
		"aaaaaaaaaaaaaa",
		"abcdefghijklmn",
		"password123456",
		"docker-secure-value",
		" leading-and-long-enough",
	} {
		if err := ValidatePassword(password); err == nil {
			t.Fatalf("weak password %q was accepted", password)
		}
	}
}

func TestHashPasswordProducesVerifiableHash(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct-horse-battery-staple") {
		t.Fatal("VerifyPassword should accept the correct password")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("VerifyPassword should reject wrong password")
	}
}

func TestHashPasswordDoesNotContainPlaintext(t *testing.T) {
	password := "supersecretvalue999"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, password) {
		t.Fatal("plaintext password leaked into hash output")
	}
}

// --- VerifyPassword ---

func TestVerifyPasswordReturnsFalseForEmptyInputs(t *testing.T) {
	if VerifyPassword("", "anything") {
		t.Fatal("empty hash should not verify")
	}
	if VerifyPassword("$2a$12$somehash", "") {
		t.Fatal("empty password should not verify")
	}
}

// --- validateBcryptHash ---

func TestValidateBcryptHashRejectsNonHash(t *testing.T) {
	if err := validateBcryptHash("notahash"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestValidateBcryptHashAcceptsValidHash(t *testing.T) {
	hash, err := HashPassword("testpw")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBcryptHash(hash); err != nil {
		t.Fatalf("validateBcryptHash(%q): %v", hash, err)
	}
}

// --- ParseAuthorizedKey ---

func TestParseAuthorizedKeyRejectsEmpty(t *testing.T) {
	if _, err := ParseAuthorizedKey(""); err == nil {
		t.Fatal("expected error for empty key, got nil")
	}
}

func TestParseAuthorizedKeyRejectsGarbage(t *testing.T) {
	if _, err := ParseAuthorizedKey("this is not a key"); err == nil {
		t.Fatal("expected error for garbage input, got nil")
	}
}

// testPubKey is a valid ed25519 public key for use in tests.
const testPubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl test-key"

func TestParseAuthorizedKeyAcceptsValidKey(t *testing.T) {
	canonical, err := ParseAuthorizedKey(testPubKey)
	if err != nil {
		t.Fatalf("ParseAuthorizedKey: %v", err)
	}
	if canonical == "" {
		t.Fatal("canonical must not be empty")
	}
	if !strings.HasPrefix(canonical, "ssh-ed25519 ") {
		t.Fatalf("canonical does not start with key type: %q", canonical)
	}
}

// --- KeyFingerprint ---

func TestKeyFingerprintRejectsEmpty(t *testing.T) {
	if _, err := KeyFingerprint(""); err == nil {
		t.Fatal("expected error for empty key, got nil")
	}
}

func TestKeyFingerprintReturnsSHA256Prefix(t *testing.T) {
	fp, err := KeyFingerprint(testPubKey)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fp, "SHA256:") {
		t.Fatalf("fingerprint does not start with SHA256:: %q", fp)
	}
}

// --- AuthorizedKeyMatches ---

func TestAuthorizedKeyMatchesParsedKey(t *testing.T) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(testPubKey))
	if err != nil {
		t.Fatal(err)
	}
	if !AuthorizedKeyMatches(testPubKey, pub) {
		t.Fatal("AuthorizedKeyMatches should match its own stored key")
	}
}

func TestAuthorizedKeyMatchesDifferentKey(t *testing.T) {
	// Second different ed25519 key for comparison.
	const otherKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILkVtxaVJEMqME+MvKNvBXxsTuJKPWvb9P6q1GH6lJiL other"
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(testPubKey))
	if err != nil {
		t.Fatal(err)
	}
	if AuthorizedKeyMatches(otherKey, pub) {
		t.Fatal("AuthorizedKeyMatches should reject a different key")
	}
}

func TestAuthorizedKeyMatchesReturnsFalseForInvalidStoredKey(t *testing.T) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(testPubKey))
	if err != nil {
		t.Fatal(err)
	}
	if AuthorizedKeyMatches("not-a-key", pub) {
		t.Fatal("AuthorizedKeyMatches should return false for invalid stored key")
	}
}
