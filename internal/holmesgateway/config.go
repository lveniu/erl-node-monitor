package holmesgateway

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultHolmesVersion = "0.38.1"
	DefaultMaxBodyBytes  = int64(256 * 1024)
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	value, err := time.ParseDuration(node.Value)
	if err != nil || value <= 0 {
		return fmt.Errorf("invalid positive duration %q", node.Value)
	}
	d.Duration = value
	return nil
}

type ModelConfig struct {
	DisplayName string `yaml:"display_name" json:"display_name"`
	Enabled     bool   `yaml:"enabled" json:"-"`
}

type LimitsConfig struct {
	MaxRange             Duration `yaml:"max_range"`
	InvestigationTimeout Duration `yaml:"investigation_timeout"`
	ToolTimeout          Duration `yaml:"tool_timeout"`
	MaxToolCalls         int      `yaml:"max_tool_calls"`
	MaxOutputBytes       int64    `yaml:"max_output_bytes"`
	MaxSessions          int      `yaml:"max_sessions"`
	SessionRetention     Duration `yaml:"session_retention"`
	MaxUserRunning       int      `yaml:"max_user_running"`
	MaxGlobalRunning     int      `yaml:"max_global_running"`
}

type Config struct {
	HolmesVersion string                 `yaml:"holmes_version"`
	HolmesURL     string                 `yaml:"holmes_url"`
	PrometheusURL string                 `yaml:"prometheus_url"`
	Models        map[string]ModelConfig `yaml:"models"`
	Limits        LimitsConfig           `yaml:"limits"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read Holmes gateway config: %w", err)
	}
	return ParseConfig(data)
}

func ParseConfig(data []byte) (Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse Holmes gateway config: %w", err)
	}
	applyConfigDefaults(&cfg)
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyConfigDefaults(cfg *Config) {
	if cfg.HolmesVersion == "" {
		cfg.HolmesVersion = DefaultHolmesVersion
	}
	if cfg.Limits.MaxRange.Duration == 0 {
		cfg.Limits.MaxRange.Duration = 24 * time.Hour
	}
	if cfg.Limits.InvestigationTimeout.Duration == 0 {
		cfg.Limits.InvestigationTimeout.Duration = 5 * time.Minute
	}
	if cfg.Limits.ToolTimeout.Duration == 0 {
		cfg.Limits.ToolTimeout.Duration = 45 * time.Second
	}
	if cfg.Limits.MaxToolCalls == 0 {
		cfg.Limits.MaxToolCalls = 12
	}
	if cfg.Limits.MaxOutputBytes == 0 {
		cfg.Limits.MaxOutputBytes = 256 * 1024
	}
	if cfg.Limits.MaxSessions == 0 {
		cfg.Limits.MaxSessions = 100
	}
	if cfg.Limits.SessionRetention.Duration == 0 {
		cfg.Limits.SessionRetention.Duration = 7 * 24 * time.Hour
	}
	if cfg.Limits.MaxUserRunning == 0 {
		cfg.Limits.MaxUserRunning = 1
	}
	if cfg.Limits.MaxGlobalRunning == 0 {
		cfg.Limits.MaxGlobalRunning = 2
	}
}

func validateConfig(cfg Config) error {
	if cfg.HolmesVersion != DefaultHolmesVersion {
		return fmt.Errorf("holmes_version must be pinned to %s", DefaultHolmesVersion)
	}
	if !isInternalHTTPURL(cfg.HolmesURL) {
		return errors.New("holmes_url must be an http(s) URL without credentials")
	}
	if !isInternalHTTPURL(cfg.PrometheusURL) {
		return errors.New("prometheus_url must be an http(s) URL without credentials")
	}
	if len(cfg.Models) == 0 {
		return errors.New("at least one model alias is required")
	}
	for alias, model := range cfg.Models {
		if !safeIdentifier(alias) {
			return fmt.Errorf("invalid model alias %q", alias)
		}
		if model.Enabled && strings.TrimSpace(model.DisplayName) == "" {
			return fmt.Errorf("enabled model %q requires display_name", alias)
		}
	}
	if cfg.Limits.MaxRange.Duration > 24*time.Hour {
		return errors.New("max_range cannot exceed 24h")
	}
	if cfg.Limits.ToolTimeout.Duration > 45*time.Second {
		return errors.New("tool_timeout cannot exceed 45s")
	}
	if cfg.Limits.MaxToolCalls < 1 || cfg.Limits.MaxToolCalls > 50 {
		return errors.New("max_tool_calls must be between 1 and 50")
	}
	if cfg.Limits.MaxOutputBytes < 32*1024 || cfg.Limits.MaxOutputBytes > 4*1024*1024 {
		return errors.New("max_output_bytes must be between 32 KiB and 4 MiB")
	}
	if cfg.Limits.MaxSessions < 1 || cfg.Limits.MaxSessions > 10000 {
		return errors.New("max_sessions must be between 1 and 10000")
	}
	if cfg.Limits.MaxUserRunning < 1 || cfg.Limits.MaxGlobalRunning < 1 || cfg.Limits.MaxUserRunning > cfg.Limits.MaxGlobalRunning {
		return errors.New("running limits must be positive and max_user_running cannot exceed max_global_running")
	}
	return nil
}

func isInternalHTTPURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return (strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")) && !strings.Contains(lower, "@")
}

func safeIdentifier(value string) bool {
	if value == "" || len(value) > 80 {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9' && i > 0) || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func ReadSecret(valueEnvironment, fileEnvironment string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(valueEnvironment)); value != "" {
		return value, nil
	}
	path := strings.TrimSpace(os.Getenv(fileEnvironment))
	if path == "" {
		return "", fmt.Errorf("required secret is not configured (%s or %s)", valueEnvironment, fileEnvironment)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read secret file for %s: %w", valueEnvironment, err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("secret file for %s is empty", valueEnvironment)
	}
	return value, nil
}
