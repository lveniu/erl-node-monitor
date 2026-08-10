package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "servers.yml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadExporterAppliesDefaults(t *testing.T) {
	path := writeConfig(t, `
defaults:
  poll_interval: 5m
servers:
  - id: external-1
    address: 127.0.0.1:22
    username: monitor
    private_key_file: test.key
    host_key_sha256: SHA256:test
`)
	cfg, err := LoadExporter(path)
	if err != nil {
		t.Fatal(err)
	}
	server := cfg.Servers[0]
	if server.PollInterval.Duration != 5*time.Minute || server.QueueThreshold != 100 || server.MemoryThresholdMBytes != 200 {
		t.Fatalf("defaults not applied: %#v", server)
	}
	if server.HostCPUAlertPercent != 80 || server.HostMemoryAlertPercent != 80 || server.VMMemoryDisplayGBytes != 10 || server.VMMemoryAlertGBytes != 15 || server.CapacityAlertPercent != 80 {
		t.Fatalf("resource thresholds not applied: %#v", server)
	}
	if server.RunQueueDisplayMultiple != 4 || server.RunQueueAlertMultiple != 16 || server.CollectionStaleAfter.Duration != 40*time.Minute {
		t.Fatalf("scheduling/freshness defaults not applied: %#v", server)
	}
	if server.ConfirmInterval.Duration != 10*time.Second || server.ConfirmAttempts != 1 {
		t.Fatalf("confirmation defaults not applied: %#v", server)
	}
	if server.NodeFailureConfirmInterval.Duration != 3*time.Minute {
		t.Fatalf("node failure confirmation default not applied: %#v", server)
	}
	if server.Name != server.ID {
		t.Fatalf("name = %q, want %q", server.Name, server.ID)
	}
}

func TestLoadExporterRejectsRepeatedConfirmation(t *testing.T) {
	path := writeConfig(t, `
defaults:
  confirm_attempts: 2
servers:
  - id: external-1
    address: 127.0.0.1:22
    username: monitor
    private_key_file: test.key
    host_key_sha256: SHA256:test
`)
	_, err := LoadExporter(path)
	if err == nil || !strings.Contains(err.Error(), "must be 1") {
		t.Fatalf("got error %v", err)
	}
}

func TestLoadExporterRejectsMissingHostVerification(t *testing.T) {
	path := writeConfig(t, `
servers:
  - id: external-1
    address: 127.0.0.1:22
    username: monitor
    private_key_file: test.key
`)
	_, err := LoadExporter(path)
	if err == nil || !strings.Contains(err.Error(), "host_key_sha256") {
		t.Fatalf("got error %v", err)
	}
}

func TestDisabledServerMayOmitCredentials(t *testing.T) {
	path := writeConfig(t, "servers:\n  - id: future\n    enabled: false\n")
	if _, err := LoadExporter(path); err != nil {
		t.Fatal(err)
	}
}

func TestLoadExporterRequiresAgentKeyFile(t *testing.T) {
	path := writeConfig(t, `
servers:
  - id: external-1
    address: 127.0.0.1:22
    username: monitor
    use_ssh_agent: true
    insecure_skip_host_key: true
`)
	_, err := LoadExporter(path)
	if err == nil || !strings.Contains(err.Error(), "ssh_key_file") {
		t.Fatalf("got error %v", err)
	}
}

func TestLoadExporterAcceptsAgentKeyFile(t *testing.T) {
	path := writeConfig(t, `
servers:
  - id: external-1
    address: 127.0.0.1:22
    username: monitor
    use_ssh_agent: true
    ssh_key_file: C:/monitor/keys/external-1.key
    insecure_skip_host_key: true
`)
	if _, err := LoadExporter(path); err != nil {
		t.Fatal(err)
	}
}

func TestLoadExporterAcceptsRemoteInstanceDirectory(t *testing.T) {
	path := writeConfig(t, `
servers:
  - id: external-1
    address: 127.0.0.1:22
    username: monitor
    use_ssh_agent: true
    ssh_key_file: C:/monitor/keys/external-1.key
    insecure_skip_host_key: true
    instance_directory: /data/server
`)
	cfg, err := LoadExporter(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Servers[0].InstanceDirectory; got != "/data/server" {
		t.Fatalf("instance_directory = %q, want /data/server", got)
	}
}

func TestLoadExporterAcceptsIgnoredAlertNodePatterns(t *testing.T) {
	path := writeConfig(t, `
alert_filters:
  ignored_nodes:
    external-1:
      - exact_node@127.0.0.1
      - "temporary_*.bk.*"
servers:
  - id: external-1
    address: 127.0.0.1:22
    username: monitor
    private_key_file: test.key
    host_key_sha256: SHA256:test
`)
	cfg, err := LoadExporter(path)
	if err != nil {
		t.Fatal(err)
	}
	patterns := cfg.AlertFilters.IgnoredNodes["external-1"]
	if len(patterns) != 2 || patterns[0] != "exact_node@127.0.0.1" || patterns[1] != "temporary_*.bk.*" {
		t.Fatalf("ignored node patterns = %#v", patterns)
	}
}

func TestLoadExporterRejectsUnknownAlertFilterServer(t *testing.T) {
	path := writeConfig(t, `
alert_filters:
  ignored_nodes:
    missing-server: ["node_1"]
servers:
  - id: external-1
    address: 127.0.0.1:22
    username: monitor
    private_key_file: test.key
    host_key_sha256: SHA256:test
`)
	_, err := LoadExporter(path)
	if err == nil || !strings.Contains(err.Error(), "unknown server id") {
		t.Fatalf("got error %v", err)
	}
}

func TestLoadExporterRejectsInvalidAlertNodePattern(t *testing.T) {
	path := writeConfig(t, `
alert_filters:
  ignored_nodes:
    external-1: ["broken_[pattern"]
servers:
  - id: external-1
    address: 127.0.0.1:22
    username: monitor
    private_key_file: test.key
    host_key_sha256: SHA256:test
`)
	_, err := LoadExporter(path)
	if err == nil || !strings.Contains(err.Error(), "invalid node pattern") {
		t.Fatalf("got error %v", err)
	}
}
