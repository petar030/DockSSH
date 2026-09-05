 TODO list for development

The permanent architecture is in [`plan.md`](plan.md), detailed backend
documentation is in [`backend/`](backend/README.md), and test procedures are in
[`testing.md`](testing.md).

- [x] Setup Go project
- [x] Setup local git environment
- [x] Develop a testing framework

# Backend API

## Slice 0: Shared observer-based backend foundation

- [x] Replace snapshot contracts with flat refresh keys and typed full-result update events
- [x] Remove `StateStore`, snapshot versions, stale-cache metadata, and snapshot query methods
- [x] Implement a separate Event Bus per page with filtering, ordering, bounded delivery, cancellation, and overflow recovery
- [x] Implement one backend-owned Refresh Manager with page routing, exact-key pending deduplication, failure publication, timeouts, and cancellation
- [x] Route explicit, scheduled, command, and Docker-event refreshes through the same bounded Refresh Manager
- [x] Implement the bounded backend-owned command queue and fixed worker pool
- [x] Keep and adapt the deterministic fake clock, scheduler, Docker adapter, and synchronization utilities
- [x] Keep the bounded recent Docker-event buffer as event history, not resource state
- [x] Update application bootstrap and preserve single ownership of shared Docker/Compose dependencies
- [x] Verify two observers on one page receive its authoritative refresh result without cross-page leakage
- [x] Verify page-lifetime subscriptions, subscribe-before-refresh startup, failure delivery, scheduler behavior, and overflow resync
- [x] Pass formatting, vet, unit, integration, race, shutdown, ownership, and repeated stability checks
- [x] Remove the temporary refactor checklist after verification

## Slice 0.5: Docker event ingestion foundation

- [x] Test and implement one process-wide Docker event listener using the shared Moby client
- [x] Normalize, buffer, and publish daemon events without blocking the Docker event stream
- [x] Map foundational Docker actions to the affected page refreshes and verify clean cancellation/reconnection

## Slice 1: Dashboard tab

- [x] Define Dashboard summary DTOs and page event contracts
- [x] Test and implement the Engine summary window
- [x] Test and implement the resource-count summary window
- [x] Test and implement the Docker disk-usage window
- [x] Test and implement recent-events data shown on the Dashboard
- [x] Verify Dashboard refreshes after relevant cross-resource changes

## Slice 2: Containers tab

- [x] Define container DTOs, filters, options, streams, and API contracts
- [x] Test and implement the container list window
- [x] Test and implement the container details window
- [x] Test and implement the container processes window
- [x] Test and implement start, stop, restart, pause, unpause, kill, rename, and remove actions
- [x] Test and implement container logs and stats windows
- [x] Verify command-triggered and Docker-event-triggered container refreshes

## Slice 3: Compose tab

- [x] Define Compose project DTOs, options, streams, jobs, and API contracts
- [x] Test and implement the Compose project list window
- [x] Test and implement project loading and validation from allowed directories
- [x] Test and implement the Compose project details and services windows
- [x] Test and implement start, stop, restart, pause, unpause, and scale actions
- [ ] Design interactive Compose exec as an explicit bidirectional session stream; deferred because it does not fit short commands or one-way logs
- [x] Test and implement up, down, pull, and build jobs with progress
- [x] Test and implement Compose logs
- [x] Verify project refreshes from Compose jobs and labeled Docker events

## Slice 4: Images tab

- [x] Define image DTOs, filters, options, jobs, and API contracts
- [x] Test and implement the image list window
- [x] Test and implement image details and history windows
- [x] Test and implement tag, remove, and safely filtered prune actions
- [x] Test and implement image pull jobs and progress
- [x] Verify image and disk-usage refreshes after image changes

## Slice 5: Volumes tab

- [x] Define volume DTOs, filters, options, and API contracts
- [x] Test and implement the volume list window
- [x] Test and implement volume details and attached-containers windows
- [x] Test and implement create, remove, and safely filtered prune actions
- [x] Test conflict handling when a volume is in use
- [x] Verify volume, container, and disk-usage refreshes

## Slice 6: Networks tab

- [x] Define network DTOs, filters, options, and API contracts
- [x] Test and implement the network list window
- [x] Test and implement network details and connected-containers windows
- [x] Test and implement create, remove, and safely filtered prune actions
- [x] Test and implement container connect and disconnect actions
- [x] Verify assigned addresses and affected container/network refreshes

