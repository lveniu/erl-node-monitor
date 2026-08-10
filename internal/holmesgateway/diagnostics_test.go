package holmesgateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	monitorconfig "erlang-monitor/internal/config"
)

func TestDiagnosticToolValidationRejectsInventoryOverridesAndBounds(t *testing.T) {
	enabled := true
	executor := NewDiagnosticToolExecutor(monitorconfig.Exporter{Servers: []monitorconfig.Server{{ID: "server-1", Name: "safe", Enabled: &enabled}}})
	session := &Session{Context: InvestigationContext{ServerID: "server-1", Node: "game@127.0.0.1"}}
	tests := []struct {
		name string
		tool string
		args string
	}{
		{name: "arbitrary server", tool: "get_host_snapshot", args: `{"server_id":"attacker-host"}`},
		{name: "arbitrary address field", tool: "get_host_snapshot", args: `{"server_id":"server-1","address":"1.2.3.4:22"}`},
		{name: "different node", tool: "get_node_snapshot", args: `{"server_id":"server-1","node":"other@127.0.0.1"}`},
		{name: "top n", tool: "get_process_hotspots", args: `{"server_id":"server-1","node":"game@127.0.0.1","metric":"reductions","top_n":21}`},
		{name: "window", tool: "get_scheduler_hotspots", args: `{"server_id":"server-1","node":"game@127.0.0.1","window_ms":5001}`},
		{name: "metric enum", tool: "get_process_hotspots", args: `{"server_id":"server-1","node":"game@127.0.0.1","metric":"messages"}`},
		{name: "shell field", tool: "get_node_snapshot", args: `{"server_id":"server-1","node":"game@127.0.0.1","command":"rm -rf /"}`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := executor.Validate(session, ToolCall{CallID: "call", Name: testCase.tool, Arguments: json.RawMessage(testCase.args)})
			if err == nil {
				t.Fatal("dangerous or out-of-bounds input was accepted")
			}
		})
	}
	if err := executor.Validate(session, ToolCall{CallID: "call", Name: "get_process_hotspots", Arguments: json.RawMessage(`{"server_id":"server-1","node":"game@127.0.0.1","metric":"reductions","top_n":20}`)}); err != nil {
		t.Fatalf("valid bounded call rejected: %v", err)
	}
	dynamicSession := &Session{Context: InvestigationContext{ServerID: "server-1"}}
	dynamicCall := ToolCall{CallID: "dynamic", Name: "get_node_snapshot", Arguments: json.RawMessage(`{"server_id":"server-1","node":"dynamic@127.0.0.1"}`)}
	if err := executor.Validate(dynamicSession, dynamicCall); err == nil || !strings.Contains(err.Error(), "list_erlang_nodes") {
		t.Fatalf("node without recent discovery was accepted: %v", err)
	}
	executor.rememberNodes("server-1", []string{"dynamic@127.0.0.1"})
	if err := executor.Validate(dynamicSession, dynamicCall); err != nil {
		t.Fatalf("recently discovered node was rejected: %v", err)
	}
}

func TestDiagnosticToolsKeepFullNodeNameBoundaryForShortContext(t *testing.T) {
	enabled := true
	executor := NewDiagnosticToolExecutor(monitorconfig.Exporter{Servers: []monitorconfig.Server{{ID: "server-1", Name: "safe", Enabled: &enabled}}})
	session := &Session{Context: InvestigationContext{ServerID: "server-1", Node: "wl_banshu_1"}}

	shortCall := ToolCall{CallID: "short", Name: "get_node_snapshot", Arguments: json.RawMessage(`{"server_id":"server-1","node":"wl_banshu_1"}`)}
	if err := executor.Validate(session, shortCall); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("short context label crossed the RPC node-name boundary: %v", err)
	}

	fullCall := ToolCall{CallID: "full", Name: "get_node_snapshot", Arguments: json.RawMessage(`{"server_id":"server-1","node":"wl_banshu_1@127.0.0.1"}`)}
	if err := executor.Validate(session, fullCall); err == nil || !strings.Contains(err.Error(), "list_erlang_nodes") {
		t.Fatalf("undiscovered full node was accepted for short context: %v", err)
	}
	executor.rememberNodes("server-1", []string{"wl_banshu_1@127.0.0.1"})
	if err := executor.Validate(session, fullCall); err != nil {
		t.Fatalf("discovered full node matching the short context was rejected: %v", err)
	}

	mismatchedCall := ToolCall{CallID: "other", Name: "get_node_snapshot", Arguments: json.RawMessage(`{"server_id":"server-1","node":"other@127.0.0.1"}`)}
	executor.rememberNodes("server-1", []string{"wl_banshu_1@127.0.0.1", "other@127.0.0.1"})
	if err := executor.Validate(session, mismatchedCall); err == nil || !strings.Contains(err.Error(), "fixed") {
		t.Fatalf("full node unrelated to the short context was accepted: %v", err)
	}
}

func TestFileAuditorRedactsCredentialFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "audit.jsonl")
	auditor, err := NewFileAuditor(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := auditor.Record(AuditRecord{Event: "tool_finished", ParameterSummary: json.RawMessage(`{"server_id":"server-1","Authorization":"Bearer secret","nested":{"api_key":"secret"}}`)}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(strings.ToLower(text), "authorization") || strings.Contains(text, "secret") || !strings.Contains(text, "server-1") {
		t.Fatalf("audit redaction failed: %s", text)
	}
}

func TestDiagnosticErrorsDoNotExposeCredentialPaths(t *testing.T) {
	message := safeDiagnosticMessage("SSH_CREDENTIAL_UNAVAILABLE")
	if strings.Contains(message, `D:\\`) || strings.Contains(strings.ToLower(message), "private_key") {
		t.Fatalf("credential path leaked: %s", message)
	}
}
