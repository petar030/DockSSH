# SSH-Native Docker TUI — Project Plan

This file is the permanent architectural source of truth. `docs/TODO.md` is the
ordered product roadmap, `docs/testing.md` defines verification rules, and
[`docs/backend/`](backend/README.md) is the detailed backend reference.

## Project goal

Build an SSH-accessible terminal UI for managing the Docker Engine on the same
server as the application. Multiple SSH sessions share one in-process backend
and one Docker/Compose integration. The first version manages only that local
Engine.

The application is a modular Go monolith. Docker is the authoritative resource
state. The backend coordinates operations and typed notifications but does not
maintain a second cache of Docker resource snapshots. Each TUI session owns the
data it renders.

## TUI and SSH stack

- [Wish](https://github.com/charmbracelet/wish) provides the SSH server,
  middleware and session lifecycle.
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) provides one
  independent `tea.Program` per SSH session.
- [Bubbles](https://github.com/charmbracelet/bubbles) provides reusable TUI
  components.
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) provides layout and
  styling.

These dependencies are added when TUI implementation starts. Each Bubble Tea
model owns its active page, selected row, filters, modal state, terminal size,
rendered backend data, subscriptions and session-owned streams. TUI code uses
the application Backend API and never imports Docker SDK types.

Wish and Bubble Tea may use several goroutines per session. Architecture does
not rely on their exact goroutine count; it relies on context ownership.

## Process, goroutine and context ownership

The SSH server, TUI programs and backend all run in one Go OS process. The
Docker daemon is a different OS process.

```text
┌──────────────────────────── ONE APPLICATION OS PROCESS ────────────────────────────┐
│                                                                                    │
│  SSH/TUI session A              SSH/TUI session B                                  │
│  Wish + tea.Program             Wish + tea.Program                                 │
│  session context                session context                                    │
│          │                              │                                           │
│          └──────────────┬───────────────┘                                           │
│                         v                                                           │
│                 Shared Backend facade                                              │
│                    │       │       │                                                │
│        Subscribe ──┘       │       └── page API calls                              │
│                            │                                                        │
│                 RequestRefresh                                                     │
│                            v                                                        │
│  ┌──────────────────────────────────────────────────────────────────────────────┐  │
│  │ BACKEND-LIFETIME WORK                                                       │  │
│  │                                                                             │  │
│  │ Refresh Manager <── scheduler                                               │  │
│  │       ^           <── Docker event listener                                 │  │
│  │       └─────────── <── command completion                                   │  │
│  │       │                                                                     │  │
│  │       └── page API ReadRefresh ──────┐                                      │  │
│  │                                      v                                      │  │
│  │ Bounded Command Executor ─────> shared Moby client                          │  │
│  │ fixed worker pool                    │                                      │  │
│  │ Bounded active Job Executor ───> shared Compose service                     │  │
│  └──────────────────────────────────────┼──────────────────────────────────────┘  │
│                                         │                                         │
│  Event Hub page buses ── typed events ──┼──> matching TUI subscriptions           │
│                                         │                                         │
│  SESSION-LIFETIME WORK                  │                                         │
│  logs/stats stream ─────────────────────┘                                         │
│                                                                                    │
└─────────────────────────────────────────┼──────────────────────────────────────────┘
                                          │ Docker endpoint
                                          v
                               ┌──────────────────────────┐
                               │ DOCKER DAEMON OS PROCESS │
                               │ authoritative resources  │
                               └──────────────────────────┘
```

Context rules:

- the backend lifecycle ends only during application shutdown;
- accepted commands and accepted refreshes use backend-owned contexts;
- a session context controls how long that TUI waits for a command result;
- subscriptions, logs and stats end with their owning page/session context;
- application shutdown rejects new work, cancels workers and closes the shared
  Docker client exactly once.

## Stable architecture decisions

- Use one Moby client per application process.
- Initialize the Docker CLI object with that same Moby client for Compose.
- Use Compose v2 Go packages in process; never shell out to Docker commands.
- Use one independent Event Bus per TUI page.
- Subscribe before requesting a page's initial refresh.
- Send refresh results as complete typed update events.
- Keep only operational state: queues, pending refresh keys, subscriptions and
  bounded recent Docker-event history.