## Slice 7: Events tab

- [x] Define normalized Docker event DTOs and Events API contracts
- [x] Test and implement the recent-events window
- [x] Test and implement live filtered event subscriptions
- [x] Complete normalization and refresh mapping for all Docker actions supported by the Events tab
- [x] Verify slow subscribers cannot block Docker events or other sessions
- [x] Keep Events display control session-local; the future TUI may clear its
      rows without pausing ingestion or altering shared history

## Slice 8: System tab

- [x] Define System DTOs, prune reports, options, and API contracts
- [x] Test and implement Docker version and information windows
- [x] Test and implement the detailed disk-usage window
- [x] Test and implement safely scoped container, image, volume, and network prune actions
- [x] Test system prune only against an explicitly dedicated Docker daemon
- [x] Verify all affected Dashboard and resource updates are broadcast after prune

## Final backend quality gates

- [x] Test and implement graceful shutdown during active refreshes, subscriptions, streams, and jobs
- [x] Verify cancellation and deterministic closure of every stream and job
- [x] Verify no goroutines, readers, subscriptions, streams, jobs, or Docker connections leak
- [x] Pass all unit and integration tests with the race detector
- [x] Confirm integration cleanup leaves no test resources behind
- [x] Complete the top-level Backend facade and full production conformance suite

# TUI implementation

