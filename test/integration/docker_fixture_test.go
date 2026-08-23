//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/petar030/ssh-native-docker-tui/test/dockerfixture"
)

func TestDockerFixtureConnectsToRealDaemon(t *testing.T) {
	fixture := dockerfixture.New(t)
	environment := fixture.Environment()

	ctx, cancel := context.WithTimeout(context.Background(), environment.Timeout)
	defer cancel()
	apiVersion, err := fixture.DockerAPIVersion(ctx)
	if err != nil {
		t.Fatalf("ping Docker daemon: %v", err)
	}
	if apiVersion == "" {
		t.Fatal("Docker daemon returned no API version")
	}
	t.Logf("Docker fixture ready: endpoint=%s API=%s prefix=%s",
		environment.DockerEndpoint, apiVersion, environment.ResourcePrefix)
}
