# Temporary plan: TUI slices 4–8 (skip Compose)

This file is a **working implementation checklist** for the next TUI series.
It is not the architectural source of truth. Follow [`plan.md`](plan.md)
(especially the TUI implementation contract), keep work ordered in
[`TODO.md`](TODO.md), and use [`testing.md`](testing.md) for verification
commands and fixture safety.

**Delete this file** after slices 4–8 are committed and the permanent docs
have been updated.

## Goal

Implement the **Images, Volumes, Networks, Events, and System** pages in the
existing Wish + Bubble Tea shell. Leave **Compose (tab `3` / TUI Slice 3)**
as the current placeholder. Do **not** change backend contracts, executors,
refresh routing, or Docker adapters in this series. If a backend gap blocks
good UX, **record it in the deferral log below** and ship a frontend-only
workaround.

Each slice must leave a usable, tested application. After each slice:
run tests, update the docs listed for that slice, then create **one git
commit**.

## Current baseline

Already done:

- Backend slices 0–8 and quality gates.
- TUI Slice 0 (SSH shell, root frame, sanitization).
- TUI Slice 1 (Dashboard).
- TUI Slice 2 (Containers: list/details, commands with y/n confirm,
  progress overlay + spinner tick, logs/stats).

Not done:

- `internal/tui/jobs.go` (session job tracker).
- Page packages `images/`, `volumes/`, `networks/`, `events/`, `system/`.
- Root wiring beyond Dashboard + Containers.
- Compose page (explicitly out of scope).

Tab indices stay as in `internal/tui/navigation.go`:

| Key | Index | Page | This series |
| --- | --- | --- | --- |
| `1` | 0 | Dashboard | already implemented |
| `2` | 1 | Containers | already implemented |
| `3` | 2 | Compose | **placeholder only** |
| `4` | 3 | Images | Slice 4 |
| `5` | 4 | Volumes | Slice 5 |
| `6` | 5 | Networks | Slice 6 |
| `7` | 6 | Events | Slice 7 |
| `8` | 7 | System | Slice 8 |

## Preconditions (before Slice 4)

1. Land the current uncommitted Containers/Dashboard polish (confirmations,
   Pause/Unpause footer, WORKING overlay, spinner tick, test helpers) in
   **its own commit**, separate from Slice 4. Do not mix that work into
   Images.
2. `make check` (or at least `gofmt`, `go vet`, `go test ./internal/tui/...`)
   is green on that commit.
3. Do not start Compose files, Compose tests, or Compose root wiring.

## Non-goals

- TUI Slice 3 (Compose list, project spec, Compose commands/jobs/logs, exec).
- Backend changes (structured command vs refresh errors, `AllowSystemPrune`
  getter, prune-report-when-refresh-fails, job reattachment/history).
- SSH public-key auth, non-loopback listen, host resources on Dashboard.
- Interactive container/Compose exec, log grep, design-notes refactors
  unless this plan says otherwise.
- Golden full-screen snapshots as the primary test strategy.
- Importing Docker SDK types in `internal/tui/**`.

## Hard rules (copy from the existing pipeline)

These are already how Dashboard and Containers work. New pages must match.

### Architecture

- One process-wide `backend.Backend`; each SSH session has one `tui.App`.
- Page packages own MVU types. No generic universal page model, no second
  Docker resource cache, no TUI Docker SDK imports.
- `View` is pure. `Update` never blocks, never calls Docker, never waits
  on a channel. Backend work is `tea.Cmd` returning a **page-local**
  message that carries `generation` (and `selectionGen` for targeted work).
- Subscribe **before** `RequestRefresh`. Only the active tab holds a
  subscription. `Deactivate` cancels page context, closes subscription and
  streams, invalidates generation.
- Selection is by **stable identity** (image ID, volume name, network ID),
  never only by row index. After a replacement list, re-find the identity;
  if gone, clear targeted panels.
- Treat backend DTOs as immutable. Filter/sort on a page-owned copy/index.
- Ignore mismatched refresh keys and payload IDs (other session’s targeted
  inspect).
- `SubscriberOverflow`: mark stale, request base refresh plus visible
  targeted panels once; no retry loop; offer manual `r` if recovery request
  fails.
- Keep last successful data on `RefreshFailed`. Initial failure has no
  data: error/empty, not fake zeros.
