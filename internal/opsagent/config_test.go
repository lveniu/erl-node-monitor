package opsagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigMaxStepsUpperBound(t *testing.T) {
	valid := Config{
		Model:     ModelConfig{Protocol: "openai", APIBase: "https://example.invalid/v1", Model: "test-model", APIKeyEnv: "TEST_API_KEY"},
		SkillsDir: "skills",
		Limits: Limits{
			MaxSteps:        1000,
			MaxCommandBytes: 4096,
			MaxOutputBytes:  65536,
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("MaxSteps=1000 rejected: %v", err)
	}
	invalid := valid
	invalid.Limits.MaxSteps = 1001
	if err := invalid.Validate(); err == nil {
		t.Fatal("MaxSteps=1001 was accepted")
	}
}

func TestLoadConfigDefaultsTaskAndCommandTimeoutsToThirtyMinutes(t *testing.T) {
	root := t.TempDir()
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.yml")
	config := `model:
  api_base: https://example.invalid/v1
  model: test-model
  api_key_env: TEST_API_KEY
skills_dir: skills
limits:
  max_steps: 1
  max_command_bytes: 4096
  max_output_bytes: 65536
`
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Limits.CommandTimeout.Duration != 30*time.Minute {
		t.Fatalf("CommandTimeout=%s, want 30m", loaded.Limits.CommandTimeout.Duration)
	}
	if loaded.Limits.TaskTimeout.Duration != 30*time.Minute {
		t.Fatalf("TaskTimeout=%s, want 30m", loaded.Limits.TaskTimeout.Duration)
	}
}
