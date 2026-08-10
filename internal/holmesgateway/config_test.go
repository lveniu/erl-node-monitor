package holmesgateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseConfigRequiresPinnedVersionAndBoundedLimits(t *testing.T) {
	valid := []byte(`
holmes_version: "0.38.1"
holmes_url: http://127.0.0.1:20905
prometheus_url: http://127.0.0.1:20901
models:
  glm: {display_name: GLM, enabled: true}
limits: {max_range: 24h, tool_timeout: 45s}
`)
	cfg, err := ParseConfig(valid)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HolmesVersion != DefaultHolmesVersion || cfg.Limits.MaxSessions != 100 {
		t.Fatalf("defaults not applied: %#v", cfg)
	}
	for _, replacement := range []string{"holmes_version: \"0.37.0\"", "max_range: 25h", "tool_timeout: 46s"} {
		candidate := valid
		switch {
		case strings.HasPrefix(replacement, "holmes_version"):
			candidate = []byte(strings.Replace(string(valid), `holmes_version: "0.38.1"`, replacement, 1))
		case strings.HasPrefix(replacement, "max_range"):
			candidate = []byte(strings.Replace(string(valid), "max_range: 24h", replacement, 1))
		default:
			candidate = []byte(strings.Replace(string(valid), "tool_timeout: 45s", replacement, 1))
		}
		if _, err := ParseConfig(candidate); err == nil {
			t.Fatalf("expected rejection for %s", replacement)
		}
	}
}

func TestReadSecretDoesNotIncludeSecretValueInErrors(t *testing.T) {
	t.Setenv("TEST_SECRET_VALUE", "")
	t.Setenv("TEST_SECRET_FILE", filepath.Join(t.TempDir(), "missing"))
	_, err := ReadSecret("TEST_SECRET_VALUE", "TEST_SECRET_FILE")
	if err == nil || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("unexpected secret error: %v", err)
	}
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("super-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_SECRET_FILE", path)
	value, err := ReadSecret("TEST_SECRET_VALUE", "TEST_SECRET_FILE")
	if err != nil || value != "super-secret" {
		t.Fatalf("secret read failed: %q %v", value, err)
	}
}

func TestStoreRestoresInterruptedSessionAsFailed(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, time.Hour, 10)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.Create(&Session{SessionID: "session1", RequestIDs: map[string]bool{}, Status: StatusRunning, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(dir, time.Hour, 10)
	if err != nil {
		t.Fatal(err)
	}
	session, err := reloaded.Get("session1")
	if err != nil || session.Status != StatusFailed || session.Error == nil || session.Error.Code != "GATEWAY_RESTARTED" {
		t.Fatalf("interrupted session was not safely restored: %#v %v", session, err)
	}
}
