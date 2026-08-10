package sshprobe

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"erlang-monitor/internal/config"
	"golang.org/x/crypto/ssh"
)

type ConnectionStages struct {
	TCPReachable      bool   `json:"tcp_reachable"`
	HandshakeComplete bool   `json:"ssh_handshake_complete"`
	Authenticated     bool   `json:"public_key_authenticated"`
	RemoteSession     bool   `json:"remote_session_created"`
	RPCSucceeded      bool   `json:"erlang_rpc_succeeded"`
	FailureStage      string `json:"failure_stage,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	Error             string `json:"error,omitempty"`
}

type DiagnosticResult struct {
	Stages ConnectionStages `json:"connection_stages"`
	Data   any              `json:"data,omitempty"`
}

type MNodeConnection struct {
	NodeID string `json:"node_id"`
	State  uint64 `json:"state"`
	Node   string `json:"node"`
	Type   string `json:"type"`
	Usable bool   `json:"usable"`
}

// CommandResult is the bounded result of one explicitly approved shell
// command. The caller owns policy and approval; sshprobe only resolves the
// configured server identity and executes through the existing SSH boundary.
type CommandResult struct {
	Stages   ConnectionStages `json:"connection_stages"`
	Output   string           `json:"output,omitempty"`
	Duration time.Duration    `json:"duration"`
}

type DiagnosticCollector struct {
	hostSamples *Collector
}

func NewDiagnosticCollector() *DiagnosticCollector {
	return &DiagnosticCollector{hostSamples: NewCollector()}
}

func (c *DiagnosticCollector) RunCommand(ctx context.Context, server config.Server, command string, timeout time.Duration) (CommandResult, error) {
	started := time.Now()
	client, stages, err := dialDiagnostic(ctx, server)
	if err != nil {
		return CommandResult{Stages: stages, Duration: time.Since(started)}, err
	}
	defer client.Close()
	if err := proveRemoteSession(client); err != nil {
		stages.FailureStage, stages.ErrorCode = "remote_session", "SSH_REMOTE_SESSION_FAILED"
		return CommandResult{Stages: stages, Duration: time.Since(started)}, err
	}
	stages.RemoteSession = true
	output, err := run(ctx, client, command, timeout)
	if err != nil {
		stages.FailureStage, stages.ErrorCode = "remote_command", "SSH_REMOTE_COMMAND_FAILED"
		return CommandResult{Stages: stages, Output: truncate(output, 64*1024), Duration: time.Since(started)}, err
	}
	return CommandResult{Stages: stages, Output: truncate(output, 64*1024), Duration: time.Since(started)}, nil
}

func (c *DiagnosticCollector) HostSnapshot(ctx context.Context, server config.Server) DiagnosticResult {
	client, stages, err := dialDiagnostic(ctx, server)
	if err != nil {
		return diagnosticFailure(stages, err)
	}
	defer client.Close()
	if err := proveRemoteSession(client); err != nil {
		stages.FailureStage, stages.ErrorCode = "remote_session", "SSH_REMOTE_SESSION_FAILED"
		return diagnosticFailure(stages, err)
	}
	stages.RemoteSession = true
	host, total, idle, err := collectHost(ctx, client, server)
	if err != nil {
		stages.FailureStage, stages.ErrorCode = "remote_command", "SSH_REMOTE_COMMAND_FAILED"
		return diagnosticFailure(stages, err)
	}
	host.CPUUsageRatio, host.CPUUsageValid = c.hostSamples.cpuUsage(server.ID+":diagnostic", total, idle)
	return DiagnosticResult{Stages: stages, Data: host}
}

func (c *DiagnosticCollector) ListNodes(ctx context.Context, server config.Server) DiagnosticResult {
	client, stages, err := dialDiagnostic(ctx, server)
	if err != nil {
		return diagnosticFailure(stages, err)
	}
	defer client.Close()
	if err := proveRemoteSession(client); err != nil {
		stages.FailureStage, stages.ErrorCode = "remote_session", "SSH_REMOTE_SESSION_FAILED"
		return diagnosticFailure(stages, err)
	}
	stages.RemoteSession = true
	processes, err := discoverBeamProcesses(ctx, client, server)
	if err != nil {
		stages.FailureStage, stages.ErrorCode = "remote_command", "SSH_REMOTE_COMMAND_FAILED"
		return diagnosticFailure(stages, err)
	}
	names := make([]string, 0, len(processes))
	for _, process := range processes {
		names = append(names, process.NodeName)
	}
	sort.Strings(names)
	return DiagnosticResult{Stages: stages, Data: map[string]any{"nodes": names, "count": len(names)}}
}

func (c *DiagnosticCollector) NodeSnapshot(ctx context.Context, server config.Server, nodeName string) DiagnosticResult {
	client, process, stages, err := c.nodeClient(ctx, server, nodeName)
	if err != nil {
		return diagnosticFailure(stages, err)
	}
	defer client.Close()
	node, err := collectNode(ctx, client, server, process)
	if err != nil {
		stages.FailureStage, stages.ErrorCode = "erlang_rpc", "ERLANG_RPC_FAILED"
		return diagnosticFailure(stages, err)
	}
	node.MaxMemoryProcess = redactProcessIdentity(node.MaxMemoryProcess)
	node.MaxQueueProcess = redactProcessIdentity(node.MaxQueueProcess)
	stages.RPCSucceeded = true
	return DiagnosticResult{Stages: stages, Data: node}
}

// NodeConnections calls the game's read-only mnode:i/0 connection inspection
// interface on one currently discovered node.
func (c *DiagnosticCollector) NodeConnections(ctx context.Context, server config.Server, nodeName string) DiagnosticResult {
	client, process, stages, err := c.nodeClient(ctx, server, nodeName)
	if err != nil {
		return diagnosticFailure(stages, err)
	}
	defer client.Close()
	output, console, err := runDiagnosticRPCWithConsole(ctx, client, server, process, "mnode:i()")
	if err != nil {
		stages.FailureStage, stages.ErrorCode = "erlang_rpc", "ERLANG_RPC_FAILED"
		return diagnosticFailure(stages, err)
	}
	connections, err := parseMNodeIConsole(console)
	if err != nil {
		stages.FailureStage, stages.ErrorCode = "erlang_result", "ERLANG_RESULT_INVALID"
		return diagnosticFailure(stages, err)
	}
	central := filterMNodeConnections(connections, "central")
	regions := filterMNodeConnections(connections, "region")
	stages.RPCSucceeded = true
	return DiagnosticResult{Stages: stages, Data: map[string]any{
		"node":             process.NodeName,
		"erlang_term":      truncate(output, 60*1024),
		"connection_count": len(connections),
		"central_nodes":    central,
		"region_nodes":     regions,
	}}
}

var mnodeConnectionRowPattern = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+(\S+)`)

