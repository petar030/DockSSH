TODO list for development

- [x] Setup Go project
- [x] Setup local git environment
- [x] Develop a testing framework

## Backend contracts and API tests

- [ ] Freeze the complete backend interfaces, DTOs, filters, options, streams, jobs, and error contracts
- [ ] Replace direct fixture client usage with safe resource arrangement, inspection, and cleanup helpers
- [ ] Write shared state and refresh conformance tests
- [ ] Write event bus, event buffer, filtering, overflow, and subscription conformance tests
- [ ] Write container API conformance tests
- [ ] Write image API conformance tests
- [ ] Write volume API conformance tests
- [ ] Write network API conformance tests
- [ ] Write Compose API conformance tests
- [ ] Write Dashboard and System API conformance tests
- [ ] Write stream, job, cancellation, and shutdown conformance tests
- [ ] Add deterministic fake clock, scheduler, Docker adapter, and synchronization test utilities
- [ ] Add unit tests for concurrency, coalescing, debounce, failures, stale snapshots, and goroutine cleanup

## Backend implementation

- [ ] Implement application bootstrap and shared Docker/Compose dependency ownership
- [ ] Implement StateStore
- [ ] Implement Event Bus and Docker event ring buffer
- [ ] Implement RefreshCoordinator and refresh scheduler
- [ ] Implement Docker event listener and event-to-refresh mapping
- [ ] Implement DockerService resource loaders and commands
- [ ] Implement ComposeService loaders, commands, streams, and jobs
- [ ] Implement the top-level Backend facade
- [ ] Connect the production BackendFactory to the conformance suite
- [ ] Make all unit, integration, race, cancellation, cleanup, and shutdown tests pass
