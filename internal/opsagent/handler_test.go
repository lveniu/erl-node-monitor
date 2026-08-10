package opsagent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	monitorconfig "erlang-monitor/internal/config"
)

func TestHandlerUsesGrafanaIdentityAndBoundedProxyPaths(t *testing.T) {
	agent := testAgent(t, &scriptedModel{}, &scriptedShell{})
	handler, err := NewHandler(agent, "token")
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/skills", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", unauthenticated.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/?"+urlQuery(map[string]string{"_path": "/servers/resolve", "name": "srv-1"}), nil)
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("X-Grafana-User", "alice")
	resolved := httptest.NewRecorder()
	handler.ServeHTTP(resolved, request)
	if resolved.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", resolved.Code, resolved.Body.String())
	}
	if got := resolved.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("authenticated JSON cache control=%q", got)
	}
	if got := resolved.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("authenticated JSON pragma=%q", got)
	}
	var payload map[string]string
	if err := json.Unmarshal(resolved.Body.Bytes(), &payload); err != nil || payload["server_id"] != "s1" {
		t.Fatalf("unexpected resolve payload: %#v", payload)
	}

	agent.servers = map[string]monitorconfig.Server{
		"qt05-internal-debug": {ID: "qt05-internal-debug", Name: "192.168.100.33", Address: "192.168.100.33:61618"},
		"qt01-internal-debug": {ID: "qt01-internal-debug", Name: "192.168.100.23", Address: "192.168.100.23:61618"},
		"other-private":       {ID: "other-private", Name: "172.19.0.23", Address: "172.19.0.23:61618"},
		"external":            {ID: "external", Name: "101.34.55.142", Address: "101.34.55.142:43999"},
	}
	serverRequest := httptest.NewRequest(http.MethodGet, "/?"+urlQuery(map[string]string{"_path": "/servers"}), nil)
	serverRequest.Header.Set("Authorization", "Bearer token")
	serverRequest.Header.Set("X-Grafana-User", "alice")
	serverResponse := httptest.NewRecorder()
	handler.ServeHTTP(serverResponse, serverRequest)
	if serverResponse.Code != http.StatusOK {
		t.Fatalf("servers status=%d body=%s", serverResponse.Code, serverResponse.Body.String())
	}
	var serverPayload struct {
		Servers []ServerSummary `json:"servers"`
	}
	if err := json.Unmarshal(serverResponse.Body.Bytes(), &serverPayload); err != nil {
		t.Fatal(err)
	}
	if len(serverPayload.Servers) != 2 || serverPayload.Servers[0].ServerID != "qt01-internal-debug" || serverPayload.Servers[1].ServerID != "qt05-internal-debug" {
		t.Fatalf("unexpected internal server list: %#v", serverPayload.Servers)
	}

	unsafe := httptest.NewRecorder()
	unsafeRequest := httptest.NewRequest(http.MethodGet, "/?"+urlQuery(map[string]string{"_path": "/tasks/../skills"}), nil)
	unsafeRequest.Header.Set("Authorization", "Bearer token")
	unsafeRequest.Header.Set("X-Grafana-User", "alice")
	handler.ServeHTTP(unsafe, unsafeRequest)
	if unsafe.Code != http.StatusNotFound {
		t.Fatalf("unsafe proxy status=%d", unsafe.Code)
	}
}

func TestEventsDisableProxyBufferingAndExposeModelPhases(t *testing.T) {
	agent := testAgent(t, &finalModel{}, &scriptedShell{})
	task, err := agent.CreateTask(httptest.NewRequest(http.MethodGet, "/", nil).Context(), CreateTaskRequest{
		Question: "检查刷新",
		Context:  TaskContext{ServerID: "s1"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := agent.Get(task.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.Status == StatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	handler, err := NewHandler(agent, "token")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/?"+urlQuery(map[string]string{"_path": "/tasks/" + task.ID + "/events"}), nil)
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("X-Grafana-User", "alice")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("events status=%d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type=%q", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-cache, no-transform" {
		t.Fatalf("cache control=%q", got)
	}
	if got := response.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering=%q", got)
	}
	body := response.Body.String()
	if !strings.Contains(body, "event: model_started") || !strings.Contains(body, "event: model_finished") || !strings.Contains(body, "event: task_completed") {
		t.Fatalf("events body missing model phases or completion: %s", body)
	}
}

func urlQuery(values map[string]string) string {
	query := ""
	for key, value := range values {
		if query != "" {
			query += "&"
		}
		query += key + "=" + value
	}
	return query
}
