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

Run the local quality checks with:

```sh
make check
```

The executable entry point is `./cmd/ssh-docker-tui`.
