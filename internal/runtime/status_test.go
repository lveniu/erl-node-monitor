package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStorePersistsAndReplacesStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "status.json")
	store := NewStore(path)
	if err := store.Update("a", ServerStatus{State: "down", LastError: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update("a", ServerStatus{State: "healthy", Nodes: 2}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "healthy" || snapshot.Servers["a"].Nodes != 2 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestSnapshotAggregatesDegraded(t *testing.T) {
	store := NewStore("")
	if err := store.Update("a", ServerStatus{State: "degraded"}); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().State; got != "degraded" {
		t.Fatalf("got %q", got)
	}
}

func TestStoreDeleteRemovesServer(t *testing.T) {
	store := NewStore("")
	if err := store.Update("a", ServerStatus{State: "healthy"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, exists := store.Snapshot().Servers["a"]; exists {
		t.Fatal("deleted server remains in snapshot")
	}
}
