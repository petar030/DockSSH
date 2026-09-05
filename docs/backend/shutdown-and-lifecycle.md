# Shutdown and Lifecycle

Ownership determines cancellation. A TUI context is never used as the execution context of an accepted refresh, command, or job; it does own subscriptions and streams.

Compose configuration reads are direct, session-owned one-shot reads. Configuration
saves are short mutations submitted to the Command Executor: once accepted, the
backend worker owns the atomic file write and the caller context controls only
submission and waiting for its result.

## Application shutdown

`Backend.Close(ctx)` starts shutdown once. Multiple callers may wait with independent contexts; cancellation of one wait does not interrupt the shared shutdown.

```text
cancel runtime context
  -> scheduler and Docker event listener stop
  -> Command Executor rejects new work, cancels workers, drains queued callers
  -> Job Executor rejects new work, cancels and waits for active jobs
  -> Refresh Manager rejects new work, cancels and waits for its worker
  -> Event Hub closes every subscription
  -> shared Moby client closes exactly once
```

The application must stop accepting SSH sessions and cancel existing session/page contexts as part of its outer shutdown. That closes session-owned logs/stats independently of backend-owned worker shutdown.

## Deterministic terminal behavior

- Subscription channels close on context cancellation, explicit close, or Event Hub shutdown.
- Container and Compose stream value channels close; `Done` emits one result and closes. `Close` is idempotent.
- Job progress closes after its terminal event; `Wait` always observes the stored result. `Cancel` is idempotent.
- Refresh, command, job, scheduler, listener, and stream goroutines honor cancellation and are waited for at their owning boundary.
- The production runtime is the sole owner of the Moby client.

## Verification

Unit lifecycle tests keep refreshes, commands, jobs, subscriptions, and stream readers active while their owner closes, then require all observable termination signals. Race-enabled unit and integration suites exercise shared state. The real-Docker fixture deletes tracked resources in dependency order, verifies no run-labeled containers/networks/volumes remain, and only then closes its own client.

See [../testing.md](../testing.md) for commands and safe fixture rules.
