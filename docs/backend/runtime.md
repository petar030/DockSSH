# Backend Runtime

Production bootstrap in `internal/platform/docker` creates one dependency graph and passes it to `internal/backend/runtime`.

## Components

- `Backend` — top-level owner and facade; starts the scheduler and Docker event listener and coordinates shutdown.
- `RefreshManager` — registers page read functions, deduplicates pending keys, performs reads, and publishes typed results.
- `CommandExecutor` — bounded FIFO channel with a fixed worker pool for short mutations.
- `JobExecutor` — starts bounded long operations immediately; it has capacity and conflict protection but no waiting queue.
- `Scheduler` — submits configured refresh keys at process-wide intervals.
- Docker event listener — owns one daemon event stream, reconnects after transient failure, records observations, and requests affected refreshes.
- Event Hub — owns one independent bus per page plus bounded recent Docker-event history.

## Production defaults

| Component | Default |
| --- | --- |
| Refresh pending capacity | 128 distinct keys |
| Refresh workers | 1 |
| Refresh timeout | 30 seconds |
| Command workers | 4 goroutines |
| Command queue | 32 entries |
| Command timeout | 30 seconds |
| Active jobs | 4 |
| Subscriber buffer | 32 events |
| Docker-event history | 256 events in production bootstrap |
| Docker-event retry | 1 second |

All values except subscriber buffer and event retry are configurable through current runtime or production bootstrap configuration where applicable.

## Context rules

- Runtime components create their own contexts from `context.Background()` and cancel them during `Backend.Close`.
- A command caller context governs queue submission and waiting only. Accepted Docker work uses the executor context.
- A job submission context governs acceptance only. The accepted job uses Job Executor lifetime and exposes explicit cancellation.
- A refresh request is asynchronous; the read uses Refresh Manager lifetime and timeout.
- A stream uses the supplied page/session context directly.
- The construction context bounds validation and Docker-event-listener readiness; it does not become the runtime lifetime.
