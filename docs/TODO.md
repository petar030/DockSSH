TODO list for development

- [x] Setup Go project
- [x] Setup local git environment
- [x] Develop a testing framework

# Backend API

## Slice 0: Shared backend foundation

- [x] Freeze shared snapshot, refresh, event, subscription, stream, job, command-result, and error contracts
- [x] Remove unrestricted fixture client access and establish the safe helper and cleanup boundary
- [x] Add deterministic fake clock, scheduler, Docker adapter, and synchronization test utilities
- [x] Test and implement StateStore versioning, atomicity, unchanged writes, stale state, and concurrent access
- [x] Test and implement Event Bus filtering, ordering, cancellation, bounded delivery, and overflow behavior
- [x] Test and implement the Docker event ring buffer and eviction behavior
- [x] Test and implement RefreshCoordinator coalescing, debounce, failure handling, and event publication
- [x] Test and implement the refresh scheduler with deterministic time
- [x] Test and implement application bootstrap and single ownership of shared Docker/Compose dependencies
- [x] Connect the first production BackendFactory to the conformance suite

## Slice 1: Dashboard tab

- [ ] Define Dashboard summary DTOs and API contracts
- [ ] Test and implement the Engine summary window
- [ ] Test and implement the resource-count summary window
- [ ] Test and implement the Docker disk-usage window
- [ ] Test and implement recent-events data shown on the Dashboard
- [ ] Verify Dashboard refreshes after relevant cross-resource changes

## Slice 2: Containers tab

- [ ] Define container DTOs, filters, options, streams, and API contracts
- [ ] Test and implement the container list window
- [ ] Test and implement the container details window
- [ ] Test and implement the container processes window
- [ ] Test and implement start, stop, restart, pause, unpause, kill, rename, and remove actions
- [ ] Test and implement one-shot container exec
- [ ] Test and implement container logs and stats windows
- [ ] Verify command-triggered and Docker-event-triggered container refreshes

## Slice 3: Compose tab

- [ ] Define Compose project DTOs, options, streams, jobs, and API contracts
- [ ] Test and implement the Compose project list window
- [ ] Test and implement project loading and validation from allowed directories
- [ ] Test and implement the Compose project details and services windows
- [ ] Test and implement start, stop, restart, pause, unpause, exec, and scale actions
- [ ] Test and implement up, down, pull, and build jobs with progress
- [ ] Test and implement Compose logs
- [ ] Verify project refreshes from Compose jobs and labeled Docker events

## Slice 4: Images tab

- [ ] Define image DTOs, filters, options, jobs, and API contracts
- [ ] Test and implement the image list window
- [ ] Test and implement image details and history windows
- [ ] Test and implement tag, remove, and safely filtered prune actions
- [ ] Test and implement image pull jobs and progress
- [ ] Verify image and disk-usage refreshes after image changes

## Slice 5: Volumes tab

- [ ] Define volume DTOs, filters, options, and API contracts
- [ ] Test and implement the volume list window
- [ ] Test and implement volume details and attached-containers windows
- [ ] Test and implement create, remove, and safely filtered prune actions
- [ ] Test conflict handling when a volume is in use
- [ ] Verify volume, container, and disk-usage refreshes

## Slice 6: Networks tab

- [ ] Define network DTOs, filters, options, and API contracts
- [ ] Test and implement the network list window
- [ ] Test and implement network details and connected-containers windows
- [ ] Test and implement create, remove, and safely filtered prune actions
- [ ] Test and implement container connect and disconnect actions
- [ ] Verify assigned addresses and affected container/network refreshes

## Slice 7: Events tab

- [ ] Define normalized Docker event DTOs and Events API contracts
- [ ] Test and implement the recent-events window
- [ ] Test and implement live filtered event subscriptions
- [ ] Test normalization and refresh-scope mapping for all supported Docker actions
- [ ] Verify slow subscribers cannot block Docker events or other sessions
- [ ] Keep pause and clear behavior session-local for the future TUI

## Slice 8: System tab

- [ ] Define System DTOs, prune reports, options, and API contracts
- [ ] Test and implement Docker version and information windows
- [ ] Test and implement the detailed disk-usage window
- [ ] Test and implement safely scoped container, image, volume, and network prune actions
- [ ] Test system prune only against an explicitly dedicated Docker daemon
- [ ] Verify all affected Dashboard and resource snapshots refresh after prune

## Final backend quality gates

- [ ] Test and implement graceful shutdown during active refreshes, subscriptions, streams, and jobs
- [ ] Verify cancellation and deterministic closure of every stream and job
- [ ] Verify no goroutines, readers, subscriptions, streams, jobs, or Docker connections leak
- [ ] Pass all unit and integration tests with the race detector
- [ ] Confirm integration cleanup leaves no test resources behind
- [ ] Complete the top-level Backend facade and full production conformance suite
