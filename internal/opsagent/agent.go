package opsagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	monitorconfig "erlang-monitor/internal/config"
)

type Agent struct {
	cfg       Config
	servers   map[string]monitorconfig.Server
	model     Model
	skills    *SkillLoader
	shell     ShellExecutor
	logger    *slog.Logger
	mu        sync.RWMutex
	tasks     map[string]*taskState
	watchers  map[string]map[chan Event]struct{}
	cleanStop chan struct{}
}

type ServerSummary struct {
	ServerID    string `json:"server_id"`
	DisplayName string `json:"display_name"`
}

func NewAgent(cfg Config, inventory monitorconfig.Exporter, model Model, skills *SkillLoader, shell ShellExecutor, logger *slog.Logger) (*Agent, error) {
	if model == nil || skills == nil || shell == nil {
		return nil, errors.New("ops agent requires model, skills, and shell executor")
	}
	if logger == nil {
		logger = slog.Default()
	}
	servers := make(map[string]monitorconfig.Server)
	for _, server := range inventory.Servers {
		if server.IsEnabled() && internalOpsServer(server) {
			servers[server.ID] = server
		}
	}
	if len(servers) == 0 {
		return nil, errors.New("ops agent requires at least one enabled 192.168.100.* server")
	}
	agent := &Agent{cfg: cfg, servers: servers, model: model, skills: skills, shell: shell, logger: logger, tasks: make(map[string]*taskState), watchers: make(map[string]map[chan Event]struct{}), cleanStop: make(chan struct{})}
	go agent.reaper()
	return agent, nil
}

