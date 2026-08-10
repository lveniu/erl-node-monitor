package dingtalk

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestSignedURL(t *testing.T) {
	got, err := signedURL("https://example.test/robot/send?access_token=x", "SEC-test", time.UnixMilli(1700000000123))
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(got)
	if parsed.Query().Get("timestamp") != "1700000000123" || parsed.Query().Get("sign") == "" || parsed.Query().Get("access_token") != "x" {
		t.Fatalf("unexpected signed URL: %s", got)
	}
}

func TestDefaultTitlePrefixContainsRobotKeyword(t *testing.T) {
	title, markdown := renderMessage(DefaultTitlePrefix, Webhook{
		Status: "firing",
		Alerts: []Alert{{Status: "firing", Labels: map[string]string{"alertname": "QueueHigh"}}},
	})
	if !strings.Contains(title, "服务器") || !strings.Contains(markdown, "服务器") {
		t.Fatalf("DingTalk robot keyword missing: title=%q markdown=%q", title, markdown)
	}
}

func TestRenderMessageUsesQTMarkerAndConciseResolvedBody(t *testing.T) {
	title, markdown := renderMessage(DefaultTitlePrefix, Webhook{
		Status:       "resolved",
		CommonLabels: map[string]string{"alertname": "ErlangNodeCollectionFailed", "server": "qt05-internal-act"},
		Alerts: []Alert{{
			Status:       "resolved",
			GeneratorURL: "http://prometheus.example.test/graph?g0.expr=up",
			Labels: map[string]string{
				"alertname": "ErlangNodeCollectionFailed",
				"server":    "qt05-internal-act",
				"name":      "192.168.100.37",
				"node":      "ysmw_act_2",
			},
			Annotations: map[string]string{
				"summary":   "192.168.100.37 节点连接失败",
				"value":     "节点=ysmw_act_2",
				"condition": "节点RPC连接失败，并等待3分钟定向复核仍未恢复",
				"impact":    "该节点指标无法更新",
				"action":    "检查该BEAM节点进程",
			},
		}},
	})

	if title != "[Erlang服务器监控]【qt-05】 恢复" {
		t.Fatalf("title = %q", title)
	}
	for _, unwanted := range []string{"ErlangNodeCollectionFailed", "状态：", "事件数：", "触发条件", "判断条件：", "影响：", "建议处理：", "Prometheus", "prometheus.example.test"} {
		if strings.Contains(markdown, unwanted) {
			t.Fatalf("markdown contains %q: %s", unwanted, markdown)
		}
	}
	for _, wanted := range []string{"#### 1. 192.168.100.37 节点连接失败", "- 当前值：节点=ysmw_act_2"} {
		if !strings.Contains(markdown, wanted) {
			t.Fatalf("markdown is missing %q: %s", wanted, markdown)
		}
	}
}

func TestRenderMessageKeepsOperationalGuidanceForFiringAlert(t *testing.T) {
	_, markdown := renderMessage(DefaultTitlePrefix, Webhook{
		Status: "firing",
		Alerts: []Alert{{
			Status: "firing",
			Annotations: map[string]string{
				"summary":   "192.168.100.37 节点连接失败",
				"condition": "节点RPC连接失败，并等待3分钟定向复核仍未恢复",
				"impact":    "该节点指标无法更新",
				"action":    "检查该BEAM节点进程",
			},
		}},
	})

	for _, wanted := range []string{"- 判断条件：", "- 影响：", "- 建议处理："} {
		if !strings.Contains(markdown, wanted) {
			t.Fatalf("firing markdown is missing %q: %s", wanted, markdown)
		}
	}
}

func TestQTMarkerSupportsConfiguredServerGroups(t *testing.T) {
	for serverID, want := range map[string]string{
		"qt01-ga":             "【qt-01】",
		"qt-05-internal-act":  "【qt-05】",
		"qt07-internal-debug": "【qt-07】",
		"external-live-check": "【qt-01】",
		"external":            "",
	} {
		if got := qtMarker(serverID); got != want {
			t.Errorf("qtMarker(%q) = %q, want %q", serverID, got, want)
		}
	}
}

func TestRenderMessageUsesQT01MarkerForLegacyExternalServer(t *testing.T) {
	title, _ := renderMessage(DefaultTitlePrefix, Webhook{
		Status: "firing",
		Alerts: []Alert{{
			Status: "firing",
			Labels: map[string]string{
				"alertname": "InternalQueueHigh",
				"server":    "external-live-check",
				"name":      "101.34.55.142",
			},
		}},
	})

	if !strings.Contains(title, "【qt-01】") {
		t.Fatalf("title is missing qt-01 marker: %q", title)
	}
}

func TestWebhookSuccess(t *testing.T) {
	var received map[string]any
	ding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer ding.Close()
	statusFile := filepath.Join(t.TempDir(), "status.json")
	adapter := newTestAdapter(t, ding.URL, statusFile)
	adapter.config.AtMobiles = []string{"13800000000"}
	adapter.config.AtUserIDs = []string{"manager01"}
	payload := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"QueueHigh","server":"ext"},"annotations":{"summary":"queue > 50"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/alertmanager", strings.NewReader(payload))
	response := httptest.NewRecorder()
	adapter.WebhookHandler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	if received["msgtype"] != "markdown" || adapter.Status().State != "healthy" {
		t.Fatalf("unexpected request/status: %#v %#v", received, adapter.Status())
	}
	at, ok := received["at"].(map[string]any)
	if !ok || len(at["atMobiles"].([]any)) != 1 || len(at["atUserIds"].([]any)) != 1 {
		t.Fatalf("DingTalk recipients missing: %#v", received["at"])
	}
	markdown := received["markdown"].(map[string]any)["text"].(string)
	if !strings.Contains(markdown, "@13800000000") {
		t.Fatalf("mobile mention missing from markdown: %s", markdown)
	}
	if _, err := os.Stat(statusFile); err != nil {
		t.Fatalf("status was not persisted: %v", err)
	}
}

