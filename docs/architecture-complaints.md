# Architecture Complaints and Open Questions

Temporary working notes. This file records concerns and questions about the
current architecture. It will be removed after the architecture review.

## Complaints

1. The distinction and naming between loaders and services is confusing.

2. Page structure is inconsistent: Dashboard defines its loader inside
   `dashboard.go`, while Containers uses a separate `loader.go` file. Page
   components should follow one consistent layout.

3. The `Service` object appears unnecessary for the current architecture.
   Loaders and command operations could instead be simple functions called by
   the TUI, with the caller waiting for the command result while successful
   commands submit a refresh request to the refresh dispatcher.

## Proposed Simplification

Each page should expose one `api.go` file containing its read operations,
commands, and streams. The functions should be grouped with clear comments by
responsibility, without separate Loader and Service objects unless a concrete
need for them appears.

## Open Questions

<!-- Add questions here as they are raised. -->
