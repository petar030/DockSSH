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

The initial TUI supports Go 1.26.3 with Wish v2.0.3, Bubble Tea v2.0.9,
Bubbles v2.2.1 and Lip Gloss v2.0.6. Each Bubble Tea model owns its active page,
selected row, filters, modal state, terminal size, rendered backend data,
subscriptions and session-owned streams. TUI code uses the application Backend
API and never imports Docker SDK types.

Wish and Bubble Tea may use several goroutines per session. Architecture does
not rely on their exact goroutine count; it relies on context ownership.

## Planned TUI code layout and MVU ownership

The TUI is a separate layer from `internal/backend`. It owns only session-local
Bubble Tea state and rendering; it never imports Docker SDK types or performs
Docker calls itself.

```text
cmd/
└── ssh-docker-tui/
    └── main.go                    composition and application startup only

internal/
├── backend/                       Docker coordination and typed contracts
├── platform/
│   ├── docker/                    production Backend construction
│   └── ssh/                       Wish server and one Bubble Tea program per session
└── tui/
    ├── app.go                     root model: tabs, active page, session lifecycle
    ├── navigation.go              shared tab movement and quit behavior
    ├── jobs.go                    session tracker for jobs started by this TUI
    ├── styles.go                  shared Lip Gloss theme and layout helpers
    ├── ui/
    │   └── ui.go                  presentation-only sanitizing/truncation helpers
    ├── dashboard/
    │   ├── model.go
    │   ├── update.go
    │   └── view.go
    ├── containers/
    │   ├── model.go
    │   ├── update.go
    │   ├── view.go
    │   └── streams.go             only when logs/stats logic warrants it
    ├── compose/                   equivalent page-local MVU files
    ├── images/
    ├── volumes/
    ├── networks/
    ├── events/
    └── system/
```

`main.go` constructs the production Backend and SSH server but contains no page
behavior. The SSH layer creates one `tea.Program` and one root `tui.App` per
SSH session. All sessions share the same Backend instance.

`Backend.Close` is process ownership, not a per-session action. Only application
startup/shutdown code in `main.go` closes the shared Backend after the SSH
server has stopped accepting sessions. A page or disconnecting TUI closes only
its own subscription and stream handles.

The root `tui.App` owns session-wide state only: terminal dimensions, tab
navigation, the active page, and final session cleanup. Each page owns its own
MVU state: rendered DTOs, selected resource, filters, loading/error state,
subscription, and any active stream. Page packages define their own Bubble Tea
message types; only navigation, resize, and quit messages are shared.

Do not introduce a generic universal page model, generic DTO renderer, or a
separate shared subscription layer initially. Page event payloads and user
interactions are domain-specific. Small mechanical helpers may be extracted
later only after real duplication appears.

`tui/ui` is one deliberately small exception for safe presentation primitives.
It is a leaf package with no MVU or backend state, allowing both the root frame
and page packages to sanitize and truncate untrusted Docker text without an
import cycle.

### Page entry, updates, and exit

When a page becomes active, its page model:

1. creates a page-derived session context;
2. subscribes to its own Event Hub page bus;
3. requests its base page refresh;
4. waits for that subscription inside its own Bubble Tea command;
5. converts received typed Event Hub payloads into that page's message types;
6. updates only that session's page model in `Update`.

When the page changes or the session ends, the page cancels its context and
closes its subscription and active streams. It must not keep inactive page
subscriptions running.

For targeted views, the page requests the targeted backend refresh when a user
opens or focuses the selected resource. After a relevant base-page update, it
requests the targeted data again only if that selected view remains visible.
This is event-driven behavior, not periodic targeted polling.

### TUI streams and render cadence

Container logs, container stats, and Compose logs are session-owned streams.
The relevant page owns opening and closing them; backend page refreshes do not
control their lifetime.

- Logs redraw when a log message arrives; no fixed TUI refresh rate is needed.
- Stats retain the newest received sample and redraw at a bounded TUI render
  cadence, initially 250–500 ms, while the stats view is visible. This limits
  terminal redraws without polling Docker or changing the backend stream.
- Leaving the stream view cancels its page/view context or calls `Stream.Close`
  so the underlying reader and stream goroutine terminate.

## TUI implementation contract

This section turns the backend contracts into rules for the future Bubble Tea
implementation. It is deliberately more specific than a visual design: the
same behavior must remain correct with multiple SSH sessions, delayed Docker
responses, tab changes and terminal disconnects.