func TestResolvedAlertDoesNotMentionRecipients(t *testing.T) {
	if hasFiringAlert(Webhook{Status: "resolved", Alerts: []Alert{{Status: "resolved"}}}) {
		t.Fatal("resolved alert must not mention recipients")
	}
}

func TestWebhookCanSuppressMentions(t *testing.T) {
	var received map[string]any
	ding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer ding.Close()
	adapter := newTestAdapter(t, ding.URL, filepath.Join(t.TempDir(), "status.json"))
	adapter.config.AtMobiles = []string{"13800000000"}
	adapter.config.AtUserIDs = []string{"manager01"}
	payload := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"InternalQueueHigh","server":"qt01-internal-act"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/alertmanager?mention=false", strings.NewReader(payload))
	response := httptest.NewRecorder()
	adapter.WebhookHandler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	at, ok := received["at"].(map[string]any)
	if !ok || at["atMobiles"] != nil || at["atUserIds"] != nil {
		t.Fatalf("suppressed request must not carry recipients: %#v", received["at"])
	}
	markdown := received["markdown"].(map[string]any)["text"].(string)
	if strings.Contains(markdown, "@13800000000") {
		t.Fatalf("suppressed request must not mention a mobile: %s", markdown)
	}
}

func TestReadRecipientsDeduplicatesValues(t *testing.T) {
	t.Setenv("TEST_RECIPIENTS", "13800000000, 13900000000;13800000000")
	got, err := ReadRecipients("TEST_RECIPIENTS", "")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v, err %v", got, err)
	}
}

func TestWebhookFiltersConfiguredNodeWithoutCallingDingTalk(t *testing.T) {
	called := false
	ding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer ding.Close()
	adapter := newTestAdapter(t, ding.URL, filepath.Join(t.TempDir(), "status.json"))
	adapter.UpdateIgnoredNodes(map[string][]string{"qt01-ga": {"wl_temporary_*"}})
	payload := `{"status":"firing","commonLabels":{"server":"qt01-ga"},"alerts":[{"status":"firing","labels":{"alertname":"NodeDown","server":"qt01-ga","node":"wl_temporary_7@127.0.0.1"}}]}`
	response := httptest.NewRecorder()
	adapter.WebhookHandler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/alertmanager", strings.NewReader(payload)))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"filtered"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if called {
		t.Fatal("filtered alert must not call DingTalk")
	}
}

func TestWebhookKeepsAlertsThatDoNotMatchNodeFilter(t *testing.T) {
	var received map[string]any
	ding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer ding.Close()
	adapter := newTestAdapter(t, ding.URL, filepath.Join(t.TempDir(), "status.json"))
	adapter.UpdateIgnoredNodes(map[string][]string{"qt01-ga": {"wl_temporary_*"}})
	payload := `{"status":"firing","commonLabels":{"alertname":"NodeDown","server":"qt01-ga"},"alerts":[{"status":"firing","labels":{"alertname":"NodeDown","server":"qt01-ga","node":"wl_temporary_7@127.0.0.1"}},{"status":"firing","labels":{"alertname":"NodeDown","server":"qt01-ga","node":"wl_game_1@127.0.0.1"}}]}`
	response := httptest.NewRecorder()
	adapter.WebhookHandler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/alertmanager", strings.NewReader(payload)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	markdown := received["markdown"].(map[string]any)["text"].(string)
	if strings.Contains(markdown, "wl_temporary_7") || !strings.Contains(markdown, "wl_game_1") {
		t.Fatalf("unexpected filtered markdown: %s", markdown)
	}
}

func TestWebhookFailurePersistsAndDegradesHealth(t *testing.T) {
	ding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"errcode":310000,"errmsg":"invalid sign"}`)
	}))
	defer ding.Close()
	statusFile := filepath.Join(t.TempDir(), "status.json")
	adapter := newTestAdapter(t, ding.URL, statusFile)
	req := httptest.NewRequest(http.MethodPost, "/alertmanager", strings.NewReader(`{"status":"firing","alerts":[{"status":"firing"}]}`))
	response := httptest.NewRecorder()
	adapter.WebhookHandler().ServeHTTP(response, req)
	if response.Code != http.StatusBadGateway || adapter.Status().State != "degraded" {
		t.Fatalf("response=%d status=%#v", response.Code, adapter.Status())
	}
	health := httptest.NewRecorder()
	adapter.HealthHandler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status = %d", health.Code)
	}
	data, err := os.ReadFile(statusFile)
	if err != nil || !strings.Contains(string(data), "invalid sign") {
		t.Fatalf("failure state missing: %v %s", err, data)
	}
}

func newTestAdapter(t *testing.T, webhookURL, statusFile string) *Adapter {
	t.Helper()
	registry := prometheus.NewRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter, err := NewAdapter(Config{WebhookURL: webhookURL, StatusFile: statusFile}, NewMetrics(registry), logger)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}
