package holmesgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	monitorconfig "erlang-monitor/internal/config"
	"erlang-monitor/internal/sshprobe"
)

type DiagnosticToolExecutor struct {
	servers    map[string]monitorconfig.Server
	collector  *sshprobe.DiagnosticCollector
	mu         sync.RWMutex
	discovered map[string]discoveredNodes
}

type discoveredNodes struct {
	at    time.Time
	nodes map[string]bool
}

func NewDiagnosticToolExecutor(serverConfig monitorconfig.Exporter) *DiagnosticToolExecutor {
	servers := make(map[string]monitorconfig.Server)
	for _, server := range serverConfig.Servers {
		if server.IsEnabled() {
			servers[server.ID] = server
		}
	}
	return &DiagnosticToolExecutor{servers: servers, collector: sshprobe.NewDiagnosticCollector(), discovered: make(map[string]discoveredNodes)}
}

func (e *DiagnosticToolExecutor) RequiresApproval(name string) bool {
	return name == "get_scheduler_hotspots" || name == "get_process_hotspots"
}

type baseToolArguments struct {
	ServerID string `json:"server_id"`
}

type nodeToolArguments struct {
	ServerID string `json:"server_id"`
	Node     string `json:"node"`
}

type schedulerToolArguments struct {
	ServerID string `json:"server_id"`
	Node     string `json:"node"`
	TopN     int    `json:"top_n,omitempty"`
	WindowMS int    `json:"window_ms,omitempty"`
}

type processToolArguments struct {
	ServerID string `json:"server_id"`
	Node     string `json:"node"`
	Metric   string `json:"metric"`
	TopN     int    `json:"top_n,omitempty"`
}

func (e *DiagnosticToolExecutor) Validate(session *Session, call ToolCall) error {
	if session == nil {
		return errors.New("missing investigation session")
	}
	if _, exists := e.servers[session.Context.ServerID]; !exists {
		return errors.New("session server is no longer enabled")
	}
	switch call.Name {
	case "get_host_snapshot", "list_erlang_nodes":
		var arguments baseToolArguments
		if err := strictArguments(call.Arguments, &arguments); err != nil {
			return err
		}
		return validateServerArgument(session, arguments.ServerID)
	case "get_node_snapshot":
		var arguments nodeToolArguments
		if err := strictArguments(call.Arguments, &arguments); err != nil {
			return err
		}
		return e.validateNodeArguments(session, arguments.ServerID, arguments.Node)
	case "get_scheduler_hotspots":
		var arguments schedulerToolArguments
		if err := strictArguments(call.Arguments, &arguments); err != nil {
			return err
		}
		if err := e.validateNodeArguments(session, arguments.ServerID, arguments.Node); err != nil {
			return err
		}
		if arguments.TopN < 0 || arguments.TopN > 20 {
			return errors.New("top_n must be between 1 and 20")
		}
		if arguments.WindowMS < 0 || arguments.WindowMS > 5000 || (arguments.WindowMS > 0 && arguments.WindowMS < 100) {
			return errors.New("window_ms must be between 100 and 5000")
		}
		return nil
	case "get_process_hotspots":
		var arguments processToolArguments
		if err := strictArguments(call.Arguments, &arguments); err != nil {
			return err
		}
		if err := e.validateNodeArguments(session, arguments.ServerID, arguments.Node); err != nil {
			return err
		}
		if arguments.TopN < 0 || arguments.TopN > 20 {
			return errors.New("top_n must be between 1 and 20")
		}
		if arguments.Metric != "reductions" && arguments.Metric != "memory" && arguments.Metric != "message_queue_len" {
			return errors.New("metric is not in the allowed enum")
		}
		return nil
	default:
		return fmt.Errorf("tool %q is not allowed", call.Name)
	}
}

