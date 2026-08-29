# SSH-Native Docker TUI — Project Plan

This file is the permanent architectural source of truth for the project.

## Project goal

Build an SSH-accessible terminal UI for managing the Docker Engine on the same
server as the application. Multiple SSH sessions may use the backend at once.
The first version manages only that local Engine; it does not connect to remote
Docker hosts.

The backend is a modular Go monolith. It owns Docker and Compose integrations,
executes refreshes and commands, and broadcasts fresh results through the bus
for the relevant page. The TUI is a client of that backend and never imports
Docker SDK types directly.

## Architecture decisions

- Use one shared Moby client per application process.
- Use the Docker CLI object and Compose v2 Go packages in process; do not shell
  out to `docker` or `docker compose`.
- Docker Engine is the authoritative state store.
- Do not maintain a second general-purpose backend cache of resource snapshots.
- Each connected TUI session is an observer and owns the data it renders.
- Every refresh request performs a new authoritative read. There is no request
  coalescing or command/event debounce state.
- Each TUI tab has its own Event Bus. A session subscribes only to the bus for
  its active page and closes that subscription when it leaves the page.
- A process-wide scheduler may request periodic refreshes. Sessions do not run
  independent polling loops.
- Manual refreshes, Docker events, successful commands, completed jobs, startup,
  and scheduled refreshes use the same coordinator.
- Commands and refresh requests are direct backend calls. Page Event Buses carry
  observations/results, not commands requiring a synchronous answer.
- Logs, stats, progress, and a future interactive terminal are session-owned
  streams. They are neither cached nor globally broadcast.
- The SSH boundary is an application boundary, not an HTTP boundary. No REST API
  is required between the TUI and backend.

## SSH and TUI requirements

The final application remains a single Go process with one shared backend and
one independent TUI model per connected SSH session. The planned UI stack is:

- [Wish](https://github.com/charmbracelet/wish) for the SSH server, middleware,
  and session lifecycle;
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) for each session's
  model/update/view loop;
- [Bubbles](https://github.com/charmbracelet/bubbles) for reusable components
  such as tables, lists, text inputs, viewports, and spinners;
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) for terminal layout and
  styling.

Wish creates a separate Bubble Tea program for every SSH connection. Each
program owns its active tab, selected row, filters, modal state, terminal size,
rendered backend data, and session-owned streams. It talks only to the in-process
`Backend` interface; it never imports the Moby or Compose SDKs.

These libraries are architectural requirements, but they are not added to
`go.mod` until TUI implementation begins. The current milestone remains the
test-first backend and its Docker integration.

## System shape

The following diagram is the intended communication shape. It distinguishes the
two responsibilities that can otherwise look similar in code: the **Event Hub**
delivers page updates to sessions, while the **Backend** receives direct
commands/refresh requests, performs or requests work, and publishes the
resulting observations. An Event Bus never invokes Docker and never refreshes a
TUI by itself; a TUI redraws only after its own session receives an update.

![Backend request and Event Hub delivery paths](<Design/ChatGPT Image Aug 29, 2026, 05_05_15 PM.png>)

```text
INITIAL OR MANUAL PAGE REFRESH

SSH session                 Backend process                         Docker
-----------                 ---------------                         ------

1. Subscribe to page ─────> Containers Event Bus

2. Refresh(Containers) ───> Backend facade
                              │
                              v
                         Refresh catalog/coordinator
                              │ selects the Containers loader
                              v
                         Container loader ──> shared Moby client ──> Engine
                              ^                                        │
                              └────────── Docker response ─────────────┘
                              │
                              v
                         Containers Event Bus ── typed update ──> SSH session


CONTAINER COMMAND

SSH session                 Backend process                         Docker
-----------                 ---------------                         ------

Containers().Start(id) ───> Container service ─> shared Moby client ─> Engine
                                  │                                      │
                                  └──── after successful command <───────┘
                                                 │
                                                 v
                                        Refresh catalog/coordinator
                                                 │ fresh Docker read
                                                 v
                                        Containers Event Bus
                                                 │
                                                 v
                                      every session viewing Containers
```

The scheduler, Backend-owned Docker-event listener, and completed jobs enter at
the same refresh-catalog step. The coordinator handles only refresh reads and
Event Hub publication; domain services execute commands. Each session listens
only to its active page bus and owns the data it renders.

## Code organization

The runtime behavior above is deliberately separate from how the Go files are
organized. The codebase keeps these responsibilities distinct:

- `internal/backend` defines the stable application contracts shared by the
  TUI, page packages, and infrastructure: pages, events, refresh identities,
  errors, clocks, and the public `Backend` interface.
- A page package such as `internal/backend/dashboard` owns only its page DTOs
  and Docker-backed loader. Future tabs follow that same shape.
