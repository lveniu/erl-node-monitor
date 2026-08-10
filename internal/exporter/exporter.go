package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"erlang-monitor/internal/config"
	runtimestatus "erlang-monitor/internal/runtime"
	"erlang-monitor/internal/sshprobe"
	"github.com/prometheus/client_golang/prometheus"
)

type Collector interface {
	Collect(context.Context, config.Server) (sshprobe.Result, error)
	CollectSelected(context.Context, config.Server, []string, bool) (sshprobe.Result, error)
}

type Metrics struct {
	serverUp          *prometheus.GaugeVec
	scrapeDuration    *prometheus.GaugeVec
	lastSuccess       *prometheus.GaugeVec
	staleThreshold    *prometheus.GaugeVec
	collectErrors     *prometheus.CounterVec
	hostUp            *prometheus.GaugeVec
	hostCPU           *prometheus.GaugeVec
	hostCPULogical    *prometheus.GaugeVec
	hostCPUCorePct    *prometheus.GaugeVec
	hostLoad1         *prometheus.GaugeVec
	hostMemoryTotal   *prometheus.GaugeVec
	hostMemoryAvail   *prometheus.GaugeVec
	hostFSSize        *prometheus.GaugeVec
	hostFSAvail       *prometheus.GaugeVec
	hostUptime        *prometheus.GaugeVec
	hostNetworkRX     *prometheus.GaugeVec
	hostNetworkTX     *prometheus.GaugeVec
	hostCPUThreshold  *prometheus.GaugeVec
	hostMemThreshold  *prometheus.GaugeVec
	nodeUp            *prometheus.GaugeVec
	registeredUsers   *prometheus.GaugeVec
	onlineUsers       *prometheus.GaugeVec
	mnodeAvailable    *prometheus.GaugeVec
	mnodeState        *prometheus.GaugeVec
	processCount      *prometheus.GaugeVec
	processLimit      *prometheus.GaugeVec
	memoryBytes       *prometheus.GaugeVec
	cpuUsageRatio     *prometheus.GaugeVec
	residentMemory    *prometheus.GaugeVec
	vmMemoryDisplay   *prometheus.GaugeVec
	vmMemoryAlert     *prometheus.GaugeVec
	runQueue          *prometheus.GaugeVec
	schedulers        *prometheus.GaugeVec
	atomCount         *prometheus.GaugeVec
	atomLimit         *prometheus.GaugeVec
	portCount         *prometheus.GaugeVec
	portLimit         *prometheus.GaugeVec
	maxProcessMemory  *prometheus.GaugeVec
	overMemory        *prometheus.GaugeVec
	maxMessageQueue   *prometheus.GaugeVec
	overQueue         *prometheus.GaugeVec
	queueThreshold    *prometheus.GaugeVec
	memoryThreshold   *prometheus.GaugeVec
	capacityThreshold *prometheus.GaugeVec
	runQueueDisplay   *prometheus.GaugeVec
	runQueueAlert     *prometheus.GaugeVec
	memoryProcessInfo *prometheus.GaugeVec
	queueProcessInfo  *prometheus.GaugeVec
}

