package exporter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"erlang-monitor/internal/config"
	runtimestatus "erlang-monitor/internal/runtime"
	"erlang-monitor/internal/sshprobe"
	"github.com/prometheus/client_golang/prometheus"
)

func TestSetHostMetricsPublishesMultiCoreDisplayWithoutChangingNormalizedCPU(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)
	poller := &Poller{metrics: metrics}
	server := config.Server{ID: "external-1", Name: "example", Address: "127.0.0.1:22"}

	poller.setHostMetrics(server, sshprobe.Host{CPUUsageValid: true, CPUUsageRatio: 0.108, LogicalCPUs: 16})

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]float64, 3)
	for _, family := range families {
		if len(family.Metric) == 1 && family.Metric[0].Gauge != nil {
			values[family.GetName()] = family.Metric[0].Gauge.GetValue()
		}
	}
	if got := values["erlang_host_cpu_usage_ratio"]; got != 0.108 {
		t.Fatalf("normalized CPU = %v, want 0.108", got)
	}
	if got := values["erlang_host_cpu_logical_cores"]; got != 16 {
		t.Fatalf("logical CPUs = %v, want 16", got)
	}
	if got := values["erlang_host_cpu_usage_cores_percent"]; got != 172.8 {
		t.Fatalf("single-core CPU percent = %v, want 172.8", got)
	}
}

func TestSetNodeMetricsPublishesMemoryAndCPUUsage(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)
	poller := &Poller{metrics: metrics}
	server := config.Server{ID: "external-1", Name: "example"}

	poller.setNodeMetrics(server, sshprobe.Node{
		Name: "game@127.0.0.1", ResidentMemoryBytes: 3 * 1024 * 1024 * 1024, ResidentMemoryValid: true,
		CPUUsageRatio: 1.25, CPUUsageValid: true,
		RegisteredUsers: 317, OnlineUsers: 1, PlayerCountsValid: true,
	})

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]float64)
	for _, family := range families {
		if len(family.Metric) == 1 && family.Metric[0].Gauge != nil {
			values[family.GetName()] = family.Metric[0].Gauge.GetValue()
		}
	}
	if got := values["erlang_beam_resident_memory_bytes"]; got != 3*1024*1024*1024 {
		t.Fatalf("node resident memory = %v", got)
	}
	if got := values["erlang_vm_cpu_usage_ratio"]; got != 1.25 {
		t.Fatalf("node CPU ratio = %v, want 1.25", got)
	}
	if got := values["erlang_game_registered_users"]; got != 317 {
		t.Fatalf("registered users = %v, want 317", got)
	}
	if got := values["erlang_game_online_users"]; got != 1 {
		t.Fatalf("online users = %v, want 1", got)
	}
}

func TestSetNodeMetricsPublishesAndReplacesMNodeConnections(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)
	poller := &Poller{metrics: metrics}
	server := config.Server{ID: "external-1", Name: "example"}
	nodeName := "wl_ssjj_1814@127.0.0.1"

	poller.setNodeMetrics(server, sshprobe.Node{
		Name: nodeName, MNodeConnectionsValid: true,
		MNodeConnections: []sshprobe.MNodeConnection{
			{NodeID: "801000001", Node: "wl_ssjj_1@172.19.33.98", Type: "central", State: 2},
			{NodeID: "901100005", Node: "wl_ssjj_100005@172.19.33.104", Type: "region", State: 1},
			{NodeID: "703000004", Node: "wl_act_4@127.0.0.1", Type: "game", State: 2},
		},
	})

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	connectionCount := 0
	for _, family := range families {
		switch family.GetName() {
		case "erlang_mnode_connections_available":
			if len(family.Metric) != 1 || family.Metric[0].Gauge.GetValue() != 1 {
				t.Fatalf("mnode availability = %#v", family.Metric)
			}
		case "erlang_mnode_connection_state":
			connectionCount = len(family.Metric)
		}
	}
	if connectionCount != 2 {
		t.Fatalf("published mnode connections = %d, want central and region only", connectionCount)
	}

	poller.setNodeMetrics(server, sshprobe.Node{Name: nodeName, MNodeConnectionsValid: true})
	families, err = registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "erlang_mnode_connection_state" && len(family.Metric) != 0 {
			t.Fatalf("stale mnode connections were not removed: %#v", family.Metric)
		}
	}
}