- Commands: no optimistic Docker state. `CommandResult` is not
  authoritative; the next typed page event is. Disable the in-flight
  action, show WORKING overlay + spinner (`tea.Batch(run, spinner.Tick)`),
  ignore repeat keys until `commandFinishedMsg`.
- Destructive work uses a centered overlay. `y` / `enter` confirm, `n` /
  `esc` cancel (same as Containers).
- Map errors with `backend.HasErrorCode` / `errors.Is`, never string
  match. Prefer permission, daemon unavailable, timeout, conflict, then
  internal. Joined command+refresh errors stay as today; do not invent a
  backend split.
- Sanitize all Docker-controlled text (`tui/ui`). Truncate for display
  only. Color is supplementary.
- Minimum 80×24; smaller terminals keep the root “too small” view.
  Wide (≥110 or the current Containers breakpoint) list+detail horizontal;
  narrower stacks vertically.
- Tests: fake frontend-facing APIs, controlled messages, focused
  assertions. SSH/root tests stay in `internal/tui/app_test.go`.

### Root `App` evolution

Grow `Application` with accessors as pages appear (`Images()`, `Volumes()`,
`Networks()`, `System()`). Events uses only `Subscribe` + `RequestRefresh`.

`Update` must:

- Route keys to the **active** page; honor `CapturesInput()` for overlays
  the same way Containers does.
- Forward **non-key** messages to **every constructed page model** so a
  job watcher, late stream, or subscription close still runs after tab
  change.
- `Init` / `switchTab` / `Deactivate` / `SetSize` / footer Help/Status /
  header Activity follow the active page. Compose index stays placeholder
  text.

Do **not** introduce a giant generic page interface unless duplication
becomes mechanical **after** two or three pages exist. Prefer copy-and-
adapt from `internal/tui/containers` first.

### Session job tracker (required for Slice 4 pull, not for Compose)

`plan.md` places jobs on the **root**, not the page. Slice 3 was going to
add this; Images pull needs it first. Implement `internal/tui/jobs.go` as
**Slice 4, increment 0**, still frontend-only:

- Track `backend.Job` handles started by **this session**.
- Session-derived context: one receive loop for `Progress()`, one `Wait`.
- Tab changes must not cancel those cmds/contexts.
- Footer: compact “N jobs” / newest status. A key (document it; e.g. `J`)
  opens a session overlay: list, newest progress line, confirm-to-cancel.
- `Job.Wait` / `JobFinished.Err` is terminal truth. Progress strings are
  best-effort and may be unknown Docker statuses.
- Images page: after `Pull` returns a handle, register it. Ignore or merge
  `JobProgressed` on `PageImages` when `JobID` is locally tracked.
  Still handle `JobProgressed`/`JobFinished` for jobs started by **another**
  session if this Images tab is subscribed.
- Capacity/conflict (`ErrorConflict`) happen **before** a handle exists:
  keep the pull editor open, show retryable error, do not register a job.
- Disconnect: drop handles; Docker work continues; no reattach UI (v1).
- Leaving Images does **not** call `Job.Cancel()`.

Compose jobs stay unimplemented. The tracker API should still be generic
enough that Slice 3 can register Compose jobs later without redesign.

## Shared page recipe (every resource page)

Use Containers as the template (`model.go`, `update.go`, `view.go`,
`model_test.go`). Typical files:

```text
internal/tui/<page>/
├── model.go      Model, Activate/Deactivate/Refresh/SetSize, Help/Status/Activity, CapturesInput
├── update.go     messages, subscribe/request/wait, commands, overlays
├── view.go       list/detail workspace, overlays
└── model_test.go fake Backend+API, state and rendering tests
```

Standard message set:

- `subscriptionReadyMsg`
- `requestFinishedMsg` (base and targeted; include `selectionGen` when keyed)
- `eventReceivedMsg`
- `commandFinishedMsg` (if the page has short commands)
- job messages only on Images (and later Compose), via root tracker
  plus page-bus `JobProgressed`/`JobFinished`

Standard overlays: filter editor, confirm, progress, page-specific forms
(tag, prune, create, pull, connect). Logs-style full overlay is not
needed unless a page has a stream (none of 4–8 do, except Events is the
subscription itself).

Error codes to surface: `invalid_input`, `not_found`, `conflict`,
`permission_denied`, `daemon_unavailable`, `timeout`, `canceled`,
`unsupported`, `internal`.