### MVU execution rules

Bubble Tea `Update` and `View` must remain fast and non-blocking. A page never
calls a backend method, waits on a channel, or performs formatting with
unbounded work directly inside either method. Instead, `Update` returns a
`tea.Cmd`; that command performs one backend call or receives one value and
returns a page-specific message.

Each page defines messages for the work it owns, for example:

```go
type subscriptionReadyMsg struct {
    generation uint64
    subscription backend.Subscription
}

type eventReceivedMsg struct {
    generation uint64
    event      backend.EventEnvelope
}

type commandFinishedMsg struct {
    generation uint64
    result     backend.CommandResult
    err        error
}
```

The exact names may differ, but the activation `generation` is required. It is
incremented whenever a page is entered again or its selected-resource view is
replaced. Messages from an old subscription, request or stream are ignored.
This prevents a late result from page A's old lifecycle overwriting page A
after the user has left and returned.

There is exactly one outstanding receive command per subscription or stream
channel. After handling a value, the page schedules the next receive. Starting
several receives for the same channel would make ordering and shutdown
nondeterministic.

`WindowSizeMsg`, keyboard input and backend messages may arrive in any order.
Models therefore support zero/unknown terminal size, no selected row and data
arriving before the first render. `View` is a pure rendering function and never
changes model state.

### Root and page state

The root model owns only state that survives tab changes:

- terminal width and height;
- active tab and global navigation/help state;
- session-wide notices and jobs initiated by this session;
- the session context and final cleanup.

A page model owns:

- its current full page DTO and the time/reason of its last update;
- identity-based selection, local sorting and local filters;
- base and targeted loading/error/stale states;
- modal/editor/confirmation state;
- its active subscription and stream handles;
- page activation and targeted-view generations.

Selection is stored by stable resource identity, never only by row index. After
a replacement list arrives, the page finds that identity in the new sorted and
filtered list. If it disappeared, the page clears its targeted panels and picks
the nearest valid row or no row. Backend DTOs are treated as immutable input;
sorting and filtering operate on a page-owned copy or index view.

Every data panel distinguishes at least these states:

| State | Rendering behavior |
| --- | --- |
| Initial loading | Empty panel plus spinner; no false zero values |
| Loaded | Current value plus optional last-updated indication |
| Refreshing | Keep current value visible and show unobtrusive activity |
| Stale/error | Keep the last successful value, mark it stale and show retry help |
| Empty | Successful authoritative result with no rows |
| Unavailable | A targeted operation is not valid for current resource state |

An initial failure has no old data and therefore renders an error/empty state.
A later `RefreshFailed` must not erase the last successful data.

### Subscription and refresh rules

Page activation follows one strict order: create a derived page context,
subscribe to the page bus, then request its base refresh. A successful
`RequestRefresh` only means the request was accepted; it contains no Docker
data. The page becomes current only when its typed update event arrives.

Only the active tab holds a subscription. On tab exit the page cancels its
context, closes the subscription and streams, and invalidates its generation.
On re-entry it subscribes and requests a fresh base result again. The model may
retain its old DTO for a quick first render, but that DTO is marked stale until
the new lifecycle receives an authoritative update.

Targeted updates use the same shared page bus. Consequently another SSH
session can request details for resource B while this session displays resource
A. The page must check the event's full refresh key and payload identity and
ignore B; it must also ignore an A response that belongs to an older selection
generation. A page requests selected details when that view is opened/focused
and again after a relevant base update if the selected view is still visible.
There is no targeted timer and no backend state for "currently selected" data.

`SubscriberOverflow` means at least one event for this subscription was lost;
the subscription itself is still usable. The page marks its data stale and
requests its base refresh plus every targeted panel that is currently visible.
It does not automatically create a second subscription. Recovery requests can
themselves fail when the Refresh Manager is closed or full, so the UI offers a
manual retry rather than entering a tight retry loop.

An unexpectedly closed subscription is a recoverable page error while the
application is otherwise running. Re-entering the page may resubscribe. During
normal page cancellation or application shutdown, channel closure is not shown
as an error.

Envelope sequence numbers are monotonic only within one page bus and reset when
the application process restarts. They are useful for ordering/debugging, not
as durable IDs or a replay cursor. `DroppedSequence` reports loss but the Event
Hub cannot replay that sequence. The refresh reason is diagnostic metadata;
deduplicated requests retain the newest reason, so rendering must not depend on
observing every intermediate reason.