func NewMetrics(registry *prometheus.Registry) *Metrics {
	serverLabels := []string{"server", "name", "address"}
	nodeLabels := []string{"server", "name", "node"}
	mnodeLabels := []string{"server", "name", "node", "node_id", "connection_node", "connection_type"}
	processInfoLabels := []string{"server", "name", "node", "pid", "registered_name", "initial_call", "current_function"}
	m := &Metrics{
		serverUp:          prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_exporter_server_up", Help: "Whether SSH and Erlang collection succeeded."}, serverLabels),
		scrapeDuration:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_exporter_collection_duration_seconds", Help: "Duration of the latest collection cycle."}, serverLabels),
		lastSuccess:       prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_exporter_last_success_timestamp_seconds", Help: "Unix timestamp of the latest successful collection."}, serverLabels),
		staleThreshold:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_exporter_collection_stale_threshold_seconds", Help: "Configured maximum age of the latest successful collection."}, serverLabels),
		collectErrors:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "erlang_exporter_collection_errors_total", Help: "Total failed collection cycles."}, serverLabels),
		hostUp:            prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_up", Help: "Whether host-level metric collection succeeded."}, serverLabels),
		hostCPU:           prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_cpu_usage_ratio", Help: "Host CPU usage ratio between the latest two collections."}, serverLabels),
		hostCPULogical:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_cpu_logical_cores", Help: "Number of online logical CPUs reported by the remote host."}, serverLabels),
		hostCPUCorePct:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_cpu_usage_cores_percent", Help: "Host CPU usage in single-core percent units, where each logical CPU contributes up to 100 percent."}, serverLabels),
		hostLoad1:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_load1", Help: "Host one-minute load average."}, serverLabels),
		hostMemoryTotal:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_memory_total_bytes", Help: "Host total memory."}, serverLabels),
		hostMemoryAvail:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_memory_available_bytes", Help: "Host available memory."}, serverLabels),
		hostFSSize:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_filesystem_size_bytes", Help: "Configured host filesystem size."}, serverLabels),
		hostFSAvail:       prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_filesystem_available_bytes", Help: "Configured host filesystem available bytes."}, serverLabels),
		hostUptime:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_uptime_seconds", Help: "Host uptime in seconds."}, serverLabels),
		hostNetworkRX:     prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_network_receive_bytes_per_second", Help: "Host network receive throughput between the latest two collections."}, serverLabels),
		hostNetworkTX:     prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_network_transmit_bytes_per_second", Help: "Host network transmit throughput between the latest two collections."}, serverLabels),
		hostCPUThreshold:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_cpu_alert_threshold_ratio", Help: "Configured host CPU alert threshold ratio."}, serverLabels),
		hostMemThreshold:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_host_memory_alert_threshold_ratio", Help: "Configured host memory alert threshold ratio."}, serverLabels),
		nodeUp:            prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_exporter_node_up", Help: "Whether collection succeeded for an Erlang node."}, nodeLabels),
		registeredUsers:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_game_registered_users", Help: "Registered player count supplied by the mlib_sys business interface; NaN when unavailable."}, nodeLabels),
		onlineUsers:       prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_game_online_users", Help: "Online player count supplied by the mlib_sys business interface; NaN when unavailable."}, nodeLabels),
		mnodeAvailable:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_mnode_connections_available", Help: "Whether mnode:i() connection data was collected successfully for an Erlang node."}, nodeLabels),
		mnodeState:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_mnode_connection_state", Help: "mnode:i() connection state for central (8...) and region (9...) nodes; state 2 is usable."}, mnodeLabels),
		processCount:      prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_process_count", Help: "Current Erlang process count."}, nodeLabels),
		processLimit:      prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_process_limit", Help: "Erlang process limit."}, nodeLabels),
		memoryBytes:       prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_memory_bytes", Help: "Total memory allocated by the Erlang VM."}, nodeLabels),
		cpuUsageRatio:     prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_cpu_usage_ratio", Help: "BEAM OS process CPU usage in single-core ratio units between samples."}, nodeLabels),
		residentMemory:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_beam_resident_memory_bytes", Help: "Resident set size of the BEAM OS process."}, nodeLabels),
		vmMemoryDisplay:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_memory_display_threshold_bytes", Help: "Configured VM memory dashboard display threshold."}, nodeLabels),
		vmMemoryAlert:     prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_memory_alert_threshold_bytes", Help: "Configured VM memory alert threshold."}, nodeLabels),
		runQueue:          prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_run_queue", Help: "Total Erlang run queue length."}, nodeLabels),
		schedulers:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_schedulers_online", Help: "Number of online Erlang schedulers."}, nodeLabels),
		atomCount:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_atom_count", Help: "Current Erlang atom count."}, nodeLabels),
		atomLimit:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_atom_limit", Help: "Erlang atom limit."}, nodeLabels),
		portCount:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_port_count", Help: "Current Erlang port count."}, nodeLabels),
		portLimit:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_port_limit", Help: "Erlang port limit."}, nodeLabels),
		maxProcessMemory:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_process_memory_max_bytes", Help: "Largest process memory value observed during the latest bounded scan."}, nodeLabels),
		overMemory:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_processes_over_memory_threshold", Help: "Number of Erlang processes over the configured memory threshold."}, nodeLabels),
		maxMessageQueue:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_process_message_queue_max", Help: "Largest process mailbox length observed during the latest bounded scan."}, nodeLabels),
		overQueue:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_processes_over_message_queue_threshold", Help: "Number of Erlang processes over the configured mailbox threshold."}, nodeLabels),
		queueThreshold:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_message_queue_threshold", Help: "Configured per-process message queue alert threshold."}, nodeLabels),
		memoryThreshold:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_process_memory_threshold_bytes", Help: "Configured per-process memory alert threshold in bytes."}, nodeLabels),
		capacityThreshold: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_capacity_alert_threshold_ratio", Help: "Configured Process, Atom, and Port capacity alert threshold ratio."}, nodeLabels),
		runQueueDisplay:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_run_queue_display_multiplier", Help: "Configured Run Queue dashboard display multiplier relative to online schedulers."}, nodeLabels),
		runQueueAlert:     prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_vm_run_queue_alert_multiplier", Help: "Configured Run Queue alert multiplier relative to online schedulers."}, nodeLabels),
		memoryProcessInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_process_memory_max_info", Help: "Identity of the process with the largest memory value in the latest bounded scan."}, processInfoLabels),
		queueProcessInfo:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "erlang_process_message_queue_max_info", Help: "Identity of the process with the largest mailbox in the latest bounded scan."}, processInfoLabels),
	}
	registry.MustRegister(m.serverUp, m.scrapeDuration, m.lastSuccess, m.staleThreshold, m.collectErrors, m.hostUp, m.hostCPU, m.hostCPULogical, m.hostCPUCorePct, m.hostLoad1, m.hostMemoryTotal, m.hostMemoryAvail, m.hostFSSize, m.hostFSAvail, m.hostUptime, m.hostNetworkRX, m.hostNetworkTX, m.hostCPUThreshold, m.hostMemThreshold, m.nodeUp, m.registeredUsers, m.onlineUsers, m.mnodeAvailable, m.mnodeState, m.processCount, m.processLimit, m.memoryBytes, m.cpuUsageRatio, m.residentMemory, m.vmMemoryDisplay, m.vmMemoryAlert, m.runQueue, m.schedulers, m.atomCount, m.atomLimit, m.portCount, m.portLimit, m.maxProcessMemory, m.overMemory, m.maxMessageQueue, m.overQueue, m.queueThreshold, m.memoryThreshold, m.capacityThreshold, m.runQueueDisplay, m.runQueueAlert, m.memoryProcessInfo, m.queueProcessInfo)
	return m
}

type Poller struct {
	mu         sync.RWMutex
	config     config.Exporter
	collector  Collector
	metrics    *Metrics
	status     *runtimestatus.Store
	logger     *slog.Logger
	wg         sync.WaitGroup
	nodesMu    sync.Mutex
	knownNodes map[string]map[string]struct{}
	controls   map[string]*scheduleControl
	workers    map[string]*serverWorker
	ctx        context.Context
	started    bool
}

type serverWorker struct {
	control *scheduleControl
	updates chan config.Server
	cancel  context.CancelFunc
	done    chan struct{}
}

// scheduleControl keeps collection scheduling independent from Grafana query refresh.
// Auto mode is the default; refresh mode pauses the timer and only manual requests collect.
type scheduleControl struct {
	mu     sync.RWMutex
	auto   bool
	wake   chan struct{}
	manual chan struct{}
}

