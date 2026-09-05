// Package compose implements the page API, typed updates, jobs, and streams
// used by the Compose tab.
package compose

import (
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const (
	RefreshKindList    backend.RefreshKind = "compose.projects"
	RefreshKindDetails backend.RefreshKind = "compose.project"

	EventProjectsUpdated backend.EventType = "compose_projects_updated"
	EventProjectUpdated  backend.EventType = "compose_project_updated"
)

// ProjectSpec identifies a Compose definition used by file-based operations.
// Every config file must be inside a root configured by application bootstrap.
type ProjectSpec struct {
	Name        string
	ConfigFiles []string
	Profiles    []string
}

// SaveConfigOptions is the complete caller input for a managed Compose file.
// The backend derives the path; callers cannot choose an arbitrary filename.
type SaveConfigOptions struct {
	ProjectName string
	Content     string
}

// ConfigDocument is one managed default Compose file returned as plain text.
type ConfigDocument struct {
	ProjectName string
	Path        string
	Content     string
}

type ProjectSummary struct {
	Name        string
	Status      string
	ConfigFiles []string
	Reason      string
}

type Service struct {
	Name       string
	Image      string
	Command    []string
	Profiles   []string
	DependsOn  []string
	Replicas   int
	Desired    int
	Containers []string
}

type Container struct {
	ID       string
	Name     string
	Service  string
	Image    string
	State    string
	Status   string
	Health   string
	ExitCode int
	Created  time.Time
}

type ProjectDetails struct {
	Name        string
	Status      string
	WorkingDir  string
	ConfigFiles []string
	Services    []Service
	Containers  []Container
}

type ProjectsUpdated struct {
	Projects []ProjectSummary
}

func (ProjectsUpdated) EventType() backend.EventType { return EventProjectsUpdated }

func (update ProjectsUpdated) CloneEventPayload() backend.EventPayload {
	update.Projects = cloneProjectSummaries(update.Projects)
	return update
}

type ProjectUpdated struct {
	Project ProjectDetails
}

func (ProjectUpdated) EventType() backend.EventType { return EventProjectUpdated }

func (update ProjectUpdated) CloneEventPayload() backend.EventPayload {
	update.Project = cloneProjectDetails(update.Project)
	return update
}

type ServiceOptions struct {
	Services []string
}

type StopOptions struct {
	Services []string
	Timeout  time.Duration
}

type RestartOptions struct {
	Services []string
	Timeout  time.Duration
	NoDeps   bool
}

type ScaleOptions struct {
	Service  string
	Replicas int
}

type UpOptions struct {
	Services      []string
	RemoveOrphans bool
}

type DownOptions struct {
	Services      []string
	RemoveOrphans bool
	Volumes       bool
	Timeout       time.Duration
}

type PullOptions struct {
	Services       []string
	IgnoreFailures bool
}

type BuildOptions struct {
	Services []string
	Pull     bool
	NoCache  bool
}

type LogsOptions struct {
	Services   []string
	Follow     bool
	Tail       int
	Since      time.Time
	Until      time.Time
	Timestamps bool
}

type LogSource string

const (
	LogStdout LogSource = "stdout"
	LogStderr LogSource = "stderr"
	LogStatus LogSource = "status"
)

type LogEntry struct {
	Container string
	Source    LogSource
	Data      string
}

func cloneProjectSummaries(values []ProjectSummary) []ProjectSummary {
	cloned := make([]ProjectSummary, len(values))
	for index, value := range values {
		value.ConfigFiles = append([]string(nil), value.ConfigFiles...)
		cloned[index] = value
	}
	return cloned
}

func cloneProjectDetails(value ProjectDetails) ProjectDetails {
	value.ConfigFiles = append([]string(nil), value.ConfigFiles...)
	value.Containers = append([]Container(nil), value.Containers...)
	value.Services = append([]Service(nil), value.Services...)
	for index := range value.Services {
		value.Services[index].Command = append([]string(nil), value.Services[index].Command...)
		value.Services[index].Profiles = append([]string(nil), value.Services[index].Profiles...)
		value.Services[index].DependsOn = append([]string(nil), value.Services[index].DependsOn...)
		value.Services[index].Containers = append([]string(nil), value.Services[index].Containers...)
	}
	return value
}
