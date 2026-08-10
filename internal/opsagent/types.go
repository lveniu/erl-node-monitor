package opsagent

import (
	"context"
	"encoding/json"
	"time"
)

type TaskStatus string

const (
	StatusRunning          TaskStatus = "running"
	StatusAwaitingApproval TaskStatus = "awaiting_approval"
	StatusCompleted        TaskStatus = "completed"
	StatusFailed           TaskStatus = "failed"
)

type TaskContext struct {
	ServerID     string            `json:"server_id"`
	ServerName   string            `json:"server_name,omitempty"`
	Node         string            `json:"node,omitempty"`
	DashboardUID string            `json:"dashboard_uid,omitempty"`
	From         string            `json:"from,omitempty"`
	To           string            `json:"to,omitempty"`
	AlertLabels  map[string]string `json:"alert_labels,omitempty"`
}

type CreateTaskRequest struct {
	RequestID string      `json:"request_id"`
	Question  string      `json:"question"`
	Context   TaskContext `json:"context"`
}

type DecisionRequest struct {
	RequestID string `json:"request_id"`
	CallID    string `json:"call_id"`
	Approved  bool   `json:"approved"`
}

type Event struct {
	ID   int64     `json:"id"`
	Type string    `json:"type"`
	At   time.Time `json:"at"`
	Data any       `json:"data,omitempty"`
}

type PendingCommand struct {
	CallID         string `json:"call_id"`
	Target         string `json:"target"`
	Command        string `json:"command"`
	Reason         string `json:"reason"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type Task struct {
	ID          string          `json:"id"`
	Creator     string          `json:"creator"`
	Status      TaskStatus      `json:"status"`
	Question    string          `json:"question"`
	Context     TaskContext     `json:"context"`
	Events      []Event         `json:"events"`
	Pending     *PendingCommand `json:"pending_command,omitempty"`
	FinalAnswer string          `json:"final_answer,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type completion struct {
	Content   string
	ToolCalls []toolCall
}

type taskState struct {
	Task
	messages     []chatMessage
	queuedCalls  []toolCall
	loadedSkills map[string]struct{}
	steps        int
	ctx          context.Context
	cancel       context.CancelFunc
}
