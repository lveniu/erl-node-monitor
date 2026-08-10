package config

import (
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	value, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", node.Value, err)
	}
	if value <= 0 {
		return fmt.Errorf("duration must be positive: %q", node.Value)
	}
	d.Duration = value
	return nil
}

type Defaults struct {
	PollInterval               Duration `yaml:"poll_interval"`
	ConnectTimeout             Duration `yaml:"connect_timeout"`
	CommandTimeout             Duration `yaml:"command_timeout"`
	ConfirmInterval            Duration `yaml:"confirm_interval"`
	NodeFailureConfirmInterval Duration `yaml:"node_failure_confirm_interval"`
	ConfirmAttempts            int      `yaml:"confirm_attempts"`
	CollectionStaleAfter       Duration `yaml:"collection_stale_after"`
	QueueThreshold             uint64   `yaml:"queue_threshold"`
	MemoryThresholdM           uint64   `yaml:"memory_threshold_mb"`
	HostCPUAlertPercent        uint64   `yaml:"host_cpu_alert_percent"`
	HostMemoryAlertPercent     uint64   `yaml:"host_memory_alert_percent"`
	VMMemoryDisplayGBytes      uint64   `yaml:"vm_memory_display_gb"`
	VMMemoryAlertGBytes        uint64   `yaml:"vm_memory_alert_gb"`
	CapacityAlertPercent       uint64   `yaml:"capacity_alert_percent"`
	RunQueueDisplayMultiple    uint64   `yaml:"run_queue_display_multiplier"`
	RunQueueAlertMultiple      uint64   `yaml:"run_queue_alert_multiplier"`
}

type Server struct {
	ID                         string   `yaml:"id"`
	Name                       string   `yaml:"name"`
	Enabled                    *bool    `yaml:"enabled"`
	Address                    string   `yaml:"address"`
	Username                   string   `yaml:"username"`
	PrivateKeyFile             string   `yaml:"private_key_file"`
	UseSSHAgent                bool     `yaml:"use_ssh_agent"`
	SSHKeyFile                 string   `yaml:"ssh_key_file"`
	PrivateKeyPassEnv          string   `yaml:"private_key_passphrase_env"`
	PrivateKeyPassFile         string   `yaml:"private_key_passphrase_file"`
	HostKeySHA256              string   `yaml:"host_key_sha256"`
	KnownHostsFile             string   `yaml:"known_hosts_file"`
	InsecureSkipHostKey        bool     `yaml:"insecure_skip_host_key"`
	PollInterval               Duration `yaml:"poll_interval"`
	ConnectTimeout             Duration `yaml:"connect_timeout"`
	CommandTimeout             Duration `yaml:"command_timeout"`
	ConfirmInterval            Duration `yaml:"confirm_interval"`
	NodeFailureConfirmInterval Duration `yaml:"node_failure_confirm_interval"`
	ConfirmAttempts            int      `yaml:"confirm_attempts"`
	CollectionStaleAfter       Duration `yaml:"collection_stale_after"`
	QueueThreshold             uint64   `yaml:"queue_threshold"`
	MemoryThresholdMBytes      uint64   `yaml:"memory_threshold_mb"`
	HostCPUAlertPercent        uint64   `yaml:"host_cpu_alert_percent"`
	HostMemoryAlertPercent     uint64   `yaml:"host_memory_alert_percent"`
	VMMemoryDisplayGBytes      uint64   `yaml:"vm_memory_display_gb"`
	VMMemoryAlertGBytes        uint64   `yaml:"vm_memory_alert_gb"`
	CapacityAlertPercent       uint64   `yaml:"capacity_alert_percent"`
	RunQueueDisplayMultiple    uint64   `yaml:"run_queue_display_multiplier"`
	RunQueueAlertMultiple      uint64   `yaml:"run_queue_alert_multiplier"`
	FilesystemPath             string   `yaml:"filesystem_path"`
	InstanceDirectory          string   `yaml:"instance_directory"`
}

type AlertFilters struct {
	// IgnoredNodes maps a configured server ID to exact node names or glob
	// patterns. Matching alerts remain visible in Prometheus/Alertmanager but
	// are omitted from DingTalk notifications.
	IgnoredNodes map[string][]string `yaml:"ignored_nodes"`
}

func (s Server) IsEnabled() bool {
	return s.Enabled == nil || *s.Enabled
}

