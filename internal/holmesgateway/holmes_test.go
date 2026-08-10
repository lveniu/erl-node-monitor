package holmesgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseSSESupportsNamedEventsAndRejectsOversizedInvalidData(t *testing.T) {
	input := "event: ai_message\ndata: {\"content\":\"第一步\"}\n\nevent: ai_answer_end\ndata: {\"analysis\":\"完成\"}\n\n"
	var events []HolmesEvent
	err := parseSSE(bytes.NewBufferString(input), func(event HolmesEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil || len(events) != 2 || events[0].Type != "ai_message" {
		t.Fatalf("unexpected events: %#v %v", events, err)
	}
	err = parseSSE(bytes.NewBufferString("event: bad\ndata: not-json\n\n"), func(HolmesEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "HOLMES_PROTOCOL_ERROR") {
		t.Fatalf("invalid stream was accepted: %v", err)
	}
}

func TestSanitizeJSONDropsReasoningAndCredentialFields(t *testing.T) {
	cleaned := sanitizeJSON(json.RawMessage(`{"content":"ok","reasoning":"hidden","Authorization":"Bearer secret","nested":{"api_key":"secret"}}`))
	text := string(cleaned)
	if strings.Contains(strings.ToLower(text), "reasoning") || strings.Contains(text, "secret") || !strings.Contains(text, "ok") {
		t.Fatalf("unexpected sanitized payload: %s", text)
	}
}

func TestNormalizeHolmesErrors(t *testing.T) {
	result := normalizeError(errors.New("MODEL_RATE_LIMITED: 429"), "request-1")
	if result.Code != "MODEL_RATE_LIMITED" || !result.Retryable || strings.Contains(result.Message, "429") {
		t.Fatalf("unexpected normalized error: %#v", result)
	}
}

func TestHolmesResumeRequestKeepsEmptyAskField(t *testing.T) {
	encoded, err := json.Marshal(HolmesChatRequest{
		Ask:                 "",
		Model:               "glm",
		Stream:              true,
		FrontendToolResults: []FrontendToolResult{{ToolCallID: "call-1", ToolName: "get_host_snapshot", Result: `{"status":"success"}`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"ask":""`)) || !bytes.Contains(encoded, []byte(`"tool_name":"get_host_snapshot"`)) {
		t.Fatalf("Holmes resume request omitted required fields: %s", encoded)
	}
}

func TestHTTPHolmesClientClassifiesAuthenticationRateLimitTimeoutAndBadParameters(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantMarker string
	}{
		{name: "authentication", status: http.StatusUnauthorized, wantMarker: "HOLMES_AUTH_FAILED"},
		{name: "rate limit", status: http.StatusTooManyRequests, wantMarker: "MODEL_RATE_LIMITED"},
		{name: "bad parameters", status: http.StatusBadRequest, body: `{"error":"unsupported parameter"}`, wantMarker: "HOLMES_REQUEST_REJECTED"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()
			client := NewHTTPHolmesClient(server.URL, "secret")
			err := client.StreamChat(context.Background(), HolmesChatRequest{}, func(HolmesEvent) error { return nil })
			if err == nil || !strings.Contains(err.Error(), testCase.wantMarker) {
				t.Fatalf("error = %v, want marker %s", err, testCase.wantMarker)
			}
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()
	client := NewHTTPHolmesClient(server.URL, "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := client.StreamChat(ctx, HolmesChatRequest{}, func(HolmesEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "HOLMES_TIMEOUT") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout classification failed: %v", err)
	}
}

func TestHTTPHolmesClientModelsAcceptsArrayAndHolmesEncodedArray(t *testing.T) {
	for _, response := range []string{
		`{"model_name":["glm"]}`,
		`{"model_name":"[\"glm\"]"}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-API-Key") != "secret" {
				t.Fatalf("missing Holmes API key")
			}
			_, _ = w.Write([]byte(response))
		}))
		client := NewHTTPHolmesClient(server.URL, "secret")
		models, err := client.Models(context.Background())
		server.Close()
		if err != nil || len(models) != 1 || models[0] != "glm" {
			t.Fatalf("response %s decoded as %#v, %v", response, models, err)
		}
	}
}
