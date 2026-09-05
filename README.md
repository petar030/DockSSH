# SSH-Native Docker TUI

A centralized, multi-user terminal application for inspecting and managing a
local Docker Engine over SSH.

The project has a shared, in-process Go backend and an SSH-served Bubble Tea
shell with a working Dashboard page. The architecture and implementation
roadmap are documented in
[`docs/plan.md`](docs/plan.md).

## Requirements

- Go 1.26.3 or newer
- Git
- Docker Engine (required by the later integration test suite)

## Development

Run formatting, static analysis, and unit tests with:

```sh
make check
```

Run the real-Docker integration environment with:

```sh
make test-integration
```

See [`docs/testing.md`](docs/testing.md) for the test layout, environment
configuration, safety rules, and manual SSH verification instructions.

## Run the TUI

Start the local SSH server and shared Docker backend:

```sh
go run ./cmd/ssh-docker-tui
```

Then connect from another terminal:

```sh
ssh -p 23234 localhost
```

The initial unauthenticated version deliberately accepts only loopback listen
addresses. It creates and reuses `.ssh-docker-tui/host_ed25519` as the server
host key. Useful startup options are:

```sh
go run ./cmd/ssh-docker-tui \
  -listen=127.0.0.1:23234 \
  -host-key=.ssh-docker-tui/host_ed25519 \
  -dashboard-refresh=10s
```

Dashboard, Containers, Images, Volumes, and Networks are implemented. The remaining tabs show
explicit placeholders and will be added page by page. Use `[`/`]` or `1`–`8` to switch
tabs, `r` to refresh, `?` for help, and `q` to disconnect. On Containers, use
the displayed footer shortcuts: `enter` for details, `p` for processes, `l`
for logs, `s` for stats, `a` for actions, `f` for filtering and `o` for sort.
On Images, use `f` to filter, `v` to cycle all/tagged/dangling, `o` to sort,
`i` for details, `h` for history, `t` to tag, `d` to remove, `p` for a safely
filtered prune, and `u` to pull. `J` opens the session-wide job tracker; changing
tabs never cancels an accepted job.
On Volumes, use `f` to filter, `i` for details, `a` for attachments, `c` to
create, `d` to remove, and `p` for a label-scoped prune.
On Networks, use `f` to filter, `i` for details, `a` for connections, `c` to
create, `n`/`x` to connect/disconnect a container, `d` to remove, and `p` for
an age- or label-scoped prune.

The executable entry point is `./cmd/ssh-docker-tui`.