type Exporter struct {
	Defaults     Defaults     `yaml:"defaults"`
	AlertFilters AlertFilters `yaml:"alert_filters"`
	Servers      []Server     `yaml:"servers"`
}

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func LoadExporter(path string) (Exporter, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Exporter{}, fmt.Errorf("read config: %w", err)
	}
	return ParseExporter(data)
}

// ParseExporter parses, defaults, and validates one complete exporter
// configuration. Hot reload uses the same path as startup so a configuration
// accepted after startup cannot behave differently from one accepted at boot.
func ParseExporter(data []byte) (Exporter, error) {
	var cfg Exporter
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Exporter{}, fmt.Errorf("parse config: %w", err)
	}
	applyDefaults(&cfg)
	if err := validate(cfg); err != nil {
		return Exporter{}, err
	}
	return cfg, nil
}

func applyDefaults(cfg *Exporter) {
	if cfg.Defaults.PollInterval.Duration == 0 {
		cfg.Defaults.PollInterval.Duration = 5 * time.Minute
	}
	if cfg.Defaults.ConnectTimeout.Duration == 0 {
		cfg.Defaults.ConnectTimeout.Duration = 10 * time.Second
	}
	if cfg.Defaults.CommandTimeout.Duration == 0 {
		cfg.Defaults.CommandTimeout.Duration = 30 * time.Second
	}
	if cfg.Defaults.ConfirmInterval.Duration == 0 {
		cfg.Defaults.ConfirmInterval.Duration = 10 * time.Second
	}
	if cfg.Defaults.NodeFailureConfirmInterval.Duration == 0 {
		cfg.Defaults.NodeFailureConfirmInterval.Duration = 3 * time.Minute
	}
	if cfg.Defaults.ConfirmAttempts == 0 {
		cfg.Defaults.ConfirmAttempts = 1
	}
	if cfg.Defaults.CollectionStaleAfter.Duration == 0 {
		cfg.Defaults.CollectionStaleAfter.Duration = 40 * time.Minute
	}
	if cfg.Defaults.QueueThreshold == 0 {
		cfg.Defaults.QueueThreshold = 100
	}
	if cfg.Defaults.MemoryThresholdM == 0 {
		cfg.Defaults.MemoryThresholdM = 200
	}
	if cfg.Defaults.HostCPUAlertPercent == 0 {
		cfg.Defaults.HostCPUAlertPercent = 80
	}
	if cfg.Defaults.HostMemoryAlertPercent == 0 {
		cfg.Defaults.HostMemoryAlertPercent = 80
	}
	if cfg.Defaults.VMMemoryDisplayGBytes == 0 {
		cfg.Defaults.VMMemoryDisplayGBytes = 10
	}
	if cfg.Defaults.VMMemoryAlertGBytes == 0 {
		cfg.Defaults.VMMemoryAlertGBytes = 15
	}
	if cfg.Defaults.CapacityAlertPercent == 0 {
		cfg.Defaults.CapacityAlertPercent = 80
	}
	if cfg.Defaults.RunQueueDisplayMultiple == 0 {
		cfg.Defaults.RunQueueDisplayMultiple = 4
	}
	if cfg.Defaults.RunQueueAlertMultiple == 0 {
		cfg.Defaults.RunQueueAlertMultiple = 16
	}
	for i := range cfg.Servers {
		s := &cfg.Servers[i]
		if s.PollInterval.Duration == 0 {
			s.PollInterval = cfg.Defaults.PollInterval
		}
		if s.ConnectTimeout.Duration == 0 {
			s.ConnectTimeout = cfg.Defaults.ConnectTimeout
		}
		if s.CommandTimeout.Duration == 0 {
			s.CommandTimeout = cfg.Defaults.CommandTimeout
		}
		if s.ConfirmInterval.Duration == 0 {
			s.ConfirmInterval = cfg.Defaults.ConfirmInterval
		}
		if s.NodeFailureConfirmInterval.Duration == 0 {
			s.NodeFailureConfirmInterval = cfg.Defaults.NodeFailureConfirmInterval
		}
		if s.ConfirmAttempts == 0 {
			s.ConfirmAttempts = cfg.Defaults.ConfirmAttempts
		}
		if s.CollectionStaleAfter.Duration == 0 {
			s.CollectionStaleAfter = cfg.Defaults.CollectionStaleAfter
		}
		if s.QueueThreshold == 0 {
			s.QueueThreshold = cfg.Defaults.QueueThreshold
		}
		if s.MemoryThresholdMBytes == 0 {
			s.MemoryThresholdMBytes = cfg.Defaults.MemoryThresholdM
		}
		if s.HostCPUAlertPercent == 0 {
			s.HostCPUAlertPercent = cfg.Defaults.HostCPUAlertPercent
		}
		if s.HostMemoryAlertPercent == 0 {
			s.HostMemoryAlertPercent = cfg.Defaults.HostMemoryAlertPercent
		}
		if s.VMMemoryDisplayGBytes == 0 {
			s.VMMemoryDisplayGBytes = cfg.Defaults.VMMemoryDisplayGBytes
		}
		if s.VMMemoryAlertGBytes == 0 {
			s.VMMemoryAlertGBytes = cfg.Defaults.VMMemoryAlertGBytes
		}
		if s.CapacityAlertPercent == 0 {
			s.CapacityAlertPercent = cfg.Defaults.CapacityAlertPercent
		}
		if s.RunQueueDisplayMultiple == 0 {
			s.RunQueueDisplayMultiple = cfg.Defaults.RunQueueDisplayMultiple
		}
		if s.RunQueueAlertMultiple == 0 {
			s.RunQueueAlertMultiple = cfg.Defaults.RunQueueAlertMultiple
		}
		if s.Name == "" {
			s.Name = s.ID
		}
		if s.FilesystemPath == "" {
			s.FilesystemPath = "/"
		}
	}
}

