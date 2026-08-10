package holmesgateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var ErrSessionNotFound = errors.New("investigation session not found")
var ErrInvestigationLimit = errors.New("investigation concurrency limit reached")
var ErrRequestIDConflict = errors.New("request ID already belongs to another actor")

type Store struct {
	dir       string
	retention time.Duration
	max       int
	mu        sync.RWMutex
	sessions  map[string]*Session
	watchers  map[string]map[chan Event]struct{}
}

func NewStore(dir string, retention time.Duration, max int) (*Store, error) {
	if retention <= 0 || max <= 0 {
		return nil, errors.New("invalid session retention configuration")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	store := &Store{dir: dir, retention: retention, max: max, sessions: make(map[string]*Session), watchers: make(map[string]map[chan Event]struct{})}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) load() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("read session directory: %w", err)
	}
	cutoff := time.Now().UTC().Add(-s.retention)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if readErr != nil {
			return fmt.Errorf("read session file: %w", readErr)
		}
		var session Session
		if json.Unmarshal(data, &session) != nil || session.SessionID == "" {
			continue
		}
		if session.UpdatedAt.Before(cutoff) && !activeStatus(session.Status) {
			if removeErr := os.Remove(filepath.Join(s.dir, entry.Name())); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return fmt.Errorf("remove expired session file: %w", removeErr)
			}
			continue
		}
		recovered := false
		if activeStatus(session.Status) {
			session.Status = StatusFailed
			session.Error = &APIError{Code: "GATEWAY_RESTARTED", Message: "网关重启中断了运行中的调查，可安全重新发起；远端工具不会自动重放", Retryable: true}
			session.RunningRequestID = ""
			recovered = true
		}
		s.sessions[session.SessionID] = cloneSession(&session)
		if recovered {
			if err := s.persistLocked(session.SessionID); err != nil {
				return err
			}
		}
	}
	return s.pruneLocked()
}

func (s *Store) Create(session *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[session.SessionID]; exists {
		return errors.New("session already exists")
	}
	s.sessions[session.SessionID] = cloneSession(session)
	if err := s.persistLocked(session.SessionID); err != nil {
		delete(s.sessions, session.SessionID)
		return err
	}
	return s.pruneLocked()
}

func (s *Store) CreateInvestigation(session *Session, userLimit, globalLimit int) (*Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.sessions {
		if !existing.RequestIDs[session.RunningRequestID] {
			continue
		}
		if existing.Creator != session.Creator {
			return nil, false, ErrRequestIDConflict
		}
		return cloneSession(existing), true, nil
	}
	var userRunning, globalRunning int
	for _, existing := range s.sessions {
		if !activeStatus(existing.Status) {
			continue
		}
		globalRunning++
		if existing.Creator == session.Creator {
			userRunning++
		}
	}
	if userRunning >= userLimit || globalRunning >= globalLimit {
		return nil, false, ErrInvestigationLimit
	}
	if _, exists := s.sessions[session.SessionID]; exists {
		return nil, false, errors.New("session already exists")
	}
	s.sessions[session.SessionID] = cloneSession(session)
	if err := s.persistLocked(session.SessionID); err != nil {
		delete(s.sessions, session.SessionID)
		return nil, false, err
	}
	if err := s.pruneLocked(); err != nil {
		return nil, false, err
	}
	return cloneSession(session), false, nil
}

func (s *Store) Get(id string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, exists := s.sessions[id]
	if !exists {
		return nil, ErrSessionNotFound
	}
	return cloneSession(session), nil
}

func (s *Store) GetByRequestID(requestID, actor string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, session := range s.sessions {
		if !session.RequestIDs[requestID] {
			continue
		}
		if session.Creator != actor {
			return nil, ErrRequestIDConflict
		}
		return cloneSession(session), nil
	}
	return nil, ErrSessionNotFound
}

func (s *Store) Update(id string, mutate func(*Session) error) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateLocked(id, mutate)
}

