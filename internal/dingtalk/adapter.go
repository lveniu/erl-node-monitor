package dingtalk

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	runtimestatus "erlang-monitor/internal/runtime"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	defaultMaxBodyBytes int64 = 1 << 20
	DefaultTitlePrefix        = "[Erlang服务器监控]"
)

type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
}

type Webhook struct {
	Status            string            `json:"status"`
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []Alert           `json:"alerts"`
}

type Config struct {
	WebhookURL   string
	Secret       string
	TitlePrefix  string
	AtMobiles    []string
	AtUserIDs    []string
	Timeout      time.Duration
	StatusFile   string
	MaxBodyBytes int64
	IgnoredNodes map[string][]string
}

type Metrics struct {
	received    *prometheus.CounterVec
	filtered    *prometheus.CounterVec
	sendTotal   *prometheus.CounterVec
	lastSuccess prometheus.Gauge
}

func NewMetrics(registry *prometheus.Registry) *Metrics {
	m := &Metrics{
		received:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "dingtalk_adapter_alerts_received_total", Help: "Alertmanager alerts received by status."}, []string{"status"}),
		filtered:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "dingtalk_adapter_alerts_filtered_total", Help: "DingTalk alerts omitted by configured node filters."}, []string{"server"}),
		sendTotal:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "dingtalk_adapter_send_total", Help: "DingTalk send attempts by result."}, []string{"result"}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{Name: "dingtalk_adapter_last_success_timestamp_seconds", Help: "Unix timestamp of the latest successful DingTalk send."}),
	}
	registry.MustRegister(m.received, m.filtered, m.sendTotal, m.lastSuccess)
	return m
}

