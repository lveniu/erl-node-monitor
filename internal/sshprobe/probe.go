package sshprobe

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"erlang-monitor/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Node struct {
	Name                         string
	CPUUsageRatio                float64
	CPUUsageValid                bool
	ResidentMemoryBytes          uint64
	ResidentMemoryValid          bool
	RegisteredUsers              uint64
	OnlineUsers                  uint64
	PlayerCountsValid            bool
	MNodeConnections             []MNodeConnection
	MNodeConnectionsValid        bool
	ProcessCount                 uint64
	ProcessLimit                 uint64
	MemoryBytes                  uint64
	RunQueue                     uint64
	SchedulersOnline             uint64
	AtomCount                    uint64
	AtomLimit                    uint64
	PortCount                    uint64
	PortLimit                    uint64
	MaxProcessMemoryBytes        uint64
	ProcessesOverMemoryThreshold uint64
	MaxMessageQueueLength        uint64
	ProcessesOverQueueThreshold  uint64
	MaxMemoryProcess             ProcessIdentity
	MaxQueueProcess              ProcessIdentity
}

type ProcessIdentity struct {
	PID             string
	RegisteredName  string
	InitialCall     string
	CurrentFunction string
}

type Result struct {
	Nodes         []Node
	Failures      []NodeFailure
	Host          Host
	HostError     string
	HostCollected bool
}

type Host struct {
	Load1                         float64
	UptimeSeconds                 float64
	LogicalCPUs                   uint64
	MemoryTotalBytes              uint64
	MemoryAvailableBytes          uint64
	FilesystemSizeBytes           uint64
	FilesystemAvailableBytes      uint64
	NetworkReceiveBytes           uint64
	NetworkTransmitBytes          uint64
	NetworkReceiveBytesPerSecond  float64
	NetworkTransmitBytesPerSecond float64
	CPUUsageRatio                 float64
	CPUUsageValid                 bool
}

type NodeFailure struct {
	Name  string
	Error string
}

type beamProcess struct {
	PID       int
	NodeName  string
	Cookie    string
	Ebin      string
	ErlBinary string
	IsServer  bool
}

type cpuSample struct {
	total uint64
	idle  uint64
}

type nodeCPUSample struct {
	total   uint64
	process uint64
}

type Collector struct {
	mu             sync.Mutex
	cpuSamples     map[string]cpuSample
	nodeCPUSamples map[string]nodeCPUSample
	networkSamples map[string]networkSample
}

type networkSample struct {
	receive  uint64
	transmit uint64
	at       time.Time
}

func NewCollector() *Collector {
	return &Collector{
		cpuSamples:     make(map[string]cpuSample),
		nodeCPUSamples: make(map[string]nodeCPUSample),
		networkSamples: make(map[string]networkSample),
	}
}

func (c *Collector) Collect(ctx context.Context, server config.Server) (Result, error) {
	return c.collect(ctx, server, nil, true, true)
}

// CollectSelected performs one confirmation pass for only the requested scope.
// Node discovery is still required to locate the requested BEAM processes, but
// unrelated nodes are not probed and host metrics are skipped unless requested.
func (c *Collector) CollectSelected(ctx context.Context, server config.Server, nodeNames []string, includeHost bool) (Result, error) {
	return c.collect(ctx, server, nodeNames, includeHost, false)
}