Docker events are refresh hints, not authoritative page data. The backend
publishes raw observations to the Events page and independently asks the
Refresh Manager for affected base pages. Other pages subscribe only to their
own typed page updates; they never need a `PageEvents` subscription.

### Command behavior in the TUI

Short commands are invoked in a Bubble Tea command. The caller's context
controls submission and waiting, but after queue acceptance a backend worker
owns execution. If the user changes tabs or disconnects, the accepted Docker
operation can still finish and request refreshes.

A successful `CommandResult` means the Docker callback completed and affected
refreshes were requested. It is useful for a notice and operation metadata, but
it is not the authoritative new resource state. Pages do not optimistically
change Docker state and do not wait forever for an event correlated by
`OperationID`; the next typed page update is authoritative. The executor also
requests refreshes after an attempted mutation returns an ambiguous Docker
error because Docker state may nevertheless have changed.

While this session is waiting for a command, disable the identical action for
the same resource and show progress. Do not assume global exclusion: another
session or Docker client may issue a conflicting command. Queue-full/conflict
errors are retryable UI errors, not crashes.

`CommandResult` contains `OperationID`, affected resource identities and the
refresh keys requested by the executor. `Asynchronous` is currently false for
all short commands and is reserved contract space; jobs use `Job` instead. A
non-zero result can accompany an error. In particular, the executor joins the
Docker callback error with any post-command refresh-submission error. Therefore
the TUI preserves useful result metadata, checks error codes with
`backend.HasErrorCode`/`errors.Is` rather than matching strings, and describes
the final state as uncertain until a typed refresh succeeds.

Map stable backend error codes consistently:

| Error code | TUI treatment |
| --- | --- |
| `invalid_input` | Keep the editor/modal open and identify the invalid field |
| `not_found` | Notify the user, clear vanished selection, request the base page |
| `conflict` | Explain current-state/busy conflict and allow retry after refresh |
| `permission_denied` | Explain daemon/application policy; do not retry automatically |
| `daemon_unavailable` | Mark affected data stale and provide manual retry |
| `timeout` | Report unknown/unfinished outcome and wait for authoritative refresh |
| `canceled` | Usually silent on navigation; visible when explicitly canceled |
| `stream_closed` | End the stream view cleanly unless accompanied by a cause |
| `unsupported` | Disable or hide the unavailable operation after reporting it |
| `internal` | Preserve data and show a concise diagnostic without secrets |

Joined errors can contain more than one stable code. The initial TUI should
prefer the most actionable/safety-relevant presentation (permission denied,
daemon unavailable, timeout, conflict, then internal), while still treating
the resource view as stale. The current backend does not separately expose
"Docker operation error" and "refresh submission error"; richer wording would
require a future structured command-outcome contract.

Destructive actions require a focused confirmation modal. This includes
resource removal, prune, Compose down with volume removal, and broad system
prune. Kill and force removal require visibly stronger wording. The broad
system prune UI must require the exact backend confirmation token and must not
silently enable volume deletion. Backend validation remains the final safety
boundary.

### Long-running job behavior

Compose up/down/pull/build and image pull return a `Job` after acceptance. The
executor allows a bounded number of active jobs (four by default), has no
waiting queue, and rejects conflicting work for the same normalized project or
image key.

Jobs started by this TUI are tracked at the root/session level, not in the
active page model. A session-derived context continuously receives their
best-effort progress and calls `Wait`; switching tabs must not abandon them.
`JobFinished`/`Wait`, not a progress status string, is the terminal truth.
Progress status text is extensible Docker/Compose data and must tolerate values
other than `started`, `loading`, `running`, `completed`, `failed` and
`canceled`.

The direct handle is the primary source for a job started by this session. To
avoid displaying every update twice, page Event Hub progress with a matching
locally tracked job ID is ignored or merged by sequence. Page events still let
an active page show jobs started by another session. A cancel action is an
explicit mutation of shared backend work and therefore requires confirmation;
leaving the page does not cancel a job.

Progress is allowed to drop under load, so the UI shows the newest known status
rather than relying on every intermediate step. `Job.Wait` is the reliable
terminal result. Completion refreshes affected pages, and their typed data—not
the job's progress text—determines final Docker state.

