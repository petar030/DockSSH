# Backend Documentation

The backend is one process-wide, in-process service shared by all SSH/TUI sessions. Docker remains authoritative; the backend keeps only operational coordination state and bounded Docker-event history, not copies of Docker resource state.

Start with [architecture.md](architecture.md), then use the focused references:

- [runtime.md](runtime.md) — runtime components, goroutines, contexts, and defaults.
- [page-apis.md](page-apis.md) — every TUI-facing page operation and its category.
- [refreshes.md](refreshes.md) — asynchronous reads and typed page updates.
- [commands-and-jobs.md](commands-and-jobs.md) — mutations, queues, jobs, progress, and cancellation.
- [events-and-streams.md](events-and-streams.md) — Event Hub, Docker events, logs, and stats.
- [docker-integration.md](docker-integration.md) — shared Moby/Compose construction and boundaries.
- [error-handling.md](error-handling.md) — stable error categories and failure delivery.
- [shutdown-and-lifecycle.md](shutdown-and-lifecycle.md) — ownership and deterministic cleanup.

The permanent project direction remains in [../plan.md](../plan.md), work ordering in [../TODO.md](../TODO.md), and verification commands in [../testing.md](../testing.md).

## Minimal client flow

```go
subscription, err := app.Subscribe(pageCtx, backend.PageContainers, backend.EventFilter{})
if err != nil { return err }
defer subscription.Close()

if err := app.RequestRefresh(backend.PageContainers); err != nil { return err }
for event := range subscription.Events() {
    // Replace this session's page data with the typed complete update.
}
```

Page APIs expose targeted refresh requests, commands, jobs, and streams. Docker SDK types stay behind those APIs.