func newScheduleControl() *scheduleControl {
	return &scheduleControl{auto: true, wake: make(chan struct{}, 1), manual: make(chan struct{}, 1)}
}

func (c *scheduleControl) isAuto() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.auto
}

func (c *scheduleControl) setAuto(auto bool) {
	c.mu.Lock()
	c.auto = auto
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *scheduleControl) requestManual() {
	select {
	case c.manual <- struct{}{}:
	default:
	}
}

func NewPoller(cfg config.Exporter, collector Collector, metrics *Metrics, status *runtimestatus.Store, logger *slog.Logger) *Poller {
	return &Poller{
		config: cfg, collector: collector, metrics: metrics, status: status, logger: logger,
		knownNodes: make(map[string]map[string]struct{}),
		workers:    make(map[string]*serverWorker),
		controls: func() map[string]*scheduleControl {
			controls := make(map[string]*scheduleControl, len(cfg.Servers))
			for _, server := range cfg.Servers {
				controls[server.ID] = newScheduleControl()
			}
			return controls
		}(),
	}
}

func (p *Poller) Start(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return
	}
	p.started = true
	p.ctx = ctx
	for _, server := range p.config.Servers {
		if !server.IsEnabled() {
			continue
		}
		p.startServerLocked(server, p.controls[server.ID])
	}
}

func (p *Poller) Wait() { p.wg.Wait() }

// ApplyConfig atomically changes the configuration visible to HTTP handlers,
// then reconciles per-server workers. Existing schedule mode is retained for a
// stable server ID; every added or changed enabled server is collected once
// immediately so Prometheus can expose values produced from the new config.
func (p *Poller) ApplyConfig(cfg config.Exporter) {
	p.mu.Lock()
	var retired []struct {
		id   string
		done <-chan struct{}
	}
	oldServers := make(map[string]config.Server, len(p.config.Servers))
	for _, server := range p.config.Servers {
		oldServers[server.ID] = server
	}

	controls := make(map[string]*scheduleControl, len(cfg.Servers))
	for _, server := range cfg.Servers {
		control := p.controls[server.ID]
		if control == nil {
			control = newScheduleControl()
		}
		controls[server.ID] = control
	}
	p.config = cfg
	p.controls = controls
	if !p.started || p.ctx == nil || p.ctx.Err() != nil {
		p.mu.Unlock()
		return
	}

	newServers := make(map[string]config.Server, len(cfg.Servers))
	for _, server := range cfg.Servers {
		newServers[server.ID] = server
	}
	for id, worker := range p.workers {
		server, exists := newServers[id]
		if !exists || !server.IsEnabled() {
			delete(p.workers, id)
			worker.cancel()
			retired = append(retired, struct {
				id   string
				done <-chan struct{}
			}{id: id, done: worker.done})
			continue
		}
		if !reflect.DeepEqual(oldServers[id], server) {
			queueServerUpdate(worker.updates, server)
		}
	}
	for _, server := range cfg.Servers {
		if !server.IsEnabled() || p.workers[server.ID] != nil {
			continue
		}
		p.startServerLocked(server, controls[server.ID])
	}
	p.mu.Unlock()
	for _, worker := range retired {
		<-worker.done
		p.deleteServerState(worker.id)
	}
	p.logger.Info("exporter configuration applied", "event", "config-applied", "servers", len(cfg.Servers))
}

func (p *Poller) startServerLocked(server config.Server, control *scheduleControl) {
	ctx, cancel := context.WithCancel(p.ctx)
	worker := &serverWorker{control: control, updates: make(chan config.Server, 1), cancel: cancel, done: make(chan struct{})}
	p.workers[server.ID] = worker
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer close(worker.done)
		p.runServer(ctx, server, worker)
	}()
}

func queueServerUpdate(updates chan config.Server, server config.Server) {
	select {
	case updates <- server:
		return
	default:
	}
	select {
	case <-updates:
	default:
	}
	updates <- server
}