After terminal completion, `JobResult` contains the job ID, affected resources,
requested refresh keys and completion time even when the job itself failed. A
TUI wait context that expires before completion instead returns no terminal
result and does not stop the job. `JobFinished.Err == nil` means successful
terminal execution; a canceled/failed job still triggers its declared
authoritative refreshes. Capacity and conflict rejection happens before a
`Job` handle exists.

Known first-version limitation: the backend has no persistent job registry or
job history. A disconnected initiating session loses its handle, and a session
that was not subscribed to the owning page may miss its progress/completion
events. The Docker operation still runs and the resource page is authoritative
when next opened, but there is no job reattachment UI.

### Stream behavior

Container logs, container stats and Compose logs bypass the executors because
they are session-owned reads. Open them using a view-derived context. Every
stream message includes a local stream ID/generation; values arriving after a
selection or view change are discarded. `Close` is idempotent and is always
called when the view closes, the page changes or the SSH session ends.

Log views use a bounded client-side ring buffer (initially about 2,000 rendered
lines, configurable later) so a busy container cannot consume unbounded
memory. Docker log entries may be chunks rather than complete lines, so the
page preserves an incomplete trailing fragment and splits complete lines before
inserting them. Follow mode redraws on arrival; non-follow mode ends normally
when the stream closes.

Stats keep only the latest sample and render it at a 250–500 ms cadence while
visible. That tick is a terminal redraw limit, not a Docker polling interval.
When no new sample exists, no additional historical samples are invented.

The stream `Done` channel supplies one terminal error and closes. Normal view
cancellation is not presented as failure. An unexpected error remains in the
stream panel with retry/reopen help. Opening processes or stats for a stopped
container may produce a Docker conflict; this is a nonfatal unavailable state,
not a page-wide failure.

### Typed page event routing

The TUI switches on concrete payload types after the envelope has arrived on
the correct page subscription. Unknown future payload types are ignored and
optionally logged; they never crash the session.

| Bus | Base payload | Targeted payloads | Other payloads the page handles |
| --- | --- | --- | --- |
| Dashboard | `dashboard.SummaryUpdated` | None | `RefreshFailed`, `SubscriberOverflow` |
| Containers | `containers.ListUpdated` | `DetailsUpdated`, `ProcessesUpdated` keyed by container ID | `RefreshFailed`, `SubscriberOverflow` |
| Compose | `compose.ProjectsUpdated` | `ProjectUpdated` keyed by project name | `JobProgressed`, `JobFinished`, failures/overflow |
| Images | `images.ListUpdated` | `DetailsUpdated`, `HistoryUpdated` keyed by image ID | `JobProgressed`, `JobFinished`, failures/overflow |
| Volumes | `volumes.ListUpdated` | `DetailsUpdated`, `AttachmentsUpdated` keyed by volume name | `RefreshFailed`, `SubscriberOverflow` |
| Networks | `networks.ListUpdated` | `DetailsUpdated`, `ConnectionsUpdated` keyed by network ID | `RefreshFailed`, `SubscriberOverflow` |
| Events | `events.RecentUpdated` | None | `DockerEventObserved`, failures/overflow |
| System | `system.InfoUpdated` | `DiskUsageUpdated` | `RefreshFailed`, `SubscriberOverflow` |

Filters are copied when `Subscribe` is called. Changing a live Events-page
filter therefore requires a controlled resubscription. Subscribe the
replacement before closing the old subscription where possible, then request
the bounded recent window to reconcile the handover; duplicate observations
may be removed by their normalized fields/time for display purposes.

### Backend call return semantics for page authors

| Call category | Immediate/direct return | Later asynchronous observation |
| --- | --- | --- |
| `Subscribe` | A subscription handle or validation/closed/canceled error | Ordered matching envelopes until context cancellation or `Close` |
| Base `RequestRefresh` | Acceptance, deduplication, unsupported, full or closed error; never data | Typed base payload or keyed `RefreshFailed` on the page bus |
| Targeted `RequestDetails`-style call | The same refresh-request acceptance semantics; never data | Typed payload/`RefreshFailed` with the requested full key |
| Short command | `CommandResult` plus optional error after the TUI's wait ends | Affected typed page updates are requested independently |
| Job start | `Job` handle, or validation/capacity/conflict/closed error | Best-effort handle/page progress, reliable `Wait`, page `JobFinished`, then affected refreshes |
| Stream open | A stream handle or immediate validation/open error where the implementation opens eagerly | Ordered values plus exactly one terminal `Done` outcome; deferred open failures also arrive here |
| System prune | `PruneResult{Command, Report}` on full success; command metadata may accompany error | Affected resource/Dashboard/System refreshes |

