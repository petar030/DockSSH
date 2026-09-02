package networks

import (
	"context"
	"net/netip"
	"sort"
	"strings"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

func (api *API) createRequest(options CreateOptions) (backend.CommandRequest, error) {
	name, err := requireNetworkID("create network", options.Name)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	dockerOptions, err := createNetworkOptions(options)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	return backend.CommandRequest{
		OperationID: "network.create", Operation: "create network",
		Affected: []backend.AffectedResource{{Kind: "network", ID: name}}, RefreshKeys: networkMutationRefreshKeys(),
		Run: func(ctx context.Context) error {
			_, err := api.docker.NetworkCreate(ctx, name, dockerOptions)
			return classifyDockerError("create network", name, err)
		},
	}, nil
}

func (api *API) removeRequest(id string, _ RemoveOptions) (backend.CommandRequest, error) {
	id, err := requireNetworkID("remove network", id)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	return backend.CommandRequest{
		OperationID: "network.remove", Operation: "remove network",
		Affected: []backend.AffectedResource{{Kind: "network", ID: id}}, RefreshKeys: networkMutationRefreshKeys(),
		Run: func(ctx context.Context) error {
			_, err := api.docker.NetworkRemove(ctx, id, client.NetworkRemoveOptions{})
			return classifyDockerError("remove network", id, err)
		},
	}, nil
}

func (api *API) pruneRequest(options PruneOptions) (backend.CommandRequest, error) {
	filters, err := networkPruneFilters(options)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	return backend.CommandRequest{
		OperationID: "network.prune", Operation: "prune networks",
		Affected: []backend.AffectedResource{{Kind: "networks", ID: "filtered"}}, RefreshKeys: networkMutationRefreshKeys(),
		Run: func(ctx context.Context) error {
			_, err := api.docker.NetworkPrune(ctx, client.NetworkPruneOptions{Filters: filters.Clone()})
			return classifyDockerError("prune networks", "filtered", err)
		},
	}, nil
}

func (api *API) connectRequest(networkID string, options ConnectOptions) (backend.CommandRequest, error) {
	networkID, err := requireNetworkID("connect network", networkID)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	containerID, err := requireContainerID("connect network", options.ContainerID)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	endpoint, err := endpointSettings(options)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	return backend.CommandRequest{
		OperationID: "network.connect", Operation: "connect network",
		Affected:    []backend.AffectedResource{{Kind: "network", ID: networkID}, {Kind: "container", ID: containerID}},
		RefreshKeys: endpointMutationRefreshKeys(networkID, containerID),
		Run: func(ctx context.Context) error {
			_, err := api.docker.NetworkConnect(ctx, networkID, client.NetworkConnectOptions{Container: containerID, EndpointConfig: endpoint})
			return classifyDockerError("connect network", networkID, err)
		},
	}, nil
}

func (api *API) disconnectRequest(networkID string, options DisconnectOptions) (backend.CommandRequest, error) {
	networkID, err := requireNetworkID("disconnect network", networkID)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	containerID, err := requireContainerID("disconnect network", options.ContainerID)
	if err != nil {
		return backend.CommandRequest{}, err
	}
	return backend.CommandRequest{
		OperationID: "network.disconnect", Operation: "disconnect network",
		Affected:    []backend.AffectedResource{{Kind: "network", ID: networkID}, {Kind: "container", ID: containerID}},
		RefreshKeys: endpointMutationRefreshKeys(networkID, containerID),
		Run: func(ctx context.Context) error {
			_, err := api.docker.NetworkDisconnect(ctx, networkID, client.NetworkDisconnectOptions{Container: containerID, Force: options.Force})
			return classifyDockerError("disconnect network", networkID, err)
		},
	}, nil
}

func networkMutationRefreshKeys() []backend.RefreshKey {
	return []backend.RefreshKey{{Kind: RefreshKindList}, {Kind: dashboard.RefreshKindSummary}}
}

func endpointMutationRefreshKeys(networkID, containerID string) []backend.RefreshKey {
	return []backend.RefreshKey{
		{Kind: RefreshKindList}, {Kind: RefreshKindDetails, ID: networkID}, {Kind: RefreshKindConnections, ID: networkID},
		{Kind: containers.RefreshKindList}, {Kind: containers.RefreshKindDetails, ID: containerID}, {Kind: dashboard.RefreshKindSummary},
	}
}

func createNetworkOptions(options CreateOptions) (client.NetworkCreateOptions, error) {
	labels, err := cleanMap("create network", "label", options.Labels, true)
	if err != nil {
		return client.NetworkCreateOptions{}, err
	}
	driverOptions, err := cleanMap("create network", "driver option", options.Options, false)
	if err != nil {
		return client.NetworkCreateOptions{}, err
	}
	ipamOptions, err := cleanMap("create network", "IPAM option", options.IPAMOptions, false)
	if err != nil {
		return client.NetworkCreateOptions{}, err
	}
	ipamConfigs := make([]network.IPAMConfig, 0, len(options.IPAM))
	for _, value := range options.IPAM {
		config, err := parseCreateIPAM(value)
		if err != nil {
			return client.NetworkCreateOptions{}, err
		}
		ipamConfigs = append(ipamConfigs, config)
	}
	var ipam *network.IPAM
	if driver := strings.TrimSpace(options.IPAMDriver); driver != "" || len(ipamOptions) > 0 || len(ipamConfigs) > 0 {
		ipam = &network.IPAM{Driver: driver, Options: ipamOptions, Config: ipamConfigs}
	}
	return client.NetworkCreateOptions{
		Driver: strings.TrimSpace(options.Driver), Scope: strings.TrimSpace(options.Scope), EnableIPv4: cloneBool(options.EnableIPv4),
		EnableIPv6: cloneBool(options.EnableIPv6), IPAM: ipam, Internal: options.Internal, Attachable: options.Attachable,
		Options: driverOptions, Labels: labels,
	}, nil
}

func parseCreateIPAM(value CreateIPAMConfig) (network.IPAMConfig, error) {
	operation := "create network"
	subnet, err := parseOptionalPrefix(operation, "IPAM subnet", value.Subnet)
	if err != nil {
		return network.IPAMConfig{}, err
	}
	ipRange, err := parseOptionalPrefix(operation, "IPAM range", value.IPRange)
	if err != nil {
		return network.IPAMConfig{}, err
	}
	gateway, err := parseOptionalAddress(operation, "IPAM gateway", value.Gateway)
	if err != nil {
		return network.IPAMConfig{}, err
	}
	if subnet.IsValid() {
		if ipRange.IsValid() && (!subnet.Contains(ipRange.Addr()) || subnet.Bits() > ipRange.Bits()) {
			return network.IPAMConfig{}, invalidNetworkInput(operation, "IPAM range")
		}
		if gateway.IsValid() && !subnet.Contains(gateway) {
			return network.IPAMConfig{}, invalidNetworkInput(operation, "IPAM gateway")
		}
	}
	aux := make(map[string]netip.Addr, len(value.AuxAddresses))
	for name, raw := range value.AuxAddresses {
		name = strings.TrimSpace(name)
		address, parseErr := parseOptionalAddress(operation, "IPAM auxiliary address", raw)
		if name == "" || parseErr != nil || !address.IsValid() || (subnet.IsValid() && !subnet.Contains(address)) {
			return network.IPAMConfig{}, invalidNetworkInput(operation, "IPAM auxiliary address")
		}
		aux[name] = address
	}
	return network.IPAMConfig{Subnet: subnet, IPRange: ipRange, Gateway: gateway, AuxAddress: aux}, nil
}

func endpointSettings(options ConnectOptions) (*network.EndpointSettings, error) {
	operation := "connect network"
	ipv4, err := parseOptionalAddress(operation, "IPv4 address", options.IPv4Address)
	if err != nil || (ipv4.IsValid() && !ipv4.Is4()) {
		return nil, invalidNetworkInput(operation, "IPv4 address")
	}
	ipv6, err := parseOptionalAddress(operation, "IPv6 address", options.IPv6Address)
	if err != nil || (ipv6.IsValid() && !ipv6.Is6()) {
		return nil, invalidNetworkInput(operation, "IPv6 address")
	}
	driverOptions, err := cleanMap(operation, "driver option", options.DriverOpts, false)
	if err != nil {
		return nil, err
	}
	aliases := make([]string, 0, len(options.Aliases))
	for _, alias := range options.Aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			return nil, invalidNetworkInput(operation, "alias")
		}
		aliases = append(aliases, alias)
	}
	return &network.EndpointSettings{
		IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: ipv4, IPv6Address: ipv6},
		Aliases:    aliases, DriverOpts: driverOptions, GwPriority: options.GwPriority,
	}, nil
}

