# SSH-Native Docker TUI — Thesis Project Plan

## Temporary Starting Context for Codex

This document is the current source of truth for an in-progress thesis project. Read it completely before proposing architecture or writing code. This section is a temporary handoff block and can be removed after Codex has absorbed the project context.

The project is a centralized, multi-user terminal application written in Go. It runs on the same Linux server as a local Docker Engine. Users connect to the application over SSH and receive an interactive TUI for inspecting and managing Docker resources. The intended product is a serious but intentionally limited SSH-native alternative to the core features of Portainer, without a web interface.

Current work is limited to the shared application layer, referred to informally as the backend. Do not implement the Wish or Bubble Tea UI yet. The immediate goal is to define a local Go API, the state-refresh architecture, the event system, and a reusable test environment for the complete backend API. There is no HTTP, REST, RPC, or separate backend process: consumers call local Go interfaces in the same process.

Treat the following decisions as established unless a concrete implementation problem requires revisiting them:

- The application is a modular monolith: one Go process, one shared application layer, and one independent Bubble Tea model per SSH session.
- The first version manages only the local Docker Engine available to the server process. It does not open outbound SSH connections to other Docker hosts.
- Docker Engine is the authoritative source of resource state.
- The backend maintains shared, derived, versioned snapshots in memory so multiple sessions do not repeat the same Docker API calls.
- A central `RefreshCoordinator` is the only component allowed to update shared snapshots.
- Startup synchronization, backend polling, Docker events, successful commands, completed jobs, and explicit TUI refresh requests all enter the same refresh pipeline.
- The same typed Event Bus distributes changes regardless of what triggered a refresh.
- TUI sessions do not perform general independent polling. They read shared snapshots, subscribe to changes, and may explicitly request a scoped refresh.
- `DockerService`, the Docker event listener, and the Compose SDK use the same Moby API client instance. The application bootstrap owns and closes that client.
- Compose is invoked through the Docker Compose Go SDK and Docker CLI Go objects. The application must not shell out to `docker` or `docker compose` through `os/exec`.
- Logs, stats, progress, and a future interactive terminal are session-owned streams and are not stored as shared snapshots.
- The implementation must be test-first. The first milestone is a no-HTTP backend conformance test environment; the second milestone is the backend API implementation.

When continuing the work, preserve these decisions, update this document when a contract changes, and keep the roadmap checkboxes current. Mockups exist for the eventual TUI, but they are not the source of truth for the current backend work.

## Project Goal

Develop a centralized, multi-user terminal application that is accessed over SSH and allows users to monitor and manage a Docker environment on a Linux server.

The application will provide an interactive TUI through which users can inspect Docker resources, observe system state, and perform common administrative operations without manually entering Docker commands.

## High-Level Architecture

The application is a modular monolith: a single Go process containing a shared application layer and multiple independent TUI sessions. Wish handles the SSH server and sessions, Bubble Tea manages TUI state and lifecycle, Bubbles provides reusable terminal components, Lip Gloss handles layout and styling, and Docker Go libraries communicate with the local Docker Engine.

```text
SSH clients
    │
    ▼
Wish SSH server
    │
    ├── Bubble Tea session A
    ├── Bubble Tea session B
    └── Bubble Tea session N
            │
            ▼
Shared in-process backend API
    │
    ├── StateStore
    ├── RefreshCoordinator
    ├── Event Bus and Event Buffer
    ├── DockerService
    └── ComposeService
            │
            ▼
One shared Moby API client
            │
            ▼
Local Docker Engine
```

### Shared Application Layer

The shared application layer:

- communicates with the local Docker Engine;
- executes user commands;
- watches Docker events and notifies active sessions;
- maintains shared, derived Docker-state snapshots for all sessions;
- centrally schedules periodic and targeted refreshes;
- owns application logic while Docker Engine remains the authoritative source of resource state.

### SSH/TUI Layer

- Wish accepts SSH connections and creates a separate Bubble Tea program for each session.
- Every session owns its UI state: active view, selected row, filters, displayed data, modal state, terminal dimensions, and open session streams.
- Sessions call the shared local backend API and subscribe to typed application events.
- Sessions never access the Moby client or Compose SDK directly.

## Shared Client Ownership and Dependency Wiring

### Main Dependencies

- Docker Engine client: `github.com/moby/moby/client`
- Docker Engine API types: `github.com/moby/moby/api`
- Docker Compose SDK: `github.com/docker/compose/v5`
- Docker CLI Go library: `github.com/docker/cli`

### One Moby Client per Application Process

The application bootstrap creates one Moby API client and owns its complete lifecycle.