func (c *Collector) collect(ctx context.Context, server config.Server, nodeNames []string, includeHost, allNodes bool) (Result, error) {
	client, err := dial(ctx, server)
	if err != nil {
		return Result{}, err
	}
	defer client.Close()

	result := Result{}
	if includeHost {
		host, total, idle, hostErr := collectHost(ctx, client, server)
		if hostErr != nil {
			// Host collection is cheap; tolerate one transient empty/failed shell session.
			host, total, idle, hostErr = collectHost(ctx, client, server)
		}
		if hostErr == nil {
			host.CPUUsageRatio, host.CPUUsageValid = c.cpuUsage(server.ID, total, idle)
			networkReceiveRate, networkTransmitRate, networkValid := c.networkUsage(server.ID, host.NetworkReceiveBytes, host.NetworkTransmitBytes)
			if !host.CPUUsageValid {
				// CPU is a delta metric. On the first collection, take one short,
				// read-only follow-up sample so dashboards do not remain empty until
				// the next (potentially low-frequency) polling interval.
				timer := time.NewTimer(250 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
				case <-timer.C:
					retryHost, retryTotal, retryIdle, retryErr := collectHost(ctx, client, server)
					if retryErr == nil {
						host = retryHost
						host.CPUUsageRatio, host.CPUUsageValid = c.cpuUsage(server.ID, retryTotal, retryIdle)
						if !networkValid {
							networkReceiveRate, networkTransmitRate, _ = c.networkUsage(server.ID, host.NetworkReceiveBytes, host.NetworkTransmitBytes)
						}
					}
				}
			}
			host.NetworkReceiveBytesPerSecond, host.NetworkTransmitBytesPerSecond = networkReceiveRate, networkTransmitRate
		}
		result.Host = host
		result.HostCollected = hostErr == nil
		if hostErr != nil {
			result.HostError = hostErr.Error()
		}
	}

	if !allNodes && len(nodeNames) == 0 {
		return result, nil
	}
	expectedNodes, err := collectExpectedNodeNames(ctx, client, server)
	if err != nil {
		return result, err
	}

	psOutput, err := run(ctx, client, "if command -v pgrep >/dev/null 2>&1; then pgrep -a beam.smp || :; else ps -eww -o pid,args | grep '[b]eam.smp' || :; fi", server.CommandTimeout.Duration)
	if err != nil {
		return result, fmt.Errorf("list beam processes: %w", err)
	}
	processes := parseBeamProcesses(psOutput)
	if len(processes) == 0 {
		missing := nodeNames
		if allNodes {
			missing = expectedNodes
		}
		for _, name := range missing {
			result.Failures = append(result.Failures, NodeFailure{Name: name, Error: "configured instance directory has no running Erlang node"})
		}
		return result, fmt.Errorf("no Erlang beam.smp nodes discovered from %d process lines", len(strings.Split(strings.TrimSpace(psOutput), "\n")))
	}
	if allNodes {
		result.Failures = append(result.Failures, missingExpectedNodes(expectedNodes, processes)...)
	} else {
		var missing []string
		processes, missing = selectBeamProcesses(processes, nodeNames)
		for _, name := range missing {
			result.Failures = append(result.Failures, NodeFailure{Name: name, Error: "Erlang node was not discovered during confirmation"})
		}
	}
	nodeUsage := c.collectNodeProcessUsage(ctx, client, server, processes)

	result.Nodes = make([]Node, 0, len(processes))
	for _, process := range processes {
		node, err := collectNode(ctx, client, server, process)
		if err != nil {
			result.Failures = append(result.Failures, NodeFailure{Name: process.NodeName, Error: err.Error()})
			continue
		}
		if usage, ok := nodeUsage[process.PID]; ok {
			node.CPUUsageRatio = usage.cpuRatio
			node.CPUUsageValid = usage.cpuValid
			node.ResidentMemoryBytes = usage.residentMemoryBytes
			node.ResidentMemoryValid = usage.residentMemoryValid
		}
		result.Nodes = append(result.Nodes, node)
	}
	if len(result.Nodes) == 0 && len(result.Failures) > 0 {
		parts := make([]string, 0, len(result.Failures))
		for _, failure := range result.Failures {
			parts = append(parts, failure.Name+": "+failure.Error)
		}
		return result, fmt.Errorf("all %d Erlang nodes failed: %s", len(result.Failures), truncate(strings.Join(parts, "; "), 800))
	}
	return result, nil
}

func selectBeamProcesses(processes []beamProcess, nodeNames []string) ([]beamProcess, []string) {
	byName := make(map[string]beamProcess, len(processes)*2)
	ambiguousShortNames := make(map[string]struct{})
	for _, process := range processes {
		byName[process.NodeName] = process
		short := shortNodeName(process.NodeName)
		if previous, exists := byName[short]; exists && previous.NodeName != process.NodeName {
			delete(byName, short)
			ambiguousShortNames[short] = struct{}{}
		} else if _, ambiguous := ambiguousShortNames[short]; !ambiguous {
			byName[short] = process
		}
	}
	selected := make([]beamProcess, 0, len(nodeNames))
	missing := make([]string, 0)
	seen := make(map[string]struct{}, len(nodeNames))
	for _, name := range nodeNames {
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		process, exists := byName[name]
		if !exists {
			missing = append(missing, name)
			continue
		}
		selected = append(selected, process)
	}
	return selected, missing
}

func missingExpectedNodes(expected []string, processes []beamProcess) []NodeFailure {
	running := make(map[string]struct{}, len(processes))
	for _, process := range processes {
		running[shortNodeName(process.NodeName)] = struct{}{}
	}
	missing := make([]NodeFailure, 0)
	for _, name := range expected {
		if _, exists := running[name]; exists {
			continue
		}
		missing = append(missing, NodeFailure{Name: name, Error: "configured instance directory has no running Erlang node"})
	}
	return missing
}

func shortNodeName(name string) string {
	short, _, _ := strings.Cut(name, "@")
	return short
}

