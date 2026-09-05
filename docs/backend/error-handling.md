# Error Handling

Public failures use `backend.AppError`, which provides a stable `ErrorCode`, operation, optional resource/ID, and wrapped implementation cause.

| Code | Meaning |
| --- | --- |
| `invalid_input` | Missing, malformed, unsafe, or unsupported option combination |
| `not_found` | Selected Docker resource does not exist |
| `conflict` | Queue/capacity conflict or resource currently cannot accept the operation |
| `permission_denied` | Docker endpoint or operation is not permitted |
| `daemon_unavailable` | Docker cannot be reached |
| `timeout` | Backend-owned operation deadline expired |
| `canceled` | Session wait, stream, job, or shutdown canceled work |
| `stream_closed` | Runtime/Event Hub already closed |
| `unsupported` | Capability is not supported by this backend/version |
| `internal` | Failure has no more specific stable category |

Page error adapters translate SDK/Compose errors without exposing SDK types. One known Docker behavior is that removing a network with active endpoints may arrive as an HTTP permission-denied response; the Networks adapter recognizes that message and returns application-level `conflict`, because the user action is blocked by current resource state rather than authorization.

Refresh errors are published as `RefreshFailed` on the routed page. Commands return their error to a caller that still waits and still request refresh after an attempted mutation. Jobs store the terminal error for `Wait` and publish it in `JobFinished`. Stream terminal errors arrive through `Done`.

Validation and queue/capacity rejection occur before Docker is called and therefore do not trigger a refresh.

Compose configuration editing follows the same contract. Invalid project names,
malformed YAML, and schema failures return `invalid_input`; a missing managed
configuration returns `not_found`; and attempts to escape the configured Compose
root or replace an unsafe filesystem object return `permission_denied`. Validation
diagnostics are bounded and do not expose Docker SDK types.
