package holmesgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	monitorconfig "erlang-monitor/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

type scriptedHolmes struct {
	mu    sync.Mutex
	calls int
}

func (h *scriptedHolmes) Models(context.Context) ([]string, error) {
	return []string{"glm", "unlisted"}, nil
}
func (h *scriptedHolmes) Health(context.Context) error { return nil }
func (h *scriptedHolmes) StreamChat(_ context.Context, request HolmesChatRequest, consume func(HolmesEvent) error) error {
	h.mu.Lock()
	h.calls++
	call := h.calls
	h.mu.Unlock()
	if !request.Stream || !request.EnableToolApproval || len(request.FrontendTools) != 5 {
		return io.ErrUnexpectedEOF
	}
	switch call {
	case 1:
		if err := consume(event("ai_message", `{"content":"先检查主机","reasoning":"hidden"}`)); err != nil {
			return err
		}
		return consume(event("approval_required", `{"conversation_history":[{"role":"system","content":"bounded"}],"pending_approvals":[],"pending_frontend_tool_calls":[{"tool_call_id":"safe-1","tool_name":"get_host_snapshot","arguments":{"server_id":"external-1"}}]}`))
	case 2:
		if len(request.FrontendToolResults) != 1 || request.FrontendToolResults[0].ToolName != "get_host_snapshot" {
			return io.ErrUnexpectedEOF
		}
		return consume(event("approval_required", `{"conversation_history":[{"role":"system","content":"bounded"}],"pending_approvals":[],"pending_frontend_tool_calls":[{"tool_call_id":"approve-1","tool_name":"get_process_hotspots","arguments":{"server_id":"external-1","node":"game@127.0.0.1","metric":"reductions","top_n":10}}]}`))
	default:
		if len(request.FrontendToolResults) != 1 || request.FrontendToolResults[0].ToolName != "get_process_hotspots" {
			return io.ErrUnexpectedEOF
		}
		return consume(event("ai_answer_end", `{"analysis":"结论：证据支持短时热点。","conversation_history":[{"role":"assistant","content":"done","reasoning":"hidden","api_key":"remove"}],"metadata":{"token_count":42,"api_key":"remove"}}`))
	}
}

func event(eventType, data string) HolmesEvent {
	return HolmesEvent{Type: eventType, Data: json.RawMessage(data)}
}

type fakeTools struct {
	mu       sync.Mutex
	executed map[string]int
	result   *ToolExecutionResult
	reject   map[string]error
}