func collectExpectedNodeNames(ctx context.Context, client *ssh.Client, server config.Server) ([]string, error) {
	if strings.TrimSpace(server.InstanceDirectory) == "" {
		return nil, nil
	}
	instanceRoot := path.Clean(strings.TrimSpace(server.InstanceDirectory))
	command := expectedNodeScanCommand(instanceRoot)
	output, err := run(ctx, client, command, server.CommandTimeout.Duration)
	if err != nil {
		return nil, fmt.Errorf("scan configured instance directory: %w", err)
	}
	return parseExpectedNodeNames(output), nil
}

func expectedNodeScanCommand(instanceRoot string) string {
	instanceRoot = path.Clean(strings.TrimSpace(instanceRoot))
	commands := []string{"set -e"}
	if path.Base(instanceRoot) == "server" {
		commands = append(commands, "find "+shellQuote(instanceRoot)+" -mindepth 2 -maxdepth 2 -type d -name 'wl_*' ! -name '*.bk' ! -name '*.bk.*' -printf '%f\\n'")
	}
	for _, pattern := range []string{"wl_*/server", "ysmw_*/server"} {
		commands = append(commands, "find "+shellQuote(instanceRoot)+" -mindepth 2 -maxdepth 2 -type d -name 'server' -path "+shellQuote(path.Join(instanceRoot, pattern))+" -printf '%h\\n'")
	}
	return strings.Join(commands, "; ")
}