- Use no general-purpose Docker resource cache or snapshot store.
- Use no HTTP/REST boundary between the TUI and backend.
- Use no global serial command queue.

## Backend facade

The process-wide facade is shared by every TUI session:

```go
type Backend interface {
    RequestRefresh(Page) error
    Subscribe(context.Context, Page, EventFilter) (Subscription, error)
    Close(context.Context) error
}
```

Domain accessors `Containers()`, `Compose()`, `Images()`, `Volumes()`,
`Networks()`, and `System()` expose their page APIs. Dashboard and Events need
no separate public accessor: their callable operations are the common page
refresh and subscription methods.

`RequestRefresh` confirms that backend work was accepted. It does not wait for
Docker. The typed success or `RefreshFailed` event arrives through that page's
subscription.

### Client communication example

```go
subscription, err := appBackend.Subscribe(ctx, backend.PageContainers, backend.EventFilter{})
if err != nil {
    return err
}
defer subscription.Close()

if err := appBackend.RequestRefresh(backend.PageContainers); err != nil {
    return err
}

for event := range subscription.Events() {
    // Replace this session's visible Containers data with the typed payload.
}
```

## Event Hub

The Event Hub contains one bounded, in-process bus for Dashboard, Containers,
Compose, Images, Volumes, Networks, Events and System. Every subscription has
its own filter, buffer, context and cleanup.

```go
type EventEnvelope struct {
    Sequence uint64
    Time     time.Time
    Key      RefreshKey
    Reason   RefreshReason
    Payload  EventPayload
}
```

Each bus provides page-local ordering, typed filtering and non-blocking
broadcast. A slow subscriber receives `SubscriberOverflow` and requests an
authoritative recovery refresh. The Event Hub never calls Docker, executes a
command or starts a refresh.

All subscriptions may filter by event type and full refresh key. Events-page
subscriptions may additionally filter raw `DockerEventObserved` values by
resource type, resource ID, action and Compose project. Non-empty filter
dimensions are combined with AND; values inside one dimension are alternatives.
These Docker-specific dimensions do not hide non-Docker payloads such as the
complete recent-window update when its event type is requested.

The Events bus also retains a bounded history of normalized Docker events. This
is event history, not cached Docker resource state.

## Refresh Manager

One `RefreshManager` replaces separate catalog, coordinator and dispatcher
objects. It owns:

- refresh-handler and page-base-route registration;
- one bounded FIFO list plus map of pending `RefreshKey` values;
- exact-key deduplication;
- one backend-owned worker;
- per-read timeout and shutdown cancellation;
- typed result or failure publication to the routed page Event Bus.

Production defaults are 128 pending refresh keys and a 30-second read timeout;
both are configurable at bootstrap.

```go
type RefreshKey struct {
    Kind RefreshKind
    ID   string
}

type RefreshHandler func(context.Context, RefreshKey) (EventPayload, error)
```

Examples include `dashboard.summary`, `containers.list`, and
`container.details` with a container ID. Routing configuration stores
functions, not loaded data.

If repeated requests for the same key are pending, one read is sufficient. A
request arriving while that key is actively being read leaves one additional
refresh pending. Different IDs remain distinct. Pending work is bounded, and
overflow or closed-manager requests return explicit errors.

All refresh triggers use this one path:

- page entry and manual refresh;
- scheduler tick;
- Docker event;
- command attempt completion;
- completed future job;
- subscriber overflow recovery.

The scheduler submits only configured base refresh keys. It does not inspect
every individual resource periodically.

## Command Executor

Short mutating operations use one bounded FIFO queue and a fixed worker pool.
This is concurrent backend ownership, not global serialization.

```text
TUI calls Containers().Start(waitCtx, id)
    -> Containers API validates and builds a command request
    -> Command Executor accepts it into the bounded channel
    -> a fixed backend worker receives it in FIFO order
    -> worker calls Docker using backend context and timeout
    -> worker requests affected Refresh Manager keys
    -> result is delivered if the initiating TUI still waits
```

Before queue acceptance, caller cancellation may prevent submission. After
acceptance, session cancellation stops only that caller's wait; the backend
operation continues. A buffered terminal-result channel prevents completion
from blocking after disconnection.