// UpdateForRequest reserves a request ID and applies a session transition under
// the same lock. This prevents the same request ID from being accepted by two
// different sessions while concurrent handlers are running.
func (s *Store) UpdateForRequest(id, requestID string, mutate func(*Session) error) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sessionID, session := range s.sessions {
		if sessionID != id && session.RequestIDs[requestID] {
			return nil, ErrRequestIDConflict
		}
	}
	return s.updateLocked(id, mutate)
}

func (s *Store) updateLocked(id string, mutate func(*Session) error) (*Session, error) {
	session, exists := s.sessions[id]
	if !exists {
		return nil, ErrSessionNotFound
	}
	updated := cloneSession(session)
	if err := mutate(updated); err != nil {
		return nil, err
	}
	updated.UpdatedAt = time.Now().UTC()
	s.sessions[id] = updated
	if err := s.persistLocked(id); err != nil {
		s.sessions[id] = session
		return nil, err
	}
	return cloneSession(updated), nil
}

func (s *Store) AppendEvent(id, eventType string, data any) (Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, exists := s.sessions[id]
	if !exists {
		return Event{}, ErrSessionNotFound
	}
	updated := cloneSession(session)
	event := Event{ID: int64(len(updated.Events) + 1), Type: eventType, At: time.Now().UTC(), Data: raw}
	updated.Events = append(updated.Events, event)
	updated.OutputBytes += int64(len(raw))
	updated.UpdatedAt = event.At
	s.sessions[id] = updated
	if err := s.persistLocked(id); err != nil {
		s.sessions[id] = session
		return Event{}, err
	}
	for watcher := range s.watchers[id] {
		select {
		case watcher <- event:
		default:
		}
	}
	return event, nil
}

func (s *Store) Subscribe(id string) (<-chan Event, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[id]; !exists {
		return nil, nil, ErrSessionNotFound
	}
	channel := make(chan Event, 32)
	if s.watchers[id] == nil {
		s.watchers[id] = make(map[chan Event]struct{})
	}
	s.watchers[id][channel] = struct{}{}
	return channel, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.watchers[id], channel)
		close(channel)
	}, nil
}

func (s *Store) RunningCounts(user string) (int, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var userCount, total int
	for _, session := range s.sessions {
		if !activeStatus(session.Status) {
			continue
		}
		total++
		if session.Creator == user {
			userCount++
		}
	}
	return userCount, total
}

func (s *Store) persistLocked(id string) error {
	session := s.sessions[id]
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	target := filepath.Join(s.dir, id+".json")
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	if err := replaceFile(temporary, target); err != nil {
		return fmt.Errorf("replace session: %w", err)
	}
	return nil
}

func (s *Store) pruneLocked() error {
	type candidate struct {
		id string
		at time.Time
	}
	cutoff := time.Now().UTC().Add(-s.retention)
	items := make([]candidate, 0, len(s.sessions))
	for id, session := range s.sessions {
		if session.UpdatedAt.Before(cutoff) && !activeStatus(session.Status) {
			if err := s.removeSessionLocked(id); err != nil {
				return err
			}
			continue
		}
		items = append(items, candidate{id: id, at: session.UpdatedAt})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at.After(items[j].at) })
	remaining := len(items)
	for index := len(items) - 1; index >= 0 && remaining > s.max; index-- {
		item := items[index]
		if activeStatus(s.sessions[item.id].Status) {
			continue
		}
		if err := s.removeSessionLocked(item.id); err != nil {
			return err
		}
		remaining--
	}
	return nil
}

func (s *Store) removeSessionLocked(id string) error {
	target := filepath.Join(s.dir, id+".json")
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove session file: %w", err)
	}
	delete(s.sessions, id)
	return nil
}

func activeStatus(status SessionStatus) bool {
	return status == StatusCreated || status == StatusRunning || status == StatusAwaitingApproval
}

func safeMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func cloneSession(session *Session) *Session {
	data, _ := json.Marshal(session)
	var cloned Session
	_ = json.Unmarshal(data, &cloned)
	return &cloned
}