- The Event Hub owns page Event Buses, subscriptions, bounded delivery, and
  recent Docker-event history. It is a transport and history component, not a
  refresh executor and not a UI state store.
- The refresh catalog/coordinator owns the mapping from refresh work to a page
  loader and turns a completed authoritative load into a typed page update.
- The Backend owns the process-wide Docker-event listener alongside its direct
  command and refresh entry points. When the listener receives a normalized
  daemon event, the Backend publishes that raw observation to the Events page
  through the Event Hub and independently requests refreshes for Dashboard and
  other affected pages. Publishing to `PageEvents` is not how Dashboard is
  refreshed.
- The runtime Backend facade owns composition, lifecycle, direct routing of
  client calls, and Docker-event handling. It contains no rendered resource
  state or general resource cache. Future domain command services are direct
  Backend APIs; after success they request refreshes through the refresh
  catalog, whose typed results are published through the Event Hub.

The implemented packages are `backend/eventhub`, `backend/refresh`, and
`backend/runtime`. This separation is a code-organization rule, not a different
architecture. The public behavior, direct-refresh model, page buses, refresh
triggers, Docker-event mapping, and state ownership described in this plan
remain the same.

## State ownership

| State | Owner | Lifetime |
| --- | --- | --- |
| Containers, images, volumes, networks, and Engine facts | Docker Engine | Docker-managed |
| Currently rendered resource data | Each TUI session | Session/view lifetime |
| Active refresh contexts | Backend | Duration of each refresh |
| Recent normalized Docker events | Event Hub history | Bounded process lifetime |
| Subscription queues | Relevant page Event Bus | Subscription lifetime |
| Logs, stats, exec, and job progress | Initiating session | Stream/job lifetime |

The backend keeps only operational state needed for scheduling, cancellation,
subscriptions, and the recent-event ring buffer. This is not a resource
snapshot cache.

Consequences:

1. A successful refresh always emits a typed data update; there is no cached
   previous value to compare against.
2. An operationally failed refresh emits `RefreshFailed` on its page bus and
   returns an error. Caller cancellation returns directly without broadcasting
   a failure. Existing UI data remains visible because the session owns it.
3. A newly opened view subscribes first and then requests an initial refresh,
   preventing a subscribe/load race.
4. If a subscriber falls behind, it receives an overflow/resync indication and
   requests a fresh update for its visible view.

## Shared dependency ownership

Bootstrap creates and owns these process-wide dependencies:

- one Moby `client.Client`, configured with Docker environment variables and API
  version negotiation;
- one Docker CLI object initialized from the same connection settings;
- Compose services built from the Compose v2 Go packages;
- one independent Event Bus for each TUI page;
- one recent Docker-event buffer;
- one refresh coordinator;
- one refresh scheduler;
- one Docker event listener.

Domain services receive shared dependencies through constructors. They must not
create or close their own Moby clients. Application shutdown cancels producers,
stops scheduling, closes subscriptions and streams, waits for owned goroutines,
and closes the shared client exactly once.

## Backend components

### Backend facade

The facade is the only entry point used by a TUI session. It groups the domain
APIs and owns lifecycle:

```go
type Backend interface {
    Refresh(context.Context, Page) error
    Subscribe(context.Context, Page, EventFilter) (Subscription, error)
    Close(context.Context) error
}
```

Domain command and stream accessors are added when their slices freeze those
contracts. Dashboard is read-only, so it needs no redundant domain accessor: its
typed summary arrives through the shared page refresh and subscription methods.
The facade must retain one shared refresh path, one observation path, and
explicit lifecycle ownership.

The public `Refresh` operation requests the complete base refresh configured for
the page. TUI sessions do not construct internal refresh keys or supply trigger
reasons.

### Client communication example

When a session enters a page, it subscribes first and then requests that page's
initial data. Later scheduler ticks, Docker events, successful commands, and
completed jobs use internal refresh keys and publish through the same page bus:

```go
subscription, err := appBackend.Subscribe(ctx, backend.PageContainers, backend.EventFilter{})
if err != nil {
    return err
}
defer subscription.Close()

if err := appBackend.Refresh(ctx, backend.PageContainers); err != nil {
    return err
}

for event := range subscription.Events() {
    // Replace this session's Containers view with the typed payload.
}
```

### Internal refresh identity

A refresh key is backend infrastructure. It mirrors the operation that will run
instead of forcing every result into generic resource/view categories:

```go
type RefreshKind string

type RefreshKey struct {
    Kind RefreshKind
    ID   string // optional container ID, project name, volume name, etc.
}
```

Examples are `dashboard.summary`, `containers.list`, `container.details` with an
ID, `compose.project` with a project name, and `system.disk-usage`. The page
refresh configuration, domain services, scheduler, event listener, and jobs
construct these keys; TUI sessions do not memorize their strings.

