package networks

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/containers"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

func TestAPIReadsNetworkListDetailsAndConnections(t *testing.T) {
	created := time.Date(2026, 8, 30, 10, 11, 12, 0, time.UTC)
	value := network.Network{
		ID: "network-one", Name: "demo", Created: created, Scope: "local", Driver: "bridge",
		EnableIPv4: true, Internal: true, Attachable: true, Labels: map[string]string{"kind": "test"},
		Options: map[string]string{"com.example.option": "yes"}, ConfigFrom: network.ConfigReference{Network: "config-source"},
		IPAM: network.IPAM{Driver: "default", Options: map[string]string{"key": "value"}, Config: []network.IPAMConfig{{
			Subnet: netip.MustParsePrefix("172.31.0.0/24"), IPRange: netip.MustParsePrefix("172.31.0.128/25"),
			Gateway: netip.MustParseAddr("172.31.0.1"), AuxAddress: map[string]netip.Addr{"dns": netip.MustParseAddr("172.31.0.2")},
		}}},
	}
	docker := &fakeNetworkClient{
		list: client.NetworkListResult{Items: []network.Summary{
			{Network: network.Network{ID: "z", Name: "zeta"}}, {Network: value},
		}},
		inspect: client.NetworkInspectResult{Network: network.Inspect{Network: value, Containers: map[string]network.EndpointResource{
			"container-z": {Name: "zeta", EndpointID: "endpoint-z"},
			"container-a": {
				Name: "alpha", EndpointID: "endpoint-a", MacAddress: network.HardwareAddr(net.HardwareAddr{2, 66, 172, 31, 0, 3}),
				IPv4Address: netip.MustParsePrefix("172.31.0.3/24"), IPv6Address: netip.MustParsePrefix("fd00::3/64"),
			},
		}}},
	}
	api := newNetworkTestAPI(t, docker)

	payload, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindList})
	if err != nil {
		t.Fatalf("read list: %v", err)
	}
	list := payload.(ListUpdated)
	if len(list.Networks) != 2 || list.Networks[0].Name != "demo" || !list.Networks[0].Internal || list.Networks[0].Created != created {
		t.Fatalf("network list = %#v", list)
	}

	payload, err = api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindDetails, ID: "network-one"})
	if err != nil {
		t.Fatalf("read details: %v", err)
	}
	details := payload.(DetailsUpdated).Network
	if details.ConfigFrom != "config-source" || details.IPAMDriver != "default" || len(details.IPAM) != 1 ||
		details.IPAM[0].Gateway != "172.31.0.1" || details.IPAM[0].AuxAddresses["dns"] != "172.31.0.2" {
		t.Fatalf("network details = %#v", details)
	}

	payload, err = api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindConnections, ID: "network-one"})
	if err != nil {
		t.Fatalf("read connections: %v", err)
	}
	connections := payload.(ConnectionsUpdated)
	if connections.NetworkID != "network-one" || len(connections.Connections) != 2 ||
		connections.Connections[0].ContainerName != "alpha" || connections.Connections[0].IPv4Address != "172.31.0.3/24" ||
		connections.Connections[0].IPv6Address != "fd00::3/64" || connections.Connections[0].MACAddress != "02:42:ac:1f:00:03" {
		t.Fatalf("connections = %#v", connections)
	}
}

func TestNetworkTargetedRefreshRequests(t *testing.T) {
	refreshes := &networkRefreshRecorder{}
	api := newNetworkTestAPIWith(t, &fakeNetworkClient{}, &networkCommandRunner{}, refreshes)
	if err := api.RequestDetails(" network-one "); err != nil {
		t.Fatalf("request details: %v", err)
	}
	if err := api.RequestConnections("network-one"); err != nil {
		t.Fatalf("request connections: %v", err)
	}
	want := []networkRefreshCall{
		{backend.RefreshKey{Kind: RefreshKindDetails, ID: "network-one"}, backend.RefreshManual},
		{backend.RefreshKey{Kind: RefreshKindConnections, ID: "network-one"}, backend.RefreshManual},
	}
	if !reflect.DeepEqual(refreshes.calls, want) {
		t.Fatalf("refresh calls = %#v, want %#v", refreshes.calls, want)
	}
}

