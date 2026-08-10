package opsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Model interface {
	Complete(context.Context, []chatMessage) (completion, error)
}

type OpenAIModel struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

func NewOpenAIModel(cfg ModelConfig, apiKey string) *OpenAIModel {
	return &OpenAIModel{
		baseURL: strings.TrimRight(cfg.APIBase, "/"),
		model:   cfg.Model,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: cfg.Timeout.Duration},
	}
}

type chatRequest struct {
	Model             string        `json:"model"`
	Messages          []chatMessage `json:"messages"`
	Tools             []toolSpec    `json:"tools"`
	ParallelToolCalls bool          `json:"parallel_tool_calls"`
}

type toolSpec struct {
	Type     string         `json:"type"`
	Function toolDefinition `json:"function"`
}

type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (m *OpenAIModel) Complete(ctx context.Context, messages []chatMessage) (completion, error) {
	payload, err := json.Marshal(chatRequest{Model: m.model, Messages: messages, Tools: agentTools(), ParallelToolCalls: false})
	if err != nil {
		return completion{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return completion{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+m.apiKey)
	response, err := m.client.Do(request)
	if err != nil {
		return completion{}, fmt.Errorf("model request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if err != nil {
		return completion{}, fmt.Errorf("read model response: %w", err)
	}
	var decoded chatResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return completion{}, fmt.Errorf("decode model response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := http.StatusText(response.StatusCode)
		if decoded.Error != nil && decoded.Error.Message != "" {
			message = decoded.Error.Message
		}
		return completion{}, fmt.Errorf("model returned HTTP %d: %s", response.StatusCode, cleanModelError(message))
	}
	if len(decoded.Choices) == 0 {
		return completion{}, errors.New("model response contained no choices")
	}
	message := decoded.Choices[0].Message
	return completion{Content: message.Content, ToolCalls: message.ToolCalls}, nil
}

func cleanModelError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		value = value[:500]
	}
	return value
}

func agentTools() []toolSpec {
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
	}
	return []toolSpec{
		{Type: "function", Function: toolDefinition{Name: "list_skills", Description: "列出当前运维 Agent 可加载的专项 Skill。开始分析时先调用。", Parameters: object(map[string]any{})}},
		{Type: "function", Function: toolDefinition{Name: "load_skill", Description: "加载一个 Skill 的完整运维流程。只允许加载 list_skills 返回的名称。", Parameters: object(map[string]any{
			"name": map[string]any{"type": "string", "description": "Skill 名称"},
		}, "name")}},
		{Type: "function", Function: toolDefinition{Name: "shell_exec", Description: "按已加载 Skill 的职责，提出一条只在当前固定 192.168.100.* 内网服务器执行的 Shell 命令；未加载 Skill 不得调用。完全由 ls、grep、ps、cd、head、tail、df、find 组成的安全只读组合，以及 erlang-ops-analysis 中通过后端固定表达式白名单的 ./mgectl exprs 会自动执行；任意 Erlang 表达式仍拒绝，GC、mgectl start/stop/restart、清理等允许动作仍等待 Grafana Admin 逐条审批。一次只提交一条命令。", Parameters: object(map[string]any{
			"target":          map[string]any{"type": "string", "enum": []string{"current-server"}},
			"command":         map[string]any{"type": "string", "description": "完整 Shell 命令"},
			"reason":          map[string]any{"type": "string", "description": "执行目的和预期结果"},
			"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 120},
		}, "target", "command", "reason")}},
	}
}