- `DockerService` uses the client directly for containers, images, volumes, networks, events, logs, stats, exec, Docker information, and disk usage.
- The same client is injected into a Docker CLI Go object through `command.WithAPIClient`.
- The Compose SDK is initialized from that Docker CLI object.
- `ComposeService` uses the Compose API service and therefore indirectly uses the same Moby client instance as `DockerService`.
- The central Docker event listener also uses the shared client and opens its own events stream.
- Services must not create, replace, or close the shared client.
- The application bootstrap closes the client exactly once during process shutdown.
- Each individual operation, stream, subscription, and job still receives its own `context.Context` for timeout and cancellation.

Conceptual dependency wiring:

```go
mobyClient := newMobyClient(...)
dockerCLI := newDockerCLI(command.WithAPIClient(mobyClient), ...)
composeAPI := newComposeAPI(dockerCLI)

dockerService := NewDockerService(mobyClient, ...)
composeService := NewComposeService(composeAPI, ...)
eventListener := NewDockerEventListener(mobyClient, ...)
```

Concrete constructors will be defined during implementation. Tests must verify instance sharing and single ownership without depending on concrete Docker client internals.

## Backend Components

### `StateStore`

`StateStore` holds shared, derived, versioned snapshots of Docker and Compose state. It is not the source of truth; it is an in-memory projection of Docker Engine state optimized for multi-session reads.

Responsibilities:

- atomically replace a snapshot for a refresh scope;
- return immutable or safely copied snapshots to callers;
- maintain a monotonically increasing version per scope;
- record the last successful refresh time and refresh reason;
- preserve the last valid snapshot when a later refresh fails;
- expose freshness metadata separately from resource data;
- allow concurrent readers without exposing partially updated state.

`StateStore` must not call Docker or publish events. Only `RefreshCoordinator` writes to it.

### `RefreshCoordinator`

`RefreshCoordinator` is the only entry point for loading Docker or Compose state into `StateStore`.

Refresh triggers:

- initial synchronization during application startup;
- central periodic backend polling;
- a normalized Docker event;
- a successful short command;
- completion of a long-running job;
- an explicit refresh requested by a TUI session;
- an optional TUI-requested auto-refresh for a specific scope.

Responsibilities:

- map a trigger to one or more refresh scopes;
- coalesce concurrent requests for the same scope;
- debounce a command and its corresponding Docker event;
- allow at most one active load per identical scope;
- fetch only the affected data through `DockerService` or `ComposeService` loaders;
- compare the new projection with the current snapshot;
- atomically store the new snapshot and increment its version only when the state changes;
- publish `SnapshotUpdated` after a successful changed write;
- publish `RefreshFailed` without deleting the last valid snapshot when loading fails;
- return a deterministic `RefreshResult` to explicit callers.

The first implementation may use a short configurable debounce window. Tests must use an injected clock or scheduler rather than real sleeps.

### Backend Refresh Scheduler

General polling is a backend policy that runs once per application process, independently of the number of TUI sessions.

- Base resource lists, resource summary, engine information, and disk usage are refreshed periodically.
- Different scopes may use different configured intervals.
- Detailed inspect snapshots are refreshed on demand, after a relevant event, or while an interested subscription exists.
- The scheduler must not periodically inspect every individual resource.
- Logs, stats, job progress, and terminal data are never polled into `StateStore`.

Client-side polling is not enabled by default. If a TUI view requests auto-refresh, it periodically submits `Refresh(scope)` to the backend; duplicate requests from multiple sessions are still coalesced centrally.

### Event Bus

The application uses one typed, in-process Event Bus for all sessions and backend components. It is a transport for notifications, not a state store and not a polling engine.

The important event types are:

- `DockerEventObserved` — a normalized Docker daemon event was received;
- `SnapshotUpdated` — a refresh successfully changed a versioned snapshot;
- `RefreshFailed` — a refresh failed and the previous valid snapshot remains available;
- optional lifecycle events for jobs or application shutdown if later required.

The same `SnapshotUpdated` contract is used whether the refresh was triggered by polling, a Docker event, a command, a completed job, or a manual refresh. The event records its reason, but consumers do not need a different communication path for each trigger.

Each session owns an independent filtered subscription. A slow or disconnected subscriber must not block the Docker events stream, the refresh pipeline, or other subscribers. Subscriber channels are bounded. Overflow behavior must be explicit, observable, and recoverable through snapshot versions and central polling.

### Event Buffer

The application keeps a bounded in-memory ring buffer of recent normalized Docker events for the Events view.

- It is separate from the Event Bus.
- It is not persisted across application restarts.
- Clearing or pausing an Events view affects only that TUI session’s local UI state.
- The application cannot delete historical events from Docker Engine.

