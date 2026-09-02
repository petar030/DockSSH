# Commands and Jobs

## Short commands

Short mutations are validated by a page API and submitted as a `CommandRequest` containing metadata, affected refresh keys, and a private callback.

```text
TUI page API call
  -> validate and build CommandRequest
  -> bounded FIFO queue
  -> one of four default worker goroutines
  -> callback calls Docker/Compose with backend-owned timeout context
  -> request declared refresh keys after every attempted mutation
  -> return CommandResult/error if the caller still waits
```

Caller cancellation before acceptance can prevent submission. After acceptance it stops only that caller's wait; a buffered result channel lets the worker finish without the session. Queue overflow is reported as conflict/busy. Docker/Compose resolves resource-level concurrency.

## Long jobs

Compose up/down/pull/build and image pull are jobs. They start immediately in their own backend-owned goroutine when capacity is available; there is no job queue. The default maximum is four active jobs.

Each job provides:

- a stable job ID;
- best-effort ordered progress;
- explicit `Cancel`;
- reliable terminal `Wait` result;
- `JobProgressed` and `JobFinished` events on its page;
- refresh requests after completion or cancellation.

Conflict keys reject overlapping long operations for the same normalized Compose project or image reference. This narrow protection is separate from global capacity.

Backend shutdown rejects new work, cancels active commands/jobs, waits for their goroutines, and closes job progress channels. Command and job callbacks must always honor their supplied context.
