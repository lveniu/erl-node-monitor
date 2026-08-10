package opsagent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Handler struct {
	agent *Agent
	token string
}

func NewHandler(agent *Agent, token string) (http.Handler, error) {
	if agent == nil || strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("ops agent handler requires agent and token")
	}
	h := &Handler{agent: agent, token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /skills", h.auth(h.skills))
	mux.HandleFunc("GET /servers", h.auth(h.servers))
	mux.HandleFunc("GET /servers/resolve", h.auth(h.resolve))
	mux.HandleFunc("POST /tasks", h.auth(h.create))
	mux.HandleFunc("GET /tasks/{id}", h.auth(h.get))
	mux.HandleFunc("GET /tasks/{id}/events", h.auth(h.events))
	mux.HandleFunc("POST /tasks/{id}/decision", h.auth(h.decision))
	mux.HandleFunc("/", compatibilityProxy(mux))
	return recoverHTTP(mux), nil
}

func (h *Handler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if provided == "" || provided != h.token || strings.TrimSpace(r.Header.Get("X-Grafana-User")) == "" {
			writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "未通过 Grafana 服务端代理认证")
			return
		}
		next(w, r)
	}
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "skills": len(h.agent.skills.List()), "tasks": h.agent.TaskCount()})
}

func (h *Handler) skills(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"skills": h.agent.skills.List()})
}

func (h *Handler) servers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"servers": h.agent.ListInternalServers()})
}

func (h *Handler) resolve(w http.ResponseWriter, r *http.Request) {
	id, display, err := h.agent.ResolveServer(r.URL.Query().Get("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "SERVER_NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"server_id": id, "display_name": display})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var request CreateTaskRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	task, err := h.agent.CreateTask(r.Context(), request, r.Header.Get("X-Grafana-User"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "TASK_REJECTED", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	task, err := h.agent.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "TASK_NOT_FOUND", err.Error())
		return
	}
	if task.Creator != r.Header.Get("X-Grafana-User") {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "任务属于其他 Grafana 用户")
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := h.agent.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "TASK_NOT_FOUND", err.Error())
		return
	}
	if task.Creator != r.Header.Get("X-Grafana-User") {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "任务属于其他 Grafana 用户")
		return
	}
	lastID, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	watcher, history, closeWatcher, err := h.agent.Subscribe(id, lastID)
	if err != nil {
		writeError(w, http.StatusNotFound, "TASK_NOT_FOUND", err.Error())
		return
	}
	defer closeWatcher()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "SSE_UNSUPPORTED", "当前响应不支持流式事件")
		return
	}
	writeEvents := func(events []Event) {
		for _, event := range events {
			data, _ := json.Marshal(event.Data)
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, data)
		}
		flusher.Flush()
	}
	writeEvents(history)
	if terminalTask(task.Status) {
		return
	}
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case event := <-watcher:
			writeEvents([]Event{event})
			if terminalEvent(event.Type) {
				return
			}
		case <-keepAlive.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Handler) decision(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.Header.Get("X-Erlang-Monitor-Role")) != "Admin" {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "Shell 执行审批需要 Grafana Admin")
		return
	}
	var request DecisionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	task, err := h.agent.Decide(r.Context(), r.PathValue("id"), request, r.Header.Get("X-Grafana-User"), "Admin")
	if err != nil {
		writeError(w, http.StatusConflict, "DECISION_REJECTED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func compatibilityProxy(mux *http.ServeMux) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := strings.TrimSpace(r.URL.Query().Get("_path"))
		if target == "" {
			http.NotFound(w, r)
			return
		}
		if !validProxyTarget(target) {
			http.NotFound(w, r)
			return
		}
		forwarded := r.Clone(r.Context())
		copied := *r.URL
		query := copied.Query()
		query.Del("_path")
		copied.Path = target
		copied.RawQuery = query.Encode()
		forwarded.URL = &copied
		mux.ServeHTTP(w, forwarded)
	}
}

var taskProxyPath = regexp.MustCompile(`^/tasks/[0-9a-f]{32}(?:/events|/decision)?$`)

func validProxyTarget(target string) bool {
	return target == "/tasks" || target == "/skills" || target == "/servers" || target == "/servers/resolve" || taskProxyPath.MatchString(target)
}

func recoverHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Agent 内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func terminalTask(status TaskStatus) bool { return status == StatusCompleted || status == StatusFailed }
func terminalEvent(event string) bool     { return event == "task_completed" || event == "task_failed" }