Request acceptance is not Docker success. Conversely, canceling only a TUI
wait after backend acceptance is not Docker cancellation. Page code must keep
those concepts separate in spinner, notice and error wording.

### Page-specific UI requirements and backend edge cases

| Page | Required TUI behavior and special cases |
| --- | --- |
| Dashboard | Subscribe/request `PageDashboard`; render engine, counts, disk usage and bounded recent Docker events as one summary. Recent-event count is process-lifetime backend history, not all daemon history. No commands or streams. |
| Containers | Base list plus keyed details/process events. Treat Docker `State`, `Status` and `Health` as open-ended strings. Processes/stats can be unavailable for stopped containers. Show environment variable names and values in details. Commands: start, stop, restart, pause, unpause, kill, rename, remove. Logs and stats are view-owned streams. |
| Compose | Active project discovery comes from Docker labels; file-based operations require an explicit project spec whose Compose files lie under configured roots. Treat project/service/container status and health as open-ended. Short commands are start, stop, restart, pause, unpause and scale; up, down, pull and build are jobs; logs are streams. Interactive exec is deferred. |
| Images | Base list plus keyed details/history. Filters are session-local. Tag/remove/prune are commands and pull is a job. Pull platform must be `os/arch[/variant]`. Prune requires an explicit safe filter and rejects `dangling=false`. |
| Volumes | Base list plus keyed details/attachments. Render `UsageKnown == false` as unknown—not zero—and show list warnings without discarding valid rows. Create/remove/prune are commands. Prune requires labels; an in-use removal is a conflict. |
| Networks | Base list plus keyed details/connections and assigned addresses. Create/remove/prune/connect/disconnect are commands. Network prune requires age or labels. Docker 29 may report removal with active endpoints as permission denied; the backend deliberately translates this case to `conflict`, so advise disconnecting endpoints instead of showing an authorization failure. |
| Events | Subscribe to `PageEvents` for both `RecentUpdated` and raw `DockerEventObserved`; backend filters may narrow resource/action/project. The live feed remains active while this tab is open; v1 has no pause action or paused-event buffer. Clear erases only displayed session rows, never backend history. History is bounded and exists only for this application process. |
| System | Base refresh returns version/info; detailed disk usage is a targeted event. Resource-specific prune calls return a `PruneResult` containing the report as well as command metadata. Broad prune may be disabled at bootstrap and requires the exact confirmation token; volumes remain a separate opt-in. Never render unknown usage as a confirmed zero. |

All list searching, sorting and filtering is session-local unless an API contract
explicitly states otherwise. A page may receive data caused by another TUI,
the scheduler, a Docker event, a command or a job; the rendering path is the
same regardless of refresh reason.

### Terminal safety, responsive layout and accessibility

Docker-controlled names, labels, log text, event attributes and error causes
are untrusted terminal input. Strip or visibly escape control sequences before
rendering while preserving intentional line breaks/tabs in log processing.
Never allow Docker output to emit terminal escape commands. Avoid showing
registry credentials or sensitive error data. V1 deliberately shows Docker
container/image environment values; masking may be added later if required.

The root view reserves header/tab, content and footer/status areas, then gives
the remaining dimensions to the active page. Every page supports a documented
minimum size and renders a compact "terminal too small" view instead of
panicking or producing negative dimensions. Long IDs and paths are truncated
visually without modifying their stored values. Color is supplementary: state
and errors remain understandable from text/icons when color is unavailable.

Global navigation is disabled while a text editor or confirmation modal owns
focus, except for an explicit cancel/quit sequence. Key bindings and help text
come from one shared navigation definition so displayed help cannot drift from
actual behavior.

### TUI verification rules

Tests primarily exercise pure page `Update`/`View` behavior with fake
frontend-facing backend APIs and controlled messages. They must cover:

- subscribe-before-refresh ordering and cleanup on tab/session exit;
- stale-generation, mismatched-target and out-of-order message rejection;
- initial/loading/loaded/empty/stale/error transitions;
- overflow recovery without a retry loop;
- identity-based selection across replacement, sorting and filtering;
- command queue rejection, backend error mapping and authoritative refresh;
- job progress loss, reliable terminal completion, tab switching and cancel;
- stream closure, late stream messages, partial log chunks and bounded history;
- zero, minimum, narrow and wide terminal dimensions;
- terminal-control sanitization and environment-value rendering;
- destructive confirmation flows and exact system-prune confirmation;
- two simulated sessions receiving shared updates without sharing UI state.