## Testing cadence (between increments, not only at slice end)

After **each increment** below:

```sh
gofmt -w <touched files>
go vet ./internal/tui/...
go test ./internal/tui/...
```

After **each full slice**, also:

```sh
go test ./internal/tui/... -race
go test ./internal/tui/ -count=5   # root + navigation; keep short
```

Do not run image prune, network wipe, or system prune against a developer
daemon in manual tests. Follow [`testing.md`](testing.md) labels and
sentinels. Broad system prune only if `BACKEND_TEST_DEDICATED_DAEMON` /
product `AllowSystemPrune` is actually on; otherwise skip that manual
step.

Hermetic TUI tests must cover, per page as applicable:

- subscribe-before-refresh and cleanup on tab exit
- stale generation / mismatched target rejection
- loading / loaded / empty / stale / error
- overflow recovery without a retry loop
- identity selection across sort/filter/replacement
- command busy overlay, error mapping, authoritative refresh
- prune/create validation staying in the editor
- sanitization and min/narrow/wide layout
- two simulated sessions: shared events, independent UI state (root test
  or page test with two models on one fake hub)

## Documentation to touch (throughout)

Permanent docs, not this file:

- [`TODO.md`](TODO.md) — check off TUI Slice 4–8 items as they land; keep
  Slice 3 unchecked; note that the session job tracker landed with Slice 4.
- [`README.md`](../README.md) — which tabs work; keys; loopback SSH.
- [`testing.md`](testing.md) — add a short manual SSH section per new page
  (mirrors the Containers paragraph).
- [`plan.md`](plan.md) — only if keys, footer job overlay, or env-masking
  wording needs a factual update. Do not rewrite architecture.
- Backend docs — **no** unless a deferred backend item is later done.

Commit messages (match history): `Implement TUI Slice N (Name page)`.

---

# Slice 4 — Images (+ session job tracker)

Backend already provides `images.API`: `RequestDetails`, `RequestHistory`,
`Tag`, `Remove`, `Prune`, `Pull` (job). Events: `ListUpdated`,
`DetailsUpdated`, `HistoryUpdated`, `RefreshFailed`, `SubscriberOverflow`,
`JobProgressed`, `JobFinished`. Refresh kinds: `images.list`,
`image.details`, `image.history`.

v1 **masks image environment values** (names visible, values not). This
differs from Containers. Do not “fix” that to match Containers.

## Increment 4.0 — Session job tracker

**Code**

- Add `internal/tui/jobs.go` (+ `jobs_test.go`).
- Root `App` owns a tracker. Session context watches progress/`Wait`.
- Footer summary + overlay (`J` or as documented in help). Cancel needs
  confirmation. Empty state when no local jobs.
- Wire non-key updates through the tracker.

**Tests**

- Register job, receive progress, `Wait` success/fail/cancel.
- Tab switch does not drop the handle (simulate by not calling a page
  `Deactivate` on the tracker).
- Duplicate `JobProgressed` with same ID does not double-count in the
  footer if the page later also sees the event (tracker is source of
  truth for local jobs).
- Conflict/capacity: no handle registered.

**Manual:** none required yet (no Images pull UI).

Do **not** commit 4.0 alone unless the tracker is unused and would sit
dead. Prefer **one Slice 4 commit** after 4.0–4.4. If 4.0 is large and
green, a commit `Implement TUI session job tracker` **before** the Images
page is allowed.

## Increment 4.1 — List, filter, sort, selection, details

**Code**

- `internal/tui/images/` model/update/view.
- Subscribe to Images bus; request `PageImages`.
- Session-local filter (text, optional dangling, labels) and sort
  (tag/name, size, created). Identity = image ID.
- Selecting a row requests `RequestDetails`. History is a secondary panel
  or overlay (`h`), `RequestHistory`, keyed by image ID.

**Tests**

- Subscribe-before-refresh ordering.
- Mismatched `DetailsUpdated` / `HistoryUpdated` ignored.
- Vanished ID clears details/history.
- Overflow requests list + visible details/history.
- Sanitization of tags/labels/history `CreatedBy`.
- Wide vs stacked layout; empty list.

## Increment 4.2 — Tag, remove, prune (short commands)

**Code**

- Tag: overlay editor, `TagOptions.Reference`. Stay open on
  `invalid_input`.