func validate(cfg Exporter) error {
	if len(cfg.Servers) == 0 {
		return errors.New("config has no servers")
	}
	seen := make(map[string]struct{}, len(cfg.Servers))
	for _, s := range cfg.Servers {
		if !idPattern.MatchString(s.ID) {
			return fmt.Errorf("server id %q is invalid", s.ID)
		}
		if _, exists := seen[s.ID]; exists {
			return fmt.Errorf("duplicate server id %q", s.ID)
		}
		seen[s.ID] = struct{}{}
		if !s.IsEnabled() {
			continue
		}
		if s.Address == "" || s.Username == "" || (s.PrivateKeyFile == "" && !s.UseSSHAgent) {
			return fmt.Errorf("server %q requires address, username, and private_key_file or use_ssh_agent", s.ID)
		}
		if s.UseSSHAgent && strings.TrimSpace(s.SSHKeyFile) == "" {
			return fmt.Errorf("server %q requires ssh_key_file when use_ssh_agent is true", s.ID)
		}
		if !s.UseSSHAgent && s.SSHKeyFile != "" {
			return fmt.Errorf("server %q cannot configure ssh_key_file when use_ssh_agent is false", s.ID)
		}
		if s.PrivateKeyFile == "" && (s.PrivateKeyPassEnv != "" || s.PrivateKeyPassFile != "") {
			return fmt.Errorf("server %q cannot configure a passphrase without private_key_file", s.ID)
		}
		if s.PrivateKeyPassEnv != "" && s.PrivateKeyPassFile != "" {
			return fmt.Errorf("server %q cannot set both private key passphrase env and file", s.ID)
		}
		if s.ConfirmAttempts != 1 {
			return fmt.Errorf("server %q confirm_attempts must be 1; repeated abnormal confirmation is disabled", s.ID)
		}
		if s.HostCPUAlertPercent > 100 || s.HostMemoryAlertPercent > 100 || s.CapacityAlertPercent > 100 {
			return fmt.Errorf("server %q percentage thresholds must be between 1 and 100", s.ID)
		}
		if s.VMMemoryDisplayGBytes > s.VMMemoryAlertGBytes {
			return fmt.Errorf("server %q vm_memory_display_gb must not exceed vm_memory_alert_gb", s.ID)
		}
		if s.RunQueueDisplayMultiple > s.RunQueueAlertMultiple {
			return fmt.Errorf("server %q run_queue_display_multiplier must not exceed run_queue_alert_multiplier", s.ID)
		}
		if !s.InsecureSkipHostKey && s.HostKeySHA256 == "" && s.KnownHostsFile == "" {
			return fmt.Errorf("server %q requires host_key_sha256 or known_hosts_file", s.ID)
		}
	}
	for serverID, patterns := range cfg.AlertFilters.IgnoredNodes {
		if _, exists := seen[serverID]; !exists {
			return fmt.Errorf("alert filter references unknown server id %q", serverID)
		}
		for _, pattern := range patterns {
			if strings.TrimSpace(pattern) == "" {
				return fmt.Errorf("alert filter for server %q contains an empty node pattern", serverID)
			}
			if _, err := path.Match(pattern, ""); err != nil {
				return fmt.Errorf("alert filter for server %q has invalid node pattern %q: %w", serverID, pattern, err)
			}
		}
	}
	return nil
}
