package holmesgateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStorePersistsRestartFailureAndDeletesPrunedFiles(t *testing.T) {
	directory := t.TempDir()
	store, err := NewStore(directory, time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	running := testStoredSession("running", "request-running", StatusRunning, now)
	if err := store.Create(running); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(directory, time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := reloaded.Get("running")
	if err != nil || recovered.Status != StatusFailed || recovered.Error == nil || recovered.Error.Code != "GATEWAY_RESTARTED" {
		t.Fatalf("running session was not recovered safely: %#v err=%v", recovered, err)
	}
	persisted, err := os.ReadFile(filepath.Join(directory, "running.json"))
	if err != nil {
		t.Fatal(err)
	}
	var disk Session
	if json.Unmarshal(persisted, &disk) != nil || disk.Status != StatusFailed {
		t.Fatalf("recovered state was not persisted: %s", persisted)
	}

	completed := testStoredSession("newer", "request-newer", StatusCompleted, now.Add(time.Second))
	if err := reloaded.Create(completed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "running.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pruned session file still exists: %v", err)
	}
}

func TestStoreAtomicallyDeduplicatesCreatesAndEnforcesConcurrency(t *testing.T) {
	store, err := NewStore(t.TempDir(), time.Hour, 20)
	if err != nil {
		t.Fatal(err)
	}
	var created atomic.Int32
	var failures atomic.Int32
	var wait sync.WaitGroup
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			session := testStoredSession(fmt.Sprintf("session-%d", index), "same-request", StatusCreated, time.Now().UTC())
			_, idempotent, createErr := store.CreateInvestigation(session, 1, 2)
			if createErr != nil {
				failures.Add(1)
				return
			}
			if !idempotent {
				created.Add(1)
			}
		}(index)
	}
	wait.Wait()
	if created.Load() != 1 || failures.Load() != 0 {
		t.Fatalf("atomic create accounting mismatch: created=%d failures=%d", created.Load(), failures.Load())
	}

	other := testStoredSession("other", "other-request", StatusCreated, time.Now().UTC())
	if _, _, err := store.CreateInvestigation(other, 1, 2); !errors.Is(err, ErrInvestigationLimit) {
		t.Fatalf("per-user concurrency limit was not enforced atomically: %v", err)
	}
}

func TestStoreUpdateForRequestRejectsCrossSessionReuse(t *testing.T) {
	store, err := NewStore(t.TempDir(), time.Hour, 20)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, session := range []*Session{
		testStoredSession("first", "shared-request", StatusCompleted, now),
		testStoredSession("second", "second-request", StatusCompleted, now),
	} {
		if err := store.Create(session); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.UpdateForRequest("second", "shared-request", func(*Session) error { return nil }); !errors.Is(err, ErrRequestIDConflict) {
		t.Fatalf("cross-session request ID reuse was accepted: %v", err)
	}
}

func testStoredSession(id, requestID string, status SessionStatus, at time.Time) *Session {
	return &Session{
		SessionID: id, RequestIDs: map[string]bool{requestID: true}, Creator: "alice", GrafanaRole: "Editor",
		Status: status, Model: "glm", ToolResults: map[string]string{}, ToolDecisions: map[string]bool{},
		RunningRequestID: requestID, CreatedAt: at, UpdatedAt: at,
	}
}
