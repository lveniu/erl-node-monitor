package opsagent

import (
	"strings"
	"testing"

	monitorconfig "erlang-monitor/internal/config"
)

const (
	analysisNodeInfoCommand        = `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:i()"`
	analysisMemoryCommand          = `cd -- '/data/wl_debug_1' && ./mgectl exprs "erlang:memory()"`
	analysisLargeProcessesCommand  = `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:get_memory(209715200)"`
	analysisHeapCommand            = `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:get_heap()"`
	analysisTotalHeapCommand       = `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:get_theap()"`
	analysisETSCommand             = `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:get_ets_memory(megabyte)"`
	analysisMnesiaCommand          = `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:get_mnesia_table_memory()"`
	analysisAtomInfoCommand        = `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:atom_info()"`
	analysisSnapshotCommand        = `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:monitor_snapshot()"`
	analysisSnapshotOptionsCommand = `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:monitor_snapshot(#{memory_threshold_bytes=>209715200,message_queue_threshold=>100})"`
	analysisHotspotsCommand        = `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:monitor_scheduler_hotspots(3000,10)"`
	analysisProcessDetailCommand   = `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:monitor_process_detail(erlang:list_to_pid(\"<0.123.0>\"))"`
	analysisProcessHeapInfoCommand = `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:info(erlang:list_to_pid(\"<0.123.0>\"),total_heap_size)"`
	analysisRoleCountsCommand      = `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:monitor_role_counts()"`
)

