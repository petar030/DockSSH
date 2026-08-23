# Backend Testing Environment

The backend is developed test-first. Tests describe observable backend behavior
before each domain implementation exists. They are not a mock backend and do
not require the TUI to be implemented first.

This document describes the implemented observer-based foundation in
`docs/plan.md`.

## Separation from production

```text
internal/backend/              production contracts and behavior
    contracts.go               shared refresh/event/lifecycle contracts
    event_bus.go               typed bounded subscriptions
    event_buffer.go            bounded recent Docker-event history
    refresh_coordinator.go     direct refresh and page-bus publication
    refresh_scheduler.go       process-wide scheduled refresh requests
    core.go                    backend facade and lifecycle

internal/platform/docker/      real infrastructure adapters
    dependencies.go            shared Moby, Docker CLI, and Compose wiring

test/backendtest/              reusable black-box backend conformance suite
    environment.go             BackendFactory and IntegrationEnvironment
    suite.go                   behavior required of any real backend

test/dockerfixture/            safe real-Docker fixture
    config.go                  unique run configuration
    fixture.go                 private Moby client and tracked resources
    cleanup.go                 dependency-order cleanup

test/testkit/                  deterministic test-only utilities
    clock.go                   manually advanced clock and ticker
    loader.go                  recording loader and synchronization gate
    closer.go                  dependency-ownership recorder

test/integration/              production implementation entry points
    docker_fixture_test.go      real-daemon fixture smoke test
    backend_conformance_test.go production factory wired into the suite
```

Production contracts stay in `internal/backend` because production code must
implement them. Reusable test scenarios, fake clocks/loaders, and Docker fixture
code stay under `test`. Small `_test.go` files beside production files are normal
Go unit tests: they can exercise unexported concurrency details and are excluded
from production binaries automatically.

## How tests precede the backend

The conformance suite depends only on public contracts and a factory:

```go
type BackendFactory func(
    context.Context,
    IntegrationEnvironment,
) (backend.Backend, error)
```

A test states what callers must observe. At first, the production factory either
does not compile against the new contract or fails the test. The minimum real
implementation is then added until it passes. This keeps the test independent
of implementation details while still running against actual production code.

The foundational observer scenario is:

1. create the real production backend through `BackendFactory`;
2. open two independent filtered subscriptions to the System page bus;
3. request one explicit System page refresh;
4. require both subscribers to receive the same typed full-result update;
5. prove updates do not leak to another page bus;
6. cancel one subscription and prove the other remains usable;
7. close the backend and verify shared dependencies close exactly once.

The session subscribes before requesting its initial data. This prevents an
update from being published between an initial load and subscription.

## What is tested without Docker

Hermetic unit tests use fake clocks and loaders for behavior that must be fast
and deterministic:

- refresh-key validation and loader selection;
- separate Event Bus delivery for each page;
- identical concurrent refresh requests each executing their own loader call;
- caller cancellation affecting only its own refresh;
- publication of every successful typed result;
- `RefreshFailed` delivery and errors returned to direct callers;
- Event Bus filtering, per-subscriber ordering, cancellation, and idempotent
  close;
- bounded subscriber queues and explicit overflow/resync behavior;
- recent-event ring-buffer ordering and eviction;
- scheduler timing without real sleeps;
- context cancellation, graceful shutdown, ownership, and leak prevention.

There are intentionally no tests for snapshot versions, cache comparison,
cached stale state, or `StateStore`: those concepts are not part of the target
architecture. On refresh failure, each session keeps the UI data it already
owns and marks it stale from the failure event.

## What is tested with Docker

Integration tests connect the production core and domain services to a real
Docker daemon. They verify things fakes cannot establish reliably:

- real Moby request/response behavior and error translation;
- typed payloads created from real Docker data;
- successful commands followed by authoritative refresh broadcasts;
- Docker events mapped to refresh requests and updates;
- logs, stats, exec, and job cancellation/closure;
- lifecycle ownership of the shared Moby client;
- cleanup of every resource created by the test.

Each tab/window slice extends the same reusable conformance suite. Tests are
implemented iteratively with the domain slice rather than writing speculative
tests for every API in advance.

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

These commands correspond to:

```sh
go test ./...
go test -race ./...
go test -tags=integration ./...
go test -race -tags=integration ./...
```

During the refactor, a red suite is acceptable only for the deliberately changed
contract currently being implemented. Each commit/phase should return the tree
to compiling tests before moving to the next phase.

## Docker configuration

By default, the integration fixture follows normal Docker client configuration
(`DOCKER_HOST` and the local Unix socket). These optional variables affect tests
only:

| Variable | Default | Purpose |
| --- | --- | --- |
| `BACKEND_TEST_DOCKER_HOST` | Docker client default | Override the test daemon endpoint. |
| `BACKEND_TEST_PREFIX` | `ssh-docker-tui-test` | Prefix test resources. |
| `BACKEND_TEST_TIMEOUT` | `30s` | Bound Docker operations and cleanup. |
| `BACKEND_TEST_DEDICATED_DAEMON` | `false` | Permit tests requiring an isolated daemon. |

Each fixture invocation appends a cryptographically random run ID and labels
created resources with:

- `io.github.petar030.ssh-native-docker-tui.test=true`
- `io.github.petar030.ssh-native-docker-tui.test-run=<unique-run-id>`

It also creates a temporary Compose root for that test.

## Resource safety and cleanup

The fixture deliberately does not expose its unrestricted Moby client. Each
domain slice adds narrow arrangement and inspection helpers. Helpers must create
names through `fixture.Name`, apply `fixture.Labels`, and register every resource
immediately after creation:

```go
fixture := dockerfixture.New(t)

containerID := arrangeContainer(t, fixture)
fixture.TrackContainer(containerID)
```

Cleanup always runs, including after `t.Fatal`, and removes only explicitly
registered resources in dependency order:

1. containers;
2. networks;
3. volumes;
4. uniquely tagged test images.

Do not register shared base images with `TrackImage`. Unrestricted prune and
similar destructive cases must call `fixture.RequireDedicatedDaemon(t)` and are
skipped unless `BACKEND_TEST_DEDICATED_DAEMON=true`.

Tests must never discover cleanup targets by listing arbitrary host resources.
Assertions and cleanup stay restricted to the current run's labels, prefix,
returned IDs, and Compose project name.

## Production backend connection

The integration entry point creates a fixture and passes its restricted
environment to the reusable suite:

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

The production factory constructs the real shared client, infrastructure
adapters, page Event Buses, coordinator, event buffer, and scheduler, and returns the
backend facade. It must not perform a hidden cache warm-up. The suite subscribes
and explicitly requests the initial refresh it needs.

## TDD workflow per slice

1. Freeze only the DTOs, typed update payloads, internal refresh keys, and operations for
   the next tab/window.
2. Add observable behavior to `test/backendtest` and deterministic edge cases to
   local unit tests.
3. Run the tests and confirm the new behavior fails for the expected reason.
4. Implement the smallest production loader/service/command path.
5. Run unit, integration, race, cleanup, and shutdown checks.
6. Update `docs/TODO.md` when the behavior and quality gates actually pass.

Fake adapters supplement rather than replace the real-Docker path. Conversely,
real Docker tests do not replace deterministic unit coverage of concurrency,
failure, overflow, and cancellation.
