package dashboard

import (
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// EngineSummary contains the small set of Engine facts shown on Dashboard.
type EngineSummary struct {
	Available       bool
	Name            string
	ServerVersion   string
	APIVersion      string
	OperatingSystem string
	Architecture    string
	CPUs            int
	MemoryBytes     int64
	SystemTime      string
}

// ResourceCounts contains the process-wide counts shown on Dashboard.
type ResourceCounts struct {
	Containers        int
	ContainersRunning int
	ContainersPaused  int
	ContainersStopped int
	Images            int
	Volumes           int
	Networks          int
}

// ResourceDiskUsage summarizes one Docker resource category in bytes.
type ResourceDiskUsage struct {
	Count            int64
	Active           int64
	TotalBytes       int64
	ReclaimableBytes int64
}

// DiskUsage contains all categories returned by Docker's system disk-usage API.
type DiskUsage struct {
	Containers ResourceDiskUsage
	Images     ResourceDiskUsage
	Volumes    ResourceDiskUsage
	BuildCache ResourceDiskUsage
}

// RecentEvent is the compact event row rendered on Dashboard.
type RecentEvent struct {
	Time       time.Time
	Resource   string
	ResourceID string
	Project    string
	Action     string
}

// SummaryUpdated replaces all data displayed by one Dashboard session.
type SummaryUpdated struct {
	Engine       EngineSummary
	Resources    ResourceCounts
	DiskUsage    DiskUsage
	RecentEvents []RecentEvent
}

func (SummaryUpdated) EventType() backend.EventType { return EventSummaryUpdated }

func (summary SummaryUpdated) CloneEventPayload() backend.EventPayload {
	summary.RecentEvents = append([]RecentEvent(nil), summary.RecentEvents...)
	return summary
}