### `DockerService`

`DockerService` wraps the shared Moby client and implements low-level Docker resource operations. It provides loader methods used by `RefreshCoordinator`, command methods, and session-owned streams.

It must not own TUI state or write directly to `StateStore`. Command methods report successful Engine operations; the coordinator owns the resulting refresh and event publication.

### `ComposeService`

`ComposeService` wraps the Compose SDK service that is initialized with the Docker CLI Go object containing the shared Moby client.

It loads and validates Compose projects, performs Compose lifecycle operations, exposes Compose logs and exec, and provides project projections to `RefreshCoordinator`. It must not shell out to the Docker CLI.

## Communication Models

There are three fundamental technical communication models. Query and Command are semantic forms of request/response. Polling and long-running jobs are behaviors built from the fundamental models.

### Request/Response

One request returns one result or one error.

**Query** reads a current shared backend snapshot without changing Docker state. TUI-facing queries normally read `StateStore` and do not call Docker Engine separately for each session.

**Command** changes Docker or Compose state. A successful command submits one or more targeted refresh requests. A command must never fabricate final resource state or publish `SnapshotUpdated` before the Engine state has been re-read.

**Refresh request** does not change a Docker resource. It explicitly asks the backend to reload one refresh scope from Docker Engine or the Compose layer, update `StateStore`, and publish a typed result event when appropriate.

### Stream

A stream produces values until completion, failure, or cancellation. Streams are used for:

- container logs;
- container stats;
- Compose logs;
- progress from long-running operations;
- a future interactive container terminal.

Every stream has a context-bound lifecycle, exactly one terminal outcome, and deterministic closure behavior. Closing a view or SSH session must cancel all streams owned by that session.

### Subscription

A subscription delivers typed application events from the Event Bus until it is closed or its context is canceled.

Subscriptions support filters by event type, resource type, resource identifier, project, and refresh scope where applicable. Cancellation must unregister the subscriber and close its event channel without leaking goroutines.

### Long-Running Job

A long-running job begins with a Command and exposes a progress Stream plus a final result. Typical examples are image pull, Compose `up`, Compose pull, and Compose build.

A job contract must provide:

- a stable job identifier;
- progress events;
- a final result or error;
- cancellation;
- a targeted refresh after successful completion;
- cleanup when the initiating session disconnects, according to an explicit detach/cancel policy.

The initial implementation should default to canceling session-owned jobs on session termination unless a job is explicitly designed to continue in the background.

## State Ownership and Consistency Rules

1. Docker Engine is the authoritative source of Docker resource state.
2. `StateStore` holds shared, derived, versioned snapshots.
3. `RefreshCoordinator` is the only component that changes snapshots.
4. Event Bus publishes notifications only after the corresponding state transition has been observed or a refresh has failed.
5. Each TUI session holds only UI state, its currently rendered data, and the last snapshot version it has observed.
6. TUI sessions do not directly poll Docker Engine.
7. Logs, stats, progress, and terminal streams belong to the session that opened them and are not written to `StateStore`.
8. Concurrent refresh requests for the same scope are coalesced or debounced.
9. A failed refresh does not erase the last valid snapshot. Its metadata is marked stale or failed and `RefreshFailed` is published.
10. Snapshot data and metadata are read atomically.

## Core Backend Contracts

The exact Go declarations may evolve during the first roadmap milestone, but the following semantics must remain stable so the conformance test suite can be written before the implementation.

### Top-Level Backend Facade

The test suite needs one stable entry point representing a running backend instance. The concrete implementation may be one struct or an aggregate of smaller interfaces, but it must expose equivalent capabilities:

```go
type Backend interface {
    Dashboard() DashboardAPI
    Containers() ContainerAPI
    Compose() ComposeAPI
    Images() ImageAPI
    Volumes() VolumeAPI
    Networks() NetworkAPI
    Events() EventAPI
    System() SystemAPI

    Refresh(ctx context.Context, scope RefreshScope, reason RefreshReason) (RefreshResult, error)
    GetSnapshotVersion(ctx context.Context, scope RefreshScope) (SnapshotMeta, error)
    SubscribeStateChanges(ctx context.Context, filter EventFilter) (Subscription, error)
    Close(ctx context.Context) error
}
```

Resource-specific interfaces keep fakes small and allow the final package layout to evolve. `Close` performs graceful backend shutdown but does not permit individual services to close the shared Moby client independently.

### Refresh Scope

A refresh scope identifies exactly what should be reloaded. It must be comparable so identical requests can be coalesced.

```go
type RefreshScope struct {
    Resource ResourceType
    ID       string
    View     SnapshotView
}
```