func (a *Agent) Close() {
	select {
	case <-a.cleanStop:
	default:
		close(a.cleanStop)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, task := range a.tasks {
		task.cancel()
	}
}

func (a *Agent) CreateTask(ctx context.Context, request CreateTaskRequest, creator string) (Task, error) {
	if strings.TrimSpace(request.Question) == "" || len(request.Question) > 8000 {
		return Task{}, errors.New("question is required and must be at most 8000 characters")
	}
	server, ok := a.servers[request.Context.ServerID]
	if !ok || !internalOpsServer(server) {
		return Task{}, errors.New("server_id is not in the enabled 192.168.100.* server inventory")
	}
	request.Context.ServerName = server.Name
	if request.Context.AlertLabels == nil {
		request.Context.AlertLabels = map[string]string{}
	}
	id, err := randomID()
	if err != nil {
		return Task{}, err
	}
	taskCtx, cancel := context.WithTimeout(context.Background(), a.cfg.Limits.TaskTimeout.Duration)
	state := &taskState{Task: Task{ID: id, Creator: creator, Status: StatusRunning, Question: request.Question, Context: request.Context, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, messages: []chatMessage{systemMessage(request.Context), {Role: "user", Content: request.Question}}, loadedSkills: make(map[string]struct{}), ctx: taskCtx, cancel: cancel}
	a.mu.Lock()
	a.tasks[id] = state
	a.mu.Unlock()
	a.emit(id, "task_started", map[string]any{"server_id": request.Context.ServerID, "server_name": server.Name})
	go a.runLoop(taskCtx, state)
	go a.watchTaskDeadline(state)
	return a.snapshot(state), nil
}

func (a *Agent) runLoop(ctx context.Context, state *taskState) {
	for {
		a.mu.Lock()
		if state.steps >= a.cfg.Limits.MaxSteps {
			a.mu.Unlock()
			a.fail(state, "达到本次任务最大步骤数")
			return
		}
		state.steps++
		step := state.steps
		messages := append([]chatMessage(nil), state.messages...)
		a.mu.Unlock()
		modelStarted := time.Now()
		a.emit(state.ID, "model_started", map[string]any{
			"step":  step,
			"model": a.cfg.Model.Model,
		})
		modelContext, modelCancel := withTimeout(ctx, a.cfg.Model.Timeout.Duration)
		completion, err := a.model.Complete(modelContext, messages)
		modelCancel()
		if err != nil {
			a.emit(state.ID, "model_finished", map[string]any{
				"step":        step,
				"status":      "error",
				"duration_ms": time.Since(modelStarted).Milliseconds(),
			})
			a.fail(state, "模型调用失败："+cleanError(err))
			return
		}
		a.emit(state.ID, "model_finished", map[string]any{
			"step":        step,
			"status":      "success",
			"duration_ms": time.Since(modelStarted).Milliseconds(),
		})
		a.mu.Lock()
		state.messages = append(state.messages, chatMessage{Role: "assistant", Content: completion.Content, ToolCalls: completion.ToolCalls})
		a.mu.Unlock()
		if strings.TrimSpace(completion.Content) != "" {
			a.emit(state.ID, "assistant_message", map[string]string{"content": safeText(completion.Content, 16000)})
		}
		if len(completion.ToolCalls) == 0 {
			a.complete(state, completion.Content)
			return
		}
		if a.processToolCalls(ctx, state, completion.ToolCalls) {
			return
		}
	}
}

// processToolCalls executes model-requested tools in their original order. Before
// each dispatch it persists the remaining calls so an approval decision can
// safely resume the sequence without issuing another model request.
func (a *Agent) processToolCalls(ctx context.Context, state *taskState, calls []toolCall) bool {
	for index, call := range calls {
		a.mu.Lock()
		state.queuedCalls = append([]toolCall(nil), calls[index+1:]...)
		a.mu.Unlock()

		result, paused := a.dispatch(ctx, state, call)
		if paused {
			return true
		}
		a.mu.Lock()
		state.messages = append(state.messages, chatMessage{Role: "tool", ToolCallID: call.ID, Content: result})
		a.mu.Unlock()
	}
	return false
}

func (a *Agent) resumeQueuedCalls(state *taskState) {
	a.mu.Lock()
	if terminalTask(state.Status) || state.Status != StatusRunning {
		a.mu.Unlock()
		return
	}
	calls := append([]toolCall(nil), state.queuedCalls...)
	state.queuedCalls = nil
	a.mu.Unlock()

	if len(calls) > 0 && a.processToolCalls(state.ctx, state, calls) {
		return
	}
	a.runLoop(state.ctx, state)
}

func (a *Agent) dispatch(ctx context.Context, state *taskState, call toolCall) (string, bool) {
	var result any
	switch call.Function.Name {
	case "list_skills":
		result = map[string]any{"skills": a.skills.List()}
	case "load_skill":
		var args struct {
			Name string `json:"name"`
		}
		if err := decodeArguments(call.Function.Arguments, &args); err != nil {
			result = toolError("INVALID_ARGUMENTS", err.Error())
			break
		}
		skill, err := a.skills.Get(args.Name)
		if err != nil {
			result = toolError("SKILL_NOT_FOUND", "Skill 不存在")
			break
		}
		a.mu.Lock()
		state.loadedSkills[skill.Name] = struct{}{}
		a.mu.Unlock()
		result = map[string]any{"name": skill.Name, "description": skill.Description, "content": skill.Content}
	case "shell_exec":
		var request ShellRequest
		if err := decodeArguments(call.Function.Arguments, &request); err != nil {
			result = toolError("INVALID_ARGUMENTS", err.Error())
			break
		}
		if err := ValidateShellRequest(request, a.cfg.Limits); err != nil {
			result = toolError("COMMAND_REJECTED", err.Error())
			break
		}
		server := a.servers[state.Context.ServerID]
		if err := ValidateServerShellRequest(server, request); err != nil {
			result = toolError("COMMAND_REJECTED", err.Error())
			break
		}
		a.mu.RLock()
		skillErr := ValidateSkillShellRequest(state.loadedSkills, request)
		a.mu.RUnlock()
		if skillErr != nil {
			result = toolError("SKILL_REQUIRED", skillErr.Error())
			break
		}
		if request.Target == "current-server" && state.Context.ServerID == "" {
			result = toolError("SERVER_REQUIRED", "当前任务没有固定服务器")
			break
		}
		if IsApprovalExemptCommand(request.Command) {
			a.emit(state.ID, "tool_started", map[string]any{
				"tool_call_id": call.ID,
				"tool_name":    "shell_exec",
				"command":      safeText(request.Command, 4096),
				"target":       request.Target,
				"approval":     "skipped-read-only",
			})
			shellResult := a.shell.Execute(ctx, server, request, a.cfg.Limits.CommandTimeout.Duration, a.cfg.Limits.MaxOutputBytes)
			a.logger.Info(
				"ops read-only shell completed without approval",
				"event", "shell-auto-approved",
				"task_id", state.ID,
				"target", request.Target,
				"status", shellResult.ExitStatus,
				"duration_ms", shellResult.DurationMS,
			)
			a.emit(state.ID, "tool_finished", map[string]any{
				"tool_call_id": call.ID,
				"tool_name":    "shell_exec",
				"result":       shellResult,
			})
			return encodeResult(shellResult), false
		}
		pending := &PendingCommand{CallID: call.ID, Target: request.Target, Command: request.Command, Reason: request.Reason, TimeoutSeconds: request.TimeoutSeconds}
		a.mu.Lock()
		state.Pending = pending
		state.Status = StatusAwaitingApproval
		state.UpdatedAt = time.Now().UTC()
		a.mu.Unlock()
		a.emit(state.ID, "approval_required", pending)
		return "", true
	default:
		result = toolError("TOOL_NOT_ALLOWED", "工具不在 Agent 白名单中")
	}
	b, _ := json.Marshal(result)
	a.emit(state.ID, "tool_finished", map[string]any{"tool_call_id": call.ID, "tool_name": call.Function.Name, "result": result})
	return string(b), false
}

func (a *Agent) Decide(ctx context.Context, id string, request DecisionRequest, actor string, role string) (Task, error) {
	if role != "Admin" {
		return Task{}, errors.New("Shell 执行审批需要 Grafana Admin")
	}
	a.mu.Lock()
	state, ok := a.tasks[id]
	if !ok {
		a.mu.Unlock()
		return Task{}, errors.New("task not found")
	}
	if state.Creator != actor {
		a.mu.Unlock()
		return Task{}, errors.New("task belongs to another Grafana user")
	}
	if state.ctx.Err() != nil {
		a.mu.Unlock()
		return Task{}, errors.New("task has expired")
	}
	if state.Status != StatusAwaitingApproval || state.Pending == nil || state.Pending.CallID != request.CallID {
		a.mu.Unlock()
		return Task{}, errors.New("task is not waiting for this command approval")
	}
	pending := *state.Pending
	state.Pending = nil
	state.Status = StatusRunning
	if !request.Approved {
		result, _ := json.Marshal(toolError("COMMAND_DENIED", "用户拒绝执行该命令"))
		state.messages = append(state.messages, chatMessage{Role: "tool", ToolCallID: pending.CallID, Content: string(result)})
		a.mu.Unlock()
		a.logger.Info("ops shell decision", "event", "shell-decision", "task_id", id, "actor", actor, "target", pending.Target, "approved", false)
		a.emit(id, "approval_decided", map[string]any{"call_id": request.CallID, "approved": false, "actor": actor})
		go a.resumeQueuedCalls(state)
		return a.snapshot(state), nil
	}
	server := a.servers[state.Context.ServerID]
	requestValue := ShellRequest{Target: pending.Target, Command: pending.Command, Reason: pending.Reason, TimeoutSeconds: pending.TimeoutSeconds}
	if err := ValidateShellRequest(requestValue, a.cfg.Limits); err != nil {
		rejected := toolError("COMMAND_REJECTED", err.Error())
		encoded, _ := json.Marshal(rejected)
		state.messages = append(state.messages, chatMessage{Role: "tool", ToolCallID: pending.CallID, Content: string(encoded)})
		a.mu.Unlock()
		a.logger.Warn("ops shell rejected after approval revalidation", "event", "shell-policy-rejected", "task_id", id, "actor", actor, "target", pending.Target)
		a.emit(id, "approval_decided", map[string]any{"call_id": request.CallID, "approved": true, "actor": actor})
		a.emit(id, "tool_finished", map[string]any{"tool_call_id": pending.CallID, "tool_name": "shell_exec", "result": rejected})
		go a.resumeQueuedCalls(state)
		return a.snapshot(state), nil
	}
	if err := ValidateServerShellRequest(server, requestValue); err != nil {
		rejected := toolError("COMMAND_REJECTED", err.Error())
		encoded, _ := json.Marshal(rejected)
		state.messages = append(state.messages, chatMessage{Role: "tool", ToolCallID: pending.CallID, Content: string(encoded)})
		a.mu.Unlock()
		a.logger.Warn("ops shell rejected after approval revalidation", "event", "shell-policy-rejected", "task_id", id, "actor", actor, "target", pending.Target)
		a.emit(id, "approval_decided", map[string]any{"call_id": request.CallID, "approved": true, "actor": actor})
		a.emit(id, "tool_finished", map[string]any{"tool_call_id": pending.CallID, "tool_name": "shell_exec", "result": rejected})
		go a.resumeQueuedCalls(state)
		return a.snapshot(state), nil
	}
	if err := ValidateSkillShellRequest(state.loadedSkills, requestValue); err != nil {
		rejected := toolError("SKILL_REQUIRED", err.Error())
		encoded, _ := json.Marshal(rejected)
		state.messages = append(state.messages, chatMessage{Role: "tool", ToolCallID: pending.CallID, Content: string(encoded)})
		a.mu.Unlock()
		a.logger.Warn("ops shell rejected after approval skill revalidation", "event", "shell-skill-rejected", "task_id", id, "actor", actor, "target", pending.Target)
		a.emit(id, "approval_decided", map[string]any{"call_id": request.CallID, "approved": true, "actor": actor})
		a.emit(id, "tool_finished", map[string]any{"tool_call_id": pending.CallID, "tool_name": "shell_exec", "result": rejected})
		go a.resumeQueuedCalls(state)
		return a.snapshot(state), nil
	}
	a.mu.Unlock()
	a.logger.Info("ops shell decision", "event", "shell-decision", "task_id", id, "actor", actor, "target", pending.Target, "approved", true)
	a.emit(id, "approval_decided", map[string]any{"call_id": request.CallID, "approved": true, "actor": actor})
	a.emit(id, "tool_started", map[string]any{"tool_call_id": pending.CallID, "tool_name": "shell_exec", "command": safeText(pending.Command, 4096), "target": pending.Target})
	result := a.shell.Execute(ctx, server, requestValue, a.cfg.Limits.CommandTimeout.Duration, a.cfg.Limits.MaxOutputBytes)
	a.mu.Lock()
	state.messages = append(state.messages, chatMessage{Role: "tool", ToolCallID: pending.CallID, Content: encodeResult(result)})
	a.mu.Unlock()
	a.logger.Info("ops shell completed", "event", "shell-completed", "task_id", id, "actor", actor, "target", pending.Target, "status", result.ExitStatus, "duration_ms", result.DurationMS)
	a.emit(id, "tool_finished", map[string]any{"tool_call_id": pending.CallID, "tool_name": "shell_exec", "result": result})
	go a.resumeQueuedCalls(state)
	return a.snapshot(state), nil
}

func (a *Agent) Get(id string) (Task, error) {
	a.mu.RLock()
	state, ok := a.tasks[id]
	a.mu.RUnlock()
	if !ok {
		return Task{}, errors.New("task not found")
	}
	return a.snapshot(state), nil
}

func (a *Agent) ResolveServer(name string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", errors.New("server name is required")
	}
	var matchID, matchName string
	for id, server := range a.servers {
		if server.Name != name {
			continue
		}
		if matchID != "" {
			return "", "", errors.New("server name is ambiguous")
		}
		matchID, matchName = id, server.Name
	}
	if matchID == "" {
		return "", "", errors.New("server name is not in the enabled inventory")
	}
	return matchID, matchName, nil
}

func (a *Agent) ListInternalServers() []ServerSummary {
	items := make([]ServerSummary, 0, len(a.servers))
	for id, server := range a.servers {
		if !internalOpsServer(server) {
			continue
		}
		display := strings.TrimSpace(server.Name)
		if display == "" {
			display = id
		}
		items = append(items, ServerSummary{ServerID: id, DisplayName: display})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].DisplayName == items[j].DisplayName {
			return items[i].ServerID < items[j].ServerID
		}
		return items[i].DisplayName < items[j].DisplayName
	})
	return items
}