func parseMNodeIConsole(output string) ([]MNodeConnection, error) {
	inConnections := false
	foundHeader := false
	connections := make([]MNodeConnection, 0)
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "nodeid----stat") {
			inConnections = true
			foundHeader = true
			continue
		}
		if inConnections && strings.Contains(trimmed, "process name") {
			break
		}
		if !inConnections || trimmed == "" {
			continue
		}
		match := mnodeConnectionRowPattern.FindStringSubmatch(line)
		if len(match) != 4 {
			continue
		}
		state, err := strconv.ParseUint(match[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse mnode connection state: %w", err)
		}
		connections = append(connections, MNodeConnection{
			NodeID: match[1], State: state, Node: match[3],
			Type: mnodeConnectionType(match[1]), Usable: state == 2,
		})
	}
	if !foundHeader {
		return nil, fmt.Errorf("mnode:i returned no connection table")
	}
	return connections, nil
}

func mnodeConnectionType(nodeID string) string {
	switch {
	case strings.HasPrefix(nodeID, "8"):
		return "central"
	case strings.HasPrefix(nodeID, "9"):
		return "region"
	default:
		return "game"
	}
}

func filterMNodeConnections(connections []MNodeConnection, connectionType string) []MNodeConnection {
	result := make([]MNodeConnection, 0)
	for _, connection := range connections {
		if connection.Type == connectionType {
			result = append(result, connection)
		}
	}
	return result
}

func (c *DiagnosticCollector) ProcessHotspots(ctx context.Context, server config.Server, nodeName, metric string, topN, windowMS int) DiagnosticResult {
	client, process, stages, err := c.nodeClient(ctx, server, nodeName)
	if err != nil {
		return diagnosticFailure(stages, err)
	}
	defer client.Close()
	var expression string
	switch metric {
	case "reductions":
		expression = fmt.Sprintf("recon:proc_window(reductions,%d,%d)", topN, windowMS)
	case "memory", "message_queue_len":
		expression = fmt.Sprintf("recon:proc_count(%s,%d)", metric, topN)
	default:
		stages.FailureStage, stages.ErrorCode = "validation", "TOOL_ARGUMENT_REJECTED"
		return diagnosticFailure(stages, fmt.Errorf("unsupported process metric %q", metric))
	}
	output, err := runDiagnosticRPC(ctx, client, server, process, expression)
	if err != nil {
		stages.FailureStage, stages.ErrorCode = "erlang_rpc", "ERLANG_RPC_FAILED"
		return diagnosticFailure(stages, err)
	}
	stages.RPCSucceeded = true
	return DiagnosticResult{Stages: stages, Data: map[string]any{"node": process.NodeName, "metric": metric, "top_n": topN, "window_ms": windowMS, "erlang_term": truncate(redactDiagnosticTerm(output), 60*1024)}}
}