- Remove: confirm; options Force / PruneChildren like Containers force/
  volumes toggles.
- Prune editor: **never** submit zero `PruneOptions` or `Dangling=false`.
  Require at least one of: dangling **true**, `Until`, or a non-empty
  label key. Client-side copy must explain the backend will reject the
  rest. WORKING overlay until command returns.

**Tests**

- Tag/remove/prune go through fake API with the same option shapes as
  `internal/backend/images`.
- Prune UI refuses empty / dangling-false before calling the API
  (defense in depth; backend still validates).
- Repeat key ignored while pending.
- `not_found` clears selection and requests list.

## Increment 4.3 — Pull job

**Code**

- Pull overlay: reference + optional platform `os/arch[/variant]`
  (empty platform allowed). Validate platform in the TUI the same way
  the backend does; keep editor open on `invalid_input`.
- `Images().Pull(sessionOrPageWaitCtx, ref, opts)` — wait context may
  end on page leave but **job continues**; register handle on the root
  tracker immediately on success.
- Show newest progress in tracker overlay and, if Images is active, a
  compact pull line. Do not parse progress statuses as an enum.
- Page-bus `JobProgressed` with local JobID: merge/ignore.
- Other-session pull: if subscribed, show via page events only.

**Tests**

- Fake `Job` with progress then `Wait`.
- Capacity/conflict: no tracker entry, editor stays, error notice.
- Tab away during pull: tracker still completes; Images model may
  deactivate without canceling the job.
- Unknown progress status string does not crash.

## Increment 4.4 — Root wiring, docs, manual

**Code**

- `Application.Images() *images.API` (or a narrow TUI interface in the
  images package, like Containers).
- Tab `4` activates Images; placeholder only for Compose.
- Header activity + footer help/status.

**Docs**

- TODO Slice 4 boxes; README Images keys; testing.md Images manual
  paragraph (fixture tags only; never prune unlabeled images).

**Manual**

- Loopback SSH: list, filter, details, history, tag, remove, **label- or
  dangling-true prune only on fixture images**, pull only if you accept
  network/registry use (otherwise skip pull in manual QA and rely on
  unit tests). Two sessions: different selection, shared list updates.

**Commit:** `Implement TUI Slice 4 (Images page)`  
(or tracker commit + this commit if split).

---

# Slice 5 — Volumes

Backend: `RequestDetails`, `RequestAttachments`, `Create`, `Remove`,
`Prune`. Events: `ListUpdated` (includes **Warnings**), `DetailsUpdated`,
`AttachmentsUpdated`. Identity = volume **name**.

## Increment 5.1 — List, warnings, details, attachments

- Show `Warnings` without dropping rows.
- `UsageKnown == false` renders as unknown (`—` / “unknown”), **never**
  `0`.
- Selection requests details; attachments panel requests
  `RequestAttachments` (visible with details or via `a`).
- Ignore mismatched volume names.

**Tests:** warnings visible; unknown usage; keyed isolation; overflow
re-requests details+attachments if visible.

## Increment 5.2 — Create, remove, prune

- Create form: name, driver, labels, driver options (keep fields
  minimal but complete enough for the backend struct).
- Remove: confirm; in-use → `conflict`, keep selection, explain retry
  after refresh.
- Prune: **at least one label**; `All` only broadens named volumes
  **still matching that label**. Copy must say that. No zero prune.

**Tests:** prune without labels never calls API; `All` still requires
labels; conflict mapping; confirmation overlay; WORKING overlay.

## Increment 5.3 — Root, docs, manual

- Wire tab `5`.
- TODO/README/testing.md.
- Manual: uniquely labeled disposable volumes only; sentinel volume
  with a different label must survive prune.

**Commit:** `Implement TUI Slice 5 (Volumes page)`

---

# Slice 6 — Networks

Backend: `RequestDetails`, `RequestConnections`, `Create`, `Remove`,
`Prune`, `Connect`, `Disconnect`. Identity = network ID (display name
in the list). Connections include MAC, IPv4, IPv6.

## Increment 6.1 — List, details, IPAM, connections

- Details show IPAM configs and options.
- Connections panel: addresses; empty vs loading vs error.
- Unknown/blank addresses as unknown, not `0.0.0.0` invented data
  (render what the DTO has).

**Tests:** keyed isolation; address rendering; overflow.

