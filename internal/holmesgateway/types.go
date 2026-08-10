package holmesgateway

import (
	"encoding/json"
	"time"
)

type SessionStatus string

const (
	StatusCreated          SessionStatus = "created"
	StatusRunning          SessionStatus = "running"
	StatusAwaitingApproval SessionStatus = "awaiting_approval"
	StatusCompleted        SessionStatus = "completed"
	StatusFailed           SessionStatus = "failed"
	StatusCancelled        SessionStatus = "cancelled"
)

type InvestigationContext struct {
	ServerID         string            `json:"server_id"`
	Node             string            `json:"node,omitempty"`
	DashboardUID     string            `json:"dashboard_uid"`
	From             time.Time         `json:"from"`
	To               time.Time         `json:"to"`
	AlertFingerprint string            `json:"alert_fingerprint,omitempty"`
	AlertLabels      map[string]string `json:"alert_labels,omitempty"`
}

type CreateRequest struct {
	RequestID string               `json:"request_id"`
	Model     string               `json:"model"`
	Ask       string               `json:"ask"`
	Context   InvestigationContext `json:"context"`
}

type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type Event struct {
	ID   int64           `json:"id"`
	Type string          `json:"type"`
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data,omitempty"`
}

type PendingTool struct {
	CallID       string          `json:"call_id"`
	Name         string          `json:"name"`
	Arguments    json.RawMessage `json:"arguments"`
	RequiresUser bool            `json:"requires_user"`
	Approved     *bool           `json:"approved,omitempty"`
	DecidedBy    string          `json:"decided_by,omitempty"`
	DecidedAt    time.Time       `json:"decided_at,omitempty"`
}

type Session struct {
	SessionID           string               `json:"session_id"`
	RequestIDs          map[string]bool      `json:"request_ids"`
	Creator             string               `json:"creator"`
	GrafanaRole         string               `json:"grafana_role"`
	Status              SessionStatus        `json:"status"`
	Model               string               `json:"model"`
	Context             InvestigationContext `json:"context"`
	Messages            []Message            `json:"messages"`
	ConversationHistory json.RawMessage      `json:"conversation_history,omitempty"`
	Events              []Event              `json:"events"`
	PendingTools        []PendingTool        `json:"pending_tools,omitempty"`
	ToolResults         map[string]string    `json:"tool_results,omitempty"`
	ToolDecisions       map[string]bool      `json:"tool_decisions,omitempty"`
	FinalAnswer         string               `json:"final_answer,omitempty"`
	Error               *APIError            `json:"error,omitempty"`
	Usage               json.RawMessage      `json:"usage,omitempty"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
	RunningRequestID    string               `json:"running_request_id,omitempty"`
	ToolCalls           int                  `json:"tool_calls"`
	OutputBytes         int64                `json:"output_bytes"`
}

type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	RequestID string `json:"request_id,omitempty"`
}

type ErrorEnvelope struct {
	Error APIError `json:"error"`
}

type DecisionRequest struct {
	RequestID  string `json:"request_id"`
	ToolCallID string `json:"tool_call_id"`
	Approved   bool   `json:"approved"`
}

type ToolCall struct {
	CallID    string
	Name      string
	Arguments json.RawMessage
}

type ToolExecutionResult struct {
	Status    string `json:"status"`
	Data      any    `json:"data,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	Error     string `json:"error,omitempty"`
	Truncated bool   `json:"truncated"`
}

type FollowUpRequest struct {
	RequestID string `json:"request_id"`
	Ask       string `json:"ask"`
}

type Actor struct {
	Name string
	Role string
}
