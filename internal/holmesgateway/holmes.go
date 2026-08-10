package holmesgateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type FrontendTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Mode        string         `json:"mode"`
}

type FrontendToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Result     string `json:"result"`
}

type HolmesChatRequest struct {
	Ask                    string               `json:"ask"`
	Model                  string               `json:"model"`
	Stream                 bool                 `json:"stream"`
	EnableToolApproval     bool                 `json:"enable_tool_approval"`
	ConversationHistory    json.RawMessage      `json:"conversation_history,omitempty"`
	AdditionalSystemPrompt string               `json:"additional_system_prompt,omitempty"`
	FrontendTools          []FrontendTool       `json:"frontend_tools"`
	FrontendToolResults    []FrontendToolResult `json:"frontend_tool_results,omitempty"`
}

type HolmesEvent struct {
	Type string
	Data json.RawMessage
}

type HolmesClient interface {
	StreamChat(context.Context, HolmesChatRequest, func(HolmesEvent) error) error
	Models(context.Context) ([]string, error)
	Health(context.Context) error
}

type HTTPHolmesClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewHTTPHolmesClient(baseURL, apiKey string) *HTTPHolmesClient {
	return &HTTPHolmesClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 0},
	}
}

func (c *HTTPHolmesClient) StreamChat(ctx context.Context, request HolmesChatRequest, consume func(HolmesEvent) error) error {
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode Holmes request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	httpRequest.Header.Set("X-API-Key", c.apiKey)
	response, err := c.client.Do(httpRequest)
	if err != nil {
		return classifyHolmesTransportError(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		limited, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return classifyHolmesHTTPError(response.StatusCode, string(limited))
	}
	if !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		return errors.New("HOLMES_PROTOCOL_ERROR: Holmes did not return an SSE stream")
	}
	return parseSSE(response.Body, consume)
}

func (c *HTTPHolmesClient) Models(ctx context.Context) ([]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/model", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-API-Key", c.apiKey)
	response, err := c.client.Do(request)
	if err != nil {
		return nil, classifyHolmesTransportError(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, classifyHolmesHTTPError(response.StatusCode, "")
	}
	var payload struct {
		Models json.RawMessage `json:"model_name"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("HOLMES_PROTOCOL_ERROR: decode model list: %w", err)
	}
	var models []string
	if err := json.Unmarshal(payload.Models, &models); err == nil {
		return models, nil
	}
	var encodedModels string
	if err := json.Unmarshal(payload.Models, &encodedModels); err != nil {
		return nil, fmt.Errorf("HOLMES_PROTOCOL_ERROR: model_name is neither an array nor an encoded array: %w", err)
	}
	if err := json.Unmarshal([]byte(encodedModels), &models); err != nil {
		return nil, fmt.Errorf("HOLMES_PROTOCOL_ERROR: decode encoded model list: %w", err)
	}
	return models, nil
}

func (c *HTTPHolmesClient) Health(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return classifyHolmesTransportError(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return classifyHolmesHTTPError(response.StatusCode, "")
	}
	return nil
}

func parseSSE(reader io.Reader, consume func(HolmesEvent) error) error {
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	var eventType string
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			eventType = ""
			return nil
		}
		raw := json.RawMessage(strings.TrimSpace(data.String()))
		if !json.Valid(raw) {
			return errors.New("HOLMES_PROTOCOL_ERROR: invalid JSON in SSE event")
		}
		typeName := strings.TrimSpace(eventType)
		if typeName == "" {
			var envelope struct {
				Event string `json:"event"`
				Type  string `json:"type"`
			}
			_ = json.Unmarshal(raw, &envelope)
			typeName = envelope.Event
			if typeName == "" {
				typeName = envelope.Type
			}
		}
		data.Reset()
		eventType = ""
		if typeName == "" {
			return nil
		}
		return consume(HolmesEvent{Type: typeName, Data: raw})
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventType = value
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("HOLMES_STREAM_ERROR: %w", err)
	}
	return flush()
}

func classifyHolmesTransportError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("HOLMES_TIMEOUT: %w", err)
	}
	return fmt.Errorf("HOLMES_UNAVAILABLE: %w", err)
}

func classifyHolmesHTTPError(status int, body string) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return errors.New("HOLMES_AUTH_FAILED: Holmes rejected gateway authentication")
	case http.StatusTooManyRequests:
		return errors.New("MODEL_RATE_LIMITED: model request was rate limited")
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return errors.New("HOLMES_TIMEOUT: Holmes request timed out")
	case http.StatusBadRequest:
		return fmt.Errorf("HOLMES_REQUEST_REJECTED: %s", cleanUpstreamMessage(body))
	default:
		return fmt.Errorf("HOLMES_UNAVAILABLE: Holmes returned HTTP %d", status)
	}
}

func cleanUpstreamMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 400 {
		value = value[:400] + "..."
	}
	for _, marker := range []string{"api_key", "api-key", "authorization", "bearer ", "cookie"} {
		if strings.Contains(strings.ToLower(value), marker) {
			return "upstream request was rejected (sensitive detail removed)"
		}
	}
	return value
}

func healthContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}
