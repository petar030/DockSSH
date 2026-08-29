package dockerfixture

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/test/backendtest"
)

// Fixture owns the real Moby client, temporary Compose directory, unique
// labels, and cleanup registry for one test.
type Fixture struct {
	client      *client.Client
	config      Config
	composeRoot string
	cleanup     cleanupStack
	closeOnce   sync.Once
	closeErr    error
}

// New loads configuration from the environment and registers automatic
// cleanup with t.
func New(t testing.TB) *Fixture {
	t.Helper()
	config, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("configure Docker integration fixture: %v", err)
	}
	return NewWithConfig(t, config)
}

// NewWithConfig constructs a fixture and verifies Docker connectivity.
func NewWithConfig(t testing.TB, config Config) *Fixture {
	t.Helper()
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	if config.ResourcePrefix == "" {
		t.Fatal("Docker integration fixture requires a resource prefix")
	}
	if len(config.ResourceLabels) == 0 {
		t.Fatal("Docker integration fixture requires identifying labels")
	}
	config.ResourceLabels = maps.Clone(config.ResourceLabels)

	options := []client.Opt{client.FromEnv}
	if config.DockerEndpoint != "" {
		options = append(options, client.WithHost(config.DockerEndpoint))
	}
	mobyClient, err := client.New(options...)
	if err != nil {
		t.Fatalf("create Moby client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()
	if _, err := mobyClient.Ping(ctx, client.PingOptions{}); err != nil {
		_ = mobyClient.Close()
		t.Fatalf("connect to Docker daemon at %q: %v", mobyClient.DaemonHost(), err)
	}

	fixture := &Fixture{
		client:      mobyClient,
		config:      config,
		composeRoot: t.TempDir(),
	}
	t.Cleanup(func() {
		if err := fixture.Close(); err != nil {
			t.Errorf("Docker fixture cleanup failed; prefix=%q labels=%v: %v",
				fixture.config.ResourcePrefix, fixture.config.ResourceLabels, err)
		}
	})
	return fixture
}

// DockerAPIVersion performs a bounded read-only connectivity check without
// exposing the fixture's unrestricted Moby client.
func (fixture *Fixture) DockerAPIVersion(ctx context.Context) (string, error) {
	ping, err := fixture.client.Ping(ctx, client.PingOptions{})
	if err != nil {
		return "", fmt.Errorf("ping Docker daemon: %w", err)
	}
	return ping.APIVersion, nil
}

// Environment returns a safely copied configuration for BackendFactory.
func (fixture *Fixture) Environment() backendtest.IntegrationEnvironment {
	return backendtest.IntegrationEnvironment{
		DockerEndpoint:  fixture.client.DaemonHost(),
		ComposeRoot:     fixture.composeRoot,
		ResourcePrefix:  fixture.config.ResourcePrefix,
		ResourceLabels:  maps.Clone(fixture.config.ResourceLabels),
		Timeout:         fixture.config.Timeout,
		DedicatedDaemon: fixture.config.DedicatedDaemon,
	}
}

// Name returns a unique, Docker-safe name within this test run.
func (fixture *Fixture) Name(suffix string) string {
	suffix = sanitizeName(suffix)
	if suffix == "" {
		panic("dockerfixture: resource suffix contains no Docker-name characters")
	}
	return fixture.config.ResourcePrefix + "-" + suffix
}

// Labels merges the identifying run labels with resource-specific labels.
func (fixture *Fixture) Labels(extra map[string]string) map[string]string {
	labels := maps.Clone(extra)
	if labels == nil {
		labels = make(map[string]string, len(fixture.config.ResourceLabels))
	}
	maps.Copy(labels, fixture.config.ResourceLabels)
	return labels
}

// RequireDedicatedDaemon prevents an unsafe test (for example, unrestricted
// prune) from running against a developer's shared daemon.
func (fixture *Fixture) RequireDedicatedDaemon(t testing.TB) {
	t.Helper()
	if !fixture.config.DedicatedDaemon {
		t.Skip("requires BACKEND_TEST_DEDICATED_DAEMON=true")
	}
}

// CreateVolume creates, labels, and immediately tracks one isolated test
// volume. It is intentionally narrow so integration tests never receive the
// fixture's unrestricted Moby client.
func (fixture *Fixture) CreateVolume(ctx context.Context, suffix string) (string, error) {
	name := fixture.Name(suffix)
	result, err := fixture.client.VolumeCreate(ctx, client.VolumeCreateOptions{
		Name: name, Labels: fixture.Labels(nil),
	})
	if err != nil {
		return "", fmt.Errorf("create test volume %q: %w", name, err)
	}
	fixture.TrackVolume(result.Volume.Name)
	return result.Volume.Name, nil
}

// TrackContainer schedules force-removal of a container created by this test.
func (fixture *Fixture) TrackContainer(id string) {
	fixture.cleanup.add(containerResource, id, func(ctx context.Context) error {
		_, err := fixture.client.ContainerRemove(ctx, id, client.ContainerRemoveOptions{
			Force:         true,
			RemoveVolumes: true,
		})
		return ignoreNotFound(err)
	})
}

// TrackNetwork schedules removal of a network created by this test.
func (fixture *Fixture) TrackNetwork(id string) {
	fixture.cleanup.add(networkResource, id, func(ctx context.Context) error {
		_, err := fixture.client.NetworkRemove(ctx, id, client.NetworkRemoveOptions{})
		return ignoreNotFound(err)
	})
}

// TrackVolume schedules force-removal of a volume created by this test.
func (fixture *Fixture) TrackVolume(name string) {
	fixture.cleanup.add(volumeResource, name, func(ctx context.Context) error {
		_, err := fixture.client.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: true})
		return ignoreNotFound(err)
	})
}

// TrackImage schedules removal of a uniquely tagged image created by this
// test. Shared base images must not be registered here.
func (fixture *Fixture) TrackImage(reference string) {
	fixture.cleanup.add(imageResource, reference, func(ctx context.Context) error {
		_, err := fixture.client.ImageRemove(ctx, reference, client.ImageRemoveOptions{Force: true})
		return ignoreNotFound(err)
	})
}

// Close removes tracked resources and closes the test-owned Moby client once.
func (fixture *Fixture) Close() error {
	fixture.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.config.Timeout)
		defer cancel()
		cleanupErr := fixture.cleanup.run(ctx)
		clientErr := fixture.client.Close()
		fixture.closeErr = errors.Join(cleanupErr, clientErr)
	})
	return fixture.closeErr
}

func ignoreNotFound(err error) error {
	if err == nil || errdefs.IsNotFound(err) {
		return nil
	}
	return fmt.Errorf("Docker API: %w", err)
}
