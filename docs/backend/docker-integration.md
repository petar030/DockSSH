# Docker Integration

`internal/platform/docker` is the composition root for Docker SDK dependencies. It creates one Moby client, injects the same client into Docker CLI, builds the in-process Compose service, constructs every page API/runtime component, and transfers client ownership to the runtime.

```text
NewBackend
  -> Moby client
  -> Docker CLI using that exact Moby client
  -> Compose service using Docker CLI
  -> page APIs + refresh handlers
  -> Event Hub, Refresh Manager, Command Executor, Job Executor
  -> runtime Backend owns final client close
```

Page packages depend on narrow internal interfaces containing only SDK methods they use. Their `docker.go` files describe those boundaries; refresh/command/job files convert between page DTOs and SDK values. The shared client supports concurrent calls, while Docker Engine decides daemon-side conflicts.

Compose file-based operations accept explicit project specs. Paths are cleaned, resolved, and restricted to configured allowed roots before definitions are loaded. Active project discovery uses Compose labels already present on Docker resources; Docker does not store arbitrary Compose source files for later retrieval.

Managed configuration creation is restricted further: callers provide a
strict project name and YAML content, and the backend derives
`<first root>/<project>/compose.yaml`. It validates the Compose schema, uses a
root-scoped filesystem handle to prevent traversal and escaping symlinks, and
replaces the file by atomic rename from a synced `0600` temporary file. Reads
use that same derived boundary. Concurrent saves are atomic last-writer-wins;
they do not introduce another lock or executor.

The event adapter converts Moby events into stable backend observations and owns reconnect readiness. Docker logs come from the daemon's retained container log driver data; follow mode then streams new output. Recent Docker events are different: the backend retains only observations received during its own process lifetime.

The runtime closes the Moby client exactly once after backend-owned work and subscriptions stop. Page APIs borrow it and never close it.