Implement the terminal interface incrementally. Each slice must leave a usable,
tested application and must use the existing backend contracts; page models do
not import Docker SDK packages or introduce a second resource cache. The TUI
contract and edge cases are specified in [`plan.md`](plan.md#tui-implementation-contract).

## TUI Slice 0: Session shell and shared behavior

- [x] Resolve and document the initial SSH exposure, host-key, layout, key-map,
      Events-feed and secret-display choices in `plan.md`
- [x] Add compatible Wish, Bubble Tea, Bubbles and Lip Gloss dependencies and
      record the supported Go/library versions
- [x] Create the root MVU model with header, tabs, content, footer/status,
      responsive sizing, help and quit behavior
- [x] Define page activation/deactivation without a universal generic page
      model; page packages own their message and model types
- [x] Implement activation generations for the Dashboard lifecycle and retain
      the same mandatory rule for later targeted views, commands and streams
- [x] Implement shared page status plus Dashboard loading/stale/error
      presentation without coupling page DTOs together
- [x] Implement terminal-control sanitization, safe truncation and consistent
      rendering that does not depend on color alone
- [x] Add the Wish SSH adapter with one independent Bubble Tea program per
      session, one shared process-wide Backend, loopback-only unauthenticated
      v1 access and a persistent generated server host key
- [x] Test resize-before-data, very small terminals, tab/help focus, independent
      session models, disconnect cleanup and graceful backend/server shutdown;
      manually verify two real SSH sessions

## TUI Slice 1: Dashboard page

- [x] Implement the Dashboard page-local model, messages, update and view
- [x] Subscribe before requesting the initial Dashboard refresh and close the
      subscription whenever the tab becomes inactive
- [x] Render Engine identity/availability, resource counts, disk usage and the
      bounded recent Docker-event summary
- [x] Distinguish initial loading, empty, refreshing and stale/error states while
      preserving the last successful summary after `RefreshFailed`
- [x] Recover from `SubscriberOverflow` through one authoritative base refresh
- [x] Test scheduled, manual, Docker-event and cross-session Dashboard updates
- [x] Manually verify the Dashboard through one and two loopback SSH sessions

## TUI Slice 2: Containers page

- [x] Decide whether the documented joined command/refresh error limitation
      needs a structured backend outcome before finalizing command messaging
- [x] Implement the container list, session-local search/state/label filters,
      sorting and identity-based selection
- [x] Keep the list and selected details in one responsive
      workspace; request details immediately whenever selection changes
- [x] Implement keyed details and processes panels; ignore updates for another
      session's selected container and stale selection generations
- [x] Show container environment variable names and values in v1, and render
      unknown Docker status/health strings safely
- [x] Implement start, stop, restart, pause, unpause, kill, rename and remove via
      asynchronous Bubble Tea commands and the existing Command Executor API
- [x] Expose ordinary container commands in the page-specific footer rather
      than a separate Actions view or duplicate content panel
- [x] Add confirmations for kill, remove, force and volume-removal options; map
      all stable backend error codes to recoverable UI states
- [x] Implement logs with partial-line assembly, terminal sanitization and a
      bounded line ring; implement stats with latest-sample storage and bounded
      250–500 ms rendering
- [x] Treat stopped-container processes/stats conflicts as targeted-panel
      unavailability rather than whole-page failure
- [x] Test command wait cancellation, authoritative post-command updates,
      mismatched keyed events, vanished selection, stream closure and overflow
- [x] Manually verify all commands, logs and stats on disposable running and
      stopped containers without modifying unrelated resources

## TUI Slice 3: Compose page (Going to be implemented after Slice 8)

- [x] Add the root/session job tracker before the first job-owning page; keep
      accepted jobs alive and observable across tab changes
- [x] Implement active-project list, session-local filtering and identity-based
      selection
- [x] Implement keyed project details/services/containers and tolerate unknown
      Compose/Docker status and health strings
- [x] Implement project-spec input with clear validation for project name,
      services, profiles and Compose files beneath configured roots
- [x] Implement start, stop, restart, pause, unpause and scale as short commands
- [x] Implement up, down, pull and build as jobs with best-effort progress,
      reliable `Wait`, conflict/capacity errors and explicit cancellation
- [x] Merge or suppress duplicate progress received from a local `Job` handle
      and the page Event Hub by job ID
- [x] Implement Compose logs as a bounded, sanitized, page-owned stream
- [x] Confirm destructive down/volume-removal actions and document that job
      reattachment/history after SSH disconnect is unavailable in v1
- [x] Test tab switching during jobs, progress loss, completion refreshes,
      per-project conflict, job capacity and disconnect survival
- [x] Manually verify commands/jobs/logs using Compose files inside a disposable
      allowed root; leave interactive Compose exec deferred

## Slice 3.5: Compose configuration editor and creator (after TUI Slice 3)

This is a separate follow-up slice. It must not be folded into Slice 3 because
it adds backend behavior for creating, editing, validating, storing, and then
running Compose configuration files.

- [ ] Add a Compose configuration editor using Bubble Tea's text-area
      component, with YAML text editing and a preconfigured starter template
- [ ] Add client-side YAML/syntax validation before any backend request is sent;
      keep the editor open and show a bounded, sanitized validation error
- [ ] Add backend contracts and implementation for safely creating a new
      Compose file and editing an existing file beneath configured Compose
      roots, using `<ComposeRoot>/<project>/compose.yaml` by default in the
      first iteration, with path and write validation
- [ ] Add an explicit save/create request separate from `Up`, so the backend
      never receives partially edited or syntactically invalid content
- [ ] Run the saved configuration through the existing Compose `ProjectSpec`
      and job pipeline, preserving the existing path-safety and job rules
- [ ] Test template creation, valid and invalid YAML, path traversal,
      permission/write failures, replacement editing, concurrent sessions, and
      running a newly created project
- [ ] Manually verify creation and editing only with disposable Compose files
      inside an allowed root; leave unrelated files unchanged

## TUI Slice 4: Images page

- [x] Implement image list, session-local filters, identity selection, details
      and history panels
- [x] Show image environment variable names but always mask their values in v1
- [x] Implement tag and remove commands with validation and destructive
      confirmation
- [x] Implement the safely filtered prune editor; never turn its zero value or
      `dangling=false` into a broad prune
- [x] Implement image pull through the session job tracker, including validated
      `os/arch[/variant]` platform input and extensible Docker progress statuses
- [x] Test keyed-event isolation, unknown/missing image state, prune validation,
      pull conflict/capacity and image/Dashboard/disk-usage refreshes
- [x] Manually verify read-only behavior against the local daemon; destructive
      image actions remain explicitly fixture-scoped

## TUI Slice 5: Volumes page

- [x] Implement volume list, session-local filters, identity selection, details
      and attached-container panels
- [x] Render list warnings without discarding valid data and render unknown usage
      as unknown rather than zero
- [x] Implement create and remove with validation, confirmation and clear in-use
      conflict handling
- [x] Implement label-scoped prune input; explain that `All` broadens eligible
      named volumes but does not remove the required label scope
- [x] Test mismatched targeted events, attachment refreshes, warning display,
      unknown usage, conflict and safe prune validation
- [x] Manually verify the read-only page against the local daemon; mutations
      remain restricted to uniquely labeled disposable volumes

## TUI Slice 6: Networks page

- [x] Implement network list, session-local filters, identity selection, details,
      IPAM and connected-container/address panels
- [x] Implement create, remove, connect and disconnect forms with field-level
      validation and destructive confirmation where appropriate
- [x] Implement age/label-scoped network prune input
- [x] Render removal with active endpoints as a conflict that advises
      disconnecting endpoints, including Docker 29's translated response
- [x] Test address rendering, keyed-event isolation, container/network refreshes,
      conflict mapping and safe prune validation
- [x] Manually verify the read-only page against the local daemon; mutations
      remain limited to uniquely labeled disposable networks/endpoints

## TUI Slice 7: Events page

- [x] Subscribe to `PageEvents` for the initial `RecentUpdated` window and raw
      live `DockerEventObserved` events in the same page lifecycle
- [x] Implement session-local resource, ID, action and Compose-project filters
      using backend subscription filters where a resubscription is warranted
- [x] Implement session-local clear without altering shared backend history
- [x] Bound displayed event rows and safely render untrusted attributes
- [x] Test newest-first history, live ordering, clear, filtering, overflow
      recovery and two-session independence
- [x] Manually verify disposable Docker activity updates Events independently

## TUI Slice 8: System page

- [x] Resolve or explicitly accept the documented case where a successful prune
      report is unavailable if its later refresh submission also returns error
- [x] Implement Docker version/host information and targeted detailed disk-usage
      panels with correct unknown/zero handling
- [x] Implement safely narrowed container, image, volume and network prune forms
      and render typed prune reports
- [x] Implement broad system prune only when product configuration permits it,
      requiring the exact confirmation token and a separate volumes opt-in
- [x] Map bootstrap-disabled system prune to a clear unavailable/policy state
- [x] Test targeted refresh generations, every prune guard, report rendering,
      stale/error preservation and all affected page updates
- [x] Manually verify System information and disk usage read-only; scoped-prune
      forms are unit tested and broad prune remains forbidden on the shared daemon

## Final TUI quality gates

- [ ] Run formatting, vet, unit, race and repeated concurrency-sensitive TUI/SSH
      tests and verify no page subscription, stream, job watcher or session leaks
- [ ] Verify every page at minimum, narrow and wide terminal sizes and without
      relying on color for meaning
- [ ] Verify terminal escape sequences in names, labels, logs, attributes and
      errors cannot control the user's terminal
- [ ] Verify two simultaneous SSH sessions keep independent selection/filter/UI
      state while receiving shared authoritative backend updates
- [ ] Verify disconnects during queued commands, active commands, refreshes,
      streams and jobs follow the ownership rules documented in `plan.md`
- [ ] Complete a disposable real-Docker manual pass for all supported pages and
      confirm cleanup leaves unrelated Docker resources untouched
- [ ] Replace the current backend demonstration in `main.go` with the documented
      Wish/SSH startup path

## Future nice-to-have: Container and Compose

- [ ] Support an explicit Compose project name (`-p` / `--project-name`) and use the resolved name for per-project job conflict protection
- [ ] Support Compose profiles
- [ ] Support custom environment variables and `.env` file selection
- [ ] Support an explicit Compose project directory
- [ ] Exec into a container terminal
## Future nice-to-have: SSH access control

- [ ] Add configurable SSH public-key authentication before permitting a
      non-loopback listen address
- [ ] Add documented authorized-key management and authentication audit events
- [ ] Consider explicitly configured password/OIDC integration only if a real
      deployment requirement appears

## Future nice-to-have: refresh throughput

- [ ] Replace the single Refresh Manager read worker with a small bounded read-worker pool, while preserving at-most-one active read per refresh key, one queued rerun after an in-flight change, and page-update ordering safety. Keep this pool separate from Command Executor workers so slow reads cannot delay Docker commands.

## Future nice-to-have: Add host resources to the Dashboard page
- [ ] Add the host-resources to the dashboard page (backend and frontend changes needed)