func TestNetworkCommandsUseExecutorAndExpectedRefreshes(t *testing.T) {
	tests := []struct {
		name string
		run  func(*API) (backend.CommandResult, error)
		call string
		keys []backend.RefreshKey
	}{
		{"create", func(api *API) (backend.CommandResult, error) {
			return api.Create(context.Background(), CreateOptions{Name: "demo"})
		}, "create", networkMutationRefreshKeys()},
		{"remove", func(api *API) (backend.CommandResult, error) {
			return api.Remove(context.Background(), "demo", RemoveOptions{})
		}, "remove", networkMutationRefreshKeys()},
		{"prune", func(api *API) (backend.CommandResult, error) {
			return api.Prune(context.Background(), PruneOptions{Labels: map[string]string{"kind": "test"}})
		}, "prune", networkMutationRefreshKeys()},
		{"connect", func(api *API) (backend.CommandResult, error) {
			return api.Connect(context.Background(), "demo", ConnectOptions{ContainerID: "container-one"})
		}, "connect", endpointMutationRefreshKeys("demo", "container-one")},
		{"disconnect", func(api *API) (backend.CommandResult, error) {
			return api.Disconnect(context.Background(), "demo", DisconnectOptions{ContainerID: "container-one", Force: true})
		}, "disconnect", endpointMutationRefreshKeys("demo", "container-one")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			docker := &fakeNetworkClient{}
			api := newNetworkTestAPIWith(t, docker, &networkCommandRunner{execute: true}, &networkRefreshRecorder{})
			result, err := test.run(api)
			if err != nil {
				t.Fatalf("command: %v", err)
			}
			if !reflect.DeepEqual(docker.calls, []string{test.call}) || !reflect.DeepEqual(result.RefreshKeys, test.keys) {
				t.Fatalf("calls/keys = %v/%#v, want %s/%#v", docker.calls, result.RefreshKeys, test.call, test.keys)
			}
		})
	}
}