The queue has a fixed capacity. When full, submission returns a stable conflict
or busy error. Independent commands run concurrently up to the fixed worker
count. Docker Engine remains responsible for resource-level concurrency.
Long Compose jobs use separate narrow per-project conflict protection; short
commands remain governed by Docker/Compose itself.

Production defaults are four workers, 32 queued commands and a 30-second
per-command timeout; all are configurable at bootstrap.

After every attempted Docker mutation, including an ambiguous Docker error, the
executor requests authoritative refreshes for the declared affected keys.
Validation failures and queue rejection do not refresh because Docker was not
called.

## Page APIs and code organization

Each page with a domain-specific TUI API separates its TUI-facing facade from
backend-only handlers. A read-only page with no domain-specific TUI calls (the
current Dashboard) has no `api.go`; it has only its runtime refresh handler.
The split is code organization only: command execution, refresh, event,
context, and stream behavior do not change because files move.

```text
internal/backend/<page>/
├── api.go       TUI-facing methods and API construction only
├── commands.go  private page-specific CommandRequest construction (if needed)
├── refresh.go   RefreshManager handler and private Docker read mapping
├── streams.go   private session-stream implementation (if needed)
├── docker.go    private narrow Docker client contract (if needed)
├── types.go     DTOs, options and typed event payloads
├── errors.go    error translation (only when needed)
└── api_test.go  page contract tests
```

Not every page needs every file. Do not create empty packages or speculative
files before a roadmap slice implements that page.

`api.go` contains only methods a TUI session may call. A targeted-refresh method
validates its input and calls the injected `RefreshRequester` directly. A short
command method builds its private request, then calls the injected
`CommandRunner` directly. `api.go` does not contain a Docker command callback
body, a refresh read, or a stream-reader goroutine. Docker SDK types never
cross a page API boundary.

`commands.go` keeps each page's private Docker mutation callback next to the
operation it describes. The shared `CommandExecutor` receives the completed
`CommandRequest`, provides queueing/workers/timeout/backend context, and is the
only component that calls its `Run` callback.

`refresh.go` defines `ReadRefresh`. This exported method is **runtime-facing**,
not TUI-facing: bootstrap registers it with `RefreshManager`, which is its only
caller. The manager owns the read context/timeout and publishes the resulting
typed event to the routed page bus.

`streams.go` holds private implementation of session-owned readers. The TUI
opens a stream through `api.go`, but its view/session context owns its lifetime.

### TUI-accessible common Backend API

Every TUI page may use only this common facade:

```go
type Backend interface {
    RequestRefresh(Page) error
    Subscribe(context.Context, Page, EventFilter) (Subscription, error)
    Close(context.Context) error
}
```

`RequestRefresh` is a generic request for a page's registered base refresh. It
is intentionally not TUI-only: manual page entry, a scheduler, Docker events,
command completion, jobs, and overflow recovery all submit work to the same
`RefreshManager`. A successful call confirms queue acceptance; a typed update
or `RefreshFailed` arrives through `Subscribe`.

### Page API inventory

Every page first subscribes with `Backend.Subscribe(ctx, Page..., filter)`.
Every page can request its base/list update through
`Backend.RequestRefresh(Page...)`. That common call is a **refresh**, not a
page-specific method. The table below shows the additional page methods and
which backend mechanism handles each one.

| Page | Refreshes → Refresh Manager → page Event Hub | Short commands → Command Executor | Long jobs → Job Executor | Session streams | Runtime refresh handler | Status |
| --- | --- | --- | --- | --- | --- | --- |
| Dashboard | Base summary refresh only | None | None | None | `Dashboard.RefreshHandler.ReadRefresh` | Implemented, read-only |
| Containers | Base list; `RequestDetails`; `RequestProcesses` | `Start`, `Stop`, `Restart`, `Pause`, `Unpause`, `Kill`, `Rename`, `Remove` | None | `Logs`, `Stats` | `Containers.API.ReadRefresh` | Implemented |
| Compose | Base project list; `RequestDetails` | `Start`, `Stop`, `Restart`, `Pause`, `Unpause`, `Scale` | `Up`, `Down`, `Pull`, `Build` | `Logs` | `Compose.API.ReadRefresh` | Implemented; interactive exec deferred |
| Images | Base list; `RequestDetails`; `RequestHistory` | `Tag`, `Remove`, guarded filtered `Prune` | `Pull` | None | `Images.API.ReadRefresh` | Implemented |
| Volumes | Base list; `RequestDetails`; `RequestAttachments` | `Create`, `Remove`, guarded label-filtered `Prune` | None | None | `Volumes.API.ReadRefresh` | Implemented |
| Networks | Base list; `RequestDetails`; `RequestConnections` | `Create`, `Remove`, guarded filtered `Prune`, `Connect`, `Disconnect` | None | None | `Networks.API.ReadRefresh` | Implemented |
| Events | Base bounded recent window; raw live observations selected through `Subscribe` filters | None | None | The page subscription is the live event feed | `Events.RefreshHandler.ReadRefresh` | Implemented, read-only |
| System | Base version/info; `RequestDiskUsage` | `PruneContainers`, `PruneImages`, `PruneVolumes`, `PruneNetworks`, guarded `PruneSystem` | None | None | `System.API.ReadRefresh` | Implemented |

