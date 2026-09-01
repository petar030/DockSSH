// Package images implements the page API, typed updates, commands, and pull
// jobs used by the Images tab.
package images

import (
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const (
	RefreshKindList    backend.RefreshKind = "images.list"
	RefreshKindDetails backend.RefreshKind = "image.details"
	RefreshKindHistory backend.RefreshKind = "image.history"

	EventListUpdated    backend.EventType = "image_list_updated"
	EventDetailsUpdated backend.EventType = "image_details_updated"
	EventHistoryUpdated backend.EventType = "image_history_updated"
)

// Filter is session-local Images-list filtering criteria. The backend always
// publishes the complete authoritative image list.
type Filter struct {
	Text     string
	Dangling *bool
	Labels   map[string]string
}

type Summary struct {
	ID          string
	ParentID    string
	RepoTags    []string
	RepoDigests []string
	Created     time.Time
	Size        int64
	SharedSize  int64
	Containers  int64
	Labels      map[string]string
}

type ListUpdated struct {
	Images []Summary
}

func (ListUpdated) EventType() backend.EventType { return EventListUpdated }

func (update ListUpdated) CloneEventPayload() backend.EventPayload {
	update.Images = cloneSummaries(update.Images)
	return update
}

type Details struct {
	ID              string
	RepoTags        []string
	RepoDigests     []string
	Comment         string
	Created         time.Time
	Author          string
	Architecture    string
	Variant         string
	OS              string
	OSVersion       string
	Size            int64
	User            string
	Entrypoint      []string
	Command         []string
	WorkingDir      string
	Environment     []string
	ExposedPorts    []string
	Volumes         []string
	Labels          map[string]string
	RootFSType      string
	Layers          []string
	GraphDriver     string
	GraphDriverData map[string]string
}

type DetailsUpdated struct {
	Image Details
}

func (DetailsUpdated) EventType() backend.EventType { return EventDetailsUpdated }

func (update DetailsUpdated) CloneEventPayload() backend.EventPayload {
	update.Image = cloneDetails(update.Image)
	return update
}

type HistoryEntry struct {
	ID        string
	Created   time.Time
	CreatedBy string
	Comment   string
	Tags      []string
	Size      int64
}

type HistoryUpdated struct {
	ImageID string
	Entries []HistoryEntry
}

func (HistoryUpdated) EventType() backend.EventType { return EventHistoryUpdated }

func (update HistoryUpdated) CloneEventPayload() backend.EventPayload {
	update.Entries = cloneHistory(update.Entries)
	return update
}

type TagOptions struct {
	Reference string
}

type RemoveOptions struct {
	Force         bool
	PruneChildren bool
}

// PruneOptions deliberately has no unrestricted zero value. At least one
// narrowing filter must be supplied before the command is accepted.
type PruneOptions struct {
	Dangling *bool
	Until    string
	Labels   map[string]string
}

type PullOptions struct {
	Platform string
}

func cloneSummaries(values []Summary) []Summary {
	cloned := make([]Summary, len(values))
	for index, value := range values {
		value.RepoTags = append([]string(nil), value.RepoTags...)
		value.RepoDigests = append([]string(nil), value.RepoDigests...)
		value.Labels = cloneStrings(value.Labels)
		cloned[index] = value
	}
	return cloned
}

func cloneDetails(value Details) Details {
	value.RepoTags = append([]string(nil), value.RepoTags...)
	value.RepoDigests = append([]string(nil), value.RepoDigests...)
	value.Entrypoint = append([]string(nil), value.Entrypoint...)
	value.Command = append([]string(nil), value.Command...)
	value.Environment = append([]string(nil), value.Environment...)
	value.ExposedPorts = append([]string(nil), value.ExposedPorts...)
	value.Volumes = append([]string(nil), value.Volumes...)
	value.Labels = cloneStrings(value.Labels)
	value.Layers = append([]string(nil), value.Layers...)
	value.GraphDriverData = cloneStrings(value.GraphDriverData)
	return value
}

func cloneHistory(values []HistoryEntry) []HistoryEntry {
	cloned := make([]HistoryEntry, len(values))
	for index, value := range values {
		value.Tags = append([]string(nil), value.Tags...)
		cloned[index] = value
	}
	return cloned
}

func cloneStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
