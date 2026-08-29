package backend

import (
	"context"
	"strings"
	"time"
)

// Backend is the in-process entry point used by every SSH session. Domain APIs
// are added to it one slice at a time as their contracts are frozen.
type Backend interface {
	RequestRefresh(Page) error
	Subscribe(context.Context, Page, EventFilter) (Subscription, error)
	Close(context.Context) error
}

// RefreshRequester records backend-owned refresh work by its complete key.
// Requests confirm acceptance; results arrive through the relevant page bus.
type RefreshRequester interface {
	Request(RefreshKey, RefreshReason) error
}

// Page identifies one TUI tab and its independent Event Bus.
type Page string

const (
	PageDashboard  Page = "dashboard"
	PageContainers Page = "containers"
	PageCompose    Page = "compose"
	PageImages     Page = "images"
	PageVolumes    Page = "volumes"
	PageNetworks   Page = "networks"
	PageEvents     Page = "events"
	PageSystem     Page = "system"
)

func (page Page) Valid() bool {
	switch page {
	case PageDashboard, PageContainers, PageCompose, PageImages,
		PageVolumes, PageNetworks, PageEvents, PageSystem:
		return true
	default:
		return false
	}
}

// RefreshKind names one internal authoritative Docker or Compose read operation.
// TUI sessions request a page refresh and do not construct refresh keys.
type RefreshKind string

const RefreshKindBackendStatus RefreshKind = "backend.status"

// RefreshKey identifies a refresh operation and, optionally, one domain item.
// It stays comparable for refresh routing, scheduling, and filter lookups.
type RefreshKey struct {
	Kind RefreshKind
	ID   string
}

// Valid reports whether the key names a refresh operation. Domain-specific
// constructors added by later slices validate whether an ID is required.
func (key RefreshKey) Valid() bool {
	return strings.TrimSpace(string(key.Kind)) != ""
}

// RefreshReason records why authoritative state was loaded.
type RefreshReason string

const (
	RefreshStartup          RefreshReason = "startup"
	RefreshScheduled        RefreshReason = "scheduled"
	RefreshDockerEvent      RefreshReason = "docker_event"
	RefreshCommand          RefreshReason = "command"
	RefreshJobCompleted     RefreshReason = "job_completed"
	RefreshManual           RefreshReason = "manual"
	RefreshOverflowRecovery RefreshReason = "overflow_recovery"
)

// RefreshPolicy schedules one process-wide refresh independently of session
// count.
type RefreshPolicy struct {
	Key      RefreshKey
	Interval time.Duration
}

// EventType identifies a typed Event Bus payload.
type EventType string

const (
	EventBackendStatusUpdated EventType = "backend_status_updated"
	EventDockerObserved       EventType = "docker_event_observed"
	EventRefreshFailed        EventType = "refresh_failed"
	EventSubscriberOverflow   EventType = "subscriber_overflow"
)

// EventPayload is one complete observation delivered to interested sessions.
// Payload values are immutable after publication.
type EventPayload interface {
	EventType() EventType
}

// EventEnvelope adds process-local delivery metadata to a typed payload.
type EventEnvelope struct {
	Sequence uint64
	Time     time.Time
	Key      RefreshKey
	Reason   RefreshReason
	Payload  EventPayload
}

// BackendStatusUpdated is the small real-Docker payload used by the shared
// foundation. User-facing domain payloads are added with their tab slices.
type BackendStatusUpdated struct {
	APIVersion   string
	OSType       string
	Experimental bool
}

func (BackendStatusUpdated) EventType() EventType { return EventBackendStatusUpdated }

// DockerEventObserved is one normalized Engine event retained by bounded event
// history and published to the Events page.
type DockerEventObserved struct {
	OccurredAt time.Time
	Resource   string
	ResourceID string
	Project    string
	Action     string
	Attributes map[string]string
}

func (DockerEventObserved) EventType() EventType { return EventDockerObserved }

func (event DockerEventObserved) CloneEventPayload() EventPayload {
	event.Attributes = cloneAttributes(event.Attributes)
	return event
}

func cloneAttributes(attributes map[string]string) map[string]string {
	if attributes == nil {
		return nil
	}
	cloned := make(map[string]string, len(attributes))
	for key, value := range attributes {
		cloned[key] = value
	}
	return cloned
}

// RefreshFailed tells observers that their existing local data may be stale.
type RefreshFailed struct {
	Err error
}

func (RefreshFailed) EventType() EventType { return EventRefreshFailed }

// SubscriberOverflow tells one slow observer to request authoritative data
// again. The subscription remains usable.
type SubscriberOverflow struct {
	DroppedSequence uint64
}

func (SubscriberOverflow) EventType() EventType { return EventSubscriberOverflow }

// EventFilter selects events for one independent subscriber. Empty fields do
// not restrict their corresponding dimension.
type EventFilter struct {
	Types []EventType
	Keys  []RefreshKey
}

// Subscription owns a bounded event channel until Close or context
// cancellation. Close is safe to call more than once.
type Subscription interface {
	Events() <-chan EventEnvelope
	Close() error
}

// AffectedResource identifies a resource touched by a command.
type AffectedResource struct {
	Kind string
	ID   string
}

// CommandResult describes an accepted short operation and the refreshes used
// to observe its final Engine state.
type CommandResult struct {
	OperationID  string
	Affected     []AffectedResource
	RefreshKeys  []RefreshKey
	Asynchronous bool
}

// CommandRequest describes one validated short operation submitted by a page
// API to the backend-owned command executor. Run must use the context supplied
// by the executor, never a TUI session context.
type CommandRequest struct {
	OperationID string
	Operation   string
	Affected    []AffectedResource
	RefreshKeys []RefreshKey
	Run         func(context.Context) error
}

// CommandRunner accepts short commands for backend-owned execution while the
// caller's context controls only submission and waiting for the result.
type CommandRunner interface {
	Run(context.Context, CommandRequest) (CommandResult, error)
}

// ProgressEvent is one ordered update from a long-running job.
type ProgressEvent struct {
	Sequence uint64
	Time     time.Time
	Status   string
	Resource string
	Current  int64
	Total    int64
	Message  string
}

// JobResult is the terminal result of a long-running command.
type JobResult struct {
	JobID       string
	Affected    []AffectedResource
	RefreshKeys []RefreshKey
	CompletedAt time.Time
}

// Job exposes progress, cancellation, and exactly one terminal outcome.
type Job interface {
	ID() string
	Progress() <-chan ProgressEvent
	Wait(context.Context) (JobResult, error)
	Cancel() error
}

// Stream owns ordered values and one terminal outcome. Done receives nil on
// normal completion or one error, then closes.
type Stream[T any] interface {
	Values() <-chan T
	Done() <-chan error
	Close() error
}
