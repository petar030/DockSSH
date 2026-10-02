package dockerfixture

import (
	"strings"
	"testing"
	"time"
)

func TestConfigFromEnvCreatesIsolatedRun(t *testing.T) {
	t.Setenv("BACKEND_TEST_PREFIX", "Project Test / Run")
	t.Setenv("BACKEND_TEST_TIMEOUT", "17s")
	t.Setenv("BACKEND_TEST_DOCKER_HOST", "tcp://127.0.0.1:2375")
	t.Setenv("BACKEND_TEST_DEDICATED_DAEMON", "true")
	t.Setenv("BACKEND_TEST_CONTAINER_IMAGE", "example:test")

	first, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("first config: %v", err)
	}
	second, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("second config: %v", err)
	}

	if first.ResourcePrefix == second.ResourcePrefix {
		t.Fatalf("run prefixes are not unique: %q", first.ResourcePrefix)
	}
	if !strings.HasPrefix(first.ResourcePrefix, "project-test-run-") {
		t.Fatalf("prefix was not sanitized: %q", first.ResourcePrefix)
	}
	if first.Timeout != 17*time.Second {
		t.Fatalf("timeout = %s, want 17s", first.Timeout)
	}
	if first.DockerEndpoint != "tcp://127.0.0.1:2375" {
		t.Fatalf("Docker endpoint = %q", first.DockerEndpoint)
	}
	if !first.DedicatedDaemon {
		t.Fatal("dedicated-daemon flag was not parsed")
	}
	if first.ContainerImage != "example:test" {
		t.Fatalf("container image = %q", first.ContainerImage)
	}
	if first.ResourceLabels[testLabel] != "true" || first.ResourceLabels[runLabel] == "" {
		t.Fatalf("missing identifying labels: %v", first.ResourceLabels)
	}
}

func TestConfigFromEnvRejectsInvalidValues(t *testing.T) {
	t.Setenv("BACKEND_TEST_TIMEOUT", "eventually")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected invalid timeout to fail")
	}
}

func TestSanitizeName(t *testing.T) {
	if got, want := sanitizeName("  My_Project///01  "), "my-project-01"; got != want {
		t.Fatalf("sanitizeName() = %q, want %q", got, want)
	}
}