**Refreshes** never return Docker data directly to the caller. The Refresh
Manager performs the read in backend-owned work and publishes a typed update to
the relevant page Event Hub. **Short commands** return a `CommandResult` after
their worker finishes, then request affected refreshes. **Jobs** return a
`Job` handle immediately after backend acceptance; progress and completion are
published to the page Event Hub, and `Job.Wait` provides the terminal result.
**Streams** are direct, session-owned readers and do not use either executor.

### Implemented page details

**Dashboard.** It has no domain-specific callable API yet. A TUI subscribes to
`PageDashboard` and calls `Backend.RequestRefresh(PageDashboard)`. The
Dashboard refresh handler performs its Engine/count/disk/history reads and
publishes `SummaryUpdated`.

**Containers.** The TUI calls the methods listed above through
`application.Containers()`. Targeted details/process methods request updates
on `PageContainers`; their results remain typed page events, filtered by full
`{kind, ID}` keys when needed. Short commands are submitted to the shared
executor. Logs and stats are session-owned streams.

**Compose.** The TUI calls the methods listed above through
`application.Compose()`. `Backend.RequestRefresh(PageCompose)` loads the active
project list; `RequestDetails(name)` loads one active project's definition,
services and containers. File-based scale/up/pull/build operations require an
explicit `ProjectSpec`; every config file must resolve beneath a configured
`ComposeRoots` directory. Short lifecycle operations use `CommandExecutor`,
logs are session-owned, and long operations use `JobExecutor`.

**Images.** `Backend.RequestRefresh(PageImages)` publishes the complete local
image list. `RequestDetails` and `RequestHistory` submit targeted refresh keys.
Tag, remove, and prune are short commands; prune rejects its zero value and
requires an explicit reviewed filter. `Pull` is a backend-owned job whose
conflict key uses the normalized image reference. Docker JSON pull messages
become job progress without exposing registry credentials. Image list filters
are session-local because the shared page refresh intentionally publishes one
complete list.

**Volumes.** `Backend.RequestRefresh(PageVolumes)` publishes the complete
volume list. `RequestDetails` and `RequestAttachments` are targeted refreshes.
Attachments are read authoritatively by listing all containers with Docker's
exact volume filter; no reverse index is stored. Create, remove, and prune are
short commands. Volume prune always requires at least one label filter; `All`
only makes named volumes matching that label eligible. Volume list filters are
session-local.

**Networks.** `Backend.RequestRefresh(PageNetworks)` publishes the complete
network list. `RequestDetails` publishes inspect/IPAM data and
`RequestConnections` publishes the connected-container view with assigned MAC,
IPv4 and IPv6 addresses. Create, remove, label/age-filtered prune, connect and
disconnect are short commands. Endpoint commands refresh the selected network,
the Containers list and the selected container details. Network list filters
remain session-local.

**Events.** The one runtime-owned Docker listener publishes every normalized
`DockerEventObserved` directly to `PageEvents` and stores it in bounded history.
`Backend.RequestRefresh(PageEvents)` publishes a newest-first `RecentUpdated`
window from that same history; the update itself is not inserted back into
history. Live subscriptions support resource, resource-ID, action and project
filters. Pause and clear are future Bubble Tea model actions only: they never
pause the process listener or erase shared history.