func (c *DiagnosticCollector) SchedulerHotspots(ctx context.Context, server config.Server, nodeName string, topN, windowMS int) DiagnosticResult {
	client, process, stages, err := c.nodeClient(ctx, server, nodeName)
	if err != nil {
		return diagnosticFailure(stages, err)
	}
	defer client.Close()
	// Reading scheduler_wall_time is side-effect free only when it is already
	// enabled. This deliberately does not call system_flag/2 to enable it.
	expression := fmt.Sprintf("begin A=erlang:statistics(scheduler_wall_time_all),case A of undefined->{error,scheduler_wall_time_disabled};_->timer:sleep(%d),B=erlang:statistics(scheduler_wall_time_all),Pairs=lists:zip(A,B),Values=[{Id,case (T2-T1) of 0->0.0;DT->((A2-A1)*100.0)/DT end} || {{Id,A1,T1},{Id,A2,T2}}<-Pairs],lists:sublist(lists:reverse(lists:keysort(2,Values)),%d) end end", windowMS, topN)
	output, err := runDiagnosticRPC(ctx, client, server, process, expression)
	if err != nil {
		stages.FailureStage, stages.ErrorCode = "erlang_rpc", "ERLANG_RPC_FAILED"
		return diagnosticFailure(stages, err)
	}
	stages.RPCSucceeded = true
	return DiagnosticResult{Stages: stages, Data: map[string]any{"node": process.NodeName, "top_n": topN, "window_ms": windowMS, "erlang_term": truncate(output, 60*1024), "note": "scheduler_wall_time 未预先启用时返回明确的不支持结果，工具不会修改该运行时标志"}}
}

func (c *DiagnosticCollector) nodeClient(ctx context.Context, server config.Server, nodeName string) (*ssh.Client, beamProcess, ConnectionStages, error) {
	client, stages, err := dialDiagnostic(ctx, server)
	if err != nil {
		return nil, beamProcess{}, stages, err
	}
	if err := proveRemoteSession(client); err != nil {
		client.Close()
		stages.FailureStage, stages.ErrorCode = "remote_session", "SSH_REMOTE_SESSION_FAILED"
		return nil, beamProcess{}, stages, err
	}
	stages.RemoteSession = true
	processes, err := discoverBeamProcesses(ctx, client, server)
	if err != nil {
		client.Close()
		stages.FailureStage, stages.ErrorCode = "remote_command", "SSH_REMOTE_COMMAND_FAILED"
		return nil, beamProcess{}, stages, err
	}
	selected, missing := selectBeamProcesses(processes, []string{nodeName})
	if len(missing) > 0 || len(selected) != 1 {
		client.Close()
		stages.FailureStage, stages.ErrorCode = "node_validation", "ERLANG_NODE_NOT_DISCOVERED"
		return nil, beamProcess{}, stages, fmt.Errorf("node is not in the current discovered-node list")
	}
	return client, selected[0], stages, nil
}

func discoverBeamProcesses(ctx context.Context, client *ssh.Client, server config.Server) ([]beamProcess, error) {
	output, err := run(ctx, client, "if command -v pgrep >/dev/null 2>&1; then pgrep -a beam.smp || :; else ps -eww -o pid,args | grep '[b]eam.smp' || :; fi", server.CommandTimeout.Duration)
	if err != nil {
		return nil, fmt.Errorf("list beam processes: %w", err)
	}
	processes := parseBeamProcesses(output)
	if len(processes) == 0 {
		return nil, fmt.Errorf("no Erlang nodes discovered")
	}
	return processes, nil
}

func runDiagnosticRPC(ctx context.Context, client *ssh.Client, server config.Server, process beamProcess, expression string) (string, error) {
	result, _, err := runDiagnosticRPCWithConsole(ctx, client, server, process, expression)
	return result, err
}

