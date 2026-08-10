package config

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const validReloadConfig = `
defaults:
  poll_interval: 5m
servers:
  - id: external-1
    name: first
    address: 127.0.0.1:22
    username: monitor
    private_key_file: test.key
    host_key_sha256: SHA256:test
`

func TestHotReloaderAppliesOnlyChangedValidConfig(t *testing.T) {
	path := writeConfig(t, validReloadConfig)
	initial, err := LoadExporter(path)
	if err != nil {
		t.Fatal(err)
	}
	var applied []Exporter
	reloader, err := NewHotReloader(path, time.Second, initial, func(cfg Exporter) {
		applied = append(applied, cfg)
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	reloader.scan()
	if len(applied) != 0 || reloader.Status().Version != 1 {
		t.Fatalf("unchanged config applied=%d status=%#v", len(applied), reloader.Status())
	}

	invalid := []byte("servers:\n  - id: invalid id\n")
	if err := os.WriteFile(path, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	reloader.scan()
	status := reloader.Status()
	if len(applied) != 0 || status.Version != 1 || status.LastError == "" || status.Servers[0].Name != "first" {
		t.Fatalf("invalid config replaced active state: applied=%d status=%#v", len(applied), status)
	}

	updated := []byte(`
defaults:
  poll_interval: 7m
alert_filters:
  ignored_nodes:
    external-1: ["ignored_*"]
servers:
  - id: external-1
    name: second
    address: 127.0.0.1:22
    username: monitor
    private_key_file: test.key
    host_key_sha256: SHA256:test
`)
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	reloader.scan()
	status = reloader.Status()
	if len(applied) != 1 || status.Version != 2 || status.LastError != "" || status.Servers[0].Name != "second" {
		t.Fatalf("valid config was not applied: applied=%d status=%#v", len(applied), status)
	}
	if got := applied[0].Servers[0].PollInterval.Duration; got != 7*time.Minute {
		t.Fatalf("poll interval = %s, want 7m", got)
	}
	if got := status.IgnoredAlertNodes["external-1"]; len(got) != 1 || got[0] != "ignored_*" {
		t.Fatalf("ignored alert nodes = %#v", status.IgnoredAlertNodes)
	}
}

func TestHotReloaderStatusHandlerRejectsNonGet(t *testing.T) {
	path := writeConfig(t, validReloadConfig)
	initial, err := LoadExporter(path)
	if err != nil {
		t.Fatal(err)
	}
	reloader, err := NewHotReloader(path, time.Second, initial, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/config/status", nil)
	response := httptest.NewRecorder()
	reloader.StatusHandler().ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestHotReloaderDoesNotMissChangeBetweenStartupLoadAndInitialization(t *testing.T) {
	path := writeConfig(t, validReloadConfig)
	initial, err := LoadExporter(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(validReloadConfig, "name: first", "name: changed-before-watcher", 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	var applied Exporter
	reloader, err := NewHotReloader(path, time.Second, initial, func(cfg Exporter) { applied = cfg }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	reloader.scan()
	if len(applied.Servers) != 1 || applied.Servers[0].Name != "changed-before-watcher" {
		t.Fatalf("startup race change was missed: %#v", applied)
	}
}

func TestHotReloaderTimerAppliesChangeAndStops(t *testing.T) {
	path := writeConfig(t, validReloadConfig)
	initial, err := LoadExporter(path)
	if err != nil {
		t.Fatal(err)
	}
	applied := make(chan Exporter, 1)
	reloader, err := NewHotReloader(path, 5*time.Millisecond, initial, func(cfg Exporter) { applied <- cfg }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(validReloadConfig, "name: first", "name: timer", 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reloader.Start(ctx)
	reloader.Start(ctx)
	select {
	case cfg := <-applied:
		if cfg.Servers[0].Name != "timer" {
			t.Fatalf("timer applied %#v", cfg.Servers[0])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for timer reload")
	}
	cancel()
	reloader.Wait()
}
