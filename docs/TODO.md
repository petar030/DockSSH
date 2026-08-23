TODO list for development

- [x] Setup Go project
- [x] Setup local git environment
- [x] Develop a testing framework

# Backend API

## Slice 0: Shared observer-based backend foundation

- [x] Replace snapshot contracts with flat refresh keys and typed full-result update events
- [x] Remove `StateStore`, snapshot versions, stale-cache metadata, and snapshot query methods
- [x] Implement a separate Event Bus per page with filtering, ordering, bounded delivery, cancellation, and overflow recovery
- [x] Implement direct RefreshCoordinator loads with page routing, failure publication, cancellation, and no coalescing/debounce state
- [x] Keep and adapt the deterministic fake clock, scheduler, Docker adapter, and synchronization utilities
- [x] Keep the bounded recent Docker-event buffer as event history, not resource state
- [x] Update application bootstrap and preserve single ownership of shared Docker/Compose dependencies
- [x] Verify two observers on one page receive its authoritative refresh result without cross-page leakage
- [x] Verify page-lifetime subscriptions, subscribe-before-refresh startup, failure delivery, scheduler behavior, and overflow resync
- [x] Pass formatting, vet, unit, integration, race, shutdown, ownership, and repeated stability checks
- [x] Remove the temporary refactor checklist after verification

## Slice 0.5: Docker event ingestion foundation

- [ ] Test and implement one process-wide Docker event listener using the shared Moby client
- [ ] Normalize, buffer, and publish daemon events without blocking the Docker event stream
- [ ] Map foundational Docker actions to the affected page refreshes and verify clean cancellation/reconnection

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
- [ ] Complete normalization and refresh mapping for all Docker actions supported by the Events tab
- [ ] Verify slow subscribers cannot block Docker events or other sessions
- [ ] Keep pause and clear behavior session-local for the future TUI

## Slice 8: System tab

- [ ] Define System DTOs, prune reports, options, and API contracts
- [ ] Test and implement Docker version and information windows
- [ ] Test and implement the detailed disk-usage window
- [ ] Test and implement safely scoped container, image, volume, and network prune actions
- [ ] Test system prune only against an explicitly dedicated Docker daemon
- [ ] Verify all affected Dashboard and resource updates are broadcast after prune

## Final backend quality gates

- [ ] Test and implement graceful shutdown during active refreshes, subscriptions, streams, and jobs
- [ ] Verify cancellation and deterministic closure of every stream and job
- [ ] Verify no goroutines, readers, subscriptions, streams, jobs, or Docker connections leak
- [ ] Pass all unit and integration tests with the race detector
- [ ] Confirm integration cleanup leaves no test resources behind
- [ ] Complete the top-level Backend facade and full production conformance suite
