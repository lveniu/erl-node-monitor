package opsagent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	monitorconfig "erlang-monitor/internal/config"
)

type scriptedModel struct {
	mu    sync.Mutex
	calls int
}

func (m *scriptedModel) Complete(_ context.Context, _ []chatMessage) (completion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	switch m.calls {
	case 1:
		return completion{ToolCalls: []toolCall{{ID: "list-1", Function: toolFunction{Name: "list_skills", Arguments: []byte(`{}`)}}}}, nil
	case 2:
		return completion{ToolCalls: []toolCall{{ID: "load-1", Function: toolFunction{Name: "load_skill", Arguments: []byte(`{"name":"test-skill"}`)}}}}, nil
	case 3:
		return completion{ToolCalls: []toolCall{{ID: "shell-1", Function: toolFunction{Name: "shell_exec", Arguments: []byte(`{"target":"current-server","command":"printf health","reason":"验证服务状态","timeout_seconds":5}`)}}}}, nil
	default:
		return completion{Content: "事实：命令返回 health。\n判断：验证通过。"}, nil
	}
}

type scriptedShell struct {
	mu       sync.Mutex
	requests []ShellRequest
}

type readOnlyModel struct {
	mu    sync.Mutex
	calls int
}

type multiReadOnlyModel struct {
	mu    sync.Mutex
	calls int
}

type mgectlAnalysisModel struct {
	mu    sync.Mutex
	calls int
}

func (m *multiReadOnlyModel) Complete(_ context.Context, _ []chatMessage) (completion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls == 1 {
		return completion{ToolCalls: []toolCall{
			{ID: "list-many-1", Function: toolFunction{Name: "list_skills", Arguments: []byte(`{}`)}},
			{ID: "load-many-2", Function: toolFunction{Name: "load_skill", Arguments: []byte(`{"name":"test-skill"}`)}},
			{ID: "shell-many-3", Function: toolFunction{Name: "shell_exec", Arguments: []byte(`{"target":"current-server","command":"ps aux | grep beam | head -n 10","reason":"check BEAM processes","timeout_seconds":5}`)}},
		}}, nil
	}
	return completion{Content: "read-only checks completed"}, nil
}

func (m *mgectlAnalysisModel) Complete(_ context.Context, _ []chatMessage) (completion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls == 1 {
		return completion{ToolCalls: []toolCall{
			{ID: "list-analysis-1", Function: toolFunction{Name: "list_skills", Arguments: []byte(`{}`)}},
			{ID: "load-analysis-2", Function: toolFunction{Name: "load_skill", Arguments: []byte(`{"name":"erlang-ops-analysis"}`)}},
			{ID: "shell-analysis-3", Function: toolFunction{Name: "shell_exec", Arguments: []byte(`{"target":"current-server","command":"cd -- '/data/wl_debug_1' && ./mgectl exprs \"mlib_sys:monitor_snapshot()\"","reason":"检查节点快照"}`)}},
		}}, nil
	}
	return completion{Content: "Erlang 只读分析完成。"}, nil
}

type queuedApprovalModel struct {
	mu    sync.Mutex
	calls int
}

func (m *queuedApprovalModel) Complete(_ context.Context, _ []chatMessage) (completion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls == 1 {
		return completion{ToolCalls: []toolCall{
			{ID: "list-approval-0", Function: toolFunction{Name: "list_skills", Arguments: []byte(`{}`)}},
			{ID: "load-approval-0", Function: toolFunction{Name: "load_skill", Arguments: []byte(`{"name":"test-skill"}`)}},
			{ID: "shell-approval-1", Function: toolFunction{Name: "shell_exec", Arguments: []byte(`{"target":"current-server","command":"printf first","reason":"run first operation","timeout_seconds":5}`)}},
			{ID: "shell-approval-2", Function: toolFunction{Name: "shell_exec", Arguments: []byte(`{"target":"current-server","command":"printf second","reason":"run second operation","timeout_seconds":5}`)}},
		}}, nil
	}
	return completion{Content: "approved operations completed"}, nil
}

