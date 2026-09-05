package setup

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// netSplitHostPort wraps net.SplitHostPort so we can call it from update.go
// without importing net there.
func netSplitHostPort(address string) (host, port string, err error) {
	return net.SplitHostPort(address)
}

// canonicalizeRoot returns the cleaned absolute path for a Compose root.
// It returns an error when the path is empty, not accessible, or not a directory.
func canonicalizeRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("path must not be empty")
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

// isDuplicate reports whether canonical already appears in roots.
func isDuplicate(roots []string, canonical string) bool {
	for _, r := range roots {
		if r == canonical {
			return true
		}
	}
	return false
}