func TestValidateServerShellRequestMGCTLBoundaries(t *testing.T) {
	internal := monitorconfig.Server{ID: "qt01-internal-debug", Address: "192.168.100.23:61618", InstanceDirectory: "/data"}
	internalWithoutLabel := monitorconfig.Server{ID: "server-23", Address: "192.168.100.23:61618", InstanceDirectory: "/data"}
	external := monitorconfig.Server{ID: "external-live-check", Address: "101.34.55.142:43999", InstanceDirectory: "/data/server"}
	publicInternalID := monitorconfig.Server{ID: "qt01-internal-debug", Address: "203.0.113.10:61618", InstanceDirectory: "/data"}
	otherPrivate := monitorconfig.Server{ID: "qt01-internal-debug", Address: "10.0.0.23:61618", InstanceDirectory: "/data"}
	nearbySubnet := monitorconfig.Server{ID: "qt01-internal-debug", Address: "192.168.101.23:61618", InstanceDirectory: "/data"}
	hostname := monitorconfig.Server{ID: "qt01-internal-debug", Address: "ops.internal:61618", InstanceDirectory: "/data"}
	tests := []struct {
		name    string
		server  monitorconfig.Server
		request ShellRequest
		wantErr bool
	}{
		{name: "internal garbage collect", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "erlang:garbage_collect()"`}},
		{name: "internal start", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl start`}},
		{name: "internal stop", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl stop`}},
		{name: "internal restart", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl restart`}},
		{name: "internal node info", server: internal, request: ShellRequest{Target: "current-server", Command: analysisNodeInfoCommand}},
		{name: "internal memory categories", server: internal, request: ShellRequest{Target: "current-server", Command: analysisMemoryCommand}},
		{name: "internal large processes", server: internal, request: ShellRequest{Target: "current-server", Command: analysisLargeProcessesCommand}},
		{name: "internal heap", server: internal, request: ShellRequest{Target: "current-server", Command: analysisHeapCommand}},
		{name: "internal total heap", server: internal, request: ShellRequest{Target: "current-server", Command: analysisTotalHeapCommand}},
		{name: "internal ETS", server: internal, request: ShellRequest{Target: "current-server", Command: analysisETSCommand}},
		{name: "internal Mnesia", server: internal, request: ShellRequest{Target: "current-server", Command: analysisMnesiaCommand}},
		{name: "internal atom info", server: internal, request: ShellRequest{Target: "current-server", Command: analysisAtomInfoCommand}},
		{name: "internal analysis snapshot", server: internal, request: ShellRequest{Target: "current-server", Command: analysisSnapshotCommand}},
		{name: "internal analysis snapshot options", server: internal, request: ShellRequest{Target: "current-server", Command: analysisSnapshotOptionsCommand}},
		{name: "internal scheduler hotspots", server: internal, request: ShellRequest{Target: "current-server", Command: analysisHotspotsCommand}},
		{name: "internal process detail", server: internal, request: ShellRequest{Target: "current-server", Command: analysisProcessDetailCommand}},
		{name: "internal bounded process info", server: internal, request: ShellRequest{Target: "current-server", Command: analysisProcessHeapInfoCommand}},
		{name: "internal role counts", server: internal, request: ShellRequest{Target: "current-server", Command: analysisRoleCountsCommand}},
		{name: "arbitrary Erlang expression rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "os:cmd(\"id\")"`}, wantErr: true},
		{name: "unsafe mlib function rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:all_atoms()"`}, wantErr: true},
		{name: "scheduler window out of range rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:monitor_scheduler_hotspots(10001,10)"`}, wantErr: true},
		{name: "snapshot threshold out of range rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:monitor_snapshot(#{memory_threshold_bytes=>1099511627777,message_queue_threshold=>100})"`}, wantErr: true},
		{name: "remote process detail rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:monitor_process_detail(erlang:list_to_pid(\"<1.123.0>\"))"`}, wantErr: true},
		{name: "remote process info rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:info(erlang:list_to_pid(\"<1.123.0>\"),memory)"`}, wantErr: true},
		{name: "process messages rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:info(erlang:list_to_pid(\"<0.123.0>\"),messages)"`}, wantErr: true},
		{name: "process dictionary rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:info(erlang:list_to_pid(\"<0.123.0>\"),dictionary)"`}, wantErr: true},
		{name: "process all info rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:info(erlang:list_to_pid(\"<0.123.0>\"),all)"`}, wantErr: true},
		{name: "process raw binary info rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:info(erlang:list_to_pid(\"<0.123.0>\"),binary)"`}, wantErr: true},
		{name: "process memory threshold too low rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:get_memory(0)"`}, wantErr: true},
		{name: "arbitrary ETS divisor rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:get_ets_memory(1)"`}, wantErr: true},
		{name: "full process info rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:info(self())"`}, wantErr: true},
		{name: "garbage collect helper rejected from analysis allowlist", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:gc(1048576)"`}, wantErr: true},
		{name: "exact subnet does not depend on server id label", server: internalWithoutLabel, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl restart`}},
		{name: "external rejected", server: external, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/server/wl_release_1' && ./mgectl restart`}, wantErr: true},
		{name: "external start rejected", server: external, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/server/wl_release_1' && ./mgectl start`}, wantErr: true},
		{name: "external stop rejected", server: external, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/server/wl_release_1' && ./mgectl stop`}, wantErr: true},
		{name: "public address rejected", server: publicInternalID, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl restart`}, wantErr: true},
		{name: "other private range rejected", server: otherPrivate, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl restart`}, wantErr: true},
		{name: "neighboring private subnet rejected", server: nearbySubnet, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl restart`}, wantErr: true},
		{name: "hostname rejected", server: hostname, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl restart`}, wantErr: true},
		{name: "root directory rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data' && ./mgectl restart`}, wantErr: true},
		{name: "sibling directory rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data2/wl_debug_1' && ./mgectl restart`}, wantErr: true},
		{name: "other subcommand rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgectl reload`}, wantErr: true},
		{name: "legacy mgect garbage collect rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `cd -- '/data/wl_debug_1' && ./mgect exprs "erlang:garbage_collect()"`}, wantErr: true},
		{name: "systemctl restart is not allowlisted", server: internal, request: ShellRequest{Target: "current-server", Command: `systemctl restart game-server`}, wantErr: true},
		{name: "systemctl start is not allowlisted", server: internal, request: ShellRequest{Target: "current-server", Command: `systemctl start game-server`}, wantErr: true},
		{name: "systemctl stop is disabled", server: internal, request: ShellRequest{Target: "current-server", Command: `systemctl stop game-server`}, wantErr: true},
		{name: "external stop rejected by subnet", server: external, request: ShellRequest{Target: "current-server", Command: `service game-server stop`}, wantErr: true},
		{name: "monitor host rejected", server: internal, request: ShellRequest{Target: "monitor-host", Command: `cd -- '/data/wl_debug_1' && ./mgectl restart`}, wantErr: true},
		{name: "ordinary internal read only command allowed", server: internal, request: ShellRequest{Target: "current-server", Command: "uptime"}},
		{name: "external read only command rejected", server: external, request: ShellRequest{Target: "current-server", Command: "uptime"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateServerShellRequest(test.server, test.request)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateServerShellRequest() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestAllowedMGCTLProcessInfoFields(t *testing.T) {
	allowed := []string{
		"memory", "heap_size", "total_heap_size", "stack_size", "message_queue_len",
		"reductions", "current_function", "status", "garbage_collection",
	}
	for _, field := range allowed {
		command := `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:info(erlang:list_to_pid(\"<0.123.0>\"),` + field + `)"`
		if _, ok := allowedMGCTLAnalysisDirectory(command); !ok {
			t.Fatalf("allowed process_info field %q was rejected", field)
		}
	}

	rejected := []string{"all", "messages", "dictionary", "binary", "current_stacktrace", "links", "monitors"}
	for _, field := range rejected {
		command := `cd -- '/data/wl_debug_1' && ./mgectl exprs "mlib_sys:info(erlang:list_to_pid(\"<0.123.0>\"),` + field + `)"`
		if _, ok := allowedMGCTLAnalysisDirectory(command); ok {
			t.Fatalf("unsafe process_info field %q was allowed", field)
		}
	}
}

func TestValidateServerShellRequestDiskCleanupBoundaries(t *testing.T) {
	internal := monitorconfig.Server{ID: "qt01-internal-debug", Address: "192.168.100.23:61618", InstanceDirectory: "/data"}
	external := monitorconfig.Server{ID: "external-live-check", Address: "101.34.55.142:43999", InstanceDirectory: "/data/server"}
	otherPrivate := monitorconfig.Server{ID: "qt01-internal-debug", Address: "172.19.0.23:61618", InstanceDirectory: "/data"}
	tests := []struct {
		name    string
		server  monitorconfig.Server
		request ShellRequest
		wantErr bool
	}{
		{name: "trash contents allowed internally", server: internal, request: ShellRequest{Target: "current-server", Command: trashCleanupCommand}},
		{name: "tmp directories allowed internally", server: internal, request: ShellRequest{Target: "current-server", Command: tmpDirsCleanupCommand}},
		{name: "trash cleanup rejected externally", server: external, request: ShellRequest{Target: "current-server", Command: trashCleanupCommand}, wantErr: true},
		{name: "trash cleanup rejected on other private range", server: otherPrivate, request: ShellRequest{Target: "current-server", Command: trashCleanupCommand}, wantErr: true},
		{name: "monitor host rejected", server: internal, request: ShellRequest{Target: "monitor-host", Command: trashCleanupCommand}, wantErr: true},
		{name: "other absolute path rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `rm -rf -- /var/tmp/cache`}, wantErr: true},
		{name: "absolute rm path rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `/bin/rm -rf -- /data/tmp/cache`}, wantErr: true},
		{name: "powershell delete rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `Remove-Item -Recurse /data/tmp/cache`}, wantErr: true},
		{name: "changed trash command rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `find /data/tmp/.Trash -mindepth 1 -delete`}, wantErr: true},
		{name: "wildcard cleanup rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `rm -rf -- /data/tmp/*`}, wantErr: true},
		{name: "script deletion rejected", server: internal, request: ShellRequest{Target: "current-server", Command: `python -c "import shutil; shutil.rmtree('/data/tmp/cache')"`}, wantErr: true},
		{name: "read only disk check unaffected", server: internal, request: ShellRequest{Target: "current-server", Command: `df -Pk /data`}},
		{name: "read only size check unaffected", server: internal, request: ShellRequest{Target: "current-server", Command: `du -sx -- /data/tmp/.Trash`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateServerShellRequest(test.server, test.request)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateServerShellRequest() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestAllowedDiskCleanupCommandsPassGenericValidation(t *testing.T) {
	limits := Limits{MaxCommandBytes: 4096, MaxOutputBytes: 65536}
	for _, command := range []string{trashCleanupCommand, tmpDirsCleanupCommand} {
		request := ShellRequest{Target: "current-server", Command: command, Reason: "recover disk space", TimeoutSeconds: 120}
		if err := ValidateShellRequest(request, limits); err != nil {
			t.Fatalf("allowed cleanup command rejected by generic validation: %v", err)
		}
	}
}

func TestValidateSkillShellRequestRequiresMatchingLoadedSkill(t *testing.T) {
	tests := []struct {
		name    string
		loaded  map[string]struct{}
		command string
		wantErr bool
	}{
		{name: "no skill rejects diagnostics", command: `df -Pk /data`, wantErr: true},
		{name: "loaded skill allows diagnostics", loaded: map[string]struct{}{"test-skill": {}}, command: `df -Pk /data`},
		{name: "unrelated skill rejects mgectl", loaded: map[string]struct{}{"test-skill": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl restart`, wantErr: true},
		{name: "analysis skill allows exempt diagnostics", loaded: map[string]struct{}{"erlang-ops-analysis": {}}, command: `ps aux | grep beam`},
		{name: "analysis skill allows node info expression", loaded: map[string]struct{}{"erlang-ops-analysis": {}}, command: analysisNodeInfoCommand},
		{name: "analysis skill allows memory expression", loaded: map[string]struct{}{"erlang-ops-analysis": {}}, command: analysisMemoryCommand},
		{name: "analysis skill allows large process expression", loaded: map[string]struct{}{"erlang-ops-analysis": {}}, command: analysisLargeProcessesCommand},
		{name: "analysis skill allows ETS expression", loaded: map[string]struct{}{"erlang-ops-analysis": {}}, command: analysisETSCommand},
		{name: "analysis skill allows snapshot expression", loaded: map[string]struct{}{"erlang-ops-analysis": {}}, command: analysisSnapshotCommand},
		{name: "analysis skill allows bounded process info", loaded: map[string]struct{}{"erlang-ops-analysis": {}}, command: analysisProcessHeapInfoCommand},
		{name: "restart skill cannot substitute for analysis skill", loaded: map[string]struct{}{"erlang-service-restart": {}}, command: analysisSnapshotCommand, wantErr: true},
		{name: "analysis skill rejects non exempt command", loaded: map[string]struct{}{"erlang-ops-analysis": {}}, command: `uname -a`, wantErr: true},
		{name: "analysis skill rejects executable find", loaded: map[string]struct{}{"erlang-ops-analysis": {}}, command: `find /data -maxdepth 1 -exec ls {} +`, wantErr: true},
		{name: "analysis skill rejects mgectl", loaded: map[string]struct{}{"erlang-ops-analysis": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl restart`, wantErr: true},
		{name: "analysis skill rejects garbage collect", loaded: map[string]struct{}{"erlang-ops-analysis": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "erlang:garbage_collect()"`, wantErr: true},
		{name: "removed legacy skill rejects mgectl", loaded: map[string]struct{}{"erlang-ops-first-response": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl restart`, wantErr: true},
		{name: "restart skill allows mgectl start", loaded: map[string]struct{}{"erlang-service-restart": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl start`},
		{name: "restart skill allows mgectl stop", loaded: map[string]struct{}{"erlang-service-restart": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl stop`},
		{name: "restart skill allows mgectl restart", loaded: map[string]struct{}{"erlang-service-restart": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl restart`},
		{name: "restart skill rejects garbage collect", loaded: map[string]struct{}{"erlang-service-restart": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "erlang:garbage_collect()"`, wantErr: true},
		{name: "restart skill rejects disk cleanup", loaded: map[string]struct{}{"erlang-service-restart": {}}, command: trashCleanupCommand, wantErr: true},
		{name: "gc skill allows garbage collect", loaded: map[string]struct{}{"erlang-node-gc": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "erlang:garbage_collect()"`},
		{name: "analysis and gc skills allow garbage collect", loaded: map[string]struct{}{"erlang-ops-analysis": {}, "erlang-node-gc": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "erlang:garbage_collect()"`},
		{name: "gc skill rejects start", loaded: map[string]struct{}{"erlang-node-gc": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl start`, wantErr: true},
		{name: "gc skill rejects stop", loaded: map[string]struct{}{"erlang-node-gc": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl stop`, wantErr: true},
		{name: "gc skill rejects restart", loaded: map[string]struct{}{"erlang-node-gc": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl restart`, wantErr: true},
		{name: "gc skill rejects analysis expression", loaded: map[string]struct{}{"erlang-node-gc": {}}, command: analysisSnapshotCommand, wantErr: true},
		{name: "unrelated skill rejects garbage collect", loaded: map[string]struct{}{"test-skill": {}}, command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "erlang:garbage_collect()"`, wantErr: true},
		{name: "disk skill allows disk cleanup", loaded: map[string]struct{}{"internal-disk-space-recovery": {}}, command: trashCleanupCommand},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSkillShellRequest(test.loaded, ShellRequest{Target: "current-server", Command: test.command})
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateSkillShellRequest() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateShellRequestPermanentlyBlocksHighRiskAndPrivateOperations(t *testing.T) {
	limits := Limits{MaxCommandBytes: 4096, MaxOutputBytes: 65536}
	blocked := []string{
		`rm -rf -- /var/tmp/cache`,
		`shutdown -h now`,
		`/sbin/reboot`,
		`systemctl restart game-server`,
		`systemctl start game-server`,
		`service game-server stop`,
		`mkfs.ext4 /dev/sdb1`,
		`wipefs -a /dev/sdb`,
		`echo x > /dev/sda`,
		`echo x 2>/dev/sda`,
		`echo x 3>/dev/null`,
		`echo x >/dev/null-extra`,
		`echo x >>/dev/null`,
		`dd if=/dev/zero of=/dev/sdb`,
		`sudo cat /var/log/messages`,
		`su`,
		`kill -9 1234`,
		`pkill beam.smp`,
		`ssh another-host`,
		`scp file another-host:/tmp/`,
		`sftp another-host`,
		`cat /run/secrets/api-token`,
		`cat /proc/1/environ`,
		`cat /proc/1/cmdline`,
		`printenv`,
		`ps auxe`,
		`cat /etc/ssh/ssh_host_rsa_key.pub`,
		`find /etc/ssh -type f -name '*.pub'`,
		`ls /root/.ssh`,
		`grep password /etc/passwd`,
		`find / -name 'ssh_host_*.pub'`,
		`/usr/bin/find -- / -name '*.key'`,
		`cat /etc/environment`,
		`getent shadow`,
	}
	for _, command := range blocked {
		t.Run(command, func(t *testing.T) {
			request := ShellRequest{Target: "current-server", Command: command, Reason: "diagnose"}
			if err := ValidateShellRequest(request, limits); err == nil {
				t.Fatalf("ValidateShellRequest(%q) unexpectedly allowed a permanently blocked operation", command)
			}
		})
	}
}

func TestValidateShellRequestAllowsSafeAndServerValidatedForms(t *testing.T) {
	limits := Limits{MaxCommandBytes: 4096, MaxOutputBytes: 65536}
	commands := []string{
		`df -Pk /data`,
		`ls /missing >/dev/null`,
		`ls /missing 1> /dev/null`,
		`ls /missing 2>/dev/null`,
		`ls /missing &> /dev/null`,
		`ps aux | grep beam | head -n 10`,
		`find /data/tmp -maxdepth 1 -type d`,
		`cd -- '/data/wl_debug_1' && ./mgectl start`,
		`cd -- '/data/wl_debug_1' && ./mgectl stop`,
		`cd -- '/data/wl_debug_1' && ./mgectl restart`,
		analysisSnapshotCommand,
		analysisProcessHeapInfoCommand,
		trashCleanupCommand,
		tmpDirsCleanupCommand,
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			request := ShellRequest{Target: "current-server", Command: command, Reason: "diagnose"}
			if err := ValidateShellRequest(request, limits); err != nil {
				t.Fatalf("ValidateShellRequest(%q) error = %v", command, err)
			}
		})
	}
}

func TestSanitizeShellOutputRedactsProtectedServerPrivateInformation(t *testing.T) {
	input := strings.Join([]string{
		"safe line",
		"/etc/ssh/ssh_host_rsa_key.pub 2026-08-10",
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCexample host",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"private-payload",
		"-----END OPENSSH PRIVATE KEY-----",
		"password=hidden-value",
		"access_token: hidden-token",
		"authorization: Bearer hidden-bearer",
		"/proc/123/environ",
		"safe tail",
	}, "\n")
	output := sanitizeShellOutput(input)
	for _, secret := range []string{
		"ssh_host_rsa_key.pub", "AAAAB3", "private-payload", "hidden-value",
		"hidden-token", "hidden-bearer", "/proc/123/environ",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("sanitizeShellOutput leaked %q in %q", secret, output)
		}
	}
	if !strings.Contains(output, "safe line") || !strings.Contains(output, "safe tail") {
		t.Fatalf("sanitizeShellOutput removed ordinary diagnostic output: %q", output)
	}
	if !strings.Contains(output, "[redacted protected server-private output]") {
		t.Fatalf("sanitizeShellOutput did not mark redacted output: %q", output)
	}
}

func TestIsApprovalExemptCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "ls", command: `ls -la`, want: true},
		{name: "absolute utility path", command: `/bin/ls /data`, want: true},
		{name: "grep", command: `grep -n beam server.log`, want: true},
		{name: "pipeline", command: `ps aux | grep beam | head -n 10`, want: true},
		{name: "cd chain", command: `cd -- '/data/game' && ls | head`, want: true},
		{name: "tail", command: `tail -n 20 server.log`, want: true},
		{name: "df", command: `df -Pk /data`, want: true},
		{name: "df pipeline", command: `df -Pk /data | grep -E 'Filesystem|data'`, want: true},
		{name: "find read only", command: `find /data/tmp/.Trash -mindepth 1 -maxdepth 1 -type f`, want: true},
		{name: "find absolute utility path", command: `/usr/bin/find /data -maxdepth 1 -type d`, want: true},
		{name: "quoted metacharacter", command: `grep 'foo|bar' file`, want: true},
		{name: "stdout to null", command: `ls /missing >/dev/null`, want: true},
		{name: "explicit stdout to null", command: `ls /missing 1> /dev/null`, want: true},
		{name: "stderr to null", command: `ls /missing 2>/dev/null`, want: true},
		{name: "spaced stderr argument and stdout to null", command: `ls /missing 2 > /dev/null`, want: true},
		{name: "stdout and stderr to null", command: `ls /missing &>/dev/null`, want: true},
		{name: "null redirection pipeline", command: `find /data -maxdepth 1 -type d 2>/dev/null | grep game | head`, want: true},
		{name: "semicolon injection", command: `ls; rm -rf /data/tmp/x`},
		{name: "null redirection does not hide semicolon injection", command: `ls >/dev/null; rm -rf /data/tmp/x`},
		{name: "mixed and chain", command: `ls && systemctl restart game`},
		{name: "redirection", command: `head file > copy`},
		{name: "append null redirection", command: `ls >>/dev/null`},
		{name: "other device redirection", command: `ls 2>/dev/sda`},
		{name: "null prefix is not exact target", command: `ls >/dev/null-extra`},
		{name: "input from null", command: `head </dev/null`},
		{name: "fd duplication still requires approval", command: `ls >/dev/null 2>&1`},
		{name: "other explicit descriptor", command: `ls 3>/dev/null`},
		{name: "command substitution", command: `grep "$(rm -rf /tmp/x)" file`},
		{name: "or chain", command: `ps || rm file`},
		{name: "mgectl mixed in", command: `cd /tmp && ./mgectl restart`},
		{name: "allowlisted mgectl analysis", command: analysisSnapshotCommand, want: true},
		{name: "allowlisted bounded process info", command: analysisProcessHeapInfoCommand, want: true},
		{name: "arbitrary mgectl expression", command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "os:cmd(\"id\")"`},
		{name: "mgectl garbage collection still requires approval", command: `cd -- '/data/wl_debug_1' && ./mgectl exprs "erlang:garbage_collect()"`},
		{name: "mgectl start still requires approval", command: `cd -- '/data/wl_debug_1' && ./mgectl start`},
		{name: "mgectl stop still requires approval", command: `cd -- '/data/wl_debug_1' && ./mgectl stop`},
		{name: "mgectl restart still requires approval", command: `cd -- '/data/wl_debug_1' && ./mgectl restart`},
		{name: "background", command: `tail -f file &`},
		{name: "other command", command: `printf health`},
		{name: "du still requires approval", command: `du -sx -- /data/tmp/.Trash`},
		{name: "find exec still requires approval", command: `find /data -type f -exec grep -l beam {} +`},
		{name: "find delete still requires approval", command: `find /data/tmp -mindepth 1 -maxdepth 1 -delete`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsApprovalExemptCommand(test.command); got != test.want {
				t.Fatalf("IsApprovalExemptCommand(%q) = %v, want %v", test.command, got, test.want)
			}
		})
	}
}