Examples include all container summaries, details for one container, all image summaries, one Compose project, Dashboard resource summary, disk usage, and Docker Engine information. An empty `ID` represents a collection or global scope. `View` distinguishes summaries, details, history, processes, or another projection when the resource type alone is insufficient.

### Refresh Reason

The initial reasons are `startup`, `scheduled`, `docker_event`, `command`, `job_completed`, `manual`, and `client_auto_refresh`. The reason is diagnostic metadata and does not change the delivery mechanism.

### Snapshot Metadata

Every stored snapshot exposes metadata equivalent to:

```go
type Snapshot[T any] struct {
    Data T
    Meta SnapshotMeta
}

type SnapshotMeta struct {
    Scope          RefreshScope
    Version        uint64
    RefreshedAt    time.Time
    Reason         RefreshReason
    Stale          bool
    LastRefreshErr error
}
```

The concrete representation of `LastRefreshErr` may use a serializable application error instead of Go’s `error` interface. Snapshot versions are monotonically increasing per scope.

### Refresh Result

```go
type RefreshResult struct {
    Scope       RefreshScope
    Version     uint64
    Changed     bool
    Coalesced   bool
    RefreshedAt time.Time
}
```

If the refresh fails, the method returns an error while the previous snapshot remains readable.

### Application Event

```go
type AppEvent struct {
    Sequence        uint64
    Type            EventType
    Time            time.Time
    ResourceType    ResourceType
    ResourceID      string
    Project         string
    Scope           RefreshScope
    SnapshotVersion uint64
    RefreshReason   RefreshReason
    Action          string
    Attributes      map[string]string
}
```

Fields that do not apply to an event type remain empty. `Sequence` is process-local and monotonically increasing so tests and subscribers can reason about ordering.

### Subscription Contract

```go
type Subscription interface {
    Events() <-chan AppEvent
    Close() error
}
```

Closing the context or calling `Close` unregisters the subscription exactly once. The overflow policy must be testable. The initial recommendation is to coalesce redundant `SnapshotUpdated` notifications for the same scope and expose an overflow signal instead of blocking publishers.

### Stream Contract

A concrete stream may use typed receive-only channels or an iterator-like interface. Every stream must guarantee ordered values, one final completion or error, context cancellation, closure of all exposed channels and underlying Docker readers, and no goroutine leaks.

### Job Contract

```go
type Job interface {
    ID() string
    Progress() <-chan ProgressEvent
    Wait(ctx context.Context) (JobResult, error)
    Cancel() error
}
```

`Wait` is safe to call once unless the final interface explicitly documents repeatable result retrieval. A successfully completed job requests the appropriate refresh scopes before its final state is considered visible to other sessions.

### Command Result

Short commands return a common result envelope containing the operation identifier, affected resources, and requested refresh scopes. The initial implementation should wait for its immediate targeted refresh when practical. If an operation is only accepted asynchronously, the result must say so explicitly and the final state must arrive through later refresh events.

### Application Errors

The backend needs stable error categories that can be asserted in tests and rendered by the TUI:

- invalid input;
- not found;
- conflict or resource in use;
- permission denied;
- Docker daemon unavailable;
- timeout;
- canceled;
- snapshot unavailable;
- stream closed;
- unsupported operation;
- internal error.

Concrete SDK errors should be wrapped while preserving the original cause for diagnostics.

## Shared State and Refresh API

- `Refresh(ctx, scope, reason) (RefreshResult, error)` — reloads one scope, updates `StateStore`, and publishes `SnapshotUpdated` if the snapshot changed. **Model:** Request/Response — Refresh request.
- `GetSnapshotVersion(ctx, scope) (SnapshotMeta, error)` — returns current version and freshness metadata. **Model:** Request/Response — Query.
- `SubscribeStateChanges(ctx, filter) (Subscription, error)` — subscribes to relevant `SnapshotUpdated` and `RefreshFailed` events. **Model:** Subscription.

The scheduler, Docker event listener, commands, completed jobs, and explicit TUI refresh actions all call the same refresh-coordination contract. External callers must not write `StateStore` directly.

## Domain API Surface

All methods receive `context.Context`. Exact DTO fields will be frozen as part of the testing-environment milestone. Unless stated otherwise, list and inspect methods return versioned snapshot data plus `SnapshotMeta`, short commands return `CommandResult`, long operations return `Job`, and streams are owned by the initiating session.

### Dashboard

**Queries:**

