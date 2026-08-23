//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	dockerplatform "github.com/petar030/ssh-native-docker-tui/internal/platform/docker"
	"github.com/petar030/ssh-native-docker-tui/test/backendtest"
	"github.com/petar030/ssh-native-docker-tui/test/dockerfixture"
)

func TestProductionBackendConformance(t *testing.T) {
	fixture := dockerfixture.New(t)
	backendtest.RunCoreConformance(t, productionBackendFactory, fixture.Environment())
}

func TestProductionBootstrapSharesOneMobyClient(t *testing.T) {
	fixture := dockerfixture.New(t)
	application, err := dockerplatform.NewBackend(context.Background(), dockerplatform.BackendConfig{
		Endpoint: fixture.Environment().DockerEndpoint,
	})
	if err != nil {
		t.Fatalf("construct production backend: %v", err)
	}
	t.Cleanup(func() {
		if err := application.Close(context.Background()); err != nil {
			t.Errorf("close production backend: %v", err)
		}
	})
	if !application.UsesSharedMobyClient() {
		t.Fatal("Docker CLI does not use the application-owned Moby client")
	}
}

func productionBackendFactory(
	ctx context.Context,
	environment backendtest.IntegrationEnvironment,
) (backend.Backend, error) {
	return dockerplatform.NewBackend(ctx, dockerplatform.BackendConfig{
		Endpoint: environment.DockerEndpoint,
	})
}
