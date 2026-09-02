# Backend Testing Environment

The backend is developed test-first. Tests describe behavior visible through
the application API before the TUI exists; they are not a mock backend.
Detailed component and lifecycle documentation starts in
[`docs/backend/README.md`](backend/README.md).

## Production and test separation

```text
internal/backend/                 production contracts and behavior
├── contracts.go                  shared page/event/command/stream contracts
├── errors.go                     stable application errors
├── clock.go                      injectable timing boundary
├── eventhub/                     page buses and Docker-event history
├── runtime/
│   ├── backend.go                process-wide facade and shutdown
│   ├── refresh_manager.go        refresh routing, pending work and publication
│   ├── command_executor.go       bounded FIFO queue and fixed workers
│   ├── job_executor.go           bounded active jobs and page progress events
│   ├── scheduler.go              periodic refresh requests
│   └── docker_events.go          reconnecting daemon event listener
├── dashboard/
│   ├── refresh.go                Dashboard refresh read
│   └── types.go                  Dashboard DTOs and update event
├── containers/                   separated facade/commands/reads/streams
├── compose/                      separated facade/commands/reads/jobs/logs
├── images/                       separated facade/commands/reads/pull job
├── volumes/                      separated facade/commands/reads
├── networks/                     separated facade/commands/reads
├── events/                       bounded-history refresh DTO/handler
└── system/                       separated facade/prune commands/reads

internal/platform/docker/         real Docker/Compose construction and adapters

test/backendtest/                 reusable black-box conformance suite
test/dockerfixture/               safe labeled real-Docker fixture
test/testkit/                     deterministic clocks/readers/closers/gates
test/integration/                 production backend against real Docker
```

Small `_test.go` files beside production files are normal Go unit tests. They
may test unexported concurrency details and are excluded from production
binaries. Reusable black-box behavior and real-Docker fixtures remain under
`test`.

## How tests precede implementation

The reusable suite depends on a public factory:

```go
type BackendFactory func(
    context.Context,
    IntegrationEnvironment,
) (backend.Backend, error)
```

A scenario first describes what sessions observe. Production code is then
implemented until the scenario passes. A foundational scenario:

1. creates the real backend;
2. subscribes two observers to the System page;
3. calls `RequestRefresh(PageSystem)` once;
4. requires both observers to receive the same typed result;
5. proves the event does not leak to another page;
6. cancels one observer without affecting the other;
7. closes the backend and verifies shared ownership.

Sessions always subscribe before requesting initial data.

## Hermetic unit coverage

Unit tests use fake Docker APIs, a manually advanced clock, explicit gates and
recording handlers. They cover:

- refresh registration, validation and page/key routing;
- exact-key pending deduplication;
- a trigger arriving during an active refresh;
- bounded refresh work and closed-manager rejection;
- backend-owned read contexts, read timeout and shutdown cancellation;
- typed success/failure publication to only the routed page;
- bounded command FIFO order and fixed worker concurrency;
- command overload behavior;
- accepted command survival after caller cancellation;
- command timeout and backend shutdown cancellation;
- refresh requests after attempted mutations;
- page API Docker calls, DTO conversion and error mapping;
- targeted details/process refresh keys;
- Event Bus filtering, ordering, overflow, cancellation and close;
- scheduler timing without wall-clock sleeps;
- Docker event routing and listener reconnection;
- logs/stats transformation and session cancellation;
- bounded jobs, per-project conflicts, cancellation and caller disconnect survival;
- page-broadcast job progress/completion and completion-triggered refreshes;
- Compose allowed-root validation, project/service conversion and operation mapping;
- Compose log transformation and job progress output;
- image list/details/history conversion, guarded prune, and pull progress decoding;
- volume list/details/attachment conversion, guarded prune, and in-use conflict mapping;
- network list/details/connections, IPAM mapping, address validation, guarded prune, and endpoint conflict mapping;
- Docker event resource/ID/action/project filtering, bounded recent-window mapping, and slow-observer isolation;
- System version/info and verbose disk-usage conversion, guarded resource prune reports, and broad-prune opt-in/confirmation;
- dependency ownership and leak-free shutdown.

There are no snapshot/cache tests because the architecture contains no Docker
resource snapshot cache.

## Real-Docker integration coverage

Integration tests verify behavior that fakes cannot establish reliably:

- real Moby request/response behavior;
- typed Dashboard, Containers, Compose, Images, Volumes, Networks, Events and System data from a real daemon;
- accepted commands followed by authoritative page updates;
- normalized Docker events and affected-page refreshes;
- details and processes delivered through `PageContainers`;
- logs and stats stream behavior;
- Compose up/details/logs/lifecycle/scale/down behavior;
- image list/details/history/tag/remove behavior without removing the shared base image;
- volume create/details/attachments/in-use conflict/remove behavior;
- label-filtered volume prune with a non-matching sentinel assertion;
- network create/details/connect/assigned-address/disconnect/in-use conflict/remove behavior;
- label-filtered network prune with a non-matching sentinel assertion;
- live resource/ID/action-filtered Docker events and the complete recent Events window;
- Docker version/info and verbose detailed disk usage;
- label-filtered container, volume and network System-prune APIs with non-matching sentinels;
- broad system prune only when `BACKEND_TEST_DEDICATED_DAEMON=true`;
- use and closure of one shared Moby client;
- cleanup of every resource created by the test.

