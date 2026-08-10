package opsagent

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	value, err := time.ParseDuration(node.Value)
	if err != nil || value <= 0 {
		return fmt.Errorf("invalid positive duration %q", node.Value)
	}
	d.Duration = value
	return nil
}

type ModelConfig struct {
	Protocol  string   `yaml:"protocol"`
	APIBase   string   `yaml:"api_base"`
	Model     string   `yaml:"model"`
	APIKeyEnv string   `yaml:"api_key_env"`
	Timeout   Duration `yaml:"timeout"`
}

type Limits struct {
	MaxSteps        int      `yaml:"max_steps"`
	TaskTimeout     Duration `yaml:"task_timeout"`
	CommandTimeout  Duration `yaml:"command_timeout"`
	MaxCommandBytes int      `yaml:"max_command_bytes"`
	MaxOutputBytes  int      `yaml:"max_output_bytes"`
	TaskTTL         Duration `yaml:"task_ttl"`
}

type Config struct {
	Model           ModelConfig `yaml:"model"`
	SkillsDir       string      `yaml:"skills_dir"`
	LocalWorkdir    string      `yaml:"local_workdir"`
	AllowLocalShell bool        `yaml:"allow_local_shell"`
	Limits          Limits      `yaml:"limits"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read ops agent config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse ops agent config: %w", err)
	}
	if cfg.Model.Timeout.Duration == 0 {
		cfg.Model.Timeout.Duration = 90 * time.Second
	}
	if strings.TrimSpace(cfg.Model.Protocol) == "" {
		cfg.Model.Protocol = "openai"
	}
	if cfg.Limits.MaxSteps == 0 {
		cfg.Limits.MaxSteps = 1000
	}
	if cfg.Limits.TaskTimeout.Duration == 0 {
		cfg.Limits.TaskTimeout.Duration = 30 * time.Minute
	}
	if cfg.Limits.CommandTimeout.Duration == 0 {
		cfg.Limits.CommandTimeout.Duration = 30 * time.Minute
	}
	if cfg.Limits.MaxCommandBytes == 0 {
		cfg.Limits.MaxCommandBytes = 4096
	}
	if cfg.Limits.MaxOutputBytes == 0 {
		cfg.Limits.MaxOutputBytes = 64 * 1024
	}
	if cfg.Limits.TaskTTL.Duration == 0 {
		cfg.Limits.TaskTTL.Duration = 30 * time.Minute
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	base := filepath.Dir(path)
	if !filepath.IsAbs(cfg.SkillsDir) {
		cfg.SkillsDir = filepath.Clean(filepath.Join(base, cfg.SkillsDir))
	}
	if cfg.LocalWorkdir != "" && !filepath.IsAbs(cfg.LocalWorkdir) {
		cfg.LocalWorkdir = filepath.Clean(filepath.Join(base, cfg.LocalWorkdir))
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Model.Protocol != "openai" && c.Model.Protocol != "anthropic" {
		return errors.New("model.protocol must be openai or anthropic")
	}
	parsed, err := url.Parse(strings.TrimSpace(c.Model.APIBase))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("model.api_base must be an absolute http(s) URL")
	}
	if strings.TrimSpace(c.Model.Model) == "" {
		return errors.New("model.model is required")
	}
	if strings.TrimSpace(c.Model.APIKeyEnv) == "" {
		return errors.New("model.api_key_env is required")
	}
	if strings.TrimSpace(c.SkillsDir) == "" {
		return errors.New("skills_dir is required")
	}
	if c.Limits.MaxSteps < 1 || c.Limits.MaxSteps > 1000 {
		return errors.New("limits.max_steps must be between 1 and 1000")
	}
	if c.Limits.MaxCommandBytes < 128 || c.Limits.MaxCommandBytes > 16384 {
		return errors.New("limits.max_command_bytes must be between 128 and 16384")
	}
	if c.Limits.MaxOutputBytes < 1024 || c.Limits.MaxOutputBytes > 1024*1024 {
		return errors.New("limits.max_output_bytes must be between 1024 and 1048576")
	}
	return nil
}
