// Package containers implements the page API, typed updates and session-owned
// streams used by the Containers tab.
package containers

import (
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const (
	RefreshKindList      backend.RefreshKind = "containers.list"
	RefreshKindDetails   backend.RefreshKind = "container.details"
	RefreshKindProcesses backend.RefreshKind = "container.processes"

	EventListUpdated      backend.EventType = "container_list_updated"
	EventDetailsUpdated   backend.EventType = "container_details_updated"
	EventProcessesUpdated backend.EventType = "container_processes_updated"
)

// Filter is session-local Containers-list filtering criteria. The backend
// publishes the authoritative list; each TUI applies its own filter.
type Filter struct {
	Text   string
	States []string
	Labels map[string]string
}

type Port struct {
	IP          string
	PrivatePort uint16
	PublicPort  uint16
	Protocol    string
}

type Summary struct {
	ID      string
	Names   []string
	Image   string
	ImageID string
	Command string
	Created time.Time
	State   string
	Status  string
	Health  string
	Ports   []Port
	Labels  map[string]string
}

type ListUpdated struct {
	Containers []Summary
}

func (ListUpdated) EventType() backend.EventType { return EventListUpdated }

func (update ListUpdated) CloneEventPayload() backend.EventPayload {
	update.Containers = cloneSummaries(update.Containers)
	return update
}

type State struct {
	Status     string
	Running    bool
	Paused     bool
	Restarting bool
	OOMKilled  bool
	Dead       bool
	PID        int
	ExitCode   int
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
	Health     string
}

type Mount struct {
	Type        string
	Name        string
	Source      string
	Destination string
	Driver      string
	Mode        string
	ReadWrite   bool
	Propagation string
}

type Network struct {
	Name        string
	NetworkID   string
	EndpointID  string
	IPAddress   string
	Gateway     string
	MACAddress  string
	IPv6Address string
}

type Details struct {
	ID           string
	Name         string
	Created      time.Time
	Image        string
	Path         string
	Args         []string
	RestartCount int
	Driver       string
	Platform     string
	Hostname     string
	User         string
	WorkingDir   string
	Environment  []string
	Labels       map[string]string
	State        State
	Mounts       []Mount
	Networks     []Network
}

type DetailsUpdated struct {
	Container Details
}

func (DetailsUpdated) EventType() backend.EventType { return EventDetailsUpdated }

func (update DetailsUpdated) CloneEventPayload() backend.EventPayload {
	update.Container = cloneDetails(update.Container)
	return update
}

type ProcessesUpdated struct {
	ContainerID string
	Titles      []string
	Rows        [][]string
}

func (ProcessesUpdated) EventType() backend.EventType { return EventProcessesUpdated }

func (update ProcessesUpdated) CloneEventPayload() backend.EventPayload {
	update.Titles = append([]string(nil), update.Titles...)
	update.Rows = cloneRows(update.Rows)
	return update
}

type StopOptions struct {
	Signal         string
	TimeoutSeconds *int
}

type RestartOptions struct {
	Signal         string
	TimeoutSeconds *int
}

type KillOptions struct {
	Signal string
}

type RenameOptions struct {
	Name string
}

type RemoveOptions struct {
	Force         bool
	RemoveVolumes bool
}

type LogsOptions struct {
	Follow     bool
	Tail       int
	Since      time.Time
	Until      time.Time
	Timestamps bool
	ShowStdout bool
	ShowStderr bool
}

type LogSource string

const (
	LogStdout LogSource = "stdout"
	LogStderr LogSource = "stderr"
)

// LogEntry is one ordered output chunk from a session-owned Docker log stream.
type LogEntry struct {
	Source LogSource
	Data   string
}

type StatsOptions struct {
	OneShot bool
}

type StatsSample struct {
	ContainerID   string
	Name          string
	ReadAt        time.Time
	CPUPercent    float64
	MemoryUsage   uint64
	MemoryLimit   uint64
	MemoryPercent float64
	NetworkRx     uint64
	NetworkTx     uint64
	BlockRead     uint64
	BlockWrite    uint64
	PIDs          uint64
}

func cloneSummaries(values []Summary) []Summary {
	cloned := make([]Summary, len(values))
	for index, value := range values {
		value.Names = append([]string(nil), value.Names...)
		value.Ports = append([]Port(nil), value.Ports...)
		value.Labels = cloneLabels(value.Labels)
		cloned[index] = value
	}
	return cloned
}

func cloneDetails(value Details) Details {
	value.Args = append([]string(nil), value.Args...)
	value.Environment = append([]string(nil), value.Environment...)
	value.Labels = cloneLabels(value.Labels)
	value.Mounts = append([]Mount(nil), value.Mounts...)
	value.Networks = append([]Network(nil), value.Networks...)
	return value
}

func cloneRows(rows [][]string) [][]string {
	cloned := make([][]string, len(rows))
	for index, row := range rows {
		cloned[index] = append([]string(nil), row...)
	}
	return cloned
}

func cloneLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}
	return cloned
}