func (e *DiagnosticToolExecutor) Execute(parent context.Context, session *Session, call ToolCall) ToolExecutionResult {
	server, exists := e.servers[session.Context.ServerID]
	if !exists {
		return ToolExecutionResult{Status: "error", ErrorCode: "SERVER_NOT_FOUND", Error: "服务器已从有效清单移除"}
	}
	timeout := 10 * time.Second
	if e.RequiresApproval(call.Name) {
		timeout = 45 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var diagnostic sshprobe.DiagnosticResult
	switch call.Name {
	case "get_host_snapshot":
		diagnostic = e.collector.HostSnapshot(ctx, server)
	case "list_erlang_nodes":
		diagnostic = e.collector.ListNodes(ctx, server)
	case "get_node_snapshot":
		var arguments nodeToolArguments
		_ = json.Unmarshal(call.Arguments, &arguments)
		diagnostic = e.collector.NodeSnapshot(ctx, server, arguments.Node)
	case "get_scheduler_hotspots":
		var arguments schedulerToolArguments
		_ = json.Unmarshal(call.Arguments, &arguments)
		if arguments.TopN == 0 {
			arguments.TopN = 10
		}
		if arguments.WindowMS == 0 {
			arguments.WindowMS = 1000
		}
		diagnostic = e.collector.SchedulerHotspots(ctx, server, arguments.Node, arguments.TopN, arguments.WindowMS)
	case "get_process_hotspots":
		var arguments processToolArguments
		_ = json.Unmarshal(call.Arguments, &arguments)
		if arguments.TopN == 0 {
			arguments.TopN = 10
		}
		diagnostic = e.collector.ProcessHotspots(ctx, server, arguments.Node, arguments.Metric, arguments.TopN, 1000)
	default:
		return ToolExecutionResult{Status: "error", ErrorCode: "TOOL_NOT_ALLOWED", Error: "工具不在服务端白名单中"}
	}
	if diagnostic.Stages.ErrorCode != "" {
		diagnostic.Stages.Error = safeDiagnosticMessage(diagnostic.Stages.ErrorCode)
		return ToolExecutionResult{Status: "error", ErrorCode: diagnostic.Stages.ErrorCode, Error: diagnostic.Stages.Error, Data: map[string]any{"connection_stages": diagnostic.Stages}}
	}
	if call.Name == "list_erlang_nodes" {
		if data, ok := diagnostic.Data.(map[string]any); ok {
			if nodes, ok := data["nodes"].([]string); ok {
				e.rememberNodes(session.Context.ServerID, nodes)
			}
		}
	}
	return ToolExecutionResult{Status: "success", Data: diagnostic}
}

func safeDiagnosticMessage(code string) string {
	messages := map[string]string{
		"SSH_CREDENTIAL_UNAVAILABLE":  "服务端托管的 SSH 身份当前不可用",
		"SSH_HOST_KEY_CONFIG_INVALID": "服务端主机指纹配置无效",
		"SSH_TCP_UNREACHABLE":         "目标服务器 TCP 端口不可达",
		"SSH_HANDSHAKE_FAILED":        "SSH 握手失败",
		"SSH_AUTHENTICATION_FAILED":   "SSH 公钥认证失败",
		"SSH_REMOTE_SESSION_FAILED":   "SSH 已认证但无法创建远端命令会话",
		"SSH_REMOTE_COMMAND_FAILED":   "受控只读远端命令执行失败",
		"ERLANG_NODE_NOT_DISCOVERED":  "请求节点不在该服务器当前发现的节点清单中",
		"ERLANG_RPC_FAILED":           "Erlang 辅助节点或只读 RPC 失败",
		"TOOL_ARGUMENT_REJECTED":      "工具参数未通过服务端校验",
	}
	if message := messages[code]; message != "" {
		return message
	}
	return "受控诊断失败"
}

func strictArguments(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("tool arguments contain trailing data")
	}
	return nil
}

func validateServerArgument(session *Session, serverID string) error {
	if serverID == "" || serverID != session.Context.ServerID {
		return errors.New("server_id must equal the current investigation server")
	}
	return nil
}

func (e *DiagnosticToolExecutor) validateNodeArguments(session *Session, serverID, node string) error {
	if err := validateServerArgument(session, serverID); err != nil {
		return err
	}
	if !safeNodeName(node) {
		return errors.New("node name is invalid")
	}
	contextNode := session.Context.Node
	if contextNode != "" {
		if safeNodeName(contextNode) {
			if node != contextNode {
				return errors.New("node must equal the node fixed in the investigation context")
			}
			return nil
		}
		if !safeNodeLabel(contextNode) || !strings.HasPrefix(node, contextNode+"@") {
			return errors.New("node must equal or expand the node fixed in the investigation context")
		}
	}
	if !e.nodeRecentlyDiscovered(session.Context.ServerID, node) {
		return errors.New("node is not in the server's recently discovered node list; call list_erlang_nodes first")
	}
	return nil
}

func (e *DiagnosticToolExecutor) rememberNodes(serverID string, nodes []string) {
	values := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if safeNodeName(node) {
			values[node] = true
		}
	}
	e.mu.Lock()
	e.discovered[serverID] = discoveredNodes{at: time.Now().UTC(), nodes: values}
	e.mu.Unlock()
}

func (e *DiagnosticToolExecutor) nodeRecentlyDiscovered(serverID, node string) bool {
	e.mu.RLock()
	entry, exists := e.discovered[serverID]
	e.mu.RUnlock()
	return exists && time.Since(entry.at) <= 5*time.Minute && entry.nodes[node]
}
