package backend

import (
	"context"
	"time"
)

// Backend is the stable, in-process entry point exercised by the conformance
// suite. Domain-specific APIs will be added to this interface test-first as
// their contracts are frozen.
type Backend interface {
	Refresh(context.Context, RefreshScope, RefreshReason) (RefreshResult, error)
	GetSnapshotVersion(context.Context, RefreshScope) (SnapshotMeta, error)
	SubscribeStateChanges(context.Context, EventFilter) (Subscription, error)
	Close(context.Context) error
}

// ResourceType identifies a backend domain.
type ResourceType string

const (
	ResourceDashboard ResourceType = "dashboard"
	ResourceContainer ResourceType = "container"
	ResourceCompose   ResourceType = "compose"
	ResourceImage     ResourceType = "image"
	ResourceVolume    ResourceType = "volume"
	ResourceNetwork   ResourceType = "network"
	ResourceEvent     ResourceType = "event"
	ResourceSystem    ResourceType = "system"
)

// SnapshotView distinguishes projections within a resource domain.
type SnapshotView string

const (
	ViewSummary   SnapshotView = "summary"
	ViewDetails   SnapshotView = "details"
	ViewHistory   SnapshotView = "history"
	ViewProcesses SnapshotView = "processes"
	ViewDiskUsage SnapshotView = "disk_usage"
	ViewEngine    SnapshotView = "engine"
)

// RefreshScope identifies one comparable unit of shared state.
type RefreshScope struct {
	Resource ResourceType
	ID       string
	View     SnapshotView
}

// RefreshReason records why shared state was reloaded.
type RefreshReason string

const (
	RefreshStartup           RefreshReason = "startup"
	RefreshScheduled         RefreshReason = "scheduled"
	RefreshDockerEvent       RefreshReason = "docker_event"
	RefreshCommand           RefreshReason = "command"
	RefreshJobCompleted      RefreshReason = "job_completed"
	RefreshManual            RefreshReason = "manual"
	RefreshClientAutoRefresh RefreshReason = "client_auto_refresh"
)

// SnapshotMeta describes the current state and freshness of one snapshot.
type SnapshotMeta struct {
	Scope          RefreshScope
	Version        uint64
	RefreshedAt    time.Time
	Reason         RefreshReason
	Stale          bool
	LastRefreshErr error
}

// RefreshResult is returned to an explicit refresh caller.
type RefreshResult struct {
	Scope       RefreshScope
	Version     uint64
	Changed     bool
	Coalesced   bool
	RefreshedAt time.Time
}

// EventType identifies a notification published on the application event bus.
type EventType string

const (
	EventDockerObserved  EventType = "docker_event_observed"
	EventSnapshotUpdated EventType = "snapshot_updated"
	EventRefreshFailed   EventType = "refresh_failed"
	EventOverflow        EventType = "subscriber_overflow"
)

// AppEvent is a typed, process-local application notification.
type AppEvent struct {
	Sequence        uint64
	Type            EventType
	Time            time.Time
	ResourceType    ResourceType
	ResourceID      string
	Project         string
	Scope           RefreshScope
	SnapshotVersion uint64
	RefreshReason   RefreshReason
	Action          string
	Attributes      map[string]string
}

// EventFilter selects events for one independent subscriber. Empty fields do
// not restrict their corresponding dimension.
type EventFilter struct {
	Types         []EventType
	ResourceTypes []ResourceType
	ResourceID    string
	Project       string
	Scopes        []RefreshScope
}

// Subscription owns a bounded event channel until Close or context
// cancellation. Close must be safe to call more than once.
type Subscription interface {
	Events() <-chan AppEvent
	Close() error
}