func TestCollectionAnomalous(t *testing.T) {
	server := thresholdTestServer()
	tests := []struct {
		name   string
		result sshprobe.Result
		err    error
		want   bool
	}{
		{name: "healthy", result: sshprobe.Result{Nodes: []sshprobe.Node{{Name: "a"}}}},
		{name: "queue threshold is not rechecked", result: sshprobe.Result{Nodes: []sshprobe.Node{{ProcessesOverQueueThreshold: 1}}}},
		{name: "memory threshold is not rechecked", result: sshprobe.Result{Nodes: []sshprobe.Node{{ProcessesOverMemoryThreshold: 1}}}},
		{name: "run queue below alert threshold is not rechecked", result: sshprobe.Result{Nodes: []sshprobe.Node{{RunQueue: 128, SchedulersOnline: 16}}}},
		{name: "run queue above alert threshold is rechecked", result: sshprobe.Result{Nodes: []sshprobe.Node{{Name: "runq", RunQueue: 129, SchedulersOnline: 16}}}, want: true},
		{name: "partial node", result: sshprobe.Result{Nodes: []sshprobe.Node{{Name: "a"}}, Failures: []sshprobe.NodeFailure{{Name: "b"}}}, want: true},
		{name: "host failure", result: sshprobe.Result{HostError: "df failed"}, want: true},
		{name: "collection error", err: errors.New("ssh failed"), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := collectionAnomalous(server, test.result, test.err); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestConfirmationScopeForTargetsOnlyCollectionFailures(t *testing.T) {
	result := sshprobe.Result{
		HostError: "df failed",
		Nodes: []sshprobe.Node{
			{Name: "healthy@127.0.0.1"},
			{Name: "queue@127.0.0.1", ProcessesOverQueueThreshold: 1},
			{Name: "memory@127.0.0.1", ProcessesOverMemoryThreshold: 2},
		},
		Failures: []sshprobe.NodeFailure{{Name: "failed@127.0.0.1", Error: "badrpc"}},
	}
	scope := confirmationScopeFor(thresholdTestServer(), result, nil)
	wantFailureNodes := []string{"failed@127.0.0.1"}
	if scope.Full || !scope.Host || !reflect.DeepEqual(scope.FailureNodes, wantFailureNodes) {
		t.Fatalf("scope = %#v, want host collection failure plus failure nodes %v only", scope, wantFailureNodes)
	}
}

func TestConfirmationScopeForUsesFullRetryOnlyWhenFailureCannotBeScoped(t *testing.T) {
	transport := confirmationScopeFor(thresholdTestServer(), sshprobe.Result{}, errors.New("SSH handshake failed"))
	if !transport.Full || !transport.Required() {
		t.Fatalf("transport scope = %#v, want full retry", transport)
	}

	nodeFailure := confirmationScopeFor(
		thresholdTestServer(),
		sshprobe.Result{Failures: []sshprobe.NodeFailure{{Name: "game@127.0.0.1", Error: "badrpc"}}},
		errors.New("all nodes failed"),
	)
	if nodeFailure.Full || !reflect.DeepEqual(nodeFailure.FailureNodes, []string{"game@127.0.0.1"}) {
		t.Fatalf("node failure scope = %#v, want targeted retry", nodeFailure)
	}
}

func TestConfirmationScheduleSeparatesFastAndNodeFailureRetries(t *testing.T) {
	server := config.Server{
		ConfirmInterval:            config.Duration{Duration: 10 * time.Second},
		NodeFailureConfirmInterval: config.Duration{Duration: 3 * time.Minute},
	}
	schedule := confirmationSchedule(server, confirmationScope{
		Host:          true,
		FailureNodes:  []string{"failed@127.0.0.1"},
		RunQueueNodes: []string{"runq@127.0.0.1"},
	})
	if len(schedule) != 2 {
		t.Fatalf("schedule = %#v, want two independent retries", schedule)
	}
	if schedule[0].After != 10*time.Second || !schedule[0].Scope.Host || len(schedule[0].Scope.FailureNodes) != 0 || !reflect.DeepEqual(schedule[0].Scope.RunQueueNodes, []string{"runq@127.0.0.1"}) {
		t.Fatalf("fast retry = %#v", schedule[0])
	}
	if schedule[1].After != 3*time.Minute || !reflect.DeepEqual(schedule[1].Scope.FailureNodes, []string{"failed@127.0.0.1"}) || schedule[1].Scope.Host {
		t.Fatalf("node failure retry = %#v", schedule[1])
	}
}

func TestConfirmationScopeRechecksOnlyRunQueueResourceRisk(t *testing.T) {
	server := thresholdTestServer()
	result := sshprobe.Result{
		HostCollected: true,
		Host:          sshprobe.Host{CPUUsageValid: true, CPUUsageRatio: 0.81, MemoryTotalBytes: 100, MemoryAvailableBytes: 40},
		Nodes: []sshprobe.Node{
			{Name: "vm", MemoryBytes: 16 * 1024 * 1024 * 1024},
			{Name: "capacity", ProcessCount: 81, ProcessLimit: 100},
			{Name: "runq", RunQueue: 129, SchedulersOnline: 16},
		},
	}
	scope := confirmationScopeFor(server, result, nil)
	if scope.Full || scope.Host || len(scope.FailureNodes) != 0 || !reflect.DeepEqual(scope.RunQueueNodes, []string{"runq"}) {
		t.Fatalf("scope = %#v, want only the high Run Queue node in the fast confirmation queue", scope)
	}
}

func TestConfirmationScopeClearsRunQueueAtExactAlertThreshold(t *testing.T) {
	scope := confirmationScopeFor(thresholdTestServer(), sshprobe.Result{Nodes: []sshprobe.Node{{Name: "runq", RunQueue: 128, SchedulersOnline: 16}}}, nil)
	if scope.Required() {
		t.Fatalf("scope = %#v, exact threshold must not trigger strict-greater-than alert confirmation", scope)
	}
}

func thresholdTestServer() config.Server {
	return config.Server{
		HostCPUAlertPercent: 80, HostMemoryAlertPercent: 80,
		VMMemoryAlertGBytes: 15, CapacityAlertPercent: 80,
		RunQueueDisplayMultiple: 4, RunQueueAlertMultiple: 8,
	}
}

func TestConfirmRunsOneTargetedNodeFailureCollection(t *testing.T) {
	collector := &recordingCollector{
		selectedResult: sshprobe.Result{Nodes: []sshprobe.Node{{Name: "queue@127.0.0.1"}}},
	}
	registry := prometheus.NewRegistry()
	poller := NewPoller(
		config.Exporter{}, collector, NewMetrics(registry), runtimestatus.NewStore(""),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	server := config.Server{
		ID: "external-1", Name: "external-1", Address: "127.0.0.1:22",
		NodeFailureConfirmInterval: config.Duration{Duration: time.Millisecond},
	}
	scope := confirmationScope{FailureNodes: []string{"queue@127.0.0.1"}}
	poller.confirm(context.Background(), server, scope)

	if collector.fullCalls != 0 || collector.selectedCalls != 1 {
		t.Fatalf("full calls=%d selected calls=%d, want 0/1", collector.fullCalls, collector.selectedCalls)
	}
	if !reflect.DeepEqual(collector.selectedNodes, scope.FailureNodes) || collector.selectedHost {
		t.Fatalf("selected nodes=%v host=%v", collector.selectedNodes, collector.selectedHost)
	}
}

func TestConfirmRunsOneTargetedRunQueueCollection(t *testing.T) {
	collector := &recordingCollector{
		selectedResult: sshprobe.Result{Nodes: []sshprobe.Node{{Name: "runq@127.0.0.1", RunQueue: 0, SchedulersOnline: 2}}},
	}
	registry := prometheus.NewRegistry()
	poller := NewPoller(
		config.Exporter{}, collector, NewMetrics(registry), runtimestatus.NewStore(""),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	server := config.Server{
		ID: "external-1", Name: "external-1", Address: "127.0.0.1:22",
		ConfirmInterval: config.Duration{Duration: time.Millisecond}, RunQueueAlertMultiple: 8,
	}
	scope := confirmationScope{RunQueueNodes: []string{"runq@127.0.0.1"}}
	poller.confirm(context.Background(), server, scope)

	if collector.fullCalls != 0 || collector.selectedCalls != 1 {
		t.Fatalf("full calls=%d selected calls=%d, want 0/1", collector.fullCalls, collector.selectedCalls)
	}
	if !reflect.DeepEqual(collector.selectedNodes, scope.RunQueueNodes) || collector.selectedHost {
		t.Fatalf("selected nodes=%v host=%v", collector.selectedNodes, collector.selectedHost)
	}
}

func TestConfirmRunsOneFullCollectionForUnscopedFailure(t *testing.T) {
	collector := &recordingCollector{}
	registry := prometheus.NewRegistry()
	poller := NewPoller(
		config.Exporter{}, collector, NewMetrics(registry), runtimestatus.NewStore(""),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	server := config.Server{
		ID: "external-1", Name: "external-1", Address: "127.0.0.1:22",
		ConfirmInterval: config.Duration{Duration: time.Millisecond},
	}
	poller.confirm(context.Background(), server, confirmationScope{Full: true})

	if collector.fullCalls != 1 || collector.selectedCalls != 0 {
		t.Fatalf("full calls=%d selected calls=%d, want 1/0", collector.fullCalls, collector.selectedCalls)
	}
}

func TestScheduleAndCollectHandlers(t *testing.T) {
	server := config.Server{ID: "external-1", Name: "101.34.55.142", Address: "101.34.55.142:43999", Enabled: boolPtr(true)}
	poller := NewPoller(config.Exporter{Servers: []config.Server{server}}, &recordingCollector{}, NewMetrics(prometheus.NewRegistry()), runtimestatus.NewStore(""), slog.New(slog.NewTextHandler(io.Discard, nil)))

	request := httptest.NewRequest(http.MethodPost, "/schedule", strings.NewReader(`{"server":"101.34.55.142","mode":"refresh"}`))
	response := httptest.NewRecorder()
	poller.ScheduleHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || poller.controls[server.ID].isAuto() {
		t.Fatalf("schedule response=%d body=%s auto=%v", response.Code, response.Body.String(), poller.controls[server.ID].isAuto())
	}

	request = httptest.NewRequest(http.MethodPost, "/collect", strings.NewReader(`{"server":"external-1"}`))
	response = httptest.NewRecorder()
	poller.CollectHandler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("collect response=%d body=%s", response.Code, response.Body.String())
	}
	select {
	case <-poller.controls[server.ID].manual:
	default:
		t.Fatal("manual collection was not queued")
	}

	request = httptest.NewRequest(http.MethodPost, "/schedule", strings.NewReader(`{"server":"external-1","mode":"auto"}`))
	response = httptest.NewRecorder()
	poller.ScheduleHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !poller.controls[server.ID].isAuto() {
		t.Fatalf("auto response=%d body=%s auto=%v", response.Code, response.Body.String(), poller.controls[server.ID].isAuto())
	}
}

func TestApplyConfigUpdatesWorkerAndPreservesScheduleMode(t *testing.T) {
	collector := &channelCollector{calls: make(chan config.Server, 4)}
	server := config.Server{
		ID: "external-1", Name: "first", Address: "127.0.0.1:22", Enabled: boolPtr(true),
		PollInterval: config.Duration{Duration: time.Hour}, ConfirmInterval: config.Duration{Duration: time.Millisecond},
	}
	poller := NewPoller(config.Exporter{Servers: []config.Server{server}}, collector, NewMetrics(prometheus.NewRegistry()), runtimestatus.NewStore(""), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	poller.Start(ctx)
	defer func() {
		cancel()
		poller.Wait()
	}()

	first := awaitCollectedServer(t, collector.calls)
	if first.Name != "first" {
		t.Fatalf("initial server = %#v", first)
	}
	poller.controls[server.ID].setAuto(false)
	updated := server
	updated.Name = "second"
	updated.QueueThreshold = 321
	poller.ApplyConfig(config.Exporter{Servers: []config.Server{updated}})

	second := awaitCollectedServer(t, collector.calls)
	if second.Name != "second" || second.QueueThreshold != 321 {
		t.Fatalf("reloaded server = %#v", second)
	}
	if poller.controls[server.ID].isAuto() {
		t.Fatal("hot reload lost the server's refresh scheduling mode")
	}
	resolved, _, err := poller.controlFor("second")
	if err != nil || resolved.ID != server.ID {
		t.Fatalf("latest config is not visible to handlers: server=%#v err=%v", resolved, err)
	}
}

func TestApplyConfigDisablesWorkerAndRemovesRuntimeStatus(t *testing.T) {
	collector := &channelCollector{calls: make(chan config.Server, 2)}
	server := config.Server{
		ID: "external-1", Name: "first", Address: "127.0.0.1:22", Enabled: boolPtr(true),
		PollInterval: config.Duration{Duration: time.Hour}, ConfirmInterval: config.Duration{Duration: time.Millisecond},
	}
	store := runtimestatus.NewStore("")
	poller := NewPoller(config.Exporter{Servers: []config.Server{server}}, collector, NewMetrics(prometheus.NewRegistry()), store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	poller.Start(ctx)
	defer func() {
		cancel()
		poller.Wait()
	}()
	awaitCollectedServer(t, collector.calls)
	awaitRuntimeServer(t, store, server.ID, true)

	disabled := server
	disabled.Enabled = boolPtr(false)
	poller.ApplyConfig(config.Exporter{Servers: []config.Server{disabled}})
	if _, exists := store.Snapshot().Servers[server.ID]; exists {
		t.Fatal("disabled server remains in runtime status")
	}
	request := httptest.NewRequest(http.MethodPost, "/collect", strings.NewReader(`{"server":"external-1"}`))
	response := httptest.NewRecorder()
	poller.CollectHandler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("disabled collect response=%d body=%s", response.Code, response.Body.String())
	}
}

func awaitRuntimeServer(t *testing.T, store *runtimestatus.Store, serverID string, want bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, exists := store.Snapshot().Servers[serverID]
		if exists == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("runtime server %q presence did not become %v", serverID, want)
}

type channelCollector struct {
	calls chan config.Server
}

func (c *channelCollector) Collect(_ context.Context, server config.Server) (sshprobe.Result, error) {
	c.calls <- server
	return sshprobe.Result{}, nil
}

func (c *channelCollector) CollectSelected(context.Context, config.Server, []string, bool) (sshprobe.Result, error) {
	return sshprobe.Result{}, nil
}

func awaitCollectedServer(t *testing.T, calls <-chan config.Server) config.Server {
	t.Helper()
	select {
	case server := <-calls:
		return server
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for collection")
		return config.Server{}
	}
}

func boolPtr(value bool) *bool { return &value }

func TestTargetedConfirmationRecoversFailedNodeStatus(t *testing.T) {
	const nodeName = "game@127.0.0.1"
	collector := &recordingCollector{
		selectedResult: sshprobe.Result{Nodes: []sshprobe.Node{{Name: nodeName}}},
	}
	store := runtimestatus.NewStore("")
	if err := store.Update("external-1", runtimestatus.ServerStatus{
		State: "down", FailedNodes: 1,
		NodeErrors: map[string]string{nodeName: "badrpc"},
	}); err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	poller := NewPoller(
		config.Exporter{}, collector, NewMetrics(registry), store,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	server := config.Server{
		ID: "external-1", Name: "external-1", Address: "127.0.0.1:22",
		NodeFailureConfirmInterval: config.Duration{Duration: time.Millisecond},
	}
	poller.confirm(context.Background(), server, confirmationScope{FailureNodes: []string{nodeName}})

	status := store.Snapshot().Servers[server.ID]
	if status.State != "healthy" || status.Nodes != 1 || status.FailedNodes != 0 || status.NodeErrors != nil {
		t.Fatalf("status = %#v, want recovered node", status)
	}
}

func TestTargetedConfirmationMatchesExpectedShortNodeName(t *testing.T) {
	const expectedName = "wl_ssjj_1802"
	collector := &recordingCollector{
		selectedResult: sshprobe.Result{Nodes: []sshprobe.Node{{Name: expectedName + "@127.0.0.1"}}},
	}
	store := runtimestatus.NewStore("")
	if err := store.Update("external-1", runtimestatus.ServerStatus{
		State: "degraded", Nodes: 8, FailedNodes: 1,
		NodeErrors: map[string]string{expectedName: "configured instance directory has no running Erlang node"},
	}); err != nil {
		t.Fatal(err)
	}
	poller := NewPoller(
		config.Exporter{}, collector, NewMetrics(prometheus.NewRegistry()), store,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	server := config.Server{
		ID: "external-1", Name: "external-1", Address: "127.0.0.1:22",
		NodeFailureConfirmInterval: config.Duration{Duration: time.Millisecond},
	}
	poller.confirm(context.Background(), server, confirmationScope{FailureNodes: []string{expectedName}})

	status := store.Snapshot().Servers[server.ID]
	if status.State != "healthy" || status.Nodes != 9 || status.FailedNodes != 0 || status.NodeErrors != nil {
		t.Fatalf("status = %#v, want recovered directory-backed node", status)
	}
}

type recordingCollector struct {
	fullCalls      int
	selectedCalls  int
	selectedNodes  []string
	selectedHost   bool
	selectedResult sshprobe.Result
	selectedErr    error
}

func (c *recordingCollector) Collect(context.Context, config.Server) (sshprobe.Result, error) {
	c.fullCalls++
	return sshprobe.Result{}, nil
}

func (c *recordingCollector) CollectSelected(_ context.Context, _ config.Server, nodes []string, host bool) (sshprobe.Result, error) {
	c.selectedCalls++
	c.selectedNodes = append([]string(nil), nodes...)
	c.selectedHost = host
	return c.selectedResult, c.selectedErr
}
