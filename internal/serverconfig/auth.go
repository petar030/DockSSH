package serverconfig

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
)

// bcryptCost is the work factor used when hashing a new password.
// Cost 12 is a current reasonable value balancing security and latency.
const bcryptCost = 12

// HashPassword hashes password with bcrypt at bcryptCost and returns the
// encoded hash string.  The plaintext password is not stored or logged.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password must not be empty")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword reports whether password matches the stored bcrypt hash.
// It returns false (not an error) on mismatch so callers can treat it as a
// boolean predicate.
func VerifyPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// validateBcryptHash returns an error when s is not a recognizable bcrypt hash.
func validateBcryptHash(s string) error {
	_, err := bcrypt.Cost([]byte(s))
	if err != nil {
		return fmt.Errorf("not a valid bcrypt hash: %w", err)
	}
	return nil
}

// ParseAuthorizedKey parses a single SSH public-key line in authorized_keys
// format and returns the canonical representation and its fingerprint comment.
// It returns an error when the line is empty or cannot be parsed.
func ParseAuthorizedKey(line string) (canonical string, err error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("empty public key")
	}
	pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return "", fmt.Errorf("parse authorized key: %w", err)
	}
	// Build a canonical representation: type SP base64(marshalled) SP comment
	marshalled := pub.Marshal()
	b64 := base64.StdEncoding.EncodeToString(marshalled)
	canonical = pub.Type() + " " + b64
	if comment != "" {
		canonical += " " + comment
	}
	return canonical, nil
}

// KeyFingerprint returns the SHA-256 fingerprint string for the authorized-key
// line, suitable for display in a UI.  The format matches ssh-keygen -l output.
func KeyFingerprint(line string) (string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("empty public key")
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return "", fmt.Errorf("parse authorized key: %w", err)
	}
	sum := sha256.Sum256(pub.Marshal())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]), nil
}

// AuthorizedKeyMatches reports whether the ssh.PublicKey presented by an SSH
// client matches the stored authorized-key line.
func AuthorizedKeyMatches(storedLine string, presented ssh.PublicKey) bool {
	stored, _, _, _, err := ssh.ParseAuthorizedKey([]byte(storedLine))
	if err != nil {
		return false
	}
	return ssh.FingerprintSHA256(stored) == ssh.FingerprintSHA256(presented)
}
