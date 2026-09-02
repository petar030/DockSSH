// Package networks implements the page API, typed updates and commands used by
// the Networks tab.
package networks

import (
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

const (
	RefreshKindList        backend.RefreshKind = "networks.list"
	RefreshKindDetails     backend.RefreshKind = "network.details"
	RefreshKindConnections backend.RefreshKind = "network.connections"

	EventListUpdated        backend.EventType = "network_list_updated"
	EventDetailsUpdated     backend.EventType = "network_details_updated"
	EventConnectionsUpdated backend.EventType = "network_connections_updated"
)

// Filter is session-local list filtering. The backend always publishes the
// complete authoritative network list.
type Filter struct {
	Text    string
	Drivers []string
	Scopes  []string
	Labels  map[string]string
}

type Summary struct {
	ID         string
	Name       string
	Created    time.Time
	Scope      string
	Driver     string
	EnableIPv4 bool
	EnableIPv6 bool
	Internal   bool
	Attachable bool
	Ingress    bool
	ConfigOnly bool
	Labels     map[string]string
}

type ListUpdated struct {
	Networks []Summary
}

func (ListUpdated) EventType() backend.EventType { return EventListUpdated }

func (update ListUpdated) CloneEventPayload() backend.EventPayload {
	update.Networks = cloneSummaries(update.Networks)
	return update
}

type IPAMConfig struct {
	Subnet       string
	IPRange      string
	Gateway      string
	AuxAddresses map[string]string
}

type Details struct {
	Summary
	IPAMDriver  string
	IPAMOptions map[string]string
	IPAM        []IPAMConfig
	Options     map[string]string
	ConfigFrom  string
}

type DetailsUpdated struct {
	Network Details
}

func (DetailsUpdated) EventType() backend.EventType { return EventDetailsUpdated }

func (update DetailsUpdated) CloneEventPayload() backend.EventPayload {
	update.Network = cloneDetails(update.Network)
	return update
}

type Connection struct {
	ContainerID   string
	ContainerName string
	EndpointID    string
	MACAddress    string
	IPv4Address   string
	IPv6Address   string
}

type ConnectionsUpdated struct {
	NetworkID   string
	Connections []Connection
}

func (ConnectionsUpdated) EventType() backend.EventType { return EventConnectionsUpdated }

func (update ConnectionsUpdated) CloneEventPayload() backend.EventPayload {
	update.Connections = append([]Connection(nil), update.Connections...)
	return update
}

type CreateIPAMConfig struct {
	Subnet       string
	IPRange      string
	Gateway      string
	AuxAddresses map[string]string
}

type CreateOptions struct {
	Name        string
	Driver      string
	Scope       string
	EnableIPv4  *bool
	EnableIPv6  *bool
	Internal    bool
	Attachable  bool
	Options     map[string]string
	Labels      map[string]string
	IPAMDriver  string
	IPAMOptions map[string]string
	IPAM        []CreateIPAMConfig
}

type RemoveOptions struct{}

// PruneOptions must contain at least one explicit filter.
type PruneOptions struct {
	Until  string
	Labels map[string]string
}

type ConnectOptions struct {
	ContainerID string
	IPv4Address string
	IPv6Address string
	Aliases     []string
	DriverOpts  map[string]string
	GwPriority  int
}

type DisconnectOptions struct {
	ContainerID string
	Force       bool
}

func cloneSummaries(values []Summary) []Summary {
	cloned := make([]Summary, len(values))
	for index, value := range values {
		value.Labels = cloneStrings(value.Labels)
		cloned[index] = value
	}
	return cloned
}

func cloneDetails(value Details) Details {
	value.Summary.Labels = cloneStrings(value.Summary.Labels)
	value.IPAMOptions = cloneStrings(value.IPAMOptions)
	value.Options = cloneStrings(value.Options)
	value.IPAM = cloneIPAM(value.IPAM)
	return value
}

func cloneIPAM(values []IPAMConfig) []IPAMConfig {
	cloned := make([]IPAMConfig, len(values))
	for index, value := range values {
		value.AuxAddresses = cloneStrings(value.AuxAddresses)
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
