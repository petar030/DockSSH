# Backend Testing Environment

The backend is developed test-first. This test environment is an executable
specification of backend behavior; it is not a temporary mock backend and does
not contain fabricated implementations.

## Test layout

```text
internal/backend/
    contracts.go             production-facing interfaces and value types
    state_store.go           immutable versioned snapshots
    event_bus.go             typed bounded subscriptions
    event_buffer.go          recent Docker-event ring
    refresh_coordinator.go   centralized snapshot writer
    refresh_scheduler.go     process-wide scheduled refreshes
    core.go                  backend lifecycle and facade

internal/platform/docker/
    dependencies.go          real Moby, Docker CLI, and Compose wiring

test/backendtest/
    environment.go      BackendFactory and IntegrationEnvironment
    suite.go            reusable backend conformance tests

test/dockerfixture/
    config.go           unique run configuration
    fixture.go          real Moby client and resource tracking
    cleanup.go          deterministic dependency-order cleanup

test/testkit/
    clock.go            manually advanced clock and ticker
    loader.go           recording loader and synchronization gate
    closer.go           dependency-ownership recorder

test/integration/
    docker_fixture_test.go       real-daemon fixture smoke test
    backend_conformance_test.go  production core conformance entry point
```

Production contracts stay in `internal/backend`. All reusable test support and
real-Docker tests stay under `test`, separate from application behavior.

## Current foundation

The normal unit suite tests contracts, shared-state behavior, concurrency, and
fixture safety without requiring Docker. The integration suite connects the
production core backend to a real Docker daemon.

`TestProductionBackendConformance` now runs startup synchronization, explicit
refresh, subscription cancellation, graceful shutdown, and shared-client
ownership through the real Moby client. Later tab slices extend the same suite;
they do not replace it with mocks.

## Commands

```sh
# Formatting, go vet, and unit tests
make check

# Unit tests only
make test

# Unit tests with the race detector
make test-race

# Unit tests and real-Docker integration tests
make test-integration

# Complete integration suite with the race detector
make test-integration-race

# Both race-enabled suites
make test-all
```

The commands correspond to the project quality gates:

```sh
go test ./...
go test -race ./...
go test -tags=integration ./...
go test -race -tags=integration ./...
```

## Docker configuration

By default, the integration fixture follows the normal Docker client
configuration (`DOCKER_HOST` and the local Unix socket). The following optional
variables affect tests only:

| Variable | Default | Purpose |
| --- | --- | --- |
| `BACKEND_TEST_DOCKER_HOST` | Docker client default | Override the daemon endpoint for tests. |
| `BACKEND_TEST_PREFIX` | `ssh-docker-tui-test` | Human-readable prefix for test resources. |
| `BACKEND_TEST_TIMEOUT` | `30s` | Bound Docker operations and cleanup. |
| `BACKEND_TEST_DEDICATED_DAEMON` | `false` | Permit tests that require a fully isolated daemon. |

Each fixture invocation appends a cryptographically random run ID to the
resource prefix and adds these labels:

- `io.github.petar030.ssh-native-docker-tui.test=true`
- `io.github.petar030.ssh-native-docker-tui.test-run=<unique-run-id>`

It also creates a temporary Compose root for that test.

## Resource safety and cleanup

The fixture deliberately does not expose its unrestricted Moby client. Each
domain slice adds narrowly scoped arrangement and inspection helpers. Helpers
must create names through `fixture.Name`, apply `fixture.Labels`, and register
every resource immediately after creation:

```go
fixture := dockerfixture.New(t)

containerID := arrangeContainer(t, fixture)
fixture.TrackContainer(containerID)
```

The fixture removes only explicitly registered resources. Cleanup always runs,
including after `t.Fatal`, and follows dependency order:

1. containers;
2. networks;
3. volumes;
4. uniquely tagged test images.

Do not register shared base images with `TrackImage`. Unrestricted prune and
similar destructive cases must call `fixture.RequireDedicatedDaemon(t)`. They
will be skipped unless `BACKEND_TEST_DEDICATED_DAEMON=true` is explicitly set.

Tests must never derive cleanup targets by listing arbitrary host resources.
Use IDs returned by the create operation and keep assertions filtered to the
current run's labels, prefix, resource IDs, or Compose project name.

## Production backend connection

The reusable suite accepts a factory instead of importing a concrete backend:

```go
type BackendFactory func(
    context.Context,
    IntegrationEnvironment,
) (backend.Backend, error)
```

The production core is already connected in
`test/integration/backend_conformance_test.go` using this shape:

```go
func TestProductionBackendConformance(t *testing.T) {
    fixture := dockerfixture.New(t)

    backendtest.RunCoreConformance(
        t,
        productionBackendFactory,
        fixture.Environment(),
    )
}
```

The current factory uses the real Moby client, Docker CLI object, Compose SDK,
state store, refresh coordinator, event bus, event buffer, and scheduler. It
returns only after initial synchronization and closes through `Backend.Close`.
The Docker event listener and its action-to-refresh mapping are added in the
Events slice.

## TDD workflow

For each backend behavior:

1. Freeze or extend the public contract in `internal/backend`.
2. Add the expected behavior to a reusable suite in `test/backendtest`.
3. Run the suite and observe the production implementation fail.
4. Implement the smallest production slice that satisfies the contract.
5. Run unit, integration, race, and cleanup checks.

Fake adapters are appropriate for deterministic clock, concurrency, failure,
overflow, and cancellation tests. They supplement rather than replace the
real-Docker conformance path.
