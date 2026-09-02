# Backend Architecture

The application is a modular Go monolith. Wish sessions, Bubble Tea programs, and the backend share one OS process; the Docker daemon is a separate process. Calls are ordinary Go method calls, while accepted refreshes, commands, and jobs move into backend-owned goroutines.

```text
ONE APPLICATION PROCESS

TUI session A ─┐
TUI session B ─┼─> complete Backend facade
TUI session N ─┘       │
                       ├─ Subscribe ───────────────> Event Hub page bus ──> sessions
                       ├─ RequestRefresh ──────────> Refresh Manager
                       ├─ page command ────────────> Command Executor workers
                       ├─ page job ────────────────> Job Executor goroutines
                       └─ logs/stats ──────────────> session-owned stream goroutine
                                                        │
Docker event listener ─> raw Events event + refresh ────┤
Refresh Manager ────────> page read ─> typed update ────┘
Command/job completion ─> refresh request

All Docker/Compose work ─> one shared Moby client / Compose service
                                      │
                                      v
                         DOCKER DAEMON PROCESS
```

## Boundaries

- The facade is the TUI entry point. It provides common refresh/subscription/shutdown methods and page API accessors.
- Page `api.go` files contain TUI-facing calls. Private command, job, refresh, Docker-conversion, and stream code stays in neighboring files.
- The Event Hub transports immutable typed observations. It never calls Docker or triggers refreshes.
- The Refresh Manager performs authoritative reads and publishes results. It stores no resource snapshots.
- The Command Executor owns accepted short mutations. The Job Executor owns accepted long operations.
- Logs and stats remain session-owned because their usefulness and lifetime are tied to the active view.

## State ownership

| State | Owner | Lifetime |
| --- | --- | --- |
| Docker resources | Docker daemon | Docker-managed |
| Rendered page data and selection | TUI model | Page/session |
| Pending refresh keys | Refresh Manager | Until handled/shutdown |
| Accepted commands | Command Executor | Completion/timeout/shutdown |
| Active jobs/conflict keys | Job Executor | Completion/cancellation/shutdown |
| Subscription buffers | Event Hub | Subscription |
| Recent normalized Docker events | Event Hub history | Bounded process lifetime |
| Log/stat values | TUI stream | View/session |

There is no HTTP boundary, global resource cache, page loader object, or global serial command queue.
