# Events and Streams

## Event Hub

The Event Hub has one independent bus for each page. Producers publish immutable `EventEnvelope` values; subscribers select a page and may filter by event type and refresh key. Events-page observers may additionally filter Docker resource, ID, action, and Compose project.

Each subscription owns a bounded channel. Publication never blocks on a slow session. When its buffer overflows, the subscriber receives `SubscriberOverflow` and can request a complete refresh. Canceling its context or calling `Close` unregisters only that subscriber; closing the Event Hub closes all subscriptions.

## Docker events and history

One process-wide listener consumes the daemon event stream. Every normalized event is:

1. published as `DockerEventObserved` on `PageEvents`;
2. retained in bounded in-memory Docker-event history;
3. mapped to Dashboard and affected page refresh requests.

History contains only normalized Docker observations, not every Event Hub message and not Docker resource state. `RequestRefresh(PageEvents)` publishes a complete recent-history window. No Events-page subscriber is required for another page to refresh.

## Session-owned streams

Container logs, container stats, and Compose logs use the caller's page/session context. They bypass the Refresh Manager and command/job executors.

`Stream[T]` exposes `Values`, `Done`, and idempotent `Close`. Leaving a view or disconnecting must cancel the owning context or close the stream. Container stream closure also closes the underlying Docker response reader. `Done` produces one terminal value and then closes; normal cancellation is reported as clean completion.

Backend shutdown and session shutdown are coordinated at application level: backend-owned workers stop through `Backend.Close`, while the SSH/TUI layer ends session contexts so their streams terminate before the shared Docker client is released.