func (a *Agent) TaskCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.tasks)
}

func (a *Agent) Subscribe(id string, lastID int64) (<-chan Event, []Event, func(), error) {
	a.mu.Lock()
	state, ok := a.tasks[id]
	if !ok {
		a.mu.Unlock()
		return nil, nil, nil, errors.New("task not found")
	}
	ch := make(chan Event, 32)
	if a.watchers[id] == nil {
		a.watchers[id] = make(map[chan Event]struct{})
	}
	a.watchers[id][ch] = struct{}{}
	history := make([]Event, 0)
	for _, event := range state.Events {
		if event.ID > lastID {
			history = append(history, event)
		}
	}
	a.mu.Unlock()
	closeFn := func() { a.mu.Lock(); delete(a.watchers[id], ch); a.mu.Unlock() }
	return ch, history, closeFn, nil
}

func (a *Agent) watchTaskDeadline(state *taskState) {
	<-state.ctx.Done()
	if !errors.Is(state.ctx.Err(), context.DeadlineExceeded) {
		return
	}
	errorMessage := "本次运维任务超过最大执行时间"
	a.mu.Lock()
	if state.Status != StatusRunning && state.Status != StatusAwaitingApproval {
		a.mu.Unlock()
		return
	}
	state.Status = StatusFailed
	state.Error = errorMessage
	state.UpdatedAt = time.Now().UTC()
	a.mu.Unlock()
	a.emit(state.ID, "task_failed", map[string]string{"error": errorMessage})
}

