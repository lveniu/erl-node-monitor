package config

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"sync"
	"time"
)

const DefaultReloadInterval = 5 * time.Second

type ApplyFunc func(Exporter)

type ServerSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Address      string `json:"address"`
	Enabled      bool   `json:"enabled"`
	PollInterval string `json:"poll_interval"`
}

type ReloadStatus struct {
	Version           uint64              `json:"version"`
	LoadedAt          time.Time           `json:"loaded_at"`
	LastCheckedAt     time.Time           `json:"last_checked_at"`
	LastError         string              `json:"last_error,omitempty"`
	LastErrorAt       time.Time           `json:"last_error_at,omitempty"`
	IgnoredAlertNodes map[string][]string `json:"ignored_alert_nodes,omitempty"`
	Servers           []ServerSummary     `json:"servers"`
}

// HotReloader owns file change detection and validation. A rejected file never
// replaces the last known-good configuration supplied to ApplyFunc.
type HotReloader struct {
	path     string
	interval time.Duration
	apply    ApplyFunc
	logger   *slog.Logger

	mu       sync.RWMutex
	seenHash [sha256.Size]byte
	status   ReloadStatus
	start    sync.Once
	wg       sync.WaitGroup
}

func NewHotReloader(path string, interval time.Duration, initial Exporter, apply ApplyFunc, logger *slog.Logger) (*HotReloader, error) {
	if interval <= 0 {
		return nil, fmt.Errorf("config reload interval must be positive")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("initialize config hot reload: %w", err)
	}
	now := time.Now().UTC()
	seenHash := [sha256.Size]byte{}
	if current, parseErr := ParseExporter(data); parseErr == nil && reflect.DeepEqual(current, initial) {
		seenHash = sha256.Sum256(data)
	}
	return &HotReloader{
		path: path, interval: interval, apply: apply, logger: logger,
		seenHash: seenHash,
		status: ReloadStatus{
			Version: 1, LoadedAt: now, LastCheckedAt: now,
			IgnoredAlertNodes: clonePatterns(initial.AlertFilters.IgnoredNodes),
			Servers:           summarizeServers(initial),
		},
	}, nil
}

func (r *HotReloader) Start(ctx context.Context) {
	r.start.Do(func() {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			ticker := time.NewTicker(r.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					r.scan()
				}
			}
		}()
	})
}

func (r *HotReloader) Wait() { r.wg.Wait() }

func (r *HotReloader) Status() ReloadStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	status := r.status
	status.Servers = append([]ServerSummary(nil), r.status.Servers...)
	status.IgnoredAlertNodes = clonePatterns(r.status.IgnoredAlertNodes)
	return status
}

func (r *HotReloader) StatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.Status())
	}
}

func (r *HotReloader) scan() {
	now := time.Now().UTC()
	data, err := os.ReadFile(r.path)
	if err != nil {
		r.reject(now, fmt.Errorf("read config: %w", err), nil)
		return
	}
	digest := sha256.Sum256(data)
	r.mu.Lock()
	r.status.LastCheckedAt = now
	if digest == r.seenHash {
		r.mu.Unlock()
		return
	}
	r.seenHash = digest
	r.mu.Unlock()

	cfg, err := ParseExporter(data)
	if err != nil {
		r.reject(now, err, &digest)
		return
	}
	if r.apply != nil {
		r.apply(cfg)
	}

	r.mu.Lock()
	r.status.Version++
	r.status.LoadedAt = now
	r.status.LastCheckedAt = now
	r.status.LastError = ""
	r.status.LastErrorAt = time.Time{}
	r.status.IgnoredAlertNodes = clonePatterns(cfg.AlertFilters.IgnoredNodes)
	r.status.Servers = summarizeServers(cfg)
	version := r.status.Version
	r.mu.Unlock()
	r.logger.Info("configuration hot reload succeeded", "event", "config-reload-succeeded", "version", version, "servers", len(cfg.Servers))
}

func (r *HotReloader) reject(now time.Time, err error, digest *[sha256.Size]byte) {
	r.mu.Lock()
	r.status.LastCheckedAt = now
	if digest != nil {
		r.seenHash = *digest
	}
	changed := r.status.LastError != err.Error()
	r.status.LastError = err.Error()
	r.status.LastErrorAt = now
	r.mu.Unlock()
	if changed {
		r.logger.Error("configuration hot reload rejected; keeping last known-good configuration", "event", "config-reload-rejected", "error", err)
	}
}

func summarizeServers(cfg Exporter) []ServerSummary {
	servers := make([]ServerSummary, 0, len(cfg.Servers))
	for _, server := range cfg.Servers {
		servers = append(servers, ServerSummary{
			ID: server.ID, Name: server.Name, Address: server.Address,
			Enabled: server.IsEnabled(), PollInterval: server.PollInterval.Duration.String(),
		})
	}
	return servers
}

func clonePatterns(source map[string][]string) map[string][]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string][]string, len(source))
	for serverID, patterns := range source {
		result[serverID] = append([]string(nil), patterns...)
	}
	return result
}