## Increment 6.2 — Create, remove, connect, disconnect, prune

- Create form with field-level validation (`invalid_input` stays in
  editor).
- Remove: confirm. Active endpoints → `conflict` (backend translates
  Docker 29 permission-denied). Copy: disconnect endpoints, not
  “authorization failed”.
- Connect/disconnect forms: container ID, optional IPv4/IPv6/aliases;
  disconnect confirm if Force.
- Prune: **Until and/or labels**; never empty filters.

**Tests:** conflict wording; prune guard; connect/disconnect fake API
shapes; confirmation.

## Increment 6.3 — Root, docs, manual

- Tab `6`.
- Manual: labeled disposable networks/endpoints only; do not delete
  `bridge`/`host`/`none`.

**Commit:** `Implement TUI Slice 6 (Networks page)`

---

# Slice 7 — Events

No domain accessor. `Subscribe(ctx, PageEvents, filter)` +
`RequestRefresh(PageEvents)`.

Payloads: `events.RecentUpdated` (newest-first window from process
history) and `backend.DockerEventObserved` (live). Also failures and
overflow.

## Increment 7.1 — Subscribe, recent window, live feed

- Same page lifecycle as others: subscribe (types: recent, observed,
  failed, overflow) **then** refresh.
- Bound displayed rows (client-side cap; history is already bounded in
  the hub). Newest-first.
- Sanitize attributes. No pause, no paused buffer (v1).

**Tests:** recent applied; live append/order; overflow → request recent
again; closed subscription while leaving is not an error.

## Increment 7.2 — Filters and session-local clear

- Session filters: resource, resource ID, action, Compose project.
- Changing Docker filter dimensions requires **resubscribe** (filters
  copied at `Subscribe`). Order: subscribe replacement, then close old,
  then `RequestRefresh` to reconcile; de-dupe display by normalized
  fields/time if duplicates appear during handover.
- Clear (`c` or documented key): wipe **session rows only**. Do not
  call any backend clear. Live feed continues.

**Tests:** filter resubscribe; clear does not empty a second model’s
rows; two models independent; untrusted attribute sanitization.

## Increment 7.3 — Root, docs, manual

- Tab `7`.
- Manual: start/stop a disposable container; Events updates; Containers
  or Dashboard also update; second session different filter/clear.

**Commit:** `Implement TUI Slice 7 (Events page)`

---

# Slice 8 — System

Backend: `RequestRefresh(PageSystem)` → version/info;
`RequestDiskUsage()` → targeted `DiskUsageUpdated`. Prunes return
`PruneResult{Command, Report}`.

`SystemPruneConfirmation = "PRUNE ALL UNUSED DOCKER RESOURCES"`.
`AllowSystemPrune` is bootstrap-only and **not** exposed on the API.

**Accepted frontend stance (no backend change):**

- Do **not** add `AllowSystemPrune()` in this series.
- Show a System prune form. If submit returns `permission_denied`,
  render **unavailable/policy** and disable further broad prune in this
  session (or keep the form with a persistent policy notice).
- If prune Docker-succeeds but joined refresh submission errors, the
  report may be missing (`PruneResult` with error). Show command
  metadata + “final disk state uncertain until refresh succeeds”; keep
  last disk-usage data stale. **Do not** invent a report. Log this as
  deferred backend structured outcome (already in `plan.md`).

Unknown usage: never render as confirmed zero (same as Dashboard/Volumes).

## Increment 8.1 — Info + detailed disk usage

- Base panel: version, host info, plugin/runtime summaries as space
  allows; truncate.
- Targeted disk-usage panel: aggregates + per-item lists with
  unknown/zero rules. Request disk usage on page enter (visible panel)
  and again after successful prune / overflow / relevant base update if
  still visible. No disk-usage timer.

**Tests:** keyed `DiskUsageUpdated`; failure preserves last info;
unknown bytes; sanitization; overflow recovery.

## Increment 8.2 — Resource prunes + system prune

- Four scoped forms: containers/images/volumes/networks. Same safety
  as backend:
  - containers/networks: until and/or labels
  - images: not zero, not dangling-false
  - volumes: at least one label; `All` does not drop the label
    requirement
- Render typed `PruneReport` (deleted IDs, space reclaimed) on success.
- Broad system prune: exact token field (no silent pre-fill that
  submits on accident—user must type it), separate volumes opt-in
  checkbox, strongest confirmation wording.
