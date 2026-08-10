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

// AnthropicModel speaks the native Anthropic Messages API. GLM exposes this
// route at /api/anthropic and it has a separate quota from the OpenAI route.
type AnthropicModel struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

func NewAnthropicModel(cfg ModelConfig, apiKey string) *AnthropicModel {
	return &AnthropicModel{baseURL: strings.TrimRight(cfg.APIBase, "/"), model: cfg.Model, apiKey: apiKey, client: &http.Client{Timeout: cfg.Timeout.Duration}}
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content []anthropicPart `json:"content"`
}

type anthropicPart struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicResponse struct {
	Content []anthropicPart `json:"content"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (m *AnthropicModel) Complete(ctx context.Context, messages []chatMessage) (completion, error) {
	requestPayload := anthropicRequest{Model: m.model, MaxTokens: 4096, Tools: anthropicTools()}
	for _, message := range messages {
		switch message.Role {
		case "system":
			if requestPayload.System != "" {
				requestPayload.System += "\n\n"
			}
			requestPayload.System += message.Content
		case "tool":
			requestPayload.Messages = append(requestPayload.Messages, anthropicMessage{Role: "user", Content: []anthropicPart{{Type: "tool_result", ToolUseID: message.ToolCallID, Content: message.Content}}})
		default:
			part := anthropicPart{Type: "text", Text: message.Content}
			content := []anthropicPart{}
			if message.Content != "" {
				content = append(content, part)
			}
			for _, call := range message.ToolCalls {
				input := call.Function.Arguments
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				content = append(content, anthropicPart{Type: "tool_use", ID: call.ID, Name: call.Function.Name, Input: input})
			}
			if len(content) == 0 {
				content = []anthropicPart{{Type: "text", Text: ""}}
			}
			role := message.Role
			if role != "assistant" {
				role = "user"
			}
			requestPayload.Messages = append(requestPayload.Messages, anthropicMessage{Role: role, Content: content})
		}
	}
	payload, err := json.Marshal(requestPayload)
	if err != nil {
		return completion{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return completion{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", m.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := m.client.Do(req)
	if err != nil {
		return completion{}, fmt.Errorf("model request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return completion{}, fmt.Errorf("read model response: %w", err)
	}
	var decoded anthropicResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return completion{}, fmt.Errorf("decode model response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := http.StatusText(resp.StatusCode)
		if decoded.Error != nil && decoded.Error.Message != "" {
			message = decoded.Error.Message
		}
		return completion{}, fmt.Errorf("model returned HTTP %d: %s", resp.StatusCode, cleanModelError(message))
	}
	var result completion
	for _, part := range decoded.Content {
		switch part.Type {
		case "text":
			result.Content += part.Text
		case "tool_use":
			if part.ID == "" || part.Name == "" {
				return completion{}, errors.New("model returned an invalid tool_use block")
			}
			input := part.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			result.ToolCalls = append(result.ToolCalls, toolCall{ID: part.ID, Type: "function", Function: toolFunction{Name: part.Name, Arguments: input}})
		}
	}
	return result, nil
}

func anthropicTools() []anthropicTool {
	result := make([]anthropicTool, 0, len(agentTools()))
	for _, tool := range agentTools() {
		result = append(result, anthropicTool{Name: tool.Function.Name, Description: tool.Function.Description, InputSchema: tool.Function.Parameters})
	}
	return result
}