- `GetEngineSummary(ctx)` — Engine status, versions, host, operating system, and uptime.
- `GetResourceSummary(ctx)` — counts of containers, images, volumes, and networks.
- `GetDiskUsage(ctx)` — Docker disk usage by resource type.
- `GetRecentEvents(ctx, filter, limit)` — recent normalized Docker events from the ring buffer.
- `GetHostResources(ctx)` — current host CPU and memory snapshot if a separate Linux metrics provider is added.

Container, image, volume, and network changes refresh affected Dashboard summaries. Central scheduled refresh protects against missed events. Detailed host CPU/RAM charts cannot be implemented through Docker SDK alone and require a separate Linux metrics source; basic CPU and memory information from Docker info remains available.

### Containers

**Queries:**

- `ListContainers(ctx, filter)` — status, health, ports, and Compose labels.
- `InspectContainer(ctx, id)` — configuration, networks, mounts, ports, and restart policy.
- `ListContainerProcesses(ctx, id)` — processes currently running inside the container.

**Commands:**

- `StartContainer(ctx, id)`
- `StopContainer(ctx, id, options)`
- `RestartContainer(ctx, id, options)`
- `PauseContainer(ctx, id)`
- `UnpauseContainer(ctx, id)`
- `KillContainer(ctx, id, signal)`
- `RenameContainer(ctx, id, name)`
- `RemoveContainer(ctx, id, options)`
- `ExecContainer(ctx, id, request)` — one-shot execution with captured output and exit code.

**Streams:**

- `WatchContainerLogs(ctx, id, options)`
- `WatchContainerStats(ctx, id)`
- `OpenContainerTerminal(ctx, id, options)` — future bidirectional exec/attach stream.

Relevant Docker actions are `create`, `start`, `stop`, `die`, `destroy`, `restart`, `pause`, `unpause`, `rename`, `health_status`, and `oom`. A successful command and its Docker event both request affected container scopes; the coordinator coalesces duplicates. Logs and stats close with the view or session.

**Deferred complexity:** The interactive terminal requires bridging the SSH terminal with Docker’s hijacked stream and resize events. One-shot `ExecContainer` remains in the first implementation scope.

### Compose Projects

**Queries:**

- `ListComposeProjects(ctx, filter)` — configured and active projects.
- `LoadComposeProject(ctx, path, options)` — loads a project definition from an allowed path.
- `ValidateComposeProject(ctx, path, options)` — validates configuration without changing Docker state.
- `GetComposeProject(ctx, name)` — summary, services, resources, configuration, and dependencies.

**Short commands:**

- `ComposeStart(ctx, project)`
- `ComposeStop(ctx, project, options)`
- `ComposeRestart(ctx, project, options)`
- `ComposePause(ctx, project)`
- `ComposeUnpause(ctx, project)`
- `ComposeExec(ctx, project, service, request)` — one-shot command with output and exit code.
- `ComposeScale(ctx, project, service, replicas)`

**Jobs:**

- `ComposeUp(ctx, project, options)`
- `ComposeDown(ctx, project, options)`
- `ComposePull(ctx, project, services, options)`
- `ComposeBuild(ctx, project, services, options)`

**Streams:**

- `WatchComposeLogs(ctx, project, services, options)`

Active projects are recognized through Compose labels on containers, networks, and volumes. Docker events map to affected project scopes. Job progress is delivered only to the initiating session; successful completion refreshes the shared project snapshot and notifies all sessions.

**Discovery limitation:** Docker Engine can identify active Compose projects through labels, but cannot reliably discover every inactive Compose project on the filesystem. The first version uses configured allowed directories plus active projects discovered through labels. Advanced watch and publish features are not planned.

### Images

**Queries:**

- `ListImages(ctx, filter)` — repository, tags, size, creation time, and usage.
- `InspectImage(ctx, id)` — image details.
- `GetImageHistory(ctx, id)` — image layers and history.

**Commands:**

- `TagImage(ctx, id, repository, tag)`
- `RemoveImage(ctx, id, options)`
- `PruneImages(ctx, filter)` — returns a final prune report.

**Jobs:**

- `PullImage(ctx, reference, auth)` — progress plus final result.

Relevant actions include `pull`, `delete`, `tag`, `untag`, `import`, `load`, `save`, and `push`. Image events and successful operations refresh image and disk-usage scopes. Pull progress belongs to the initiating session; the resulting shared snapshot update is delivered to all interested sessions.

**Deferred features:** image push and advanced registry credential management are not part of the first version. Image build is initially exposed through Compose.

### Volumes

**Queries:**

- `ListVolumes(ctx, filter)` — volume summaries and attached-container count.
- `InspectVolume(ctx, name)` — driver, scope, mountpoint, labels, and options.
- `ListVolumeContainers(ctx, name)` — containers using the volume, derived from volume and container snapshots.

