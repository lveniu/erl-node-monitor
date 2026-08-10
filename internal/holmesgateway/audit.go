package holmesgateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type AuditRecord struct {
	At               time.Time       `json:"at"`
	Event            string          `json:"event"`
	RequestID        string          `json:"request_id,omitempty"`
	SessionID        string          `json:"session_id,omitempty"`
	Actor            string          `json:"actor,omitempty"`
	GrafanaRole      string          `json:"grafana_role,omitempty"`
	Model            string          `json:"model,omitempty"`
	ServerID         string          `json:"server_id,omitempty"`
	Node             string          `json:"node,omitempty"`
	ToolName         string          `json:"tool_name,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ParameterSummary json.RawMessage `json:"parameter_summary,omitempty"`
	Approved         *bool           `json:"approved,omitempty"`
	Approver         string          `json:"approver,omitempty"`
	DurationMS       int64           `json:"duration_ms,omitempty"`
	ResultStatus     string          `json:"result_status,omitempty"`
	ErrorCode        string          `json:"error_code,omitempty"`
	OutputTruncated  bool            `json:"output_truncated,omitempty"`
}

type Auditor interface {
	Record(AuditRecord) error
}

type FileAuditor struct {
	path string
	mu   sync.Mutex
}

func NewFileAuditor(path string) (*FileAuditor, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create audit directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open audit log: %w", err)
	}
	_ = file.Close()
	return &FileAuditor{path: path}, nil
}

func (a *FileAuditor) Record(record AuditRecord) error {
	record.At = time.Now().UTC()
	record.ParameterSummary = sanitizeJSON(record.ParameterSummary)
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode audit record: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	file, err := os.OpenFile(a.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

type discardAuditor struct{}

func (discardAuditor) Record(AuditRecord) error { return nil }