`RefreshReason` records why work was requested: startup, scheduled, manual,
Docker event, command completion, job completion, or overflow recovery.

### Typed update events

The Event Bus transports complete results, not “something changed” pointers
back to a cache:

```go
type EventEnvelope struct {
    Sequence uint64
    Time     time.Time
    Key      RefreshKey
    Reason   RefreshReason
    Payload  EventPayload
}

type EventPayload interface {
    EventType() EventType
}
```

Payloads are domain-specific value types introduced with their slice, for
example `ContainerListUpdated`, `ContainerDetailsUpdated`,
`DashboardSummaryUpdated`, `DockerEventObserved`, `RefreshFailed`, and
`SubscriberOverflow`. Update payloads contain everything a view needs to
replace its local data. DTOs are treated as immutable after publication.

The envelope sequence provides page-bus-local ordering and diagnostics. It is
not a snapshot version and is not used as a cache key.

### Page Event Buses

The backend owns one physical in-process Event Bus for each tab: Dashboard,
Containers, Compose, Images, Volumes, Networks, Events, and System. Each bus
provides:

- typed filtering by event type and, where relevant, refresh key or resource ID;
- ordered delivery for each subscriber;
- independent bounded queues;
- cancellation and idempotent close;
- non-blocking publication so one slow session cannot block others;
- an explicit `SubscriberOverflow` payload when a queue fills; it replaces the
  oldest queued update, keeps that subscription usable, and tells the session
  which visible data to refresh.

Each bus broadcasts observations only for its page. It is not used as a hidden
RPC mechanism:
commands and refreshes remain direct calls so context cancellation, errors, and
results return to the initiating session predictably.

### Refresh catalog and coordinator

The catalog is a routing table that maps each `RefreshKind` to its page and
loader; it stores no loaded Docker data. The coordinator turns a keyed refresh
request into a Docker/Compose load and a typed update event. The complete
`RefreshKey`, including its optional ID, is passed to that loader.

For each request it:

1. validates the key and selects its page and loader;
2. executes a new load for that request;
3. lets caller cancellation or backend shutdown cancel the load;
4. publishes the successful typed payload to that page's subscribers;
5. publishes `RefreshFailed` for operational failures and returns the error.

Concurrent refreshes, including identical keys, execute independently. The
coordinator keeps no request map, debounce record, or loaded resource result.

### Refresh scheduler

The scheduler is process-wide. It submits configured base refreshes such as
container lists, image lists, project lists, dashboard summaries, disk usage,
and Engine information. It must never inspect every individual resource on a
timer.

Detailed windows refresh when opened, manually requested, or affected by a
known Docker event/command. The first implementation may refresh base topics
even with no subscribers; interest-aware scheduling is a later optimization.
All timing is injectable and deterministic in unit tests.

### Backend Docker-event listener and Event Hub history

The Backend owns one process-wide listener that consumes the Engine event
stream. It reconnects after transient stream failures and never opens more than
one stream at a time. Each event is normalized once, published by the Backend
to the Event Hub's Events-page bus, added to bounded Event Hub history, and
mapped to affected refresh keys. The Backend requests those refreshes through
the same coordinator used by every other trigger.

An Engine event is a hint to reload authoritative state, not itself the final UI
state. Each mapped refresh performs a new read; occasional duplicate reads are
accepted in exchange for straightforward behavior.

### Domain services

Domain services wrap the shared Docker/Compose dependencies. They translate SDK
types and errors into stable application DTOs, perform commands, and request the
affected refreshes after successful mutations. They do not publish fabricated
final state before Docker has been read again.

## Communication models

- **Request/response:** validation, commands, one-shot exec, and explicit
  refresh. The caller needs an immediate result or error.
- **Subscription:** typed resource updates and normalized Docker events shared
  with every interested session.
- **Stream:** logs, stats, exec output, and similar ordered session-owned data.
- **Job:** long-running Compose up/build/pull and image pull operations with
  progress, completion, and cancellation.

Successful commands return a `CommandResult` and then request the required
refresh keys. Successful jobs do the same on completion. Progress remains
private to the initiating session; final resource updates are broadcast.

Every blocking operation accepts `context.Context`. Cancellation must stop
waiting promptly and must not leak goroutines, readers, subscriptions, or
streams.

## Error model

Application errors have stable categories such as invalid input, not found,
conflict, permission denied, unavailable, unsupported, timeout, canceled,
subscriber overflow, and internal failure. Errors wrap useful causes and include
the operation and resource identity without exposing secrets.

`RefreshFailed` carries safe structured failure information so every observing
session can mark the relevant view stale or show a retry action. A later success
replaces that session-local state normally.

## Domain API roadmap

