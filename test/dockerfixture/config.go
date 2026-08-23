// Package dockerfixture provides isolated, real-Docker resources for backend
// integration and conformance tests.
package dockerfixture

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPrefix  = "ssh-docker-tui-test"
	defaultTimeout = 30 * time.Second
	testLabel      = "io.github.petar030.ssh-native-docker-tui.test"
	runLabel       = "io.github.petar030.ssh-native-docker-tui.test-run"
)

// Config controls one isolated integration-test run.
type Config struct {
	DockerEndpoint  string
	ResourcePrefix  string
	ResourceLabels  map[string]string
	Timeout         time.Duration
	DedicatedDaemon bool
}

// ConfigFromEnv creates a unique configuration from BACKEND_TEST_* variables.
// DockerEndpoint may remain empty, in which case the standard Docker client
// environment and local-socket default are used.
func ConfigFromEnv() (Config, error) {
	timeout := defaultTimeout
	if value := os.Getenv("BACKEND_TEST_TIMEOUT"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("BACKEND_TEST_TIMEOUT must be a positive duration: %q", value)
		}
		timeout = parsed
	}

	dedicated, err := parseOptionalBool("BACKEND_TEST_DEDICATED_DAEMON")
	if err != nil {
		return Config{}, err
	}

	basePrefix := os.Getenv("BACKEND_TEST_PREFIX")
	if basePrefix == "" {
		basePrefix = defaultPrefix
	}
	basePrefix = sanitizeName(basePrefix)
	if basePrefix == "" {
		return Config{}, fmt.Errorf("BACKEND_TEST_PREFIX contains no Docker-name characters")
	}

	runID, err := randomRunID()
	if err != nil {
		return Config{}, fmt.Errorf("create integration-test run ID: %w", err)
	}
	prefix := basePrefix + "-" + runID

	return Config{
		DockerEndpoint:  os.Getenv("BACKEND_TEST_DOCKER_HOST"),
		ResourcePrefix:  prefix,
		ResourceLabels:  map[string]string{testLabel: "true", runLabel: runID},
		Timeout:         timeout,
		DedicatedDaemon: dedicated,
	}, nil
}

func parseOptionalBool(name string) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %q", name, value)
	}
	return parsed, nil
}

func randomRunID() (string, error) {
	var value [6]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func sanitizeName(value string) string {
	value = strings.ToLower(value)
	var result strings.Builder
	previousSeparator := false
	for _, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if valid {
			result.WriteRune(char)
			previousSeparator = false
			continue
		}
		if result.Len() > 0 && !previousSeparator {
			result.WriteByte('-')
			previousSeparator = true
		}
	}
	return strings.Trim(result.String(), "-")
}