func (p *Poller) runServer(ctx context.Context, server config.Server, worker *serverWorker) {
	control := worker.control
	if scope := p.collect(ctx, server); scope.Required() {
		p.confirm(ctx, server, scope)
	}
	timer := time.NewTimer(server.PollInterval.Duration)
	defer timer.Stop()
	for {
		if control.isAuto() {
			select {
			case <-ctx.Done():
				return
			case updated := <-worker.updates:
				p.applyServerUpdate(ctx, server, updated)
				server = updated
				resetTimer(timer, server.PollInterval.Duration)
			case <-control.wake:
				resetTimer(timer, server.PollInterval.Duration)
			case <-control.manual:
				p.collectAndConfirm(ctx, server)
				if control.isAuto() {
					resetTimer(timer, server.PollInterval.Duration)
				}
			case <-timer.C:
				p.collectAndConfirm(ctx, server)
				resetTimer(timer, server.PollInterval.Duration)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case updated := <-worker.updates:
			p.applyServerUpdate(ctx, server, updated)
			server = updated
			if control.isAuto() {
				resetTimer(timer, server.PollInterval.Duration)
			}
		case <-control.wake:
			if control.isAuto() {
				resetTimer(timer, server.PollInterval.Duration)
			}
		case <-control.manual:
			p.collectAndConfirm(ctx, server)
		}
	}
}

func (p *Poller) applyServerUpdate(ctx context.Context, previous, current config.Server) {
	if previous.Name != current.Name || previous.Address != current.Address {
		p.deleteServerState(current.ID)
	}
	p.collectAndConfirm(ctx, current)
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

func (p *Poller) collectAndConfirm(ctx context.Context, server config.Server) {
	if scope := p.collect(ctx, server); scope.Required() {
		p.confirm(ctx, server, scope)
	}
}

func (p *Poller) confirm(ctx context.Context, server config.Server, scope confirmationScope) {
	started := time.Now()
	schedule := confirmationSchedule(server, scope)
	for i := 0; i < len(schedule); i++ {
		item := schedule[i]
		if !waitForConfirmation(ctx, time.Until(started.Add(item.After))) {
			return
		}
		p.logger.Info("running one abnormal-state confirmation", "event", "server-confirmation-started", "server", server.ID, "full", item.Scope.Full, "host", item.Scope.Host, "failure_nodes", item.Scope.FailureNodes, "run_queue_nodes", item.Scope.RunQueueNodes, "delay", item.After)

		confirmed := p.runConfirmation(ctx, server, item.Scope)
		if !confirmed.Required() {
			p.logger.Info("abnormal state cleared during confirmation", "event", "server-confirmation-cleared", "server", server.ID)
			continue
		}
		p.logger.Warn("abnormal state confirmed", "event", "server-abnormal-confirmed", "server", server.ID, "full", confirmed.Full, "host", confirmed.Host, "failure_nodes", confirmed.FailureNodes, "run_queue_nodes", confirmed.RunQueueNodes)
		if len(item.Scope.FailureNodes) == 0 && len(confirmed.FailureNodes) > 0 {
			schedule = addFailureConfirmation(schedule, i+1, server.NodeFailureConfirmInterval.Duration, confirmed.FailureNodes)
		}
	}
}

type scheduledConfirmation struct {
	After time.Duration
	Scope confirmationScope
}

func confirmationSchedule(server config.Server, scope confirmationScope) []scheduledConfirmation {
	fast := confirmationScope{Full: scope.Full, Host: scope.Host, RunQueueNodes: append([]string(nil), scope.RunQueueNodes...)}
	schedule := make([]scheduledConfirmation, 0, 2)
	if fast.Required() {
		schedule = append(schedule, scheduledConfirmation{After: server.ConfirmInterval.Duration, Scope: fast})
	}
	if len(scope.FailureNodes) > 0 {
		schedule = append(schedule, scheduledConfirmation{
			After: server.NodeFailureConfirmInterval.Duration,
			Scope: confirmationScope{FailureNodes: append([]string(nil), scope.FailureNodes...)},
		})
	}
	sort.SliceStable(schedule, func(i, j int) bool { return schedule[i].After < schedule[j].After })
	return schedule
}

func addFailureConfirmation(schedule []scheduledConfirmation, start int, after time.Duration, nodes []string) []scheduledConfirmation {
	for i := start; i < len(schedule); i++ {
		if len(schedule[i].Scope.FailureNodes) > 0 {
			schedule[i].Scope.FailureNodes = mergeNodeNames(schedule[i].Scope.FailureNodes, nodes)
			return schedule
		}
	}
	schedule = append(schedule, scheduledConfirmation{After: after, Scope: confirmationScope{FailureNodes: append([]string(nil), nodes...)}})
	sort.SliceStable(schedule[start:], func(i, j int) bool { return schedule[start+i].After < schedule[start+j].After })
	return schedule
}

func mergeNodeNames(left, right []string) []string {
	unique := make(map[string]struct{}, len(left)+len(right))
	for _, node := range left {
		unique[node] = struct{}{}
	}
	for _, node := range right {
		unique[node] = struct{}{}
	}
	merged := make([]string, 0, len(unique))
	for node := range unique {
		merged = append(merged, node)
	}
	sort.Strings(merged)
	return merged
}

func waitForConfirmation(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (p *Poller) runConfirmation(ctx context.Context, server config.Server, scope confirmationScope) confirmationScope {
	if scope.Full {
		return p.collect(ctx, server)
	}
	started := time.Now()
	result, err := p.collector.CollectSelected(ctx, server, scope.targetNodes(), scope.Host)
	p.applySelectedConfirmation(server, scope, result, err, time.Since(started))
	return confirmationScopeFor(server, result, err)
}

func (p *Poller) collect(ctx context.Context, server config.Server) confirmationScope {
	started := time.Now()
	labels := []string{server.ID, server.Name, server.Address}
	p.metrics.staleThreshold.WithLabelValues(labels...).Set(server.CollectionStaleAfter.Duration.Seconds())
	p.metrics.hostCPUThreshold.WithLabelValues(labels...).Set(float64(server.HostCPUAlertPercent) / 100)
	p.metrics.hostMemThreshold.WithLabelValues(labels...).Set(float64(server.HostMemoryAlertPercent) / 100)
	result, err := p.collector.Collect(ctx, server)
	duration := time.Since(started)
	p.metrics.scrapeDuration.WithLabelValues(labels...).Set(duration.Seconds())
	status := runtimestatus.ServerStatus{LastAttempt: time.Now().UTC(), DurationMS: duration.Milliseconds()}
	if result.HostCollected {
		p.metrics.hostUp.WithLabelValues(labels...).Set(1)
		p.setHostMetrics(server, result.Host)
	} else {
		p.metrics.hostUp.WithLabelValues(labels...).Set(0)
		status.HostError = result.HostError
	}
	if err != nil {
		p.metrics.serverUp.WithLabelValues(labels...).Set(0)
		p.metrics.collectErrors.WithLabelValues(labels...).Inc()
		status.State = "down"
		status.LastError = err.Error()
		status.FailedNodes = len(result.Failures)
		status.NodeErrors = make(map[string]string, len(result.Failures))
		for _, failure := range result.Failures {
			p.metrics.nodeUp.WithLabelValues(server.ID, server.Name, failure.Name).Set(0)
			status.NodeErrors[failure.Name] = failure.Error
		}
		// A transport or discovery error has no reliable node-level result. Mark
		// every previously known node down so the node table cannot look healthy
		// while the server is unreachable.
		if len(result.Failures) == 0 {
			for _, node := range p.knownNodeNames(server.ID) {
				p.metrics.nodeUp.WithLabelValues(server.ID, server.Name, node).Set(0)
				status.NodeErrors[node] = err.Error()
			}
			status.FailedNodes = len(status.NodeErrors)
		}
		if len(status.NodeErrors) == 0 {
			status.NodeErrors = nil
		}
		if len(result.Failures) > 0 {
			p.removeStaleNodes(server, result)
		}
		if previous, ok := p.status.Snapshot().Servers[server.ID]; ok {
			status.LastSuccess = previous.LastSuccess
		}
		p.logger.Error("collection failed", "event", "server-collection-failed", "server", server.ID, "duration_ms", duration.Milliseconds(), "error", err)
	} else {
		now := time.Now().UTC()
		p.metrics.serverUp.WithLabelValues(labels...).Set(1)
		p.metrics.lastSuccess.WithLabelValues(labels...).Set(float64(now.Unix()))
		status.State = "healthy"
		status.LastSuccess = now
		status.Nodes = len(result.Nodes)
		status.FailedNodes = len(result.Failures)
		if len(result.Failures) > 0 {
			status.State = "degraded"
			status.NodeErrors = make(map[string]string, len(result.Failures))
		}
		if result.HostError != "" {
			status.State = "degraded"
		}
		for _, node := range result.Nodes {
			p.metrics.nodeUp.WithLabelValues(server.ID, server.Name, node.Name).Set(1)
			p.setNodeMetrics(server, node)
		}
		for _, failure := range result.Failures {
			p.metrics.nodeUp.WithLabelValues(server.ID, server.Name, failure.Name).Set(0)
			status.NodeErrors[failure.Name] = failure.Error
		}
		p.removeStaleNodes(server, result)
		if len(result.Failures) > 0 {
			p.logger.Warn("collection partially succeeded", "event", "server-collection-degraded", "server", server.ID, "nodes", len(result.Nodes), "failed_nodes", len(result.Failures), "duration_ms", duration.Milliseconds())
		} else {
			p.logger.Info("collection succeeded", "event", "server-collection-succeeded", "server", server.ID, "nodes", len(result.Nodes), "duration_ms", duration.Milliseconds())
		}
	}
	if err := p.status.Update(server.ID, status); err != nil {
		p.logger.Error("status persistence failed", "event", "status-persist-failed", "server", server.ID, "error", err)
	}
	return confirmationScopeFor(server, result, err)
}

func (p *Poller) knownNodeNames(serverID string) []string {
	p.nodesMu.Lock()
	defer p.nodesMu.Unlock()
	known := p.knownNodes[serverID]
	result := make([]string, 0, len(known))
	for node := range known {
		result = append(result, node)
	}
	sort.Strings(result)
	return result
}

func collectionAnomalous(server config.Server, result sshprobe.Result, err error) bool {
	return confirmationScopeFor(server, result, err).Required()
}

type confirmationScope struct {
	Full          bool
	Host          bool
	FailureNodes  []string
	RunQueueNodes []string
}

func (s confirmationScope) Required() bool {
	return s.Full || s.Host || len(s.FailureNodes) > 0 || len(s.RunQueueNodes) > 0
}

func (s confirmationScope) targetNodes() []string {
	return mergeNodeNames(s.FailureNodes, s.RunQueueNodes)
}

func confirmationScopeFor(server config.Server, result sshprobe.Result, err error) confirmationScope {
	scope := confirmationScope{Host: result.HostError != ""}
	failureNodes := make(map[string]struct{}, len(result.Failures))
	for _, failure := range result.Failures {
		failureNodes[failure.Name] = struct{}{}
	}
	for node := range failureNodes {
		scope.FailureNodes = append(scope.FailureNodes, node)
	}
	sort.Strings(scope.FailureNodes)
	for _, node := range result.Nodes {
		if node.SchedulersOnline > 0 && node.RunQueue > node.SchedulersOnline*server.RunQueueAlertMultiple {
			scope.RunQueueNodes = append(scope.RunQueueNodes, node.Name)
		}
	}
	sort.Strings(scope.RunQueueNodes)
	// A transport or discovery failure has no reliable node scope. It receives
	// exactly one full retry; node-level RPC failures remain targeted.
	if err != nil && len(scope.FailureNodes) == 0 && !scope.Host {
		scope.Full = true
	}
	return scope
}

func (p *Poller) applySelectedConfirmation(server config.Server, scope confirmationScope, result sshprobe.Result, collectErr error, duration time.Duration) {
	labels := []string{server.ID, server.Name, server.Address}
	targetNodes := scope.targetNodes()
	if scope.Host {
		if result.HostCollected {
			p.metrics.hostUp.WithLabelValues(labels...).Set(1)
			p.setHostMetrics(server, result.Host)
		} else {
			p.metrics.hostUp.WithLabelValues(labels...).Set(0)
		}
	}

	succeeded := make(map[string]string, len(result.Nodes)*2)
	failed := make(map[string]string, len(result.Failures))
	for _, node := range result.Nodes {
		succeeded[node.Name] = node.Name
		succeeded[shortNodeName(node.Name)] = node.Name
		p.metrics.nodeUp.WithLabelValues(server.ID, server.Name, node.Name).Set(1)
		p.setNodeMetrics(server, node)
	}
	for _, failure := range result.Failures {
		failed[failure.Name] = failure.Error
		p.metrics.nodeUp.WithLabelValues(server.ID, server.Name, failure.Name).Set(0)
	}
	if collectErr != nil {
		for _, node := range targetNodes {
			if _, ok := succeeded[node]; ok {
				continue
			}
			if _, ok := failed[node]; !ok {
				failed[node] = collectErr.Error()
				p.metrics.nodeUp.WithLabelValues(server.ID, server.Name, node).Set(0)
			}
		}
	}

	snapshot := p.status.Snapshot()
	previous, exists := snapshot.Servers[server.ID]
	if !exists {
		return
	}
	status := previous
	status.LastAttempt = time.Now().UTC()
	status.DurationMS = duration.Milliseconds()
	status.NodeErrors = cloneErrors(previous.NodeErrors)
	for _, node := range targetNodes {
		_, wasFailed := status.NodeErrors[node]
		if actualName, ok := succeeded[node]; ok {
			delete(status.NodeErrors, node)
			if actualName != node {
				p.deleteNodeMetrics(server, node)
			}
			if wasFailed {
				status.Nodes++
			}
			continue
		}
		if message, ok := failed[node]; ok {
			status.NodeErrors[node] = message
			if !wasFailed && status.Nodes > 0 {
				status.Nodes--
			}
		}
	}
	status.FailedNodes = len(status.NodeErrors)
	if len(status.NodeErrors) == 0 {
		status.NodeErrors = nil
	}
	if scope.Host {
		if result.HostCollected {
			status.HostError = ""
		} else if result.HostError != "" {
			status.HostError = result.HostError
		} else if collectErr != nil {
			status.HostError = collectErr.Error()
		}
	}

	status.LastError = ""
	status.State = "healthy"
	if status.Nodes == 0 && status.FailedNodes > 0 {
		status.State = "down"
		if collectErr != nil {
			status.LastError = collectErr.Error()
		}
	} else if status.FailedNodes > 0 || status.HostError != "" || collectErr != nil {
		status.State = "degraded"
		if collectErr != nil {
			status.LastError = collectErr.Error()
		}
	}
	if previous.State == "down" && len(result.Nodes) > 0 && collectErr == nil {
		now := time.Now().UTC()
		status.LastSuccess = now
		p.metrics.serverUp.WithLabelValues(labels...).Set(1)
		p.metrics.lastSuccess.WithLabelValues(labels...).Set(float64(now.Unix()))
	}
	if err := p.status.Update(server.ID, status); err != nil {
		p.logger.Error("confirmation status persistence failed", "event", "status-persist-failed", "server", server.ID, "error", err)
	}
}

func shortNodeName(name string) string {
	short, _, _ := strings.Cut(name, "@")
	return short
}

func cloneErrors(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (p *Poller) setHostMetrics(server config.Server, host sshprobe.Host) {
	labels := []string{server.ID, server.Name, server.Address}
	if host.CPUUsageValid {
		p.metrics.hostCPU.WithLabelValues(labels...).Set(host.CPUUsageRatio)
		p.metrics.hostCPUCorePct.WithLabelValues(labels...).Set(host.CPUUsageRatio * float64(host.LogicalCPUs) * 100)
	}
	p.metrics.hostCPULogical.WithLabelValues(labels...).Set(float64(host.LogicalCPUs))
	p.metrics.hostLoad1.WithLabelValues(labels...).Set(host.Load1)
	p.metrics.hostMemoryTotal.WithLabelValues(labels...).Set(float64(host.MemoryTotalBytes))
	p.metrics.hostMemoryAvail.WithLabelValues(labels...).Set(float64(host.MemoryAvailableBytes))
	p.metrics.hostFSSize.WithLabelValues(labels...).Set(float64(host.FilesystemSizeBytes))
	p.metrics.hostFSAvail.WithLabelValues(labels...).Set(float64(host.FilesystemAvailableBytes))
	p.metrics.hostUptime.WithLabelValues(labels...).Set(host.UptimeSeconds)
	p.metrics.hostNetworkRX.WithLabelValues(labels...).Set(host.NetworkReceiveBytesPerSecond)
	p.metrics.hostNetworkTX.WithLabelValues(labels...).Set(host.NetworkTransmitBytesPerSecond)
}

func (p *Poller) removeStaleNodes(server config.Server, result sshprobe.Result) {
	current := make(map[string]struct{}, len(result.Nodes)+len(result.Failures))
	for _, node := range result.Nodes {
		current[node.Name] = struct{}{}
	}
	for _, failure := range result.Failures {
		current[failure.Name] = struct{}{}
	}
	p.nodesMu.Lock()
	defer p.nodesMu.Unlock()
	for node := range p.knownNodes[server.ID] {
		if _, exists := current[node]; !exists {
			p.deleteNodeMetrics(server, node)
			p.logger.Info("removed stale node metrics", "event", "node-metrics-removed", "server", server.ID, "node", node)
		}
	}
	p.knownNodes[server.ID] = current
}

func (p *Poller) deleteNodeMetrics(server config.Server, node string) {
	labels := []string{server.ID, server.Name, node}
	p.metrics.nodeUp.DeleteLabelValues(labels...)
	p.metrics.registeredUsers.DeleteLabelValues(labels...)
	p.metrics.onlineUsers.DeleteLabelValues(labels...)
	p.metrics.mnodeAvailable.DeleteLabelValues(labels...)
	p.metrics.mnodeState.DeletePartialMatch(prometheus.Labels{"server": server.ID, "name": server.Name, "node": node})
	p.metrics.processCount.DeleteLabelValues(labels...)
	p.metrics.processLimit.DeleteLabelValues(labels...)
	p.metrics.memoryBytes.DeleteLabelValues(labels...)
	p.metrics.cpuUsageRatio.DeleteLabelValues(labels...)
	p.metrics.residentMemory.DeleteLabelValues(labels...)
	p.metrics.runQueue.DeleteLabelValues(labels...)
	p.metrics.schedulers.DeleteLabelValues(labels...)
	p.metrics.atomCount.DeleteLabelValues(labels...)
	p.metrics.atomLimit.DeleteLabelValues(labels...)
	p.metrics.portCount.DeleteLabelValues(labels...)
	p.metrics.portLimit.DeleteLabelValues(labels...)
	p.metrics.maxProcessMemory.DeleteLabelValues(labels...)
	p.metrics.overMemory.DeleteLabelValues(labels...)
	p.metrics.maxMessageQueue.DeleteLabelValues(labels...)
	p.metrics.overQueue.DeleteLabelValues(labels...)
	p.metrics.queueThreshold.DeleteLabelValues(labels...)
	p.metrics.memoryThreshold.DeleteLabelValues(labels...)
	p.metrics.vmMemoryDisplay.DeleteLabelValues(labels...)
	p.metrics.vmMemoryAlert.DeleteLabelValues(labels...)
	p.metrics.capacityThreshold.DeleteLabelValues(labels...)
	p.metrics.runQueueDisplay.DeleteLabelValues(labels...)
	p.metrics.runQueueAlert.DeleteLabelValues(labels...)
	p.metrics.memoryProcessInfo.DeletePartialMatch(prometheus.Labels{"server": server.ID, "name": server.Name, "node": node})
	p.metrics.queueProcessInfo.DeletePartialMatch(prometheus.Labels{"server": server.ID, "name": server.Name, "node": node})
}

func (p *Poller) setNodeMetrics(server config.Server, node sshprobe.Node) {
	labels := []string{server.ID, server.Name, node.Name}
	p.metrics.processCount.WithLabelValues(labels...).Set(float64(node.ProcessCount))
	if node.PlayerCountsValid {
		p.metrics.registeredUsers.WithLabelValues(labels...).Set(float64(node.RegisteredUsers))
		p.metrics.onlineUsers.WithLabelValues(labels...).Set(float64(node.OnlineUsers))
	} else {
		// Keep stable NaN series on nodes that have not deployed the optional
		// mlib_sys interface; never substitute BEAM process or connection counts.
		p.metrics.registeredUsers.WithLabelValues(labels...).Set(math.NaN())
		p.metrics.onlineUsers.WithLabelValues(labels...).Set(math.NaN())
	}
	p.metrics.mnodeState.DeletePartialMatch(prometheus.Labels{"server": server.ID, "name": server.Name, "node": node.Name})
	if node.MNodeConnectionsValid {
		p.metrics.mnodeAvailable.WithLabelValues(labels...).Set(1)
		for _, connection := range node.MNodeConnections {
			if connection.Type != "central" && connection.Type != "region" {
				continue
			}
			p.metrics.mnodeState.WithLabelValues(server.ID, server.Name, node.Name, connection.NodeID, connection.Node, connection.Type).Set(float64(connection.State))
		}
	} else {
		p.metrics.mnodeAvailable.WithLabelValues(labels...).Set(0)
	}
	p.metrics.processLimit.WithLabelValues(labels...).Set(float64(node.ProcessLimit))
	p.metrics.memoryBytes.WithLabelValues(labels...).Set(float64(node.MemoryBytes))
	if node.CPUUsageValid {
		p.metrics.cpuUsageRatio.WithLabelValues(labels...).Set(node.CPUUsageRatio)
	} else {
		p.metrics.cpuUsageRatio.WithLabelValues(labels...).Set(math.NaN())
	}
	if node.ResidentMemoryValid {
		p.metrics.residentMemory.WithLabelValues(labels...).Set(float64(node.ResidentMemoryBytes))
	} else {
		p.metrics.residentMemory.WithLabelValues(labels...).Set(math.NaN())
	}
	p.metrics.runQueue.WithLabelValues(labels...).Set(float64(node.RunQueue))
	p.metrics.schedulers.WithLabelValues(labels...).Set(float64(node.SchedulersOnline))
	p.metrics.atomCount.WithLabelValues(labels...).Set(float64(node.AtomCount))
	p.metrics.atomLimit.WithLabelValues(labels...).Set(float64(node.AtomLimit))
	p.metrics.portCount.WithLabelValues(labels...).Set(float64(node.PortCount))
	p.metrics.portLimit.WithLabelValues(labels...).Set(float64(node.PortLimit))
	p.metrics.maxProcessMemory.WithLabelValues(labels...).Set(float64(node.MaxProcessMemoryBytes))
	p.metrics.overMemory.WithLabelValues(labels...).Set(float64(node.ProcessesOverMemoryThreshold))
	p.metrics.maxMessageQueue.WithLabelValues(labels...).Set(float64(node.MaxMessageQueueLength))
	p.metrics.overQueue.WithLabelValues(labels...).Set(float64(node.ProcessesOverQueueThreshold))
	p.metrics.queueThreshold.WithLabelValues(labels...).Set(float64(server.QueueThreshold))
	p.metrics.memoryThreshold.WithLabelValues(labels...).Set(float64(server.MemoryThresholdMBytes * 1024 * 1024))
	p.metrics.vmMemoryDisplay.WithLabelValues(labels...).Set(float64(server.VMMemoryDisplayGBytes * 1024 * 1024 * 1024))
	p.metrics.vmMemoryAlert.WithLabelValues(labels...).Set(float64(server.VMMemoryAlertGBytes * 1024 * 1024 * 1024))
	p.metrics.capacityThreshold.WithLabelValues(labels...).Set(float64(server.CapacityAlertPercent) / 100)
	p.metrics.runQueueDisplay.WithLabelValues(labels...).Set(float64(server.RunQueueDisplayMultiple))
	p.metrics.runQueueAlert.WithLabelValues(labels...).Set(float64(server.RunQueueAlertMultiple))
	p.setProcessInfo(p.metrics.memoryProcessInfo, server, node.Name, node.MaxMemoryProcess, float64(node.MaxProcessMemoryBytes))
	p.setProcessInfo(p.metrics.queueProcessInfo, server, node.Name, node.MaxQueueProcess, float64(node.MaxMessageQueueLength))
}

func (p *Poller) setProcessInfo(metric *prometheus.GaugeVec, server config.Server, node string, process sshprobe.ProcessIdentity, value float64) {
	metric.DeletePartialMatch(prometheus.Labels{"server": server.ID, "name": server.Name, "node": node})
	if process.PID == "" {
		return
	}
	metric.WithLabelValues(server.ID, server.Name, node, process.PID, process.RegisteredName, process.InitialCall, process.CurrentFunction).Set(value)
}

func (p *Poller) deleteServerState(serverID string) {
	labels := prometheus.Labels{"server": serverID}
	p.metrics.serverUp.DeletePartialMatch(labels)
	p.metrics.scrapeDuration.DeletePartialMatch(labels)
	p.metrics.lastSuccess.DeletePartialMatch(labels)
	p.metrics.staleThreshold.DeletePartialMatch(labels)
	p.metrics.collectErrors.DeletePartialMatch(labels)
	p.metrics.hostUp.DeletePartialMatch(labels)
	p.metrics.hostCPU.DeletePartialMatch(labels)
	p.metrics.hostCPULogical.DeletePartialMatch(labels)
	p.metrics.hostCPUCorePct.DeletePartialMatch(labels)
	p.metrics.hostLoad1.DeletePartialMatch(labels)
	p.metrics.hostMemoryTotal.DeletePartialMatch(labels)
	p.metrics.hostMemoryAvail.DeletePartialMatch(labels)
	p.metrics.hostFSSize.DeletePartialMatch(labels)
	p.metrics.hostFSAvail.DeletePartialMatch(labels)
	p.metrics.hostUptime.DeletePartialMatch(labels)
	p.metrics.hostNetworkRX.DeletePartialMatch(labels)
	p.metrics.hostNetworkTX.DeletePartialMatch(labels)
	p.metrics.hostCPUThreshold.DeletePartialMatch(labels)
	p.metrics.hostMemThreshold.DeletePartialMatch(labels)
	p.metrics.nodeUp.DeletePartialMatch(labels)
	p.metrics.registeredUsers.DeletePartialMatch(labels)
	p.metrics.onlineUsers.DeletePartialMatch(labels)
	p.metrics.mnodeAvailable.DeletePartialMatch(labels)
	p.metrics.mnodeState.DeletePartialMatch(labels)
	p.metrics.processCount.DeletePartialMatch(labels)
	p.metrics.processLimit.DeletePartialMatch(labels)
	p.metrics.memoryBytes.DeletePartialMatch(labels)
	p.metrics.cpuUsageRatio.DeletePartialMatch(labels)
	p.metrics.residentMemory.DeletePartialMatch(labels)
	p.metrics.vmMemoryDisplay.DeletePartialMatch(labels)
	p.metrics.vmMemoryAlert.DeletePartialMatch(labels)
	p.metrics.runQueue.DeletePartialMatch(labels)
	p.metrics.schedulers.DeletePartialMatch(labels)
	p.metrics.atomCount.DeletePartialMatch(labels)
	p.metrics.atomLimit.DeletePartialMatch(labels)
	p.metrics.portCount.DeletePartialMatch(labels)
	p.metrics.portLimit.DeletePartialMatch(labels)
	p.metrics.maxProcessMemory.DeletePartialMatch(labels)
	p.metrics.overMemory.DeletePartialMatch(labels)
	p.metrics.maxMessageQueue.DeletePartialMatch(labels)
	p.metrics.overQueue.DeletePartialMatch(labels)
	p.metrics.queueThreshold.DeletePartialMatch(labels)
	p.metrics.memoryThreshold.DeletePartialMatch(labels)
	p.metrics.capacityThreshold.DeletePartialMatch(labels)
	p.metrics.runQueueDisplay.DeletePartialMatch(labels)
	p.metrics.runQueueAlert.DeletePartialMatch(labels)
	p.metrics.memoryProcessInfo.DeletePartialMatch(labels)
	p.metrics.queueProcessInfo.DeletePartialMatch(labels)
	p.nodesMu.Lock()
	delete(p.knownNodes, serverID)
	p.nodesMu.Unlock()
	if err := p.status.Delete(serverID); err != nil {
		p.logger.Error("removed server status persistence failed", "event", "status-persist-failed", "server", serverID, "error", err)
	}
}

// ScheduleHandler controls the exporter scheduler. It intentionally does not
// reuse Grafana's query-refresh controls: POST {"server":"...","mode":"auto"}
// enables the configured poll interval; mode "refresh" pauses it.
func (p *Poller) ScheduleHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Server string `json:"server"`
			Mode   string `json:"mode"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		server, control, err := p.controlFor(body.Server)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if !server.IsEnabled() {
			http.Error(w, "server is disabled", http.StatusConflict)
			return
		}
		var auto bool
		switch strings.ToLower(strings.TrimSpace(body.Mode)) {
		case "auto":
			auto = true
		case "refresh", "manual":
			auto = false
		default:
			http.Error(w, "mode must be auto or refresh", http.StatusBadRequest)
			return
		}
		control.setAuto(auto)
		writeJSON(w, http.StatusOK, map[string]any{"server": server.ID, "name": server.Name, "mode": scheduleMode(auto)})
	}
}

// CollectHandler queues one collection. In refresh mode this is the only way
// to collect; it never re-enables the configured timer.
func (p *Poller) CollectHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Server string `json:"server"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		server, control, err := p.controlFor(body.Server)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if !server.IsEnabled() {
			http.Error(w, "server is disabled", http.StatusConflict)
			return
		}
		control.requestManual()
		writeJSON(w, http.StatusAccepted, map[string]any{"server": server.ID, "name": server.Name, "mode": scheduleMode(control.isAuto()), "queued": true})
	}
}

func (p *Poller) controlFor(key string) (config.Server, *scheduleControl, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	key = strings.TrimSpace(key)
	for _, server := range p.config.Servers {
		if server.ID == key || server.Name == key || server.Address == key {
			return server, p.controls[server.ID], nil
		}
	}
	return config.Server{}, nil, fmt.Errorf("unknown server %q", key)
}

func scheduleMode(auto bool) string {
	if auto {
		return "auto"
	}
	return "refresh"
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func StatusHandler(store *runtimestatus.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(store.Snapshot())
	}
}

func HealthHandler(store *runtimestatus.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		snapshot := store.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		if snapshot.State == "down" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": snapshot.State, "updated_at": snapshot.UpdatedAt})
	}
}