func runDiagnosticRPCWithConsole(ctx context.Context, client *ssh.Client, server config.Server, process beamProcess, expression string) (string, string, error) {
	helperName := fmt.Sprintf("holmes_%d_%d@127.0.0.1", process.PID, time.Now().UnixNano())
	wrapper := fmt.Sprintf("Target=list_to_atom(%s),{ok,Tokens,_}=erl_scan:string(%s++\".\"),{ok,Forms}=erl_parse:parse_exprs(Tokens),case rpc:call(Target,erl_eval,exprs,[Forms,[]],%d) of {badrpc,Reason}->io:format(\"HOLMES_ERROR:~p~n\",[Reason]),halt(2);{value,Value,_}->io:format(\"HOLMES_RESULT:~P~n\",[Value,20]),halt(0);Other->io:format(\"HOLMES_ERROR:~p~n\",[Other]),halt(3) end.", strconv.Quote(process.NodeName), strconv.Quote(expression), server.CommandTimeout.Duration.Milliseconds())
	command := strings.Join([]string{
		shellQuote(process.ErlBinary), "-name", shellQuote(helperName), "-noinput",
		"-setcookie", shellQuote(process.Cookie), "-hidden",
		"-kernel inet_dist_listen_min 20000 -kernel inet_dist_listen_max 30000",
		"-eval", shellQuote(wrapper),
	}, " ")
	output, err := run(ctx, client, command, server.CommandTimeout.Duration)
	if err != nil {
		return "", "", err
	}
	marker := "HOLMES_RESULT:"
	index := strings.Index(output, marker)
	if index < 0 {
		return "", "", fmt.Errorf("RPC returned no bounded result: %s", truncate(output, 800))
	}
	return strings.TrimSpace(output[index+len(marker):]), strings.TrimSpace(output[:index]), nil
}

func dialDiagnostic(ctx context.Context, server config.Server) (*ssh.Client, ConnectionStages, error) {
	stages := ConnectionStages{}
	auth, agentCloser, err := authMethods(server)
	if err != nil {
		stages.FailureStage, stages.ErrorCode = "credential_config", "SSH_CREDENTIAL_UNAVAILABLE"
		return nil, stages, err
	}
	if agentCloser != nil {
		defer agentCloser.Close()
	}
	hostKeyCallback, err := hostKeyCallback(server)
	if err != nil {
		stages.FailureStage, stages.ErrorCode = "host_key_config", "SSH_HOST_KEY_CONFIG_INVALID"
		return nil, stages, err
	}
	sshConfig := &ssh.ClientConfig{User: server.Username, Auth: auth, HostKeyCallback: hostKeyCallback, Timeout: server.ConnectTimeout.Duration}
	dialer := &net.Dialer{Timeout: server.ConnectTimeout.Duration}
	connection, err := dialer.DialContext(ctx, "tcp", server.Address)
	if err != nil {
		stages.FailureStage, stages.ErrorCode = "tcp", "SSH_TCP_UNREACHABLE"
		return nil, stages, err
	}
	stages.TCPReachable = true
	connectionState, channels, requests, err := ssh.NewClientConn(connection, server.Address, sshConfig)
	if err != nil {
		connection.Close()
		if authenticationFailure(err) {
			stages.HandshakeComplete = true
			stages.FailureStage, stages.ErrorCode = "authentication", "SSH_AUTHENTICATION_FAILED"
		} else {
			stages.FailureStage, stages.ErrorCode = "handshake", "SSH_HANDSHAKE_FAILED"
		}
		return nil, stages, err
	}
	stages.HandshakeComplete = true
	stages.Authenticated = true
	return ssh.NewClient(connectionState, channels, requests), stages, nil
}

func authenticationFailure(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "unable to authenticate") || strings.Contains(text, "no supported methods remain") || strings.Contains(text, "permission denied")
}

func proveRemoteSession(client *ssh.Client) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	return session.Close()
}

func diagnosticFailure(stages ConnectionStages, err error) DiagnosticResult {
	stages.Error = truncate(err.Error(), 800)
	return DiagnosticResult{Stages: stages}
}

var roleIdentifierPattern = regexp.MustCompile(`(?i)\brole_[0-9]+\b`)
var longBusinessIdentifierPattern = regexp.MustCompile(`[0-9]{11,}`)

func redactDiagnosticTerm(value string) string {
	value = roleIdentifierPattern.ReplaceAllString(value, "role_[redacted]")
	return longBusinessIdentifierPattern.ReplaceAllString(value, "[id_redacted]")
}

func redactProcessIdentity(identity ProcessIdentity) ProcessIdentity {
	identity.RegisteredName = redactDiagnosticTerm(identity.RegisteredName)
	return identity
}