**System.** `Backend.RequestRefresh(PageSystem)` publishes Docker version and
host information; `RequestDiskUsage` publishes verbose aggregate and per-item
container, image, volume and build-cache usage. The four resource-specific
prunes require explicit narrowing filters and return a typed `PruneResult` in
addition to command metadata. Broad `PruneSystem` requires both bootstrap
`AllowSystemPrune` opt-in and the exact `SystemPruneConfirmation` token.

The current implemented packages are therefore:

```text
internal/backend/dashboard/
├── refresh.go
├── types.go
└── api_test.go

internal/backend/containers/
├── api.go
├── commands.go
├── docker.go
├── refresh.go
├── streams.go
├── types.go
├── errors.go
└── api_test.go

internal/backend/compose/
├── api.go
├── commands.go
├── docker.go
├── errors.go
├── jobs.go
├── paths.go
├── refresh.go
├── streams.go
├── types.go
└── api_test.go

internal/backend/images/
├── api.go
├── commands.go
├── docker.go
├── errors.go
├── jobs.go
├── refresh.go
├── types.go
└── api_test.go

internal/backend/volumes/
├── api.go
├── commands.go
├── docker.go
├── errors.go
├── refresh.go
├── types.go
└── api_test.go

internal/backend/networks/
├── api.go
├── commands.go
├── docker.go
├── errors.go
├── refresh.go
├── types.go
└── api_test.go

internal/backend/events/
├── refresh.go
├── types.go
└── refresh_test.go

internal/backend/system/
├── api.go
├── commands.go
├── docker.go
├── errors.go
├── refresh.go
├── types.go
└── api_test.go
```

Page APIs have no `Open`, `Close`, `closed`, `ensureOpen`, lifecycle mutex or
resource cache. Runtime components own process lifecycle. Container list,
details and process results are typed events on `PageContainers`. Details and
processes use the full `{kind, ID}` refresh key; subscriptions may filter by
type/key. They are deliberately not changed to direct return values.

## API categories

### Page and targeted refreshes

Backend-owned asynchronous reads followed by typed Event Hub publication.

### Short commands

Backend-owned after bounded queue acceptance. The TUI may wait for a structured
`CommandResult`, while resulting authoritative state arrives through page
events.

### Session-owned streams

Container logs/stats and Compose logs use the session/view context directly. Closing the
stream, leaving the view or disconnecting closes the Docker reader and stream
goroutine. Streams do not use the command queue or Refresh Manager.

Container exec is not part of this version. Compose exec is also deferred. A
future interactive terminal must be designed explicitly as a session-owned
bidirectional stream rather than being forced into a short command.

### Long-running jobs

Compose up/down/pull/build and image pull use one backend-owned `JobExecutor`,
separate from the short-command queue. It permits four active jobs by default
and rejects new work when full. There is no waiting job queue. Conflict keys
prevent two long jobs from mutating the same Compose project or pulling the
same normalized image reference concurrently.

Accepted work uses backend lifetime. The initiating TUI receives a `Job` handle
with best-effort progress, cancellation and reliable `Wait`. Progress and
terminal `JobFinished` events are broadcast on the job's owning page, so other
subscribed sessions observe the same operation. Completion requests all
declared Refresh Manager keys.

## Docker event ingestion

The backend owns one process-wide Docker event stream and reconnects after
transient failures without opening overlapping listeners.

For each normalized event it independently:

1. publishes the raw observation to `PageEvents`;
2. adds it to bounded Event Hub history;
3. requests Dashboard and affected resource-page refreshes.

Container `create` and `destroy` observations also request Volumes and Networks
refreshes because those actions can change their authoritative reverse views.
Container start/stop events do not do so because they do not change declared
mounts or endpoints. Network `connect` and `disconnect` observations also
request Containers because they change container inspect data. Event-triggered
work requests base page refreshes because the backend stores no session
selection. A TUI displaying targeted attachments/connections/details may
re-request its selected target after the base update.

`PageEvents` subscribers are never required for Dashboard or another page to
refresh. Docker events are hints to re-read authoritative state, not final UI
state.

## State ownership

