# SSH-Native Docker TUI

A centralized, multi-user terminal application for inspecting and managing a
local Docker Engine over SSH.

The project is currently establishing its shared, in-process Go backend. The
architecture and implementation roadmap are documented in
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
configuration, safety rules, and instructions for connecting the future
production backend to the conformance suite.

Until the Wish/Bubble Tea interface is implemented, manually exercise the real
Dashboard/Containers APIs, command workers, refresh manager, and Docker event
listener with:

```sh
go run ./cmd/ssh-docker-tui
```

The executable entry point is `./cmd/ssh-docker-tui`.