func (a *Agent) emit(id, eventType string, data any) {
	a.mu.Lock()
	state, ok := a.tasks[id]
	if !ok {
		a.mu.Unlock()
		return
	}
	event := Event{ID: int64(len(state.Events) + 1), Type: eventType, At: time.Now().UTC(), Data: data}
	state.Events = append(state.Events, event)
	state.UpdatedAt = event.At
	watchers := make([]chan Event, 0, len(a.watchers[id]))
	for watcher := range a.watchers[id] {
		watchers = append(watchers, watcher)
	}
	a.mu.Unlock()
	for _, watcher := range watchers {
		select {
		case watcher <- event:
		default:
		}
	}
}

func (a *Agent) complete(state *taskState, answer string) {
	finalAnswer := safeText(answer, 32000)
	a.mu.Lock()
	if terminalTask(state.Status) {
		a.mu.Unlock()
		return
	}
	state.Status = StatusCompleted
	state.FinalAnswer = finalAnswer
	state.UpdatedAt = time.Now().UTC()
	a.mu.Unlock()
	state.cancel()
	a.emit(state.ID, "task_completed", map[string]string{"answer": finalAnswer})
}
func (a *Agent) fail(state *taskState, message string) {
	errorMessage := safeText(message, 1000)
	a.mu.Lock()
	if terminalTask(state.Status) {
		a.mu.Unlock()
		return
	}
	state.Status = StatusFailed
	state.Error = errorMessage
	state.UpdatedAt = time.Now().UTC()
	a.mu.Unlock()
	state.cancel()
	a.emit(state.ID, "task_failed", map[string]string{"error": errorMessage})
}