**Commands:**

- `CreateVolume(ctx, options)`
- `RemoveVolume(ctx, name, options)`
- `PruneVolumes(ctx, filter)`

Relevant actions are `create`, `destroy`, `mount`, and `unmount`. Refreshes update volume, disk-usage, and affected container scopes. Before deletion, the backend checks attached containers and returns a stable conflict error when appropriate.

**Limitation:** Docker API does not provide a universal operation for browsing, backing up, or restoring volume contents. Those features require a helper container or direct filesystem access and are not part of the first version.

### Networks

**Queries:**

- `ListNetworks(ctx, filter)` — name, driver, scope, subnet, gateway, and attached-container count.
- `InspectNetwork(ctx, id)` — IPAM configuration and connected containers.

**Commands:**

- `CreateNetwork(ctx, options)`
- `RemoveNetwork(ctx, id)`
- `PruneNetworks(ctx, filter)`
- `ConnectContainer(ctx, networkID, containerID, options)`
- `DisconnectContainer(ctx, networkID, containerID, options)`

Relevant actions are `create`, `connect`, `disconnect`, `destroy`, and `remove`. Refreshes update network scopes and affected container details. Connect and disconnect commands immediately request a targeted network refresh to confirm the assigned IP address.

**Deferred features:** advanced custom network-driver configuration and Swarm overlay networks are not planned. The first version focuses on local bridge networks.

### Events

**Internal infrastructure:**

- `RunDockerEventListener(ctx)` — runs one central Docker events stream for the process. TUI sessions never call it.
- `RunRefreshScheduler(ctx, policy)` — runs the central scheduled refresh loop.
- `RequestRefresh(scope, reason)` — submits and coalesces targeted refresh work.
- `Publish(event)` — publishes a typed event; normalized Docker events are also added to the ring buffer.

**Session-facing API:**

- `SubscribeEvents(ctx, filter)` — subscribes to `DockerEventObserved`, `SnapshotUpdated`, `RefreshFailed`, and future typed events.
- `GetRecentEvents(ctx, filter, limit)` — reads normalized Docker events from the ring buffer.
- `Subscription.Close()` — closes the subscription; context cancellation performs the same cleanup.

Pausing an Events view and clearing its visible rows are local TUI operations, not backend commands. The central listener and ring buffer continue running.

### System

**Queries:**

- `GetDockerVersion(ctx)` — client, server, and API versions.
- `GetDockerInfo(ctx)` — host, kernel, CPU, memory, storage, logging, and cgroup drivers.
- `GetDiskUsage(ctx)` — image, container, volume, and build-cache usage.

**Commands:**

- `PruneContainers(ctx, options)`
- `PruneImages(ctx, options)`
- `PruneVolumes(ctx, options)`
- `PruneNetworks(ctx, options)`
- `SystemPrune(ctx, options)`

Prune commands return removed resources and reclaimed space, then request refreshes for Dashboard, disk usage, and affected resource scopes.

**Limitations:** the application will not modify Docker daemon configuration, manage the `dockerd` service, update Docker Engine, or provide detailed Linux host monitoring through the planned Docker SDK layer.

## Refresh Scope Mapping

| Trigger | Scopes refreshed by `RefreshCoordinator` |
| --- | --- |
| Container `start`, `stop`, `die`, `restart`, `pause`, `unpause`, `health_status`, `oom` | container list, affected container details, related Compose project |
| Container `create`, `destroy`, `rename` | container list, Dashboard resource summary, related Compose project |
| Image `pull`, `delete`, `tag`, `untag`, `import`, `load` | image list, affected image details, disk usage |
| Volume `create`, `destroy`, `mount`, `unmount` | volume list, affected volume details, related container details, disk usage |
| Network `create`, `destroy`, `connect`, `disconnect` | network list, affected network details, related container details |
| Daemon `reload` | Docker information, Dashboard Engine summary |
| Successful short command | command-declared affected scopes |
| Successful job completion | job-declared affected scopes |
| Scheduled refresh | scopes configured by refresh policy |
| Manual or client auto-refresh | explicitly requested scope |

This mapping is used by `RefreshCoordinator`, not individual TUI sessions. Event Bus publishes only after the resulting snapshot is stored or the refresh fails.

## End-to-End Integration Flow