func networkPruneFilters(options PruneOptions) (client.Filters, error) {
	filters := make(client.Filters)
	if until := strings.TrimSpace(options.Until); until != "" {
		filters.Add("until", until)
	}
	labels, err := cleanMap("prune networks", "label filter", options.Labels, true)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if labels[key] == "" {
			filters.Add("label", key)
		} else {
			filters.Add("label", key+"="+labels[key])
		}
	}
	if len(filters) == 0 {
		return nil, invalidNetworkInput("prune networks", "filters")
	}
	return filters, nil
}

func cleanMap(operation, resource string, values map[string]string, allowEmptyValue bool) (map[string]string, error) {
	if values == nil {
		return nil, nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || (!allowEmptyValue && value == "") {
			return nil, invalidNetworkInput(operation, resource)
		}
		result[key] = value
	}
	return result, nil
}

func parseOptionalPrefix(operation, resource, value string) (netip.Prefix, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return netip.Prefix{}, nil
	}
	parsed, err := netip.ParsePrefix(value)
	if err != nil {
		return netip.Prefix{}, invalidNetworkInput(operation, resource)
	}
	return parsed.Masked(), nil
}

func parseOptionalAddress(operation, resource, value string) (netip.Addr, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return netip.Addr{}, nil
	}
	parsed, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, invalidNetworkInput(operation, resource)
	}
	return parsed.Unmap(), nil
}

func invalidNetworkInput(operation, resource string) error {
	return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: operation, Resource: resource}
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
