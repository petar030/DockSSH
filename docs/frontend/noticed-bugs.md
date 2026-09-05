# Noticed frontend bugs

This is a short register of observed frontend problems that are intentionally
deferred. Each item should state the visible failure, its likely cause, and the
agreed direction before implementation begins.

## High-volume live logs can overwhelm the TUI

**Observed:** A container or Compose service that writes stdout very quickly can
make the log overlay slow to refresh, prevent reliable scrolling, and leave the
active filter apparently limited to older lines until the overlay is reopened.

**Likely cause:** The current backend stream producer writes entries into a
bounded channel and Bubble Tea receives those entries as frequent update
messages. At a sufficiently high output rate, rendering and input handling
cannot keep up, which also backpressures the stream.

**Planned resolution:** Keep Docker-specific opening and decoding inside the
page API, but return a typed pull-based stream reader. The active TUI log
overlay will own one collector goroutine that continuously drains that reader
into a bounded session-local history. Bubble Tea will receive a snapshot at a
fixed rendering cadence (roughly 10--15 FPS), rather than one message per log
entry. Scrolling away from the bottom will anchor the displayed history while
collection continues; `G` resumes following. Filtering will apply to the most
recent retained snapshot. The UI may display a small notice if retained history
is evicted while the user is reading older lines.

**Scope:** Container logs and Compose logs. This does not apply to the
process-wide Docker event listener.