1. Application bootstrap creates one Moby client, injects it into `DockerService` and the Docker CLI object used by Compose SDK, and constructs the remaining backend components.
2. Bootstrap starts the Docker event listener, refresh scheduler, and initial synchronization.
3. `RefreshCoordinator` populates `StateStore` with initial snapshots.
4. A TUI session opens a view and reads the corresponding backend snapshot without directly calling Docker Engine.
5. The session may execute a Command, start a Stream or Job, or explicitly request `Refresh(scope)`.
6. A successful command or completed job submits affected scopes to `RefreshCoordinator`.
7. Docker events and the periodic scheduler use the same `RequestRefresh(scope, reason)` pipeline.
8. `RefreshCoordinator` coalesces duplicate work, calls Moby or Compose, and atomically updates `StateStore`.
9. If data changed, Event Bus publishes `SnapshotUpdated`; on failure it publishes `RefreshFailed` and preserves the previous snapshot.
10. Active sessions cheaply re-read the new snapshot from `StateStore`. Inactive sessions remember that a newer version exists and read it when the relevant view is opened.
11. Session-owned streams, subscriptions, and jobs are canceled and cleaned up when their contexts end.
12. Process shutdown stops schedulers and listeners, closes subscriptions and streams, waits for owned goroutines, and closes the shared Moby client once.

A command and its corresponding Docker event may arrive almost simultaneously. Coalescing, single-flight loading, and a short debounce prevent duplicate Docker API calls.

## Backend API Testing Strategy

The backend testing environment is established before the production backend is implemented. Tests call the local Go interfaces directly; no HTTP server, REST API, RPC layer, or separate backend process is involved.

The primary test suite exercises the real production backend against a real Docker daemon. Fake-based tests are used only as supporting unit tests for deterministic timing, concurrency, and failure scenarios that are difficult to reproduce reliably with a real daemon.

### Real-Docker Conformance Suite

Create a reusable backend conformance suite that accepts a `BackendFactory` or equivalent fixture. The production backend implementation must pass the complete suite while connected to a real Docker daemon.

Conceptually:

```go
type BackendFactory func(
    ctx context.Context,
    env IntegrationEnvironment,
) (Backend, error)
```

`IntegrationEnvironment` contains the configuration and isolated resources required to construct the real production backend:

```go
type IntegrationEnvironment struct {
    DockerEndpoint string
    ComposeRoot    string
    ResourcePrefix string
    ResourceLabels map[string]string
}
```

The factory must construct the production backend using:

* a real Moby API client;
* the real Docker Compose SDK;
* the real Docker CLI Go object configured with the shared Moby client;
* the production `StateStore`;
* the production `RefreshCoordinator`;
* the production Event Bus and event buffer;
* the production Docker event listener and refresh scheduler.

The integration fixture provides:

* access to a real Docker daemon;
* a unique prefix for all test resources;
* identifying labels for all test resources;
* isolated temporary Compose project directories;
* test images or image references;
* bounded timeouts for commands, events, streams, and jobs;
* tracked cleanup for every created resource;
* backend startup and graceful shutdown;
* diagnostics when a test fails.

### Real-Docker Integration Tests

The real-Docker suite is the primary backend API test suite. It verifies the complete path:

```text
Integration test
    ↓
Production backend API
    ↓
DockerService / ComposeService
    ↓
Real Moby client / Compose SDK
    ↓
Real Docker daemon
    ↓
Docker events
    ↓
RefreshCoordinator
    ↓
StateStore
    ↓
Event Bus
    ↓
Test subscriber
```

The suite covers:

* application bootstrap and shared Moby-client wiring;
* initial backend synchronization;
* every public Query method;
* every public Command method;
* every supported Refresh scope and reason;
* Docker event reception and normalization;
* `StateStore` updates after real Docker events;
* `SnapshotUpdated` delivery to multiple subscribers;
* container creation, start, stop, restart, pause, unpause, rename, kill, exec, and removal;
* container process listing;
* container logs and stats streams;
* image listing, inspection, history, pull, tagging, removal, and prune behavior;
* volume creation, inspection, attachment checks, removal, and prune behavior;
* network creation, inspection, connect, disconnect, removal, and prune behavior;
* Compose project loading, validation, lifecycle commands, jobs, logs, exec, and scaling;
* Docker version, information, and disk-usage queries;
* safe system and resource prune operations;
* command-triggered refreshes;
* job-completion refreshes;
* Docker-event-triggered refreshes;
* scheduled backend refreshes;
* manual refresh requests;
* client-requested auto-refresh;
* cancellation of streams, subscriptions, and jobs;
* graceful backend shutdown.

A typical command test must verify the complete state transition. For example:

1. Create a real test container.
2. Read the initial container snapshot.
3. Subscribe to relevant application events.
4. Call `StartContainer`.
5. Confirm that Docker Engine starts the container.
6. Confirm that the real Docker event is observed.
7. Confirm that `RefreshCoordinator` refreshes the affected scope.
8. Confirm that `StateStore` contains the new container state.
9. Confirm that `SnapshotUpdated` is delivered to subscribers.
10. Stop and remove the container.
11. Clean up every resource created by the test.

