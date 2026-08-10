package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type ServerStatus struct {
	State       string            `json:"state"`
	LastAttempt time.Time         `json:"last_attempt,omitempty"`
	LastSuccess time.Time         `json:"last_success,omitempty"`
	LastError   string            `json:"last_error,omitempty"`
	Nodes       int               `json:"nodes"`
	FailedNodes int               `json:"failed_nodes"`
	NodeErrors  map[string]string `json:"node_errors,omitempty"`
	HostError   string            `json:"host_error,omitempty"`
	DurationMS  int64             `json:"duration_ms"`
}

type Snapshot struct {
	State     string                  `json:"state"`
	UpdatedAt time.Time               `json:"updated_at"`
	Servers   map[string]ServerStatus `json:"servers"`
}

type Store struct {
	mu        sync.RWMutex
	persistMu sync.Mutex
	path      string
	servers   map[string]ServerStatus
	updated   time.Time
}

func NewStore(path string) *Store {
	return &Store{path: path, servers: make(map[string]ServerStatus)}
}

func (s *Store) Update(server string, status ServerStatus) error {
	s.mu.Lock()
	s.servers[server] = status
	s.updated = time.Now().UTC()
	s.mu.Unlock()
	if s.path == "" {
		return nil
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	snapshot := s.Snapshot()
	return WriteJSONAtomic(s.path, snapshot)
}

func (s *Store) Delete(server string) error {
	s.mu.Lock()
	delete(s.servers, server)
	s.updated = time.Now().UTC()
	s.mu.Unlock()
	if s.path == "" {
		return nil
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	return WriteJSONAtomic(s.path, s.Snapshot())
}

func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

func (s *Store) snapshotLocked() Snapshot {
	copyServers := make(map[string]ServerStatus, len(s.servers))
	healthy, degraded, down := 0, 0, 0
	for id, status := range s.servers {
		copyServers[id] = status
		switch status.State {
		case "healthy":
			healthy++
		case "degraded":
			degraded++
		default:
			down++
		}
	}
	state := "starting"
	if healthy > 0 && degraded == 0 && down == 0 {
		state = "healthy"
	} else if healthy+degraded > 0 {
		state = "degraded"
	} else if down > 0 {
		state = "down"
	}
	return Snapshot{State: state, UpdatedAt: s.updated, Servers: copyServers}
}

func WriteJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create status directory: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode status: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".status-*.tmp")
	if err != nil {
		return fmt.Errorf("create status temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write status temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close status temp file: %w", err)
	}
	if err := replaceFile(tmpName, path); err != nil {
		return fmt.Errorf("replace status file: %w", err)
	}
	return nil
}