func parseExpectedNodeNames(output string) []string {
	unique := make(map[string]struct{})
	for _, line := range strings.Split(output, "\n") {
		name := path.Base(strings.TrimRight(strings.TrimSpace(line), "/"))
		lowerName := strings.ToLower(name)
		isNodeName := strings.HasPrefix(lowerName, "wl_") || strings.HasPrefix(lowerName, "ysmw_")
		isExcluded := strings.Contains(lowerName, "accter") || strings.HasSuffix(lowerName, ".bk") || strings.Contains(lowerName, ".bk.")
		if name != "" && isNodeName && !isExcluded {
			unique[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(unique))
	for name := range unique {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *Collector) cpuUsage(serverID string, total, idle uint64) (float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	previous, exists := c.cpuSamples[serverID]
	c.cpuSamples[serverID] = cpuSample{total: total, idle: idle}
	if !exists || total <= previous.total || idle < previous.idle {
		return 0, false
	}
	totalDelta := total - previous.total
	idleDelta := idle - previous.idle
	if idleDelta > totalDelta {
		return 0, false
	}
	return float64(totalDelta-idleDelta) / float64(totalDelta), true
}

func (c *Collector) nodeCPUUsage(serverID, nodeName string, total, process uint64, logicalCPUs uint64) (float64, bool) {
	key := serverID + "\x00" + nodeName
	c.mu.Lock()
	defer c.mu.Unlock()
	previous, exists := c.nodeCPUSamples[key]
	c.nodeCPUSamples[key] = nodeCPUSample{total: total, process: process}
	if !exists || logicalCPUs == 0 || total <= previous.total || process < previous.process {
		return 0, false
	}
	totalDelta := total - previous.total
	processDelta := process - previous.process
	return float64(processDelta) / float64(totalDelta) * float64(logicalCPUs), true
}

type nodeCPUStats struct {
	total       uint64
	logicalCPUs uint64
	processes   map[int]uint64
	resident    map[int]uint64
}

type nodeProcessUsage struct {
	cpuRatio            float64
	cpuValid            bool
	residentMemoryBytes uint64
	residentMemoryValid bool
}

func (c *Collector) collectNodeProcessUsage(ctx context.Context, client *ssh.Client, server config.Server, processes []beamProcess) map[int]nodeProcessUsage {
	stats, err := collectNodeCPUStats(ctx, client, server, processes)
	if err != nil {
		return nil
	}
	usage, validCPU := c.calculateNodeProcessUsage(server.ID, processes, stats)
	if validCPU == len(processes) {
		return usage
	}
	timer := time.NewTimer(250 * time.Millisecond)
	select {
	case <-ctx.Done():
		timer.Stop()
		return usage
	case <-timer.C:
	}
	retry, err := collectNodeCPUStats(ctx, client, server, processes)
	if err != nil {
		return usage
	}
	retried, _ := c.calculateNodeProcessUsage(server.ID, processes, retry)
	for pid, previous := range usage {
		if _, exists := retried[pid]; !exists {
			retried[pid] = previous
		}
	}
	return retried
}

func (c *Collector) calculateNodeProcessUsage(serverID string, processes []beamProcess, stats nodeCPUStats) (map[int]nodeProcessUsage, int) {
	usage := make(map[int]nodeProcessUsage, len(processes))
	validCPU := 0
	for _, process := range processes {
		current := nodeProcessUsage{}
		if resident, ok := stats.resident[process.PID]; ok {
			current.residentMemoryBytes = resident
			current.residentMemoryValid = true
		}
		processTicks, ok := stats.processes[process.PID]
		if ok {
			if ratio, valid := c.nodeCPUUsage(serverID, process.NodeName, stats.total, processTicks, stats.logicalCPUs); valid {
				current.cpuRatio = ratio
				current.cpuValid = true
				validCPU++
			}
		}
		if current.cpuValid || current.residentMemoryValid {
			usage[process.PID] = current
		}
	}
	return usage, validCPU
}

func collectNodeCPUStats(ctx context.Context, client *ssh.Client, server config.Server, processes []beamProcess) (nodeCPUStats, error) {
	if len(processes) == 0 {
		return nodeCPUStats{}, nil
	}
	commands := []string{
		"awk '/^cpu / {t=0; for(i=2;i<=NF;i++) t+=$i; printf \"cpu_total=%.0f\\n\",t}' /proc/stat",
		"awk '/^cpu[0-9]+ / {n++} END {printf \"cpu_logical_cores=%d\\n\",n}' /proc/stat",
	}
	for _, process := range processes {
		commands = append(commands,
			fmt.Sprintf("awk '{printf \"pid_%d=%%.0f\\n\",$14+$15}' /proc/%d/stat 2>/dev/null || :", process.PID, process.PID),
			fmt.Sprintf("awk '/^VmRSS:/ {printf \"rss_%d=%%.0f\\n\",$2*1024}' /proc/%d/status 2>/dev/null || :", process.PID, process.PID),
		)
	}
	output, err := run(ctx, client, "sh -c "+shellQuote(strings.Join(commands, "; ")), server.CommandTimeout.Duration)
	if err != nil {
		return nodeCPUStats{}, fmt.Errorf("collect node CPU metrics: %w", err)
	}
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			values[key] = value
		}
	}
	parse := func(key string) (uint64, error) {
		value, err := strconv.ParseUint(values[key], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid node CPU %s value %q", key, values[key])
		}
		return value, nil
	}
	total, err := parse("cpu_total")
	if err != nil {
		return nodeCPUStats{}, err
	}
	logicalCPUs, err := parse("cpu_logical_cores")
	if err != nil || logicalCPUs == 0 {
		return nodeCPUStats{}, fmt.Errorf("invalid node CPU logical core count %q", values["cpu_logical_cores"])
	}
	stats := nodeCPUStats{
		total: total, logicalCPUs: logicalCPUs,
		processes: make(map[int]uint64, len(processes)), resident: make(map[int]uint64, len(processes)),
	}
	for _, process := range processes {
		if ticks, err := parse(fmt.Sprintf("pid_%d", process.PID)); err == nil {
			stats.processes[process.PID] = ticks
		}
		if resident, err := parse(fmt.Sprintf("rss_%d", process.PID)); err == nil {
			stats.resident[process.PID] = resident
		}
	}
	if len(stats.processes) == 0 {
		return nodeCPUStats{}, fmt.Errorf("node CPU metrics missing all %d BEAM processes", len(processes))
	}
	return stats, nil
}

func collectHost(ctx context.Context, client *ssh.Client, server config.Server) (Host, uint64, uint64, error) {
	script := "awk '/^cpu / {t=0; for(i=2;i<=NF;i++) t+=$i; printf \"cpu_total=%.0f\\n\",t; printf \"cpu_idle=%.0f\\n\",($5+$6)}' /proc/stat; " +
		"awk '/^cpu[0-9]+ / {n++} END {printf \"cpu_logical_cores=%d\\n\",n}' /proc/stat; " +
		"awk '/^MemTotal:/ {printf \"mem_total_bytes=%.0f\\n\",$2*1024} /^MemAvailable:/ {printf \"mem_available_bytes=%.0f\\n\",$2*1024}' /proc/meminfo; " +
		"awk '{print \"load1=\" $1}' /proc/loadavg; awk '{print \"uptime_seconds=\" $1}' /proc/uptime; " +
		"awk 'NR>2 {gsub(\":\",\"\",$1); if($1 != \"lo\") {rx+=$2; tx+=$10}} END {printf \"net_rx_bytes=%.0f\\nnet_tx_bytes=%.0f\\n\",rx,tx}' /proc/net/dev; " +
		"df -Pk " + shellQuote(server.FilesystemPath) + " | awk 'NR==2 {printf \"fs_size_bytes=%.0f\\n\",$2*1024; printf \"fs_available_bytes=%.0f\\n\",$4*1024}'"
	output, err := run(ctx, client, "sh -c "+shellQuote(script), server.CommandTimeout.Duration)
	if err != nil {
		return Host{}, 0, 0, fmt.Errorf("collect host metrics: %w", err)
	}
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			values[key] = value
		}
	}
	required := []string{"cpu_total", "cpu_idle", "cpu_logical_cores", "mem_total_bytes", "mem_available_bytes", "load1", "uptime_seconds", "net_rx_bytes", "net_tx_bytes", "fs_size_bytes", "fs_available_bytes"}
	for _, key := range required {
		if values[key] == "" {
			return Host{}, 0, 0, fmt.Errorf("host metrics missing %s: %s", key, truncate(output, 500))
		}
	}
	parseUint := func(key string) (uint64, error) {
		value, err := strconv.ParseFloat(values[key], 64)
		if err != nil || value < 0 {
			return 0, fmt.Errorf("invalid %s value %q", key, values[key])
		}
		return uint64(value), nil
	}
	total, err := parseUint("cpu_total")
	if err != nil {
		return Host{}, 0, 0, err
	}
	idle, err := parseUint("cpu_idle")
	if err != nil {
		return Host{}, 0, 0, err
	}
	logicalCPUs, err := parseUint("cpu_logical_cores")
	if err != nil || logicalCPUs == 0 {
		return Host{}, 0, 0, fmt.Errorf("invalid cpu_logical_cores value %q", values["cpu_logical_cores"])
	}
	memoryTotal, err := parseUint("mem_total_bytes")
	if err != nil {
		return Host{}, 0, 0, err
	}
	memoryAvailable, err := parseUint("mem_available_bytes")
	if err != nil {
		return Host{}, 0, 0, err
	}
	filesystemSize, err := parseUint("fs_size_bytes")
	if err != nil {
		return Host{}, 0, 0, err
	}
	filesystemAvailable, err := parseUint("fs_available_bytes")
	if err != nil {
		return Host{}, 0, 0, err
	}
	load1, err := strconv.ParseFloat(values["load1"], 64)
	if err != nil {
		return Host{}, 0, 0, fmt.Errorf("invalid load1 value %q", values["load1"])
	}
	uptime, err := strconv.ParseFloat(values["uptime_seconds"], 64)
	if err != nil {
		return Host{}, 0, 0, fmt.Errorf("invalid uptime value %q", values["uptime_seconds"])
	}
	networkReceive, err := parseUint("net_rx_bytes")
	if err != nil {
		return Host{}, 0, 0, err
	}
	networkTransmit, err := parseUint("net_tx_bytes")
	if err != nil {
		return Host{}, 0, 0, err
	}
	return Host{Load1: load1, UptimeSeconds: uptime, LogicalCPUs: logicalCPUs, MemoryTotalBytes: memoryTotal, MemoryAvailableBytes: memoryAvailable, FilesystemSizeBytes: filesystemSize, FilesystemAvailableBytes: filesystemAvailable, NetworkReceiveBytes: networkReceive, NetworkTransmitBytes: networkTransmit}, total, idle, nil
}