| State | Owner | Lifetime |
| --- | --- | --- |
| Containers, images, volumes, networks and Engine facts | Docker daemon | Docker-managed |
| Rendered resource data | Each Bubble Tea model | Session/page lifetime |
| Subscription buffers | Event Hub | Subscription lifetime |
| Recent normalized Docker events | Event Hub history | Bounded process lifetime |
| Pending refresh keys | Refresh Manager | Until handled/shutdown |
| Accepted short commands | Command Executor | Until completion/timeout/shutdown |
| Active jobs and resource conflict keys | Job Executor | Until completion/cancellation/shutdown |
| Logs and stats | Initiating TUI session | View/session lifetime |

Operational queue and subscription state is necessary coordination state; it is
not a duplicate Docker resource cache.

## Destructive resource-operation safety

Page APIs never turn a zero-value prune request into a daemon-wide prune.
Images prune requires an explicit supported filter and rejects
`dangling=false`; Volumes prune requires at least one label filter; Networks
and stopped-container prune require at least one label or age filter. The APIs
construct Moby filters only after validation. Integration tests use unique
fixture identities and verify non-matching container, volume and network
sentinels survive filtered prune. Broad system prune is unavailable unless
bootstrap sets `AllowSystemPrune`, and still requires an exact confirmation
token. Its real-Docker test calls `RequireDedicatedDaemon` and therefore skips
on a normal developer daemon. Standard integration does not image-prune or pull
from the Internet; those boundaries remain unit-tested unless a
fixture-controlled registry or dedicated daemon is configured.

## Error model

Application errors have stable categories: invalid input, not found, conflict,
permission denied, daemon unavailable, timeout, canceled, stream closed,
unsupported and internal failure. Errors wrap useful causes and identify the
operation/resource without exposing secrets.

Refresh failures publish `RefreshFailed` on the affected page so sessions may
keep their existing local data visible and offer retry. Command callers receive
their structured error when still connected.

## Domain roadmap summary

### Dashboard

- Engine availability/version/host/capacity.
- Container, image, volume and network counts.
- Docker disk usage.
- Recent normalized Docker events.

### Containers

- List, filters, inspect/details and processes.
- Start, stop, restart, pause, unpause, kill, rename and remove.
- Session-owned logs and stats.
- Container exec is deferred from this version.

### Compose

- Discover active projects from Compose labels.
- Load definitions only from configured allowed directories.
- Project/service details and short lifecycle commands.
- Up, down, pull and build as jobs where appropriate.
- Logs and job progress.

### Images, Volumes and Networks

- Typed list/details APIs and safely scoped mutations.
- Pull/build progress as jobs where needed.
- No unrestricted prune without a dedicated test daemon and deliberate
  production confirmation.

### Events and System

- Live and bounded recent normalized Docker events.
- Docker version/info, detailed disk usage and safely scoped prune operations.

Detailed work remains ordered in `docs/TODO.md`.

## Session page lifecycle

1. Create a session-derived context for the active page.
2. Subscribe to that page Event Bus.
3. Call `RequestRefresh(page)`.
4. Render the typed event received by the subscription.
5. Replace session-local data on later scheduled/event/command updates.
6. On overflow, request recovery through the same Refresh Manager path.
7. Cancel the page context and close its subscription/streams when leaving.

This subscribe-before-request ordering avoids missing the initial result.

## Shutdown order

1. Reject new facade requests and stop scheduler/event producers.
2. Cancel and wait for Command Executor workers.
3. Cancel and wait for active Job Executor operations.
4. Cancel and wait for the Refresh Manager worker.
5. End SSH sessions so their subscriptions and streams close.
6. Close the Event Hub.
7. Close the shared Moby client exactly once.

Every owned goroutine and reader must terminate under race and leak checks.

## Testing strategy

Development is test-first. Hermetic tests cover routing, exact-key refresh
deduplication, typed publication, queue FIFO behavior, bounded concurrency,
overload, disconnect survival, timeouts, cancellation, page isolation, stream
closure and shutdown. Integration tests use a labeled Docker fixture and verify
the same exported contracts against a real daemon.

Required gates are formatting, `go vet`, unit tests, integration tests,
race-enabled variants, repeated concurrency-sensitive tests, deterministic
fixture cleanup and `git diff --check`. Exact commands and fixture safety rules
are in `docs/testing.md`.

## Explicitly removed architecture

The project does not use `StateStore`, snapshots, snapshot versions, stale-cache
metadata, page Loader objects, domain Service lifecycle objects, a separate
refresh catalog/coordinator/dispatcher stack, or synchronous session-owned page
refresh reads.