func TestNetworkCreateAndConnectMapValidatedOptionsAndCopyInputs(t *testing.T) {
	enableIPv6 := true
	labels := map[string]string{"kind": "original"}
	aux := map[string]string{"dns": "172.29.0.3"}
	aliases := []string{"api"}
	driverOpts := map[string]string{"key": "original"}
	docker := &fakeNetworkClient{}
	runner := &networkCommandRunner{}
	api := newNetworkTestAPIWith(t, docker, runner, &networkRefreshRecorder{})
	if _, err := api.Create(context.Background(), CreateOptions{
		Name: "demo", Driver: "bridge", EnableIPv6: &enableIPv6, Labels: labels,
		IPAM: []CreateIPAMConfig{{Subnet: "172.29.0.0/24", IPRange: "172.29.0.128/25", Gateway: "172.29.0.1", AuxAddresses: aux}},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := api.Connect(context.Background(), "demo", ConnectOptions{
		ContainerID: "container-one", IPv4Address: "172.29.0.8", IPv6Address: "fd00::8", Aliases: aliases, DriverOpts: driverOpts,
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	labels["kind"] = "changed"
	aux["dns"] = "172.29.0.4"
	aliases[0] = "changed"
	driverOpts["key"] = "changed"
	for _, request := range runner.requests {
		if err := request.Run(context.Background()); err != nil {
			t.Fatalf("run callback: %v", err)
		}
	}
	if docker.createOptions.Labels["kind"] != "original" || docker.createOptions.IPAM.Config[0].AuxAddress["dns"].String() != "172.29.0.3" ||
		docker.connectOptions.EndpointConfig.Aliases[0] != "api" || docker.connectOptions.EndpointConfig.DriverOpts["key"] != "original" {
		t.Fatalf("captured options mutated: create=%#v connect=%#v", docker.createOptions, docker.connectOptions)
	}
}

func TestNetworkPruneRequiresExplicitFilterAndMapsFilters(t *testing.T) {
	for _, options := range []PruneOptions{{}, {Labels: map[string]string{"": "bad"}}} {
		runner := &networkCommandRunner{}
		api := newNetworkTestAPIWith(t, &fakeNetworkClient{}, runner, &networkRefreshRecorder{})
		if _, err := api.Prune(context.Background(), options); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
			t.Fatalf("Prune(%#v) error = %v", options, err)
		}
		if len(runner.requests) != 0 {
			t.Fatal("invalid prune reached Command Executor")
		}
	}
	docker := &fakeNetworkClient{}
	api := newNetworkTestAPIWith(t, docker, &networkCommandRunner{execute: true}, &networkRefreshRecorder{})
	if _, err := api.Prune(context.Background(), PruneOptions{Until: "24h", Labels: map[string]string{"kind": "test"}}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if !docker.pruneOptions.Filters["until"]["24h"] || !docker.pruneOptions.Filters["label"]["kind=test"] {
		t.Fatalf("prune filters = %#v", docker.pruneOptions.Filters)
	}
}

func TestNetworkInputValidationErrorsAndDeepCopies(t *testing.T) {
	invalidCreate := []CreateOptions{
		{}, {Name: "demo", IPAM: []CreateIPAMConfig{{Subnet: "bad"}}},
		{Name: "demo", IPAM: []CreateIPAMConfig{{Subnet: "172.30.0.0/24", Gateway: "172.31.0.1"}}},
	}
	for _, options := range invalidCreate {
		api := newNetworkTestAPI(t, &fakeNetworkClient{})
		if _, err := api.Create(context.Background(), options); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
			t.Fatalf("Create(%#v) error = %v", options, err)
		}
	}
	for _, options := range []ConnectOptions{{}, {ContainerID: "c", IPv4Address: "fd00::1"}, {ContainerID: "c", IPv6Address: "192.0.2.1"}, {ContainerID: "c", Aliases: []string{""}}} {
		api := newNetworkTestAPI(t, &fakeNetworkClient{})
		if _, err := api.Connect(context.Background(), "network", options); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
			t.Fatalf("Connect(%#v) error = %v", options, err)
		}
	}
	api := newNetworkTestAPI(t, &fakeNetworkClient{err: errdefs.ErrNotFound})
	if _, err := api.ReadRefresh(context.Background(), backend.RefreshKey{Kind: RefreshKindDetails, ID: "missing"}); !backend.HasErrorCode(err, backend.ErrorNotFound) {
		t.Fatalf("details error = %v", err)
	}
	for _, key := range []backend.RefreshKey{{Kind: "unknown"}, {Kind: RefreshKindList, ID: "bad"}, {Kind: RefreshKindConnections}} {
		if _, err := api.ReadRefresh(context.Background(), key); err == nil {
			t.Fatalf("ReadRefresh(%#v) succeeded", key)
		}
	}
	original := DetailsUpdated{Network: Details{Summary: Summary{Labels: map[string]string{"key": "value"}}, IPAM: []IPAMConfig{{AuxAddresses: map[string]string{"dns": "one"}}}}}
	clone := original.CloneEventPayload().(DetailsUpdated)
	original.Network.Labels["key"] = "changed"
	original.Network.IPAM[0].AuxAddresses["dns"] = "changed"
	if clone.Network.Labels["key"] != "value" || clone.Network.IPAM[0].AuxAddresses["dns"] != "one" {
		t.Fatalf("clone changed = %#v", clone)
	}
}

func TestNetworkActiveEndpointsMapsToConflict(t *testing.T) {
	docker := &fakeNetworkClient{err: errors.New("error while removing network: network demo has active endpoints")}
	api := newNetworkTestAPIWith(t, docker, &networkCommandRunner{execute: true}, &networkRefreshRecorder{})
	if _, err := api.Remove(context.Background(), "demo", RemoveOptions{}); !backend.HasErrorCode(err, backend.ErrorConflict) {
		t.Fatalf("remove in-use network error = %v", err)
	}
}

func TestNetworksAPIRejectsMissingDependencies(t *testing.T) {
	docker := &fakeNetworkClient{}
	commands := &networkCommandRunner{}
	refreshes := &networkRefreshRecorder{}
	for _, construct := range []func() (*API, error){
		func() (*API, error) { return NewAPI(nil, commands, refreshes) },
		func() (*API, error) { return NewAPI(docker, nil, refreshes) },
		func() (*API, error) { return NewAPI(docker, commands, nil) },
	} {
		if _, err := construct(); !backend.HasErrorCode(err, backend.ErrorInvalidInput) {
			t.Fatalf("constructor error = %v", err)
		}
	}
}

func TestNetworkRefreshKeysCoverAffectedPages(t *testing.T) {
	if got := endpointMutationRefreshKeys("network", "container"); !reflect.DeepEqual(got, []backend.RefreshKey{
		{Kind: RefreshKindList}, {Kind: RefreshKindDetails, ID: "network"}, {Kind: RefreshKindConnections, ID: "network"},
		{Kind: containers.RefreshKindList}, {Kind: containers.RefreshKindDetails, ID: "container"}, {Kind: dashboard.RefreshKindSummary},
	}) {
		t.Fatalf("endpoint refresh keys = %#v", got)
	}
}

func newNetworkTestAPI(t *testing.T, docker networkClient) *API {
	t.Helper()
	return newNetworkTestAPIWith(t, docker, &networkCommandRunner{}, &networkRefreshRecorder{})
}

func newNetworkTestAPIWith(t *testing.T, docker networkClient, commands backend.CommandRunner, refreshes backend.RefreshRequester) *API {
	t.Helper()
	api, err := NewAPI(docker, commands, refreshes)
	if err != nil {
		t.Fatalf("new Networks API: %v", err)
	}
	return api
}

type networkRefreshCall struct {
	key    backend.RefreshKey
	reason backend.RefreshReason
}

type networkRefreshRecorder struct{ calls []networkRefreshCall }

func (recorder *networkRefreshRecorder) Request(key backend.RefreshKey, reason backend.RefreshReason) error {
	recorder.calls = append(recorder.calls, networkRefreshCall{key, reason})
	return nil
}

type networkCommandRunner struct {
	requests []backend.CommandRequest
	execute  bool
}

func (runner *networkCommandRunner) Run(_ context.Context, request backend.CommandRequest) (backend.CommandResult, error) {
	runner.requests = append(runner.requests, request)
	var err error
	if runner.execute {
		err = request.Run(context.Background())
	}
	return backend.CommandResult{OperationID: request.OperationID, Affected: request.Affected, RefreshKeys: request.RefreshKeys}, err
}

type fakeNetworkClient struct {
	list              client.NetworkListResult
	inspect           client.NetworkInspectResult
	createOptions     client.NetworkCreateOptions
	connectOptions    client.NetworkConnectOptions
	disconnectOptions client.NetworkDisconnectOptions
	pruneOptions      client.NetworkPruneOptions
	calls             []string
	err               error
}

func (fake *fakeNetworkClient) NetworkList(context.Context, client.NetworkListOptions) (client.NetworkListResult, error) {
	return fake.list, fake.err
}

func (fake *fakeNetworkClient) NetworkInspect(context.Context, string, client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
	return fake.inspect, fake.err
}

func (fake *fakeNetworkClient) NetworkCreate(_ context.Context, _ string, options client.NetworkCreateOptions) (client.NetworkCreateResult, error) {
	fake.calls = append(fake.calls, "create")
	fake.createOptions = options
	return client.NetworkCreateResult{}, fake.err
}

func (fake *fakeNetworkClient) NetworkRemove(context.Context, string, client.NetworkRemoveOptions) (client.NetworkRemoveResult, error) {
	fake.calls = append(fake.calls, "remove")
	return client.NetworkRemoveResult{}, fake.err
}

func (fake *fakeNetworkClient) NetworkPrune(_ context.Context, options client.NetworkPruneOptions) (client.NetworkPruneResult, error) {
	fake.calls = append(fake.calls, "prune")
	fake.pruneOptions = options
	return client.NetworkPruneResult{}, fake.err
}

func (fake *fakeNetworkClient) NetworkConnect(_ context.Context, _ string, options client.NetworkConnectOptions) (client.NetworkConnectResult, error) {
	fake.calls = append(fake.calls, "connect")
	fake.connectOptions = options
	return client.NetworkConnectResult{}, fake.err
}

func (fake *fakeNetworkClient) NetworkDisconnect(_ context.Context, _ string, options client.NetworkDisconnectOptions) (client.NetworkDisconnectResult, error) {
	fake.calls = append(fake.calls, "disconnect")
	fake.disconnectOptions = options
	return client.NetworkDisconnectResult{}, fake.err
}