Each domain slice freezes only the DTOs, update events, commands, streams, jobs,
and refresh keys needed by that tab/window. Docker SDK types never cross the
domain boundary.

### Dashboard

- Engine summary: availability, version, host, OS, architecture, capacity, and
  Engine-reported system time. Docker does not expose a reliable daemon uptime
  field through the Engine API.
- Resource counts: running/stopped containers, images, volumes, and networks.
- Docker disk usage.
- Recent normalized events from the backend ring buffer.
- Cross-resource changes trigger affected dashboard refresh keys.

### Containers

- List, filter, inspect, and top/processes.
- Start, stop, restart, pause, unpause, kill, rename, and remove.
- One-shot exec.
- Session-owned logs and stats streams.
- Optional future interactive terminal stream.

### Compose

- Discover active projects from Compose labels.
- Load project definitions only from configured allowed directories.
- List projects and inspect services/containers.
- Start, stop, restart, pause, unpause, exec, and scale.
- Up, down, pull, and build as cancellable jobs where appropriate.
- Session-owned logs and job progress.

Docker cannot reliably discover every inactive Compose project on disk. The
first version combines configured allowed directories with active projects
found through labels.

### Images

- List, inspect, and history.
- Tag, remove, pull, and safely filtered prune.
- Image pull progress is session-owned; resulting list/disk-usage updates are
  broadcast.
- Push and advanced registry credential management are deferred.

### Volumes

- List, inspect, create, remove, and safely filtered prune.
- Show attached containers using Docker data.
- Browsing, backup, and restore are deferred because they require a helper
  container or direct filesystem access.

### Networks

- List, inspect, create, remove, and safely filtered prune.
- Show connected containers and assigned addresses.
- Connect and disconnect containers.
- Focus on local bridge networks; advanced drivers and Swarm overlays are
  deferred.

### Events

- Bounded recent normalized events.
- Live filtered subscriptions.
- Session-local pause, filtering, and clearing in the future TUI.
- Mapping from supported Docker event actions to affected refresh keys.

### System

- Docker client/server/API version and Engine information.
- Detailed disk usage.
- Safely scoped resource prune operations.
- Fully unrestricted system prune only in an explicitly dedicated test daemon
  and only through deliberate production confirmation.

## Session lifecycle

When a session opens a data-backed page:

1. construct a narrow subscription filter;
2. subscribe to the Event Bus for the active page;
3. request the page's initial refresh through `Refresh(ctx, page)`;
4. render the typed update received by the subscription;
5. replace local view data on later scheduled/event/command updates;
6. on overflow, request a recovery refresh for the active page;
7. cancel and close the subscription when the view/session ends.

This ordering avoids missing an update between the initial load and
subscription. If multiple sessions independently request the same refresh, each
request performs a read and each result is published to the active page
observers. Correctness is preferred over deduplicating these inexpensive calls.

## Testing strategy

Development is test-first. Production contracts remain in `internal/backend`;
reusable suites and real-Docker fixtures remain under `test`.

Hermetic unit tests cover page-bus isolation, Event Bus delivery and overflow,
independent concurrent refreshes, failures, deterministic scheduling,
event-buffer eviction, cancellation, lifecycle ownership, trigger mapping, and
leak-free shutdown.

The reusable integration conformance suite receives a production
`BackendFactory`, starts the real backend against a safe Docker fixture, and
verifies behavior through exported contracts. Its foundational observer test
subscribes two sessions, requests one refresh, and requires both to receive the
same typed authoritative result. Later tab slices add real resource creation,
commands, event-triggered updates, streams, jobs, and cleanup checks.

Tests must not receive unrestricted access to the fixture's Moby client. Narrow
arrangement/inspection helpers label every created resource with a unique run
ID, register explicit cleanup targets immediately, and remove only those
targets. Destructive prune tests require an explicitly dedicated daemon.

Required quality gates are formatting, `go vet`, unit tests, integration tests,
race-enabled variants, deterministic cleanup, and leak-free shutdown. Exact
commands and fixture rules are in `docs/testing.md`.

## Implementation order

1. Use the completed observer and Docker-event foundations from Slices 0 and
   0.5 for every domain.
2. Use the completed Dashboard slice as the page-package pattern.
3. Implement Containers, Compose, Images, Volumes, Networks, Events, and System
   in the order listed in `docs/TODO.md`.
4. Finish lifecycle, race, cleanup, and full-facade quality gates.

## Explicitly removed architecture

The project does not use `StateStore`, `Snapshot`, `SnapshotMeta`,
`SnapshotView`, snapshot versions, stale cache metadata, or
`GetSnapshotVersion`. Those types belonged to the earlier cache-based design
and were removed. The recent Docker-event ring buffer remains because it is
bounded event history, not a duplicate cache of Docker resource state.
