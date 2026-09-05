# Page APIs

The complete production facade exposes `RequestRefresh`, `Subscribe`, `Close`, and accessors for Containers, Compose, Images, Volumes, Networks, and System. Dashboard and Events need no accessor because their public behavior is page refresh plus subscription.

`ReadRefresh` functions are runtime callbacks registered with the Refresh Manager. They are not TUI operations even though they live on page API implementations.

## Targeted-refresh rule

When a TUI opens or focuses a selected resource view, it calls that view's
targeted request method. If the view remains visible after a relevant base-page
update, it calls the targeted request again. For example, a Containers page
showing container `A` calls `RequestDetails(A)` after receiving a relevant
container-list update.

This is session-local TUI behavior, not a backend `RefreshPolicy`. Periodic
policies request only configured base page refreshes; the backend does not know
which resource any session has selected. It is event-driven, not periodic TUI
polling.

| Page | Refresh requests and results | Commands | Jobs | Streams |
| --- | --- | --- | --- | --- |
| Dashboard | `RequestRefresh(PageDashboard)` → `SummaryUpdated` | — | — | — |
| Containers | page list; `RequestDetails`; `RequestProcesses` | `Start`, `Stop`, `Restart`, `Pause`, `Unpause`, `Kill`, `Rename`, `Remove` | — | `Logs`, `Stats` |
| Compose | page project list; `RequestDetails`; managed `ConfigPath`/`ReadConfig` | `Start`, `Stop`, `Restart`, `Pause`, `Unpause`, `Scale`, `SaveConfig` | `Up`, `Down`, `Pull`, `Build` | `Logs` |
| Images | page list; `RequestDetails`; `RequestHistory` | `Tag`, `Remove`, `Prune` | `Pull` | — |
| Volumes | page list; `RequestDetails`; `RequestAttachments` | `Create`, `Remove`, `Prune` | — | — |
| Networks | page list; `RequestDetails`; `RequestConnections` | `Create`, `Remove`, `Prune`, `Connect`, `Disconnect` | — | — |
| Events | `RequestRefresh(PageEvents)` → bounded `RecentUpdated`; live `DockerEventObserved` through subscription | — | — | — |
| System | page info; `RequestDiskUsage` | `PruneContainers`, `PruneImages`, `PruneVolumes`, `PruneNetworks`, guarded `PruneSystem` | — | — |

## Delivery model

- Base and targeted refreshes return acceptance errors only; results arrive as typed events on that page bus.
- Short commands return `CommandResult` or a stable error to a caller that is still waiting. Authoritative state follows through refresh events.
- Jobs return a `Job` handle with progress, `Wait`, and `Cancel`; progress and completion are also page events.
- Streams return ordered values and one terminal result through `Done`.

Every public DTO and option type is defined in its page package. Docker SDK response types never cross this boundary. Container/Compose exec is intentionally outside the current version.

Compose configuration authoring is deliberately narrower than general host
file access. `ConfigPath(project)` and `ReadConfig(ctx, project)` can address
only `<first configured root>/<project>/compose.yaml`; `SaveConfig` accepts the
same strict project identity plus YAML text and runs through the Command
Executor. Save and `Up` are separate calls. A saved project is not part of the
active-project refresh until Compose creates labeled Docker resources.
