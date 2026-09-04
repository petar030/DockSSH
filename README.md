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

The Dashboard is implemented. The remaining tabs currently show explicit
placeholders and will be added page by page. Use `[`/`]` or `1`–`8` to switch
tabs, `r` to refresh Dashboard, `?` for help, and `q` to disconnect.

The executable entry point is `./cmd/ssh-docker-tui`.
