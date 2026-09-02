package networks

import (
	"context"
	"net/netip"
	"sort"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

// ReadRefresh is registered by bootstrap and called only by RefreshManager.
func (api *API) ReadRefresh(ctx context.Context, key backend.RefreshKey) (backend.EventPayload, error) {
	switch key.Kind {
	case RefreshKindList:
		if key.ID != "" {
			return nil, unsupportedRefresh(key)
		}
		return api.readList(ctx)
	case RefreshKindDetails:
		id, err := requireNetworkID("read network details", key.ID)
		if err != nil {
			return nil, err
		}
		return api.readDetails(ctx, id)
	case RefreshKindConnections:
		id, err := requireNetworkID("read network connections", key.ID)
		if err != nil {
			return nil, err
		}
		return api.readConnections(ctx, id)
	default:
		return nil, unsupportedRefresh(key)
	}
}

func (api *API) readList(ctx context.Context) (backend.EventPayload, error) {
	result, err := api.docker.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, classifyDockerError("list networks", "", err)
	}
	values := make([]Summary, 0, len(result.Items))
	for _, item := range result.Items {
		values = append(values, networkSummary(item.Network))
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Name == values[j].Name {
			return values[i].ID < values[j].ID
		}
		return values[i].Name < values[j].Name
	})
	return ListUpdated{Networks: values}, nil
}

func (api *API) readDetails(ctx context.Context, id string) (backend.EventPayload, error) {
	result, err := api.docker.NetworkInspect(ctx, id, client.NetworkInspectOptions{})
	if err != nil {
		return nil, classifyDockerError("inspect network", id, err)
	}
	value := result.Network
	details := Details{
		Summary: networkSummary(value.Network), IPAMDriver: value.IPAM.Driver,
		IPAMOptions: cloneStrings(value.IPAM.Options), Options: cloneStrings(value.Options), ConfigFrom: value.ConfigFrom.Network,
		IPAM: mapIPAM(value.IPAM.Config),
	}
	return DetailsUpdated{Network: details}, nil
}

func (api *API) readConnections(ctx context.Context, id string) (backend.EventPayload, error) {
	result, err := api.docker.NetworkInspect(ctx, id, client.NetworkInspectOptions{})
	if err != nil {
		return nil, classifyDockerError("read network connections", id, err)
	}
	values := make([]Connection, 0, len(result.Network.Containers))
	for containerID, endpoint := range result.Network.Containers {
		values = append(values, Connection{
			ContainerID: containerID, ContainerName: endpoint.Name, EndpointID: endpoint.EndpointID,
			MACAddress: endpoint.MacAddress.String(), IPv4Address: prefixString(endpoint.IPv4Address), IPv6Address: prefixString(endpoint.IPv6Address),
		})
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].ContainerName == values[j].ContainerName {
			return values[i].ContainerID < values[j].ContainerID
		}
		return values[i].ContainerName < values[j].ContainerName
	})
	return ConnectionsUpdated{NetworkID: result.Network.ID, Connections: values}, nil
}

func networkSummary(value network.Network) Summary {
	return Summary{
		ID: value.ID, Name: value.Name, Created: value.Created, Scope: value.Scope, Driver: value.Driver,
		EnableIPv4: value.EnableIPv4, EnableIPv6: value.EnableIPv6, Internal: value.Internal,
		Attachable: value.Attachable, Ingress: value.Ingress, ConfigOnly: value.ConfigOnly, Labels: cloneStrings(value.Labels),
	}
}

func mapIPAM(values []network.IPAMConfig) []IPAMConfig {
	result := make([]IPAMConfig, 0, len(values))
	for _, value := range values {
		aux := make(map[string]string, len(value.AuxAddress))
		for name, address := range value.AuxAddress {
			aux[name] = addressString(address)
		}
		result = append(result, IPAMConfig{
			Subnet: prefixString(value.Subnet), IPRange: prefixString(value.IPRange), Gateway: addressString(value.Gateway), AuxAddresses: aux,
		})
	}
	return result
}

func prefixString(value netip.Prefix) string {
	if !value.IsValid() {
		return ""
	}
	return value.String()
}

func addressString(value netip.Addr) string {
	if !value.IsValid() {
		return ""
	}
	return value.String()
}

func unsupportedRefresh(key backend.RefreshKey) error {
	return &backend.AppError{Code: backend.ErrorUnsupported, Operation: "read Networks refresh", Resource: string(key.Kind), ID: key.ID}
}