type Status struct {
	State       string    `json:"state"`
	LastAttempt time.Time `json:"last_attempt,omitempty"`
	LastSuccess time.Time `json:"last_success,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
}

type Adapter struct {
	config    Config
	client    *http.Client
	metrics   *Metrics
	logger    *slog.Logger
	filterMu  sync.RWMutex
	ignored   map[string][]string
	mu        sync.RWMutex
	persistMu sync.Mutex
	status    Status
}

func NewAdapter(cfg Config, metrics *Metrics, logger *slog.Logger) (*Adapter, error) {
	if cfg.WebhookURL == "" {
		return nil, errors.New("DingTalk webhook URL is empty")
	}
	parsed, err := url.Parse(cfg.WebhookURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid DingTalk webhook URL")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" {
		return nil, errors.New("DingTalk webhook URL must use HTTPS")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = defaultMaxBodyBytes
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Adapter{
		config: cfg, client: &http.Client{Timeout: cfg.Timeout}, metrics: metrics, logger: logger,
		ignored: cloneIgnoredNodes(cfg.IgnoredNodes), status: Status{State: "starting"},
	}, nil
}

// UpdateIgnoredNodes atomically replaces the notification-only node filters.
// It is safe to call from the exporter configuration hot-reload callback.
func (a *Adapter) UpdateIgnoredNodes(patterns map[string][]string) {
	a.filterMu.Lock()
	a.ignored = cloneIgnoredNodes(patterns)
	a.filterMu.Unlock()
	patternCount := 0
	for _, values := range patterns {
		patternCount += len(values)
	}
	a.logger.Info("DingTalk node alert filters updated", "event", "dingtalk-node-filters-updated", "servers", len(patterns), "patterns", patternCount)
}

func ReadSecret(valueEnv, fileEnv string) (string, error) {
	if value := os.Getenv(valueEnv); value != "" {
		return value, nil
	}
	path := os.Getenv(fileEnv)
	if path == "" {
		return "", nil
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(value), "\r\n"), nil
}

func ReadRecipients(valueEnv, fileEnv string) ([]string, error) {
	value, err := ReadSecret(valueEnv, fileEnv)
	if err != nil {
		return nil, err
	}
	fields := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' })
	seen := make(map[string]struct{}, len(fields))
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if _, exists := seen[field]; exists {
			continue
		}
		seen[field] = struct{}{}
		result = append(result, field)
	}
	return result, nil
}

func (a *Adapter) WebhookHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body := http.MaxBytesReader(w, r.Body, a.config.MaxBodyBytes)
		defer body.Close()
		var payload Webhook
		if err := json.NewDecoder(body).Decode(&payload); err != nil {
			a.recordFailure(fmt.Errorf("decode Alertmanager webhook: %w", err))
			http.Error(w, "invalid Alertmanager webhook", http.StatusBadRequest)
			return
		}
		if len(payload.Alerts) == 0 {
			a.recordFailure(errors.New("Alertmanager webhook contains no alerts"))
			http.Error(w, "webhook contains no alerts", http.StatusBadRequest)
			return
		}
		for _, alert := range payload.Alerts {
			a.metrics.received.WithLabelValues(normalizeStatus(alert.Status)).Inc()
		}
		var filtered []filteredAlert
		payload, filtered = a.filterIgnoredNodes(payload)
		if len(filtered) > 0 {
			nodes := make([]string, 0, len(filtered))
			for _, alert := range filtered {
				a.metrics.filtered.WithLabelValues(firstNonEmpty(alert.server, "unknown")).Inc()
				nodes = append(nodes, alert.server+"/"+alert.node)
			}
			a.logger.Info("DingTalk alerts filtered by node configuration", "event", "dingtalk-alerts-filtered", "alerts", len(filtered), "nodes", strings.Join(nodes, ","))
		}
		if len(payload.Alerts) == 0 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "filtered"})
			return
		}
		title, markdown := renderMessage(a.config.TitlePrefix, payload)
		atMobiles, atUserIDs := []string(nil), []string(nil)
		mentionEnabled := hasFiringAlert(payload) && requestAllowsMention(r)
		if mentionEnabled {
			atMobiles, atUserIDs = a.config.AtMobiles, a.config.AtUserIDs
			for _, mobile := range atMobiles {
				markdown += " @" + mobile
			}
			if len(atMobiles) > 0 || len(atUserIDs) > 0 {
				markdown += "\n"
			}
		}
		if err := a.send(r.Context(), title, markdown, atMobiles, atUserIDs); err != nil {
			a.metrics.sendTotal.WithLabelValues("error").Inc()
			a.recordFailure(err)
			a.logger.Error("DingTalk delivery failed", "event", "dingtalk-send-failed", "alerts", len(payload.Alerts), "status", payload.Status, "error", err)
			http.Error(w, "DingTalk delivery failed", http.StatusBadGateway)
			return
		}
		a.metrics.sendTotal.WithLabelValues("success").Inc()
		a.metrics.lastSuccess.Set(float64(time.Now().Unix()))
		a.recordSuccess()
		a.logger.Info("DingTalk delivery succeeded", "event", "dingtalk-send-succeeded", "alerts", len(payload.Alerts), "status", payload.Status, "mention_enabled", mentionEnabled)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func (a *Adapter) HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		status := a.Status()
		w.Header().Set("Content-Type", "application/json")
		if status.State == "degraded" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(status)
	}
}

func (a *Adapter) Status() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

func (a *Adapter) send(ctx context.Context, title, markdown string, atMobiles, atUserIDs []string) error {
	payload, err := json.Marshal(map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"title": title, "text": markdown},
		"at":       map[string]any{"atMobiles": atMobiles, "atUserIds": atUserIDs, "isAtAll": false},
	})
	if err != nil {
		return fmt.Errorf("encode DingTalk request: %w", err)
	}
	endpoint, err := signedURL(a.config.WebhookURL, a.config.Secret, time.Now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("create DingTalk request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("call DingTalk webhook: %w", err)
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("read DingTalk response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("DingTalk returned HTTP %d: %s", resp.StatusCode, truncate(string(response), 300))
	}
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return fmt.Errorf("decode DingTalk response: %w", err)
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("DingTalk rejected message: code=%d message=%s", result.ErrCode, truncate(result.ErrMsg, 200))
	}
	return nil
}

func signedURL(rawURL, secret string, now time.Time) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse DingTalk webhook URL: %w", err)
	}
	if secret == "" {
		return parsed.String(), nil
	}
	timestamp := strconv.FormatInt(now.UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + secret))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	query := parsed.Query()
	query.Set("timestamp", timestamp)
	query.Set("sign", signature)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func renderMessage(prefix string, payload Webhook) (string, string) {
	status := strings.ToLower(normalizeStatus(payload.Status))
	statusText := map[string]string{"firing": "告警", "resolved": "恢复"}[status]
	if statusText == "" {
		statusText = strings.ToUpper(status)
	}
	title := strings.TrimSpace(prefix + qtTitleMarker(payload) + " " + statusText)
	if title == "" {
		title = "Erlang monitoring alert"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n\n", title)
	for i, alert := range payload.Alerts {
		summary := firstNonEmpty(alert.Annotations["summary"], alert.Annotations["description"], "无说明")
		fmt.Fprintf(&b, "#### %d. %s\n\n", i+1, summary)
		appendAnnotation(&b, "当前值", alert.Annotations["value"])
		alertStatus := normalizeStatus(alert.Status)
		if alertStatus == "unknown" {
			alertStatus = status
		}
		if alertStatus != "resolved" {
			appendAnnotation(&b, "判断条件", alert.Annotations["condition"])
			appendAnnotation(&b, "影响", alert.Annotations["impact"])
			appendAnnotation(&b, "建议处理", alert.Annotations["action"])
		}
		labels := selectedLabels(alert.Labels)
		if len(labels) > 0 {
			fmt.Fprintf(&b, "- 标签：%s\n", strings.Join(labels, "，"))
		}
		if !alert.StartsAt.IsZero() {
			fmt.Fprintf(&b, "- 开始：%s\n", alert.StartsAt.In(time.Local).Format("2006-01-02 15:04:05"))
		}
		b.WriteString("\n")
	}
	return truncate(title, 100), truncate(b.String(), 18000)
}

func qtTitleMarker(payload Webhook) string {
	serverIDs := []string{payload.CommonLabels["server"], payload.GroupLabels["server"]}
	for _, alert := range payload.Alerts {
		serverIDs = append(serverIDs, alert.Labels["server"])
	}
	for _, serverID := range serverIDs {
		if marker := qtMarker(serverID); marker != "" {
			return marker
		}
	}
	return ""
}

func qtMarker(serverID string) string {
	serverID = strings.ToLower(strings.TrimSpace(serverID))
	if serverID == "external-live-check" {
		return "【qt-01】"
	}
	if !strings.HasPrefix(serverID, "qt") {
		return ""
	}
	serverID = strings.TrimPrefix(serverID, "qt")
	serverID = strings.TrimPrefix(serverID, "-")
	if len(serverID) < 2 || serverID[0] < '0' || serverID[0] > '9' || serverID[1] < '0' || serverID[1] > '9' {
		return ""
	}
	if len(serverID) > 2 && serverID[2] != '-' {
		return ""
	}
	return "【qt-" + serverID[:2] + "】"
}

func appendAnnotation(b *strings.Builder, label, value string) {
	if strings.TrimSpace(value) != "" {
		fmt.Fprintf(b, "- %s：%s\n", label, value)
	}
}

func hasFiringAlert(payload Webhook) bool {
	for _, alert := range payload.Alerts {
		if normalizeStatus(alert.Status) == "firing" {
			return true
		}
	}
	return normalizeStatus(payload.Status) == "firing"
}

func requestAllowsMention(r *http.Request) bool {
	value := strings.TrimSpace(r.URL.Query().Get("mention"))
	if value == "" {
		return true
	}
	enabled, err := strconv.ParseBool(value)
	return err != nil || enabled
}

type filteredAlert struct {
	server string
	node   string
}

func (a *Adapter) filterIgnoredNodes(payload Webhook) (Webhook, []filteredAlert) {
	a.filterMu.RLock()
	defer a.filterMu.RUnlock()
	if len(a.ignored) == 0 {
		return payload, nil
	}
	kept := make([]Alert, 0, len(payload.Alerts))
	filtered := make([]filteredAlert, 0)
	for _, alert := range payload.Alerts {
		serverID := firstNonEmpty(alert.Labels["server"], payload.CommonLabels["server"])
		node := firstNonEmpty(alert.Labels["node"], payload.CommonLabels["node"])
		if node != "" && matchesNodePattern(a.ignored[serverID], node) {
			filtered = append(filtered, filteredAlert{server: serverID, node: node})
			continue
		}
		kept = append(kept, alert)
	}
	payload.Alerts = kept
	return payload, filtered
}

func matchesNodePattern(patterns []string, node string) bool {
	for _, pattern := range patterns {
		matched, err := path.Match(pattern, node)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func cloneIgnoredNodes(source map[string][]string) map[string][]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string][]string, len(source))
	for serverID, patterns := range source {
		result[serverID] = append([]string(nil), patterns...)
	}
	return result
}

func selectedLabels(labels map[string]string) []string {
	wanted := []string{"severity", "server", "name", "node", "pid", "registered_name", "initial_call", "current_function", "instance", "job"}
	result := make([]string, 0, len(wanted))
	for _, key := range wanted {
		if value := labels[key]; value != "" {
			result = append(result, key+"="+value)
		}
	}
	if len(result) == 0 {
		keys := make([]string, 0, len(labels))
		for key := range labels {
			if key != "alertname" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			result = append(result, key+"="+labels[key])
		}
	}
	return result
}

func (a *Adapter) recordSuccess() {
	now := time.Now().UTC()
	a.mu.Lock()
	a.status = Status{State: "healthy", LastAttempt: now, LastSuccess: now}
	status := a.status
	a.mu.Unlock()
	a.persist(status)
}

func (a *Adapter) recordFailure(err error) {
	now := time.Now().UTC()
	a.mu.Lock()
	a.status.State = "degraded"
	a.status.LastAttempt = now
	a.status.LastError = err.Error()
	status := a.status
	a.mu.Unlock()
	a.persist(status)
}

func (a *Adapter) persist(status Status) {
	if a.config.StatusFile == "" {
		return
	}
	a.persistMu.Lock()
	defer a.persistMu.Unlock()
	if err := runtimestatus.WriteJSONAtomic(a.config.StatusFile, status); err != nil {
		a.logger.Error("adapter status persistence failed", "event", "status-persist-failed", "error", err)
	}
}

func normalizeStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return "unknown"
	}
	return status
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
