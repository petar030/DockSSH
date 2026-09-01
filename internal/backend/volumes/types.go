// Package volumes implements the page API, typed updates, and commands used by
// the Volumes tab.
package volumes

import (
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const (
	RefreshKindList        backend.RefreshKind = "volumes.list"
	RefreshKindDetails     backend.RefreshKind = "volume.details"
	RefreshKindAttachments backend.RefreshKind = "volume.attachments"

	EventListUpdated        backend.EventType = "volume_list_updated"
	EventDetailsUpdated     backend.EventType = "volume_details_updated"
	EventAttachmentsUpdated backend.EventType = "volume_attachments_updated"
)

type Filter struct {
	Text   string
	Driver string
	Labels map[string]string
}

type Volume struct {
	Name           string
	Driver         string
	Scope          string
	Created        time.Time
	Mountpoint     string
	Labels         map[string]string
	Options        map[string]string
	Status         map[string]string
	UsageKnown     bool
	Size           int64
	ReferenceCount int64
}

type ListUpdated struct {
	Volumes  []Volume
	Warnings []string
}

func (ListUpdated) EventType() backend.EventType { return EventListUpdated }

func (update ListUpdated) CloneEventPayload() backend.EventPayload {
	update.Volumes = cloneVolumes(update.Volumes)
	update.Warnings = append([]string(nil), update.Warnings...)
	return update
}

type DetailsUpdated struct {
	Volume Volume
}

func (DetailsUpdated) EventType() backend.EventType { return EventDetailsUpdated }

func (update DetailsUpdated) CloneEventPayload() backend.EventPayload {
	update.Volume = cloneVolume(update.Volume)
	return update
}

type Attachment struct {
	ContainerID   string
	ContainerName string
	State         string
	Status        string
	Destination   string
	Mode          string
	ReadWrite     bool
	Propagation   string
	MountType     string
}

type AttachmentsUpdated struct {
	VolumeName  string
	Attachments []Attachment
}

func (AttachmentsUpdated) EventType() backend.EventType { return EventAttachmentsUpdated }

func (update AttachmentsUpdated) CloneEventPayload() backend.EventPayload {
	update.Attachments = append([]Attachment(nil), update.Attachments...)
	return update
}

type CreateOptions struct {
	Name          string
	Driver        string
	DriverOptions map[string]string
	Labels        map[string]string
}

type RemoveOptions struct {
	Force bool
}

// PruneOptions requires at least one label filter. All only controls whether
// named volumes matching those labels are eligible.
type PruneOptions struct {
	All    bool
	Labels map[string]string
}

func cloneVolumes(values []Volume) []Volume {
	cloned := make([]Volume, len(values))
	for index, value := range values {
		cloned[index] = cloneVolume(value)
	}
	return cloned
}

func cloneVolume(value Volume) Volume {
	value.Labels = cloneStrings(value.Labels)
	value.Options = cloneStrings(value.Options)
	value.Status = cloneStrings(value.Status)
	return value
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