func (c *Collector) networkUsage(serverID string, receive, transmit uint64) (float64, float64, bool) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	previous, exists := c.networkSamples[serverID]
	c.networkSamples[serverID] = networkSample{receive: receive, transmit: transmit, at: now}
	if !exists || receive < previous.receive || transmit < previous.transmit || now.Sub(previous.at) <= 0 {
		return 0, 0, false
	}
	seconds := now.Sub(previous.at).Seconds()
	return float64(receive-previous.receive) / seconds, float64(transmit-previous.transmit) / seconds, true
}

func dial(ctx context.Context, server config.Server) (*ssh.Client, error) {
	auth, agentCloser, err := authMethods(server)
	if err != nil {
		return nil, err
	}
	if agentCloser != nil {
		defer agentCloser.Close()
	}

	hostKeyCallback, err := hostKeyCallback(server)
	if err != nil {
		return nil, err
	}
	sshConfig := &ssh.ClientConfig{
		User:            server.Username,
		Auth:            auth,
		HostKeyCallback: hostKeyCallback,
		Timeout:         server.ConnectTimeout.Duration,
	}
	dialer := &net.Dialer{Timeout: server.ConnectTimeout.Duration}
	connection, err := dialer.DialContext(ctx, "tcp", server.Address)
	if err != nil {
		return nil, fmt.Errorf("connect SSH: %w", err)
	}
	conn, channels, requests, err := ssh.NewClientConn(connection, server.Address, sshConfig)
	if err != nil {
		connection.Close()
		return nil, fmt.Errorf("SSH handshake: %w", err)
	}
	return ssh.NewClient(conn, channels, requests), nil
}