- WORKING overlay; do not allow a second prune while pending.

**Tests:** every prune guard; token mismatch `invalid_input`;
permission_denied → policy state; report rendering; joined error
without report.

## Increment 8.3 — Root, docs, manual

- Tab `8`.
- TODO Slice 8; README; testing.md (scoped prune on labeled fixtures;
  **never** broad system prune on a shared daemon).
- Optional: note in plan.md that TUI accepted the prune-report-on-
  refresh-error limitation.

**Commit:** `Implement TUI Slice 8 (System page)`

---

# After Slice 8 (not this series, but do not contradict)

- TUI Slice 3: Compose page **reusing** the job tracker from Slice 4.
- Final TUI quality gates in TODO (full `make check`, two-session
  pass on all pages, leak/race, `main.go` already on Wish—confirm no
  leftover demo).
- Backend deferrals below.

---

# Backend deferral log (do not implement now)

Record these in commit messages / TODO only as “known, later”:

1. **Joined command/refresh errors** — TUI cannot say which phase
   failed. Same as Containers.
2. **System prune report omitted** when executor joins a refresh
   submission error after a successful prune.
3. **`AllowSystemPrune` not readable from TUI** — policy discovered via
   `permission_denied`. A getter would be a small API addition later.
4. **Job reattachment / history** after SSH disconnect.
5. **Command/request correlation IDs** for rapid reselection (A→B→A).
6. **Interactive exec** (container/Compose) as bidirectional streams.
7. **Refresh Manager read-worker pool**.
8. **Dashboard host resources**.

If implementation discovers a **bug** in existing backend behavior
(wrong refresh keys, missing event), stop and note it here; do not
silently “tweak” backend in a TUI commit unless the user later asks.

---

# Suggested key map (pages 4–8)

Keep global keys from `plan.md`. Page keys should stay in the footer
Help string (Containers style), not a hidden Actions view.

Proposed starting point (adjust only if a collision appears; then
update `plan.md` + README together):

| Key | Images | Volumes | Networks | Events | System |
| --- | --- | --- | --- | --- | --- |
| `f` | filter | filter | filter | filter (may resubscribe) | — |
| `i` / `enter` | details | details | details | — | disk usage focus |
| `h` | history | — | — | — | — |
| `a` | — | attachments | connections | — | — |
| `t` | tag | — | — | — | — |
| `d` | remove | remove | remove | — | — |
| `c` | — | create | create / connect* | clear rows | — |
| `p` | prune / pull* | prune | prune | — | prune forms |
| `u` | pull | — | — | — | — |
| `x` | — | — | disconnect | — | — |
| `J` | session jobs overlay (root) | same | same | same | same |
| `r` | base refresh | same | same | same | same |

\*If `p` is overloaded, prefer `u` for pull and `p` for prune (Images).
Document the final map in Help() and README.

`J` is root-level: works on any implemented tab when the tracker has
jobs or to inspect empty state.

---

# Implementation order (strict)

```text
[optional] Commit remaining Containers polish
Slice 4.0  job tracker
Slice 4.1  Images list/details/history     ── tests ──
Slice 4.2  Images tag/remove/prune         ── tests ──
Slice 4.3  Images pull + tracker           ── tests ──
Slice 4.4  root + docs + manual            ── commit Slice 4
Slice 5.1 … 5.3                            ── commit Slice 5
Slice 6.1 … 6.3                            ── commit Slice 6
Slice 7.1 … 7.3                            ── commit Slice 7
Slice 8.1 … 8.3                            ── commit Slice 8
Delete this file in a small follow-up commit or the Slice 8 commit
```

Do not start Slice 5 until Slice 4 is committed and the Images tab is
usable over loopback SSH. Same gate between later slices.

## Copy-paste review before every commit

- [ ] No `internal/tui` import of Moby/Compose SDK.
- [ ] No backend file changes.
- [ ] Compose tab still placeholder.
- [ ] `gofmt`, `vet`, `go test ./internal/tui/...`.
- [ ] TODO/README/testing.md match the new tab.
- [ ] Destructive UI cannot submit unrestricted prune.
- [ ] Job cancel is confirm-only; tab switch does not cancel.
- [ ] This temporary plan’s deferral log updated if a new backend gap
      appeared.