func (m *queuedApprovalModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

type finalModel struct{}

func (*finalModel) Complete(_ context.Context, _ []chatMessage) (completion, error) {
	return completion{Content: "检查完成。"}, nil
}

type noSkillShellModel struct {
	calls int
}

func (m *noSkillShellModel) Complete(_ context.Context, _ []chatMessage) (completion, error) {
	m.calls++
	if m.calls == 1 {
		return completion{ToolCalls: []toolCall{{ID: "shell-without-skill", Function: toolFunction{Name: "shell_exec", Arguments: []byte(`{"target":"current-server","command":"df -Pk /data","reason":"check disk"}`)}}}}, nil
	}
	return completion{Content: "未加载 Skill，未执行 Shell。"}, nil
}

func (m *readOnlyModel) Complete(_ context.Context, _ []chatMessage) (completion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls == 1 {
		return completion{ToolCalls: []toolCall{
			{ID: "list-read-0", Function: toolFunction{Name: "list_skills", Arguments: []byte(`{}`)}},
			{ID: "load-read-0", Function: toolFunction{Name: "load_skill", Arguments: []byte(`{"name":"test-skill"}`)}},
			{ID: "shell-read-1", Function: toolFunction{Name: "shell_exec", Arguments: []byte(`{"target":"current-server","command":"ps aux | grep beam | head -n 10","reason":"检查 BEAM 进程","timeout_seconds":5}`)}},
		}}, nil
	}
	return completion{Content: "只读检查已完成。"}, nil
}

func (s *scriptedShell) Execute(_ context.Context, _ monitorconfig.Server, request ShellRequest, _ time.Duration, _ int) ShellResult {
	s.mu.Lock()
	s.requests = append(s.requests, request)
	s.mu.Unlock()
	return ShellResult{Target: request.Target, Output: "health", ExitStatus: "success"}
}

func testAgent(t *testing.T, model Model, shell ShellExecutor) *Agent {
	t.Helper()
	root := t.TempDir()
	skillDir := filepath.Join(root, "test-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test-skill\ndescription: test workflow\n---\n\n先检查，再验证。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	analysisSkillDir := filepath.Join(root, "erlang-ops-analysis")
	if err := os.MkdirAll(analysisSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(analysisSkillDir, "SKILL.md"), []byte("---\nname: erlang-ops-analysis\ndescription: read-only Erlang analysis\n---\n\n只执行固定只读诊断。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, err := LoadSkills(root)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	inventory := monitorconfig.Exporter{Servers: []monitorconfig.Server{
		{ID: "s1", Name: "srv-1", Address: "192.168.100.23:61618", InstanceDirectory: "/data", Enabled: &enabled},
		{ID: "external", Name: "external", Address: "203.0.113.10:61618", InstanceDirectory: "/data", Enabled: &enabled},
	}}
	cfg := Config{Model: ModelConfig{Timeout: Duration{Duration: time.Second}}, SkillsDir: root, Limits: Limits{MaxSteps: 8, TaskTimeout: Duration{Duration: time.Minute}, CommandTimeout: Duration{Duration: time.Second}, MaxCommandBytes: 512, MaxOutputBytes: 4096, TaskTTL: Duration{Duration: time.Minute}}}
	agent, err := NewAgent(cfg, inventory, model, skills, shell, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(agent.Close)
	return agent
}

func TestSingleTaskLoadsSkillWaitsForApprovalAndCompletes(t *testing.T) {
	shell := &scriptedShell{}
	agent := testAgent(t, &scriptedModel{}, shell)
	task, err := agent.CreateTask(context.Background(), CreateTaskRequest{Question: "处理当前告警", Context: TaskContext{ServerID: "s1"}}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := agent.Get(task.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.Status == StatusAwaitingApproval {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	waiting, err := agent.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != StatusAwaitingApproval || waiting.Pending == nil {
		t.Fatalf("task did not pause for shell approval: %#v", waiting)
	}
	if _, err := agent.Decide(context.Background(), task.ID, DecisionRequest{CallID: waiting.Pending.CallID, Approved: true}, "alice", "Admin"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := agent.Get(task.ID)
		if current.Status == StatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	completed, _ := agent.Get(task.ID)
	if completed.Status != StatusCompleted {
		t.Fatalf("task did not complete: %#v", completed)
	}
	if completed.FinalAnswer == "" || len(shell.requests) != 1 || shell.requests[0].Command != "printf health" {
		t.Fatalf("unexpected result: answer=%q shell=%#v", completed.FinalAnswer, shell.requests)
	}
	started, finished := 0, 0
	for _, event := range completed.Events {
		switch event.Type {
		case "model_started":
			started++
		case "model_finished":
			finished++
		}
	}
	if started != 4 || finished != 4 {
		t.Fatalf("unexpected model phase event counts: started=%d finished=%d events=%#v", started, finished, completed.Events)
	}
}

func TestReadOnlyShellCombinationSkipsApprovalAndCompletes(t *testing.T) {
	shell := &scriptedShell{}
	agent := testAgent(t, &readOnlyModel{}, shell)
	task, err := agent.CreateTask(context.Background(), CreateTaskRequest{Question: "检查 BEAM 进程", Context: TaskContext{ServerID: "s1"}}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := agent.Get(task.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.Status == StatusAwaitingApproval {
			t.Fatalf("read-only command unexpectedly required approval: %#v", current.Pending)
		}
		if current.Status == StatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	completed, err := agent.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != StatusCompleted || completed.Pending != nil {
		t.Fatalf("task did not complete without approval: %#v", completed)
	}
	shell.mu.Lock()
	defer shell.mu.Unlock()
	if len(shell.requests) != 1 || shell.requests[0].Command != "ps aux | grep beam | head -n 10" {
		t.Fatalf("unexpected shell requests: %#v", shell.requests)
	}
}

func TestAllowlistedMGCTLAnalysisSkipsApprovalAndCompletes(t *testing.T) {
	shell := &scriptedShell{}
	agent := testAgent(t, &mgectlAnalysisModel{}, shell)
	task, err := agent.CreateTask(context.Background(), CreateTaskRequest{Question: "分析 Erlang 内存", Context: TaskContext{ServerID: "s1"}}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := agent.Get(task.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.Status == StatusAwaitingApproval {
			t.Fatalf("allowlisted mgectl analysis unexpectedly required approval: %#v", current.Pending)
		}
		if current.Status == StatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	completed, err := agent.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != StatusCompleted || completed.Pending != nil {
		t.Fatalf("task did not complete without approval: %#v", completed)
	}
	shell.mu.Lock()
	defer shell.mu.Unlock()
	if len(shell.requests) != 1 || shell.requests[0].Command != analysisSnapshotCommand {
		t.Fatalf("unexpected shell requests: %#v", shell.requests)
	}
}

func TestShellWithoutLoadedSkillIsRejected(t *testing.T) {
	shell := &scriptedShell{}
	agent := testAgent(t, &noSkillShellModel{}, shell)
	task, err := agent.CreateTask(context.Background(), CreateTaskRequest{Question: "检查磁盘", Context: TaskContext{ServerID: "s1"}}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForStatus(t, agent, task.ID, StatusCompleted)
	shell.mu.Lock()
	defer shell.mu.Unlock()
	if len(shell.requests) != 0 {
		t.Fatalf("shell executed without a loaded Skill: %#v", shell.requests)
	}
	foundRejected := false
	for _, event := range completed.Events {
		if event.Type != "tool_finished" {
			continue
		}
		data, ok := event.Data.(map[string]any)
		if ok && data["tool_call_id"] == "shell-without-skill" {
			foundRejected = true
		}
	}
	if !foundRejected {
		t.Fatalf("missing rejected tool event: %#v", completed.Events)
	}
}

func TestCreateTaskRejectsNonInternalServerEvenIfEnabledInInventory(t *testing.T) {
	agent := testAgent(t, &finalModel{}, &scriptedShell{})
	_, err := agent.CreateTask(context.Background(), CreateTaskRequest{Question: "检查", Context: TaskContext{ServerID: "external"}}, "alice")
	if err == nil {
		t.Fatal("external server task was accepted")
	}
}

func TestMultipleToolCallsRunSequentiallyWithoutApproval(t *testing.T) {
	model := &multiReadOnlyModel{}
	shell := &scriptedShell{}
	agent := testAgent(t, model, shell)
	task, err := agent.CreateTask(context.Background(), CreateTaskRequest{Question: "run checks", Context: TaskContext{ServerID: "s1"}}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := agent.Get(task.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.Status == StatusAwaitingApproval {
			t.Fatalf("read-only tool sequence unexpectedly required approval: %#v", current.Pending)
		}
		if current.Status == StatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	completed, err := agent.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != StatusCompleted {
		t.Fatalf("task did not complete: %#v", completed)
	}
	finishedIDs := make([]string, 0, 3)
	for _, event := range completed.Events {
		if event.Type != "tool_finished" {
			continue
		}
		data, ok := event.Data.(map[string]any)
		if !ok {
			t.Fatalf("unexpected tool event data: %#v", event.Data)
		}
		finishedIDs = append(finishedIDs, data["tool_call_id"].(string))
	}
	want := []string{"list-many-1", "load-many-2", "shell-many-3"}
	if len(finishedIDs) != len(want) {
		t.Fatalf("unexpected tool completion order: %#v", finishedIDs)
	}
	for i := range want {
		if finishedIDs[i] != want[i] {
			t.Fatalf("unexpected tool completion order: got %#v want %#v", finishedIDs, want)
		}
	}
	shell.mu.Lock()
	defer shell.mu.Unlock()
	if len(shell.requests) != 1 || shell.requests[0].Command != "ps aux | grep beam | head -n 10" {
		t.Fatalf("unexpected shell requests: %#v", shell.requests)
	}
}

func TestMultipleApprovalCommandsPauseAndResumeOneAtATime(t *testing.T) {
	model := &queuedApprovalModel{}
	shell := &scriptedShell{}
	agent := testAgent(t, model, shell)
	task, err := agent.CreateTask(context.Background(), CreateTaskRequest{Question: "run two operations", Context: TaskContext{ServerID: "s1"}}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	first := waitForPendingCall(t, agent, task.ID, "shell-approval-1")
	if model.callCount() != 1 {
		t.Fatalf("model was called before first queued approval completed: %d", model.callCount())
	}
	if _, err := agent.Decide(context.Background(), task.ID, DecisionRequest{CallID: first.Pending.CallID, Approved: true}, "alice", "Admin"); err != nil {
		t.Fatal(err)
	}
	second := waitForPendingCall(t, agent, task.ID, "shell-approval-2")
	if model.callCount() != 1 {
		t.Fatalf("model was called before queued tools completed: %d", model.callCount())
	}
	shell.mu.Lock()
	if len(shell.requests) != 1 || shell.requests[0].Command != "printf first" {
		shell.mu.Unlock()
		t.Fatalf("first command execution mismatch: %#v", shell.requests)
	}
	shell.mu.Unlock()
	if _, err := agent.Decide(context.Background(), task.ID, DecisionRequest{CallID: second.Pending.CallID, Approved: true}, "alice", "Admin"); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, agent, task.ID, StatusCompleted)
	shell.mu.Lock()
	defer shell.mu.Unlock()
	if len(shell.requests) != 2 || shell.requests[0].Command != "printf first" || shell.requests[1].Command != "printf second" {
		t.Fatalf("queued commands did not execute exactly once in order: %#v", shell.requests)
	}
	if model.callCount() != 2 {
		t.Fatalf("unexpected model call count: %d", model.callCount())
	}
}

func TestDeniedApprovalContinuesQueuedToolCalls(t *testing.T) {
	model := &queuedApprovalModel{}
	shell := &scriptedShell{}
	agent := testAgent(t, model, shell)
	task, err := agent.CreateTask(context.Background(), CreateTaskRequest{Question: "run two operations", Context: TaskContext{ServerID: "s1"}}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	first := waitForPendingCall(t, agent, task.ID, "shell-approval-1")
	if _, err := agent.Decide(context.Background(), task.ID, DecisionRequest{CallID: first.Pending.CallID, Approved: false}, "alice", "Admin"); err != nil {
		t.Fatal(err)
	}
	second := waitForPendingCall(t, agent, task.ID, "shell-approval-2")
	shell.mu.Lock()
	if len(shell.requests) != 0 {
		shell.mu.Unlock()
		t.Fatalf("denied command was executed: %#v", shell.requests)
	}
	shell.mu.Unlock()
	if _, err := agent.Decide(context.Background(), task.ID, DecisionRequest{CallID: second.Pending.CallID, Approved: false}, "alice", "Admin"); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, agent, task.ID, StatusCompleted)
}

func waitForPendingCall(t *testing.T, agent *Agent, taskID, callID string) Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, err := agent.Get(taskID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == StatusAwaitingApproval && current.Pending != nil && current.Pending.CallID == callID {
			return current
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, _ := agent.Get(taskID)
	t.Fatalf("task did not wait for call %q: %#v", callID, current)
	return Task{}
}

func waitForStatus(t *testing.T, agent *Agent, taskID string, status TaskStatus) Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, err := agent.Get(taskID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == status {
			return current
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, _ := agent.Get(taskID)
	t.Fatalf("task did not reach status %q: %#v", status, current)
	return Task{}
}

func TestApprovalRevalidatesPendingCommandBeforeExecution(t *testing.T) {
	shell := &scriptedShell{}
	agent := testAgent(t, &scriptedModel{}, shell)
	task, err := agent.CreateTask(context.Background(), CreateTaskRequest{Question: "处理当前告警", Context: TaskContext{ServerID: "s1"}}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var waiting Task
	for time.Now().Before(deadline) {
		waiting, err = agent.Get(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if waiting.Status == StatusAwaitingApproval && waiting.Pending != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if waiting.Pending == nil {
		t.Fatal("task did not reach approval")
	}
	agent.mu.Lock()
	agent.tasks[task.ID].Pending.Command = trashCleanupCommand
	agent.mu.Unlock()
	if _, err := agent.Decide(context.Background(), task.ID, DecisionRequest{CallID: waiting.Pending.CallID, Approved: true}, "alice", "Admin"); err != nil {
		t.Fatal(err)
	}
	shell.mu.Lock()
	defer shell.mu.Unlock()
	if len(shell.requests) != 0 {
		t.Fatalf("policy-invalid command executed after approval: %#v", shell.requests)
	}
}

func TestValidateShellRequestBlocksDestructiveOperations(t *testing.T) {
	limits := Limits{MaxCommandBytes: 512}
	if err := ValidateShellRequest(ShellRequest{Target: "current-server", Command: "systemctl status exporter", Reason: "check"}, limits); err != nil {
		t.Fatal(err)
	}
	if err := ValidateShellRequest(ShellRequest{Target: "current-server", Command: "rm -rf /", Reason: "cleanup"}, limits); err == nil {
		t.Fatal("destructive command was accepted")
	}
}