Broad full-screen golden files are optional because they are brittle. Prefer
focused assertions on state transitions and key rendered regions, with a small
number of stable visual snapshots for the root layout. SSH integration tests
verify that two sessions get independent programs, disconnect cleanup is
complete and backend shutdown ends both sessions. Manual tests run first in a
loopback SSH session, then in two concurrent loopback sessions against a
disposable Docker fixture.

### Initial layout and key map

The initial full layout targets terminals of at least 80 columns by 24 rows.
Smaller terminals show a resize message and only the quit/help controls. The
root layout uses one title/connection row, one horizontal tab row, the remaining
space for page content, and two footer rows for status and contextual help.

At 120 columns and wider, list/detail pages use a roughly 40/60 horizontal
split. Between 80 and 119 columns they show one primary panel at a time and use
`enter`/`esc` to move between list and details. Dashboard cards use two columns
when they fit and one column otherwise. Modals are centered within the content
area and never exceed the terminal bounds.

The initial global key map is deliberately small and may evolve with the page
implementations:

| Keys | Action |
| --- | --- |
| `[` / `]`, `1`–`8` | Previous/next tab or direct tab selection |
| `up`/`down`, `j`/`k` | Move within the focused list |
| `tab` / `shift+tab` | Move focus between visible panels or form fields |
| `enter` | Open details, accept a non-destructive choice, or activate focus |
| `esc` | Close/back/cancel the current view or modal |
| `f` | Focus the current page's find/filter input |
| `r` | Request the active page's authoritative refresh |
| `a` | Open the selected resource's actions menu where one exists |
| `?` | Toggle contextual help |
| `q` | Quit from normal navigation mode |
| `ctrl+c` | End the TUI session from any mode |

Text editors and confirmation dialogs consume ordinary character keys before
global navigation. Every page footer displays only currently valid bindings;
page-specific shortcuts are introduced with that page rather than reserved in
advance.

### Resolved TUI/SSH product decisions

- Client authentication is deferred. Until it exists, the SSH server binds to
  loopback by default and must not silently expose Docker control on a public
  interface. A persistent generated server host key is still required because
  host identity is separate from client authentication.
- There is no separate direct local-terminal application mode. Development and
  manual testing use the same Wish/SSH path as production, initially through
  loopback.
- The initial responsive layout, 80x24 minimum and provisional key map are
  defined above and may be refined during page implementation.
- Container and image inspect environment entries show both names and values.
  Optional masking or reveal controls can be considered later.
- The Events page has no pause action and no paused-event buffer. It continually
  consumes live events while active; clear remains session-local.
- Locally initiated jobs appear in a compact global footer summary. Selecting
  that summary opens a session-level job overlay; backend reattachment/history
  remains outside v1.

Two existing backend return-contract limitations do not block the first TUI
slices but must be revisited before polishing command/System messaging:

- short-command errors join Docker execution and refresh-submission failures,
  so the UI cannot always state which phase failed;
- System prune returns command metadata but omits the prune report whenever the
  joined executor error is non-nil, including the rare case where pruning
  succeeded but a subsequent refresh request failed.

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

Every TUI page uses the refresh and subscription portion of the common facade:

```go
type Backend interface {
    RequestRefresh(Page) error
    Subscribe(context.Context, Page, EventFilter) (Subscription, error)
    Close(context.Context) error
}
```

`Close` appears on the shared interface because the application composition
root owns shutdown. An individual TUI session must never call it. Domain page
accessors provide the additional operations listed below.

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
filters. The TUI's clear action affects only that session's displayed rows; it
never pauses the process listener or erases shared history. V1 has no pause
action.

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
selection.

Targeted data is a TUI responsibility: when a user opens or focuses selected
details, processes, history, attachments, connections, project details, or
detailed disk usage, that TUI requests the corresponding targeted refresh. If
the selected view remains visible after a relevant base-page update, the TUI
requests it again. There is no periodic targeted-refresh policy or TUI polling:
commands and Docker events cause the relevant base update, and the backend
never stores a session's selected resource. Periodic backend policies refresh
only configured base page data.

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
