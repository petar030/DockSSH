# Refreshes

A refresh is an asynchronous authoritative read followed by a typed page event. It is not a cache lookup.

```text
TUI / scheduler / Docker event / command / job
                    │
                    v
          RefreshManager.Request(key, reason)
                    │ deduplicate pending full key
                    v
             one backend worker
                    │ route by RefreshKind
                    v
          page ReadRefresh(ctx, key) ──> Docker/Compose
                    │
                    v
       typed update or RefreshFailed ──> owning page bus
```

`RefreshKey` contains a kind and optional ID. Base examples are `dashboard.summary`, `containers.list`, and `system.info`; targeted examples are `{container.details, ID}` and `{network.connections, ID}`.

`RequestRefresh(page)` resolves a registered base key. Targeted page methods construct complete keys themselves. TUI code does not invoke `ReadRefresh` directly.

The TUI requests targeted keys when it opens or focuses a selected view, then
requests the same key again if that view stays visible after a relevant base
page update. Targeted refreshes are not periodic policies because selection is
session-local and the backend does not retain session selection state. There is
no TUI timer for targeted reads.

## Pending behavior

- The manager maintains an ordered bounded set of different pending keys.
- Repeated pending requests for the same full key collapse into one read.
- A trigger received while that key is actively reading retains one rerun, preventing a state change from being lost.
- Different IDs are different keys.
- Capacity overflow and requests after closure return stable errors.

The single worker is deliberately simple. A small read-worker pool remains a documented future optimization; it must preserve per-key exclusion and safe page ordering.

Reasons identify the trigger: startup, scheduled, Docker event, command, job completion, manual request, or overflow recovery. On read failure the existing TUI state can remain visible while `RefreshFailed` tells the session it may be stale.