func (a *Agent) snapshot(state *taskState) Task {
	a.mu.RLock()
	defer a.mu.RUnlock()
	copy := state.Task
	copy.Events = append([]Event(nil), state.Events...)
	if state.Pending != nil {
		pending := *state.Pending
		copy.Pending = &pending
	}
	return copy
}

func (a *Agent) reaper() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now().UTC()
			a.mu.Lock()
			for id, state := range a.tasks {
				if now.Sub(state.UpdatedAt) > a.cfg.Limits.TaskTTL.Duration {
					state.cancel()
					delete(a.tasks, id)
				}
			}
			a.mu.Unlock()
		case <-a.cleanStop:
			return
		}
	}
}

func systemMessage(ctx TaskContext) chatMessage {
	labels, _ := json.Marshal(ctx.AlertLabels)
	return chatMessage{Role: "system", Content: fmt.Sprintf("你是 Erlang 外服运维 Agent。一次任务内完成分析、Skill 加载、逐条 Shell 审批执行和结果验证。当前服务器由服务端固定且仅允许配置地址属于 192.168.100.* 的内网节点：server_id=%s, server_name=%s, node=%s, dashboard_uid=%s, from=%s, to=%s, alert_labels=%s。不得自行更换服务器、生成隐藏推理或声称未执行的操作已经完成。先调用 list_skills，基于返回的已有 Skill 推荐解决方案，再按任务需要 load_skill；没有匹配 Skill 时只报告分析结果和能力缺口，不得调用 Shell。Shell 只能服务于已加载 Skill 的职责，Skill 也不能绕过后端安全策略。Shell 每次只能调用一条。最终用中文分开写事实、判断、执行记录、验证结果和未解决项。", ctx.ServerID, ctx.ServerName, ctx.Node, ctx.DashboardUID, ctx.From, ctx.To, labels)}
}

func decodeArguments(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
func toolError(code, message string) map[string]string {
	return map[string]string{"status": "error", "code": code, "message": message}
}
func encodeResult(value any) string { raw, _ := json.Marshal(value); return string(raw) }
func cleanError(err error) string   { return safeText(err.Error(), 500) }
func safeText(value string, max int) string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\x00", ""), "\r", "")
	if len(value) > max {
		return value[:max] + "…"
	}
	return value
}
func randomID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
func withTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}
