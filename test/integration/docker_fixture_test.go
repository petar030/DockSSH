//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/test/dockerfixture"
)

func TestDockerFixtureConnectsToRealDaemon(t *testing.T) {
	fixture := dockerfixture.New(t)
	environment := fixture.Environment()

	ctx, cancel := context.WithTimeout(context.Background(), environment.Timeout)
	defer cancel()
	ping, err := fixture.Client().Ping(ctx, client.PingOptions{})
	if err != nil {
		t.Fatalf("ping Docker daemon: %v", err)
	}
	if ping.APIVersion == "" {
		t.Fatal("Docker daemon returned no API version")
	}
	t.Logf("Docker fixture ready: endpoint=%s API=%s prefix=%s",
		environment.DockerEndpoint, ping.APIVersion, environment.ResourcePrefix)
}