func (f *fakeTools) Validate(session *Session, call ToolCall) error {
	if f.reject != nil && f.reject[call.CallID] != nil {
		return f.reject[call.CallID]
	}
	var args map[string]any
	if json.Unmarshal(call.Arguments, &args) != nil || args["server_id"] != session.Context.ServerID {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func (f *fakeTools) RequiresApproval(name string) bool { return name == "get_process_hotspots" }
func (f *fakeTools) Execute(_ context.Context, _ *Session, call ToolCall) ToolExecutionResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executed[call.CallID]++
	if f.result != nil {
		return *f.result
	}
	return ToolExecutionResult{Status: "success", Data: map[string]any{"call": call.CallID}}
}

type capturingAuditor struct {
	mu      sync.Mutex
	records []AuditRecord
}

type blockingHolmes struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingHolmes() *blockingHolmes {
	return &blockingHolmes{started: make(chan struct{}), release: make(chan struct{})}
}

func (h *blockingHolmes) Models(context.Context) ([]string, error) { return []string{"glm"}, nil }
func (h *blockingHolmes) Health(context.Context) error             { return nil }
func (h *blockingHolmes) StreamChat(ctx context.Context, _ HolmesChatRequest, consume func(HolmesEvent) error) error {
	h.mu.Lock()
	h.calls++
	h.mu.Unlock()
	h.once.Do(func() { close(h.started) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.release:
		return consume(event("ai_answer_end", `{"analysis":"done","conversation_history":[{"role":"assistant","content":"done"}]}`))
	}
}

type mixedApprovalHolmes struct {
	mu      sync.Mutex
	calls   int
	results []FrontendToolResult
}

type overlappingRoundHolmes struct {
	mu            sync.Mutex
	calls         int
	secondStarted chan struct{}
	releaseSecond chan struct{}
}

func newOverlappingRoundHolmes() *overlappingRoundHolmes {
	return &overlappingRoundHolmes{secondStarted: make(chan struct{}), releaseSecond: make(chan struct{})}
}

func (h *overlappingRoundHolmes) Models(context.Context) ([]string, error) {
	return []string{"glm"}, nil
}
func (h *overlappingRoundHolmes) Health(context.Context) error { return nil }
func (h *overlappingRoundHolmes) StreamChat(_ context.Context, _ HolmesChatRequest, consume func(HolmesEvent) error) error {
	h.mu.Lock()
	h.calls++
	call := h.calls
	h.mu.Unlock()
	if call == 1 {
		if err := consume(event("approval_required", `{"conversation_history":[{"role":"assistant","content":"pause"}],"pending_approvals":[],"pending_frontend_tool_calls":[{"tool_call_id":"safe-overlap","tool_name":"get_host_snapshot","arguments":{"server_id":"external-1"}}]}`)); err != nil {
			return err
		}
		return errors.New("MODEL_ERROR: stale first round failure")
	}
	close(h.secondStarted)
	<-h.releaseSecond
	return consume(event("ai_answer_end", `{"analysis":"new round completed","conversation_history":[{"role":"assistant","content":"done"}]}`))
}

func (h *mixedApprovalHolmes) Models(context.Context) ([]string, error) { return []string{"glm"}, nil }
func (h *mixedApprovalHolmes) Health(context.Context) error             { return nil }
func (h *mixedApprovalHolmes) StreamChat(_ context.Context, request HolmesChatRequest, consume func(HolmesEvent) error) error {
	h.mu.Lock()
	h.calls++
	call := h.calls
	if call > 1 {
		h.results = append([]FrontendToolResult(nil), request.FrontendToolResults...)
	}
	h.mu.Unlock()
	if call == 1 {
		return consume(event("approval_required", `{"conversation_history":[{"role":"assistant","content":"pause"}],"pending_approvals":[],"pending_frontend_tool_calls":[{"tool_call_id":"invalid-1","tool_name":"get_host_snapshot","arguments":{"server_id":"external-1","command":"blocked"}},{"tool_call_id":"manual-1","tool_name":"get_process_hotspots","arguments":{"server_id":"external-1","node":"game@127.0.0.1","metric":"reductions","top_n":5}}]}`))
	}
	return consume(event("ai_answer_end", `{"analysis":"done","conversation_history":[{"role":"assistant","content":"done"}]}`))
}

func (a *capturingAuditor) Record(record AuditRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, record)
	return nil
}

func TestGatewayRunsTwoToolRoundsWithApprovalAndPersistence(t *testing.T) {
	gateway, handler, holmes, tools := testGateway(t)
	auditor := &capturingAuditor{}
	gateway.SetAuditor(auditor)
	now := time.Now().UTC()
	body := `{"request_id":"request-0001","model":"glm","ask":"分析告警","context":{"server_id":"external-1","node":"game@127.0.0.1","dashboard_uid":"erlang-monitor-overview","from":"` + now.Add(-time.Hour).Format(time.RFC3339) + `","to":"` + now.Format(time.RFC3339) + `","alert_labels":{}}}`
	created := request(t, handler, http.MethodPost, "/investigations", body, "Editor", "alice")
	if created.Code != http.StatusAccepted {
		t.Fatalf("create failed: %d %s", created.Code, created.Body.String())
	}
	var response map[string]string
	_ = json.Unmarshal(created.Body.Bytes(), &response)
	sessionID := response["session_id"]
	waitStatus(t, gateway.store, sessionID, StatusAwaitingApproval)
	session, _ := gateway.store.Get(sessionID)
	if len(session.PendingTools) != 1 || session.PendingTools[0].CallID != "approve-1" || tools.executed["safe-1"] != 1 {
		t.Fatalf("safe tool/approval flow mismatch: %#v %#v", session.PendingTools, tools.executed)
	}
	decision := request(t, handler, http.MethodPost, "/investigations/"+sessionID+"/decisions", `{"request_id":"request-0002","tool_call_id":"approve-1","approved":true}`, "Admin", "admin")
	if decision.Code != http.StatusAccepted {
		t.Fatalf("decision failed: %d %s", decision.Code, decision.Body.String())
	}
	waitStatus(t, gateway.store, sessionID, StatusCompleted)
	session, _ = gateway.store.Get(sessionID)
	if !strings.Contains(session.FinalAnswer, "证据") || tools.executed["approve-1"] != 1 || holmes.calls != 3 {
		t.Fatalf("unexpected completion: %#v calls=%d tools=%#v", session, holmes.calls, tools.executed)
	}
	if len(session.PendingTools) != 0 {
		t.Fatalf("completed session retained pending tools: %#v", session.PendingTools)
	}
	if strings.Contains(string(session.Usage), "api_key") {
		t.Fatalf("usage leaked sensitive key name: %s", session.Usage)
	}
	if strings.Contains(string(session.ConversationHistory), "reasoning") || strings.Contains(string(session.ConversationHistory), "api_key") {
		t.Fatalf("conversation history persisted hidden fields: %s", session.ConversationHistory)
	}
	repeated := request(t, handler, http.MethodPost, "/investigations/"+sessionID+"/decisions", `{"request_id":"request-0003","tool_call_id":"approve-1","approved":true}`, "Admin", "admin")
	if repeated.Code != http.StatusOK || tools.executed["approve-1"] != 1 {
		t.Fatalf("approval was not idempotent: %d %s executions=%d", repeated.Code, repeated.Body.String(), tools.executed["approve-1"])
	}
	requiredAudit := map[string]bool{"investigation_created": false, "investigation_started": false, "tool_finished": false, "tool_decision": false, "investigation_completed": false}
	for _, record := range auditor.records {
		if _, exists := requiredAudit[record.Event]; exists {
			requiredAudit[record.Event] = true
		}
	}
	for eventName, found := range requiredAudit {
		if !found {
			t.Fatalf("required audit event %s is missing: %#v", eventName, auditor.records)
		}
	}
}

func TestCompletionFallsBackToAssistantHistoryWhenAnalysisIsEmpty(t *testing.T) {
	gateway, _, _, _ := testGateway(t)
	session := testStoredSession("empty-analysis", "empty-analysis-request", StatusRunning, time.Now().UTC())
	if err := gateway.store.Create(session); err != nil {
		t.Fatal(err)
	}
	err := gateway.complete(session.SessionID, json.RawMessage(`{"analysis":"","conversation_history":[{"role":"assistant","content":"fallback conclusion"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	completed, err := gateway.store.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != StatusCompleted || completed.FinalAnswer != "fallback conclusion" {
		t.Fatalf("empty analysis was not recovered: status=%s answer=%q", completed.Status, completed.FinalAnswer)
	}
}

func TestCompletionRejectsEmptyAnswerWithoutFallback(t *testing.T) {
	gateway, _, _, _ := testGateway(t)
	session := testStoredSession("no-answer", "no-answer-request", StatusRunning, time.Now().UTC())
	if err := gateway.store.Create(session); err != nil {
		t.Fatal(err)
	}
	if err := gateway.complete(session.SessionID, json.RawMessage(`{"analysis":"","conversation_history":[]}`)); err == nil {
		t.Fatal("empty completion should be rejected")
	}
	current, err := gateway.store.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status == StatusCompleted || current.FinalAnswer != "" {
		t.Fatalf("empty completion was persisted as completed: %#v", current)
	}
}

func TestSystemPromptExplainsLocalExporterBoundary(t *testing.T) {
	gateway, _, _, _ := testGateway(t)
	session := testStoredSession("prompt-session", "prompt-request", StatusCreated, time.Now().UTC())
	session.Context.ServerID = "external-1"
	prompt := gateway.systemPrompt(session)
	for _, required := range []string{"20903", "监控主机本地 Exporter", "绝不能把 20903 当成被监控服务器端口"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("system prompt is missing %q: %s", required, prompt)
		}
	}
}

func TestHolmesOrchestrationEventsDoNotDuplicateGatewayToolEvidence(t *testing.T) {
	gateway, _, _, _ := testGateway(t)
	now := time.Now().UTC()
	session := testStoredSession("tool-events", "tool-events-request", StatusRunning, now)
	session.Context.ServerID = "external-1"
	if err := gateway.store.Create(session); err != nil {
		t.Fatal(err)
	}

	frontendStart := event("start_tool_calling", `{"tool_name":"get_node_snapshot","tool_call_id":"frontend-1"}`)
	frontendFinish := event("tool_calling_result", `{"tool_name":"get_node_snapshot","tool_call_id":"frontend-1","result":{"status":"success"}}`)
	internalStart := event("start_tool_calling", `{"tool_name":"TodoWrite","tool_call_id":"internal-1"}`)
	internalFinish := event("tool_calling_result", `{"tool_name":"TodoWrite","tool_call_id":"internal-1","result":{"status":"success"}}`)
	for _, holmesEvent := range []HolmesEvent{frontendStart, frontendFinish, internalStart, internalFinish} {
		if err := gateway.consumeHolmesEvent(context.Background(), session.SessionID, holmesEvent); err != nil {
			t.Fatal(err)
		}
	}

	stored, err := gateway.store.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Events) != 2 {
		t.Fatalf("frontend orchestration events were not filtered: %#v", stored.Events)
	}
	for _, storedEvent := range stored.Events {
		if !strings.Contains(string(storedEvent.Data), "TodoWrite") {
			t.Fatalf("unexpected persisted event: %#v", storedEvent)
		}
	}
}

func TestGatewayAcceptsSafeShortNodeContextAndRejectsUnsafeOrUnknownLabels(t *testing.T) {
	now := time.Now().UTC()
	body := func(requestID, node string) string {
		return `{"request_id":"` + requestID + `","model":"glm","ask":"分析告警","context":{"server_id":"external-1","node":"` + node + `","dashboard_uid":"erlang-monitor-overview","from":"` + now.Add(-time.Hour).Format(time.RFC3339) + `","to":"` + now.Format(time.RFC3339) + `","alert_labels":{}}}`
	}

	t.Run("safe short node present in Prometheus", func(t *testing.T) {
		_, handler, _, _ := testGateway(t)
		created := request(t, handler, http.MethodPost, "/investigations", body("short-node-present", "wl_banshu_1"), "Editor", "alice")
		if created.Code != http.StatusAccepted {
			t.Fatalf("safe short node context was rejected: %d %s", created.Code, created.Body.String())
		}
	})

	t.Run("unsafe short node", func(t *testing.T) {
		_, handler, _, _ := testGateway(t)
		created := request(t, handler, http.MethodPost, "/investigations", body("short-node-unsafe", "wl_banshu/1"), "Editor", "alice")
		if created.Code != http.StatusBadRequest || !strings.Contains(created.Body.String(), "node 格式无效") {
			t.Fatalf("unsafe short node label was not rejected: %d %s", created.Code, created.Body.String())
		}
	})

	t.Run("unknown short node", func(t *testing.T) {
		gateway, handler, _, _ := testGateway(t)
		prometheus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
		}))
		t.Cleanup(prometheus.Close)
		gateway.config.PrometheusURL = prometheus.URL
		created := request(t, handler, http.MethodPost, "/investigations", body("short-node-unknown", "missing_node_1"), "Editor", "alice")
		if created.Code != http.StatusBadRequest || !strings.Contains(created.Body.String(), "Prometheus 节点清单") {
			t.Fatalf("unknown short node label was not rejected: %d %s", created.Code, created.Body.String())
		}
	})
}

func TestHolmesErrorEventClassifiesProviderQuotaWithoutLeakingDescription(t *testing.T) {
	err := classifyHolmesEventError(json.RawMessage(`{"description":"litellm.RateLimitError: 余额不足或无可用资源包，请充值。 secret-detail","error_code":5204,"msg":"Rate limit exceeded"}`))
	if err == nil || !strings.Contains(err.Error(), "MODEL_QUOTA_EXHAUSTED") || strings.Contains(err.Error(), "secret-detail") {
		t.Fatalf("provider quota event was not safely classified: %v", err)
	}
	apiError := normalizeError(err, "request-quota")
	if apiError.Code != "MODEL_QUOTA_EXHAUSTED" || apiError.Retryable || !strings.Contains(apiError.Message, "余额不足") {
		t.Fatalf("provider quota API error mismatch: %#v", apiError)
	}
}

func TestGatewayRejectsAnonymousAndFiltersModels(t *testing.T) {
	_, handler, _, _ := testGateway(t)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/models", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous request accepted: %d", unauthorized.Code)
	}
	missingUser := httptest.NewRecorder()
	missingUserRequest := httptest.NewRequest(http.MethodGet, "/models", nil)
	missingUserRequest.Header.Set("Authorization", "Bearer test-token")
	missingUserRequest.Header.Set("X-Erlang-Monitor-Role", "Editor")
	handler.ServeHTTP(missingUser, missingUserRequest)
	if missingUser.Code != http.StatusUnauthorized || !strings.Contains(missingUser.Body.String(), "真实用户身份") {
		t.Fatalf("request without Grafana username was accepted: %d %s", missingUser.Code, missingUser.Body.String())
	}
	models := request(t, handler, http.MethodGet, "/models", "", "Editor", "alice")
	if models.Code != http.StatusOK || strings.Contains(models.Body.String(), "unlisted") || !strings.Contains(models.Body.String(), "glm") {
		t.Fatalf("model filtering failed: %d %s", models.Code, models.Body.String())
	}
}

func TestCompatibilityProxyForwardsOnlyBoundedGatewayPaths(t *testing.T) {
	_, handler, _, _ := testGateway(t)

	models := request(t, handler, http.MethodGet, "/?_path=%2Fmodels", "", "Editor", "operator")
	if models.Code != http.StatusOK || !strings.Contains(models.Body.String(), `"alias":"glm"`) {
		t.Fatalf("compatibility models proxy failed: %d %s", models.Code, models.Body.String())
	}
	resolved := request(t, handler, http.MethodGet, "/?_path=%2Fservers%2Fresolve&name=101.34.55.142", "", "Editor", "operator")
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"server_id":"external-1"`) {
		t.Fatalf("compatibility resolve proxy failed: %d %s", resolved.Code, resolved.Body.String())
	}
	for _, unsafe := range []string{"/healthz", "/investigations/../models", "/investigations/not-a-session/events", "/investigations/0123456789abcdef0123456789abcdef/unknown"} {
		response := request(t, handler, http.MethodGet, "/?_path="+unsafe, "", "Editor", "operator")
		if response.Code != http.StatusNotFound {
			t.Fatalf("unsafe compatibility path %q returned %d", unsafe, response.Code)
		}
	}
}

func TestHealthReportsIndependentDependencyStates(t *testing.T) {
	_, handler, _, _ := testGateway(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("health failed: %d %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Status       string            `json:"status"`
		Dependencies map[string]string `json:"dependencies"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &payload) != nil || payload.Status != "healthy" {
		t.Fatalf("invalid health payload: %s", recorder.Body.String())
	}
	for dependency, expected := range map[string]string{"holmes_process": "healthy", "model_config": "configured", "model_availability": "available", "prometheus": "healthy", "diagnostic_tools": "configured"} {
		if payload.Dependencies[dependency] != expected {
			t.Fatalf("dependency %s = %q, want %q: %#v", dependency, payload.Dependencies[dependency], expected, payload.Dependencies)
		}
	}
}

func TestConcurrentFollowUpsReserveOneSessionRequest(t *testing.T) {
	gateway, handler, _, _ := testGateway(t)
	holmes := newBlockingHolmes()
	gateway.holmes = holmes
	now := time.Now().UTC()
	session := testStoredSession("follow-up-session", "initial-request", StatusCompleted, now)
	session.Context = InvestigationContext{ServerID: "external-1", DashboardUID: "erlang-monitor-overview", From: now.Add(-time.Hour), To: now}
	if err := gateway.store.Create(session); err != nil {
		t.Fatal(err)
	}

	statuses := concurrentPosts(handler, "/investigations/follow-up-session/messages", []string{
		`{"request_id":"follow-up-0001","ask":"first"}`,
		`{"request_id":"follow-up-0002","ask":"second"}`,
	}, "Editor", "alice")
	if !containsInt(statuses, http.StatusAccepted) || !containsInt(statuses, http.StatusConflict) {
		t.Fatalf("concurrent follow-ups were not serialized: %#v", statuses)
	}
	close(holmes.release)
}

func TestBusyFollowUpReturnsLocalizedConflict(t *testing.T) {
	gateway, handler, _, _ := testGateway(t)
	holmes := newBlockingHolmes()
	gateway.holmes = holmes
	now := time.Now().UTC()
	session := testStoredSession("busy-follow-up", "initial-request", StatusCompleted, now)
	session.Context = InvestigationContext{ServerID: "external-1", DashboardUID: "erlang-monitor-overview", From: now.Add(-time.Hour), To: now}
	if err := gateway.store.Create(session); err != nil {
		t.Fatal(err)
	}

	accepted := request(t, handler, http.MethodPost, "/investigations/busy-follow-up/messages", `{"request_id":"follow-up-0001","ask":"first"}`, "Editor", "alice")
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("first follow-up failed: %d %s", accepted.Code, accepted.Body.String())
	}
	conflict := request(t, handler, http.MethodPost, "/investigations/busy-follow-up/messages", `{"request_id":"follow-up-0002","ask":"second"}`, "Editor", "alice")
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"code":"SESSION_BUSY"`) || !strings.Contains(conflict.Body.String(), "上一条追问正在处理中") {
		t.Fatalf("unexpected busy response: %d %s", conflict.Code, conflict.Body.String())
	}
	close(holmes.release)
}

func TestConcurrentApprovalResumesAndExecutesOnlyOnce(t *testing.T) {
	gateway, handler, _, tools := testGateway(t)
	holmes := newBlockingHolmes()
	gateway.holmes = holmes
	now := time.Now().UTC()
	session := testStoredSession("approval-session", "initial-request", StatusAwaitingApproval, now)
	session.Context = InvestigationContext{ServerID: "external-1", Node: "game@127.0.0.1", DashboardUID: "erlang-monitor-overview", From: now.Add(-time.Hour), To: now}
	session.PendingTools = []PendingTool{{CallID: "manual-1", Name: "get_process_hotspots", Arguments: json.RawMessage(`{"server_id":"external-1","node":"game@127.0.0.1","metric":"reductions"}`), RequiresUser: true}}
	if err := gateway.store.Create(session); err != nil {
		t.Fatal(err)
	}

	statuses := concurrentPosts(handler, "/investigations/approval-session/decisions", []string{
		`{"request_id":"decision-0001","tool_call_id":"manual-1","approved":true}`,
		`{"request_id":"decision-0002","tool_call_id":"manual-1","approved":true}`,
	}, "Admin", "admin")
	for _, status := range statuses {
		if status != http.StatusAccepted && status != http.StatusOK {
			t.Fatalf("concurrent idempotent decision returned %d: %#v", status, statuses)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tools.mu.Lock()
		executions := tools.executed["manual-1"]
		tools.mu.Unlock()
		if executions == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	tools.mu.Lock()
	executions := tools.executed["manual-1"]
	tools.mu.Unlock()
	if executions != 1 {
		t.Fatalf("approved tool executed %d times", executions)
	}
	select {
	case <-holmes.started:
	case <-time.After(2 * time.Second):
		t.Fatal("approved session did not resume Holmes")
	}
	holmes.mu.Lock()
	holmesCalls := holmes.calls
	holmes.mu.Unlock()
	if holmesCalls != 1 {
		t.Fatalf("approval resumed Holmes %d times", holmesCalls)
	}
	close(holmes.release)
}

func TestMixedApprovalResumesRejectedAndApprovedResultsTogether(t *testing.T) {
	gateway, handler, _, tools := testGateway(t)
	holmes := &mixedApprovalHolmes{}
	gateway.holmes = holmes
	tools.reject = map[string]error{"invalid-1": errors.New("command field is forbidden")}
	now := time.Now().UTC()
	body := `{"request_id":"mixed-request","model":"glm","ask":"mixed tools","context":{"server_id":"external-1","node":"game@127.0.0.1","dashboard_uid":"erlang-monitor-overview","from":"` + now.Add(-time.Hour).Format(time.RFC3339) + `","to":"` + now.Format(time.RFC3339) + `","alert_labels":{}}}`
	created := request(t, handler, http.MethodPost, "/investigations", body, "Editor", "alice")
	var response map[string]string
	_ = json.Unmarshal(created.Body.Bytes(), &response)
	waitStatus(t, gateway.store, response["session_id"], StatusAwaitingApproval)
	decision := request(t, handler, http.MethodPost, "/investigations/"+response["session_id"]+"/decisions", `{"request_id":"mixed-decision","tool_call_id":"manual-1","approved":true}`, "Admin", "admin")
	if decision.Code != http.StatusAccepted {
		t.Fatalf("mixed decision failed: %d %s", decision.Code, decision.Body.String())
	}
	waitStatus(t, gateway.store, response["session_id"], StatusCompleted)
	holmes.mu.Lock()
	results := append([]FrontendToolResult(nil), holmes.results...)
	holmes.mu.Unlock()
	seen := map[string]bool{}
	for _, result := range results {
		seen[result.ToolCallID] = true
	}
	if !seen["invalid-1"] || !seen["manual-1"] || len(results) != 2 {
		t.Fatalf("resume omitted a mixed tool result: %#v", results)
	}
}

func TestStaleRoundFailureCannotOverwriteNewAutomaticRound(t *testing.T) {
	gateway, handler, _, _ := testGateway(t)
	holmes := newOverlappingRoundHolmes()
	gateway.holmes = holmes
	now := time.Now().UTC()
	body := `{"request_id":"overlap-request","model":"glm","ask":"overlap","context":{"server_id":"external-1","dashboard_uid":"erlang-monitor-overview","from":"` + now.Add(-time.Hour).Format(time.RFC3339) + `","to":"` + now.Format(time.RFC3339) + `","alert_labels":{}}}`
	created := request(t, handler, http.MethodPost, "/investigations", body, "Editor", "alice")
	if created.Code != http.StatusAccepted {
		t.Fatalf("create failed: %d %s", created.Code, created.Body.String())
	}
	var response map[string]string
	_ = json.Unmarshal(created.Body.Bytes(), &response)
	select {
	case <-holmes.secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("automatic second round did not start")
	}
	time.Sleep(30 * time.Millisecond)
	session, err := gateway.store.Get(response["session_id"])
	if err != nil || session.Status != StatusRunning {
		t.Fatalf("stale first-round failure overwrote the active round: %#v err=%v", session, err)
	}
	close(holmes.releaseSecond)
	waitStatus(t, gateway.store, response["session_id"], StatusCompleted)
}

func TestSanitizeConversationHistoryRemovesHiddenAndInlineSecrets(t *testing.T) {
	raw := json.RawMessage(`[{"role":"assistant","content":"Authorization: Bearer abcdefghijklmnop","reasoning":"hidden","thinking":"hidden","nested":{"api_key":"secret","content":"token=abcdefghi"}}]`)
	cleaned := strings.ToLower(string(sanitizeConversationHistory(raw)))
	for _, forbidden := range []string{"reasoning", "thinking", "api_key", "abcdefghijklmnop", "abcdefghi"} {
		if strings.Contains(cleaned, forbidden) {
			t.Fatalf("conversation history retained %q: %s", forbidden, cleaned)
		}
	}
}

func concurrentPosts(handler http.Handler, path string, bodies []string, role, user string) []int {
	statuses := make([]int, len(bodies))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range bodies {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(bodies[index]))
			request.Header.Set("Authorization", "Bearer test-token")
			request.Header.Set("X-Erlang-Monitor-Role", role)
			request.Header.Set("X-Grafana-User", user)
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(recorder, request)
			statuses[index] = recorder.Code
		}(index)
	}
	close(start)
	wait.Wait()
	return statuses
}

func containsInt(values []int, expected int) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestGatewayAccountsForPerToolAndCumulativeOutputTruncation(t *testing.T) {
	gateway, _, _, tools := testGateway(t)
	gateway.config.Limits.MaxOutputBytes = 32 * 1024
	auditor := &capturingAuditor{}
	gateway.SetAuditor(auditor)
	registry := prometheus.NewRegistry()
	gateway.SetMetrics(NewMetrics(registry))

	createToolSession := func(id string, outputBytes int64) *Session {
		now := time.Now().UTC()
		session := &Session{
			SessionID: id, RequestIDs: map[string]bool{}, Creator: "alice", GrafanaRole: "Editor",
			Status: StatusRunning, Model: "glm", Context: InvestigationContext{ServerID: "external-1"},
			ToolResults: map[string]string{}, CreatedAt: now, UpdatedAt: now, OutputBytes: outputBytes,
		}
		if err := gateway.store.Create(session); err != nil {
			t.Fatal(err)
		}
		return session
	}

	tools.result = &ToolExecutionResult{Status: "success", Data: strings.Repeat("x", 40*1024)}
	perTool := gateway.executeToolOnce(context.Background(), createToolSession("per-tool", 0), ToolCall{CallID: "large-1", Name: "get_host_snapshot"})
	var perToolResult ToolExecutionResult
	if err := json.Unmarshal([]byte(perTool.Result), &perToolResult); err != nil {
		t.Fatal(err)
	}
	if !perToolResult.Truncated || len(perTool.Result) > toolOutputLimit("get_host_snapshot") {
		t.Fatalf("per-tool truncation was not bounded and marked: bytes=%d result=%#v", len(perTool.Result), perToolResult)
	}
	assertStoredToolResultTruncated(t, gateway.store, "per-tool", "")

	tools.result = &ToolExecutionResult{Status: "success", Data: "small"}
	cumulative := gateway.executeToolOnce(context.Background(), createToolSession("cumulative", gateway.config.Limits.MaxOutputBytes-1), ToolCall{CallID: "small-1", Name: "get_host_snapshot"})
	var cumulativeResult ToolExecutionResult
	if err := json.Unmarshal([]byte(cumulative.Result), &cumulativeResult); err != nil {
		t.Fatal(err)
	}
	if !cumulativeResult.Truncated || cumulativeResult.ErrorCode != "TOOL_OUTPUT_LIMIT_REACHED" {
		t.Fatalf("cumulative truncation was not marked: %#v", cumulativeResult)
	}
	assertStoredToolResultTruncated(t, gateway.store, "cumulative", "TOOL_OUTPUT_LIMIT_REACHED")

	var finished []AuditRecord
	for _, record := range auditor.records {
		if record.Event == "tool_finished" {
			finished = append(finished, record)
		}
	}
	if len(finished) != 2 || !finished[0].OutputTruncated || !finished[1].OutputTruncated {
		t.Fatalf("audit truncation accounting mismatch: %#v", auditor.records)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var truncations float64
	for _, family := range families {
		if family.GetName() == "holmes_gateway_tool_output_truncations_total" && len(family.Metric) == 1 {
			truncations = family.Metric[0].GetCounter().GetValue()
		}
	}
	if truncations != 2 {
		t.Fatalf("truncation metric = %v, want 2", truncations)
	}
}

func assertStoredToolResultTruncated(t *testing.T, store *Store, sessionID, errorCode string) {
	t.Helper()
	session, err := store.Get(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Events) != 1 || session.Events[0].Type != "tool_finished" {
		t.Fatalf("unexpected stored events: %#v", session.Events)
	}
	var payload struct {
		Result ToolExecutionResult `json:"result"`
	}
	if err := json.Unmarshal(session.Events[0].Data, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Result.Truncated || payload.Result.ErrorCode != errorCode {
		t.Fatalf("stored result truncation mismatch: %#v", payload.Result)
	}
}

func testGateway(t *testing.T) (*Gateway, http.Handler, *scriptedHolmes, *fakeTools) {
	t.Helper()
	prometheus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"name":"101.34.55.142","node":"game@127.0.0.1"},"value":[1,"1"]}]}}`)
	}))
	t.Cleanup(prometheus.Close)
	cfg, err := ParseConfig([]byte(fmt.Sprintf(`holmes_version: "0.38.1"
holmes_url: http://127.0.0.1:20905
prometheus_url: %s
models:
  glm: {display_name: GLM, enabled: true}
  kimi: {display_name: Kimi, enabled: true}
limits: {max_range: 24h, investigation_timeout: 1m, tool_timeout: 5s, max_sessions: 10, session_retention: 1h}
`, prometheus.URL)))
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	servers := monitorconfig.Exporter{Servers: []monitorconfig.Server{{ID: "external-1", Name: "101.34.55.142", Enabled: &enabled}}}
	store, err := NewStore(t.TempDir(), time.Hour, 10)
	if err != nil {
		t.Fatal(err)
	}
	holmes := &scriptedHolmes{}
	tools := &fakeTools{executed: make(map[string]int)}
	gateway, err := NewGateway(cfg, servers, store, holmes, tools, "test-token", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return gateway, gateway.Handler(), holmes, tools
}

func request(t *testing.T, handler http.Handler, method, path, body, role, user string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("X-Erlang-Monitor-Role", role)
	request.Header.Set("X-Grafana-User", user)
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	return recorder
}

func waitStatus(t *testing.T, store *Store, sessionID string, status SessionStatus) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		session, _ := store.Get(sessionID)
		if session != nil && session.Status == status {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	session, _ := store.Get(sessionID)
	t.Fatalf("session did not reach %s: %#v", status, session)
}
