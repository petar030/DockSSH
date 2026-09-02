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
- [x] Keep pause and clear behavior session-local for the future TUI

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

## Future nice-to-have: fuller Compose invocation options

- [ ] Support an explicit Compose project name (`-p` / `--project-name`) and use the resolved name for per-project job conflict protection
- [ ] Support Compose profiles
- [ ] Support custom environment variables and `.env` file selection
- [ ] Support an explicit Compose project directory
- [ ] Exec into a container terminal
- [ ] Edit docker compose using nano editor

## Future nice-to-have: refresh throughput

- [ ] Replace the single Refresh Manager read worker with a small bounded read-worker pool, while preserving at-most-one active read per refresh key, one queued rerun after an in-flight change, and page-update ordering safety. Keep this pool separate from Command Executor workers so slow reads cannot delay Docker commands.