### Integration-Test Isolation and Safety

Every integration test must follow these rules:

* Every Docker resource uses a unique test-run prefix.
* Every supported resource is assigned identifying test labels.
* Tests operate only on resources created by the current test run.
* Cleanup runs even when a test fails.
* Cleanup removes resources in dependency order.
* Tests never use unrestricted prune operations against the host daemon.
* Prune tests must use supported filters or an otherwise isolated Docker daemon.
* Tests never stop, remove, rename, connect, disconnect, or inspect unrelated resources as part of assertions.
* Compose projects use isolated temporary directories and unique project names.
* Tests must tolerate unrelated Docker events by filtering on test labels, resource identifiers, and project names.
* A failed cleanup prints the identifiers of every remaining test resource.
* The suite must refuse to run destructive cases when the required isolation guarantees are unavailable.

For maximum isolation, CI may run the suite against a dedicated Docker daemon. Local development may use the developer’s daemon only when the resource-prefix, labeling, filtering, and cleanup rules are enforced.

### Supporting Hermetic Unit Tests

A smaller supporting suite uses in-memory fakes. These tests do not replace the real-Docker conformance suite and do not claim to validate Moby or Compose SDK integration.

Fake-based tests cover internal behavior that is difficult to trigger deterministically with a real daemon:

* atomic `StateStore` reads and writes;
* snapshot versioning;
* preservation of the last valid snapshot after refresh failure;
* concurrent readers and writers;
* coalescing of simultaneous refresh requests;
* debouncing a command and its corresponding Docker event;
* deterministic scheduler behavior;
* precise timeout and cancellation timing;
* refresh failures and daemon-unavailable errors;
* event filtering and process-local sequence ordering;
* ring-buffer capacity and eviction;
* slow subscribers;
* subscriber-channel overflow;
* subscription removal;
* stream closure edge cases;
* job cancellation edge cases;
* shutdown during active refreshes or streams;
* goroutine-leak and data-race scenarios.

These tests use injected clocks, explicit synchronization barriers, recording adapters, and deterministic channels. They must not depend on wall-clock sleeps.

### Test Implementation Order

The testing milestone is completed before the production backend milestone, but the complete test suite cannot pass until the production backend is implemented.

The intended order is:

1. Define the backend interfaces, DTOs, event types, refresh scopes, errors, and lifecycle contracts.
2. Build the real-Docker integration fixture and cleanup system.
3. Write the reusable conformance tests against the public backend interfaces.
4. Add supporting deterministic unit tests for internal edge cases.
5. Implement the production backend.
6. Make the production backend pass the complete real-Docker conformance suite.
7. Make all supporting unit, race, leak, and shutdown tests pass.

During the first milestone, tests may initially fail to compile or remain explicitly pending until the corresponding production API is introduced. Placeholder implementations must not return fabricated success merely to make tests pass.

### Required Quality Gates

The exact commands may depend on the final package and build-tag layout, but the project must provide equivalent required checks:

```text
go test ./...
go test -race ./...
go test -tags=integration ./...
go test -race -tags=integration ./...
```

The integration build tag may be used to select tests that require a Docker daemon, but the real-Docker suite is not optional for project acceptance.

Additional requirements:

* all public backend methods are exercised against a real Docker daemon;
* all application event types are covered;
* concurrency-sensitive packages pass deterministic repeated runs;
* no goroutines, readers, hijacked connections, subscriptions, streams, or jobs leak;
* test cleanup leaves no test containers, images, volumes, networks, or Compose resources behind;
* unit tests, real-Docker integration tests, and any later TUI tests remain clearly separated;
* failures include enough diagnostics to identify the Docker resource, refresh scope, event, and snapshot version involved.

## Initial Scope and Deferred Features

The first implementation includes:

- shared backend bootstrap and dependency wiring;
- `StateStore`, `RefreshCoordinator`, scheduler, Event Bus, and event ring buffer;
- Dashboard base data;
- container management, logs, stats, process listing, and one-shot exec;
- images, volumes, and local bridge networks;
- basic Compose project loading and lifecycle operations;
- Docker events;
- system information and safe prune operations;
- the complete backend conformance test suite.

Deferred or optional features:

- interactive container terminal if it requires disproportionate time;
- detailed Linux host monitoring;
- volume browsing, backup, and restore;
- registry credential management and image push;
- Docker Swarm;
- multiple Docker hosts;
- persistent event history;
- automatic scanning of the entire filesystem for Compose projects;
- Docker daemon configuration and service management.