Container exec and interactive Compose exec are not part of this version and
have no contract or test. Image pull is fully unit-tested through the real
Moby response contract but the standard integration suite does not depend on
Internet/registry access. Image prune is filter-validated in unit tests and is
not run against a shared developer daemon because an existing test image cannot
be safely retrofitted with a unique image label.

## Commands

```sh
# Formatting, vet and unit tests
make check

# Unit tests
make test

# Race-enabled unit tests
make test-race

# Real-Docker tests
make test-integration

# Race-enabled real-Docker tests
make test-integration-race

# Both race-enabled suites
make test-all
```

Equivalent direct commands are:

```sh
go test ./...
go test -race ./...
go test -tags=integration ./...
go test -race -tags=integration ./...
```

The executable is a temporary manual backend page demo:

```sh
# Containers page only, then watch updates for ten seconds.
go run ./cmd/ssh-docker-tui

# Initial Containers data only.
go run ./cmd/ssh-docker-tui -watch=0

# Dashboard and Containers.
go run ./cmd/ssh-docker-tui -page=all

# Exercise an existing container command.
go run ./cmd/ssh-docker-tui \
  -container-id=my-container \
  -container-action=restart \
  -watch=20s

# List active Compose projects.
go run ./cmd/ssh-docker-tui -page=compose -watch=0

# Inspect one active Compose project.
go run ./cmd/ssh-docker-tui \
  -page=compose \
  -compose-project=my-project \
  -compose-file=/srv/compose/my-project/compose.yaml

# Run a long Compose operation and print progress.
go run ./cmd/ssh-docker-tui \
  -page=compose \
  -compose-project=my-project \
  -compose-file=/srv/compose/my-project/compose.yaml \
  -compose-action=up

# List local images or inspect one image's history.
go run ./cmd/ssh-docker-tui -page=images -watch=0
go run ./cmd/ssh-docker-tui -page=images -image-id=nginx:latest -image-action=history

# Pull an image as a backend-owned job and print progress.
go run ./cmd/ssh-docker-tui -page=images -image-action=pull -image-reference=alpine:latest

# List volumes or inspect one volume's attached containers.
go run ./cmd/ssh-docker-tui -page=volumes -watch=0
go run ./cmd/ssh-docker-tui -page=volumes -volume-name=my-volume -volume-action=attachments

# List networks, inspect IPAM, or show connected containers and addresses.
go run ./cmd/ssh-docker-tui -page=networks -watch=0
go run ./cmd/ssh-docker-tui -page=networks -network-id=my-network -network-action=connections

# Show bounded event history, then watch matching live observations.
go run ./cmd/ssh-docker-tui -page=events -event-resource=container -event-action=start,stop -watch=20s

# Show Docker/host information and detailed disk usage (read-only).
go run ./cmd/ssh-docker-tui -page=system -watch=0
```

Supported manual actions are start, stop, restart, pause, unpause, kill,
rename and remove. Destructive operations act only on the explicitly selected
existing resource. Compose actions are start, stop, restart, pause, unpause,
scale, up, down, pull, build and logs. `up`, `pull`, `build` and `scale` require
an explicit allowed `-compose-file`. Image actions are details, history, tag,
remove and pull. Volume actions are details, attachments, create and remove.
Network actions are details, connections, create, remove, connect and
disconnect; every mutation requires an explicit target. Events and System
modes are read-only. Prune is intentionally not exposed by this temporary
manual executable.

## Docker configuration

The integration fixture follows normal Docker client configuration by default.
Optional test-only variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `BACKEND_TEST_DOCKER_HOST` | Docker client default | Override the test daemon. |
| `BACKEND_TEST_PREFIX` | `ssh-docker-tui-test` | Prefix test resources. |
| `BACKEND_TEST_TIMEOUT` | `30s` | Bound Docker operations and cleanup. |
| `BACKEND_TEST_DEDICATED_DAEMON` | `false` | Permit isolated-daemon destructive tests. |
| `BACKEND_TEST_CONTAINER_IMAGE` | `nginx:latest` | Existing Linux image with `sh`; never pulled or removed by tests. |

Every run receives a random ID and labels its resources with:

- `io.github.petar030.ssh-native-docker-tui.test=true`
- `io.github.petar030.ssh-native-docker-tui.test-run=<unique-run-id>`

## Resource safety and cleanup

The fixture does not expose its unrestricted Moby client. Domain tests use
narrow arrangement and inspection helpers. Every created resource must use
`fixture.Name`, apply `fixture.Labels`, and be tracked immediately. Compose
tests register project-label fallback cleanup before starting a job, so partial
failures remain isolated. Image tests remove only fixture-created tags.
Container, volume and network prune tests require a unique label and assert
that a differently labeled sentinel remains.

Cleanup removes only explicitly tracked resources, in dependency order:

1. containers;
2. networks;
3. volumes;
4. uniquely tagged test images.

Never track or remove shared base images. Unrestricted prune tests must call
`fixture.RequireDedicatedDaemon(t)` and remain skipped unless
`BACKEND_TEST_DEDICATED_DAEMON=true`.

Tests never discover cleanup targets by listing arbitrary host resources.

## TDD workflow per slice

1. Freeze only the next page's DTOs, typed events, refresh keys and operations.
2. Add observable conformance behavior and deterministic unit edge cases.
3. Confirm the new behavior fails for the expected reason.
4. Implement the smallest page API and runtime wiring.
5. Run unit, integration, race, repeated concurrency and cleanup checks.
6. Mark `docs/TODO.md` only when behavior and gates pass.

Fake adapters supplement rather than replace real-Docker tests. Real Docker
does not replace deterministic concurrency, failure and cancellation tests.