func authMethods(server config.Server) ([]ssh.AuthMethod, io.Closer, error) {
	methods := make([]ssh.AuthMethod, 0, 2)
	var agentCloser io.Closer
	if server.UseSSHAgent {
		expectedFingerprint, err := publicKeyFingerprintFromFile(server.SSHKeyFile)
		if err != nil {
			return nil, nil, err
		}
		rwc, err := openSSHAgent()
		if err != nil {
			return nil, nil, fmt.Errorf("connect SSH agent: %w", err)
		}
		agentCloser = rwc
		agentClient := agent.NewClient(rwc)
		methods = append(methods, ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
			signers, signerErr := agentClient.Signers()
			if signerErr != nil {
				return nil, signerErr
			}
			for _, signer := range signers {
				if ssh.FingerprintSHA256(signer.PublicKey()) == expectedFingerprint {
					return []ssh.Signer{signer}, nil
				}
			}
			return nil, fmt.Errorf("SSH key from %s (%s) is not loaded in SSH agent", server.SSHKeyFile, expectedFingerprint)
		}))
	}
	if server.PrivateKeyFile != "" {
		key, err := os.ReadFile(server.PrivateKeyFile)
		if err != nil {
			if agentCloser != nil {
				agentCloser.Close()
			}
			return nil, nil, fmt.Errorf("read private key: %w", err)
		}
		var signer ssh.Signer
		if server.PrivateKeyPassFile != "" {
			passphrase, readErr := os.ReadFile(server.PrivateKeyPassFile)
			if readErr != nil {
				if agentCloser != nil {
					agentCloser.Close()
				}
				return nil, nil, fmt.Errorf("read private key passphrase file: %w", readErr)
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(strings.TrimRight(string(passphrase), "\r\n")))
		} else if server.PrivateKeyPassEnv != "" {
			passphrase := os.Getenv(server.PrivateKeyPassEnv)
			if passphrase == "" {
				if agentCloser != nil {
					agentCloser.Close()
				}
				return nil, nil, fmt.Errorf("private key passphrase environment variable %s is empty", server.PrivateKeyPassEnv)
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			if agentCloser != nil {
				agentCloser.Close()
			}
			return nil, nil, fmt.Errorf("parse private key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	return methods, agentCloser, nil
}

func publicKeyFingerprintFromFile(path string) (string, error) {
	keyData, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("read SSH key identity file: %w", err)
	}

	if publicKey, _, _, _, parseErr := ssh.ParseAuthorizedKey(keyData); parseErr == nil {
		return ssh.FingerprintSHA256(publicKey), nil
	}

	signer, parseErr := ssh.ParsePrivateKey(keyData)
	if parseErr == nil {
		return ssh.FingerprintSHA256(signer.PublicKey()), nil
	}
	var passphraseMissing *ssh.PassphraseMissingError
	if errors.As(parseErr, &passphraseMissing) && passphraseMissing.PublicKey != nil {
		return ssh.FingerprintSHA256(passphraseMissing.PublicKey), nil
	}
	return "", fmt.Errorf("parse SSH key identity file %s: %w", path, parseErr)
}

func hostKeyCallback(server config.Server) (ssh.HostKeyCallback, error) {
	if server.InsecureSkipHostKey {
		return ssh.InsecureIgnoreHostKey(), nil // explicitly configured for test environments
	}
	if server.HostKeySHA256 != "" {
		expected := strings.TrimSpace(server.HostKeySHA256)
		return func(_ string, _ net.Addr, key ssh.PublicKey) error {
			actual := ssh.FingerprintSHA256(key)
			if actual != expected {
				return fmt.Errorf("host key mismatch: got %s", actual)
			}
			return nil
		}, nil
	}
	callback, err := knownhosts.New(filepath.Clean(server.KnownHostsFile))
	if err != nil {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}
	return callback, nil
}

func run(ctx context.Context, client *ssh.Client, command string, timeout time.Duration) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("create SSH session: %w", err)
	}
	defer session.Close()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.Start(command); err != nil {
		return "", fmt.Errorf("start remote command: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		output := combineOutput(stdout.String(), stderr.String())
		if err != nil {
			return output, fmt.Errorf("remote command failed: %w: %s", err, truncate(output, 800))
		}
		return output, nil
	case <-ctx.Done():
		_ = session.Close()
		waitForSession(done)
		return combineOutput(stdout.String(), stderr.String()), ctx.Err()
	case <-timer.C:
		_ = session.Close()
		waitForSession(done)
		return combineOutput(stdout.String(), stderr.String()), fmt.Errorf("remote command timed out after %s", timeout)
	}
}

func combineOutput(stdout, stderr string) string {
	if stdout == "" {
		return stderr
	}
	if stderr == "" {
		return stdout
	}
	return stdout + "\n" + stderr
}

func waitForSession(done <-chan error) {
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

func parseBeamProcesses(output string) []beamProcess {
	var result []beamProcess
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		args := fields[1:]
		p := beamProcess{PID: pid}
		for i := 0; i+1 < len(args); i++ {
			switch args[i] {
			case "-name":
				p.NodeName = args[i+1]
			case "-setcookie":
				p.Cookie = args[i+1]
			case "-pa":
				p.Ebin = args[i+1]
			case "-bindir":
				p.ErlBinary = strings.TrimRight(args[i+1], "/") + "/erl"
			case "-s":
				if i+2 < len(args) && args[i+1] == "mmgr" && args[i+2] == "start" {
					p.IsServer = true
				}
			}
		}
		if p.IsServer && p.NodeName != "" && p.Cookie != "" && p.ErlBinary != "" {
			result = append(result, p)
		}
	}
	return result
}

func collectNode(ctx context.Context, client *ssh.Client, server config.Server, process beamProcess) (Node, error) {
	expression := buildProbeExpression(server)
	helperName := fmt.Sprintf("monitor_%d_%d@127.0.0.1", process.PID, time.Now().UnixNano())
	wrapper := fmt.Sprintf("Target=list_to_atom(%s),{ok,Tokens,_}=erl_scan:string(%s++\".\"),{ok,Forms}=erl_parse:parse_exprs(Tokens),case rpc:call(Target,erl_eval,exprs,[Forms,[]],%d) of {badrpc,Reason}->io:format(\"MONITOR_ERROR:~p~n\",[Reason]),halt(2);{value,{Metrics,MemoryProcess,QueueProcess,RoleCounts,MNodeStatus},_}->io:format(\"MONITOR_METRICS:~p~nMONITOR_MEMORY_PROCESS:~s~nMONITOR_QUEUE_PROCESS:~s~nMONITOR_ROLE_COUNTS:~p~nMONITOR_MNODE_STATUS:~p~n\",[Metrics,MemoryProcess,QueueProcess,RoleCounts,MNodeStatus]),halt(0);Other->io:format(\"MONITOR_ERROR:~p~n\",[Other]),halt(3) end.", strconv.Quote(process.NodeName), strconv.Quote(expression), server.CommandTimeout.Duration.Milliseconds())
	command := strings.Join([]string{
		shellQuote(process.ErlBinary), "-name", shellQuote(helperName), "-noinput",
		"-setcookie", shellQuote(process.Cookie), "-hidden",
		"-kernel inet_dist_listen_min 20000 -kernel inet_dist_listen_max 30000",
		"-eval", shellQuote(wrapper),
	}, " ")
	output, err := run(ctx, client, command, server.CommandTimeout.Duration)
	if err != nil {
		return Node{}, err
	}
	values, err := parseProbeOutput(output)
	if err != nil {
		return Node{}, err
	}
	memoryProcess, err := parseProcessIdentity(output, "MONITOR_MEMORY_PROCESS")
	if err != nil {
		return Node{}, err
	}
	queueProcess, err := parseProcessIdentity(output, "MONITOR_QUEUE_PROCESS")
	if err != nil {
		return Node{}, err
	}
	registeredUsers, onlineUsers, playerCountsValid, err := parseRoleCounts(output)
	if err != nil {
		return Node{}, err
	}
	mnodeConnections, mnodeConnectionsValid := parseProbeMNodeConnections(output)
	return Node{
		Name: process.NodeName, ProcessCount: values[0], ProcessLimit: values[1], MemoryBytes: values[2],
		RunQueue: values[3], SchedulersOnline: values[4], AtomCount: values[5], AtomLimit: values[6],
		PortCount: values[7], PortLimit: values[8], MaxProcessMemoryBytes: values[9],
		ProcessesOverMemoryThreshold: values[10], MaxMessageQueueLength: values[11],
		ProcessesOverQueueThreshold: values[12],
		MaxMemoryProcess:            memoryProcess, MaxQueueProcess: queueProcess,
		RegisteredUsers: registeredUsers, OnlineUsers: onlineUsers, PlayerCountsValid: playerCountsValid,
		MNodeConnections: mnodeConnections, MNodeConnectionsValid: mnodeConnectionsValid,
	}, nil
}

func buildProbeExpression(server config.Server) string {
	memoryThreshold := server.MemoryThresholdMBytes * 1024 * 1024
	return fmt.Sprintf("begin Info=fun(undefined)-><<>>;(P)->case process_info(P,[registered_name,initial_call,current_function]) of [{registered_name,R},{initial_call,I},{current_function,C}]->base64:encode(iolist_to_binary([pid_to_list(P),\"\\t\",io_lib:format(\"~p\",[R]),\"\\t\",io_lib:format(\"~p\",[I]),\"\\t\",io_lib:format(\"~p\",[C])]));_-><<>> end end,{MaxM,MaxMP,OverM,MaxQ,MaxQP,OverQ}=lists:foldl(fun(P,{MM,MMP,OM,MQ,MQP,OQ})->case process_info(P,[memory,message_queue_len]) of [{memory,M},{message_queue_len,Q}]->{NMM,NMP}=case M>MM of true->{M,P};false->{MM,MMP} end,{NMQ,NQP}=case Q>MQ of true->{Q,P};false->{MQ,MQP} end,{NMM,NMP,case M>%d of true->OM+1;false->OM end,NMQ,NQP,case Q>%d of true->OQ+1;false->OQ end};_->{MM,MMP,OM,MQ,MQP,OQ} end end,{0,undefined,0,0,undefined,0},processes()),RoleCounts=case erlang:function_exported(mlib_sys,monitor_role_counts,0) of true->try mlib_sys:monitor_role_counts() of {ok,#{total_role_count:=TotalRoleCount,online_role_count:=OnlineRoleCount}} when is_integer(TotalRoleCount),TotalRoleCount>=0,is_integer(OnlineRoleCount),OnlineRoleCount>=0->{TotalRoleCount,OnlineRoleCount};_->undefined catch _:_ -> undefined end;false->undefined end,MNodeStatus=case erlang:function_exported(mnode,i,0) of true->try mnode:i() of _->available catch _:_ -> failed end;false->unavailable end,{[erlang:system_info(process_count),erlang:system_info(process_limit),erlang:memory(total),erlang:statistics(run_queue),erlang:system_info(schedulers_online),erlang:system_info(atom_count),erlang:system_info(atom_limit),erlang:system_info(port_count),erlang:system_info(port_limit),MaxM,OverM,MaxQ,OverQ],Info(MaxMP),Info(MaxQP),RoleCounts,MNodeStatus} end", memoryThreshold, server.QueueThreshold)
}

var probePattern = regexp.MustCompile(`\[(?:\s*\d+\s*,){12}\s*\d+\s*\]`)

func parseProbeOutput(output string) ([]uint64, error) {
	match := probePattern.FindString(output)
	if match == "" {
		return nil, fmt.Errorf("probe returned no metrics: %s", truncate(output, 800))
	}
	parts := strings.Split(strings.Trim(match, "[]"), ",")
	if len(parts) != 13 {
		return nil, fmt.Errorf("probe returned %d metrics, expected 13", len(parts))
	}
	values := make([]uint64, len(parts))
	for i, part := range parts {
		value, err := strconv.ParseUint(strings.TrimSpace(part), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse probe metric %d: %w", i, err)
		}
		values[i] = value
	}
	return values, nil
}

var roleCountsPattern = regexp.MustCompile(`(?m)^MONITOR_ROLE_COUNTS:(undefined|\{\s*(\d+)\s*,\s*(\d+)\s*\})\s*$`)

func parseRoleCounts(output string) (uint64, uint64, bool, error) {
	match := roleCountsPattern.FindStringSubmatch(output)
	if len(match) == 0 {
		return 0, 0, false, fmt.Errorf("probe returned no role-count marker: %s", truncate(output, 800))
	}
	if match[1] == "undefined" {
		return 0, 0, false, nil
	}
	registered, err := strconv.ParseUint(match[2], 10, 64)
	if err != nil {
		return 0, 0, false, fmt.Errorf("parse total role count: %w", err)
	}
	online, err := strconv.ParseUint(match[3], 10, 64)
	if err != nil {
		return 0, 0, false, fmt.Errorf("parse online role count: %w", err)
	}
	return registered, online, true, nil
}

var mnodeStatusPattern = regexp.MustCompile(`(?m)^MONITOR_MNODE_STATUS:(available|unavailable|failed)\s*$`)

func parseProbeMNodeConnections(output string) ([]MNodeConnection, bool) {
	match := mnodeStatusPattern.FindStringSubmatch(output)
	if len(match) != 2 || match[1] != "available" {
		return nil, false
	}
	connections, err := parseMNodeIConsole(output)
	if err != nil {
		return nil, false
	}
	return connections, true
}

func parseProcessIdentity(output, marker string) (ProcessIdentity, error) {
	pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(marker) + `:([A-Za-z0-9+/=]*)\s*$`)
	match := pattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return ProcessIdentity{}, fmt.Errorf("probe returned no %s detail", marker)
	}
	if match[1] == "" {
		return ProcessIdentity{}, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("decode %s detail: %w", marker, err)
	}
	parts := strings.Split(string(decoded), "\t")
	if len(parts) != 4 {
		return ProcessIdentity{}, fmt.Errorf("decode %s detail: got %d fields, expected 4", marker, len(parts))
	}
	return ProcessIdentity{PID: parts[0], RegisteredName: parts[1], InitialCall: parts[2], CurrentFunction: parts[3]}, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
