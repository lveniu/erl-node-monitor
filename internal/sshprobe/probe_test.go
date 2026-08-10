package sshprobe

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"erlang-monitor/internal/config"
	"golang.org/x/crypto/ssh"
)

func TestPublicKeyFingerprintFromFileAcceptsAuthorizedKey(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "monitor.pub")
	if err := os.WriteFile(path, ssh.MarshalAuthorizedKey(publicKey), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := publicKeyFingerprintFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := ssh.FingerprintSHA256(publicKey); got != want {
		t.Fatalf("fingerprint = %q, want %q", got, want)
	}
}

func TestPublicKeyFingerprintFromFileAcceptsEncryptedOpenSSHPrivateKey(t *testing.T) {
	sshKeygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen is unavailable")
	}
	path := filepath.Join(t.TempDir(), "monitor_key")
	command := exec.Command(sshKeygen, "-q", "-t", "rsa", "-b", "2048", "-N", "test-passphrase", "-f", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate encrypted OpenSSH key: %v\n%s", err, output)
	}

	got, err := publicKeyFingerprintFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	publicData, err := os.ReadFile(path + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey(publicData)
	if err != nil {
		t.Fatal(err)
	}
	if want := ssh.FingerprintSHA256(publicKey); got != want {
		t.Fatalf("fingerprint = %q, want %q", got, want)
	}
}

func TestCPUUsageNeedsTwoSamplesThenReturnsRatio(t *testing.T) {
	collector := NewCollector()
	if ratio, valid := collector.cpuUsage("server-a", 1000, 400); valid || ratio != 0 {
		t.Fatalf("first sample = (%v, %v), want (0, false)", ratio, valid)
	}
	ratio, valid := collector.cpuUsage("server-a", 1100, 430)
	if !valid || math.Abs(ratio-0.70) > 0.000001 {
		t.Fatalf("second sample = (%v, %v), want (0.70, true)", ratio, valid)
	}
}

func TestNodeCPUUsageNeedsTwoSamplesThenReturnsSingleCoreRatio(t *testing.T) {
	collector := NewCollector()
	if ratio, valid := collector.nodeCPUUsage("server-a", "game@127.0.0.1", 10_000, 500, 16); valid || ratio != 0 {
		t.Fatalf("first sample ratio=%v valid=%v, want 0/false", ratio, valid)
	}
	ratio, valid := collector.nodeCPUUsage("server-a", "game@127.0.0.1", 11_600, 580, 16)
	if !valid || ratio != 0.8 {
		t.Fatalf("second sample ratio=%v valid=%v, want 0.8/true", ratio, valid)
	}
}

func TestParseBeamProcesses(t *testing.T) {
	output := strings.Join([]string{
		" 123 /srv/game/erts-11.2/bin/beam.smp -- -root /srv/game -name game_1@127.0.0.1 -setcookie cookie-one -pa /srv/game/ebin -bindir /srv/game/erts-11.2/bin -s mmgr start",
		" 999 grep beam.smp",
		" 456 /opt/erts/bin/beam.smp -name game_2@127.0.0.1 -setcookie cookie-two -pa '/opt/game ebin' -bindir /opt/erts/bin -s mmgr start",
		" 789 /opt/erts/bin/beam.smp -name debug@127.0.0.1 -setcookie cookie-debug -bindir /opt/erts/bin -remsh game_1@127.0.0.1",
	}, "\n")
	processes := parseBeamProcesses(output)
	if len(processes) != 2 {
		t.Fatalf("got %d processes, want 2: %#v", len(processes), processes)
	}
	want := beamProcess{PID: 123, NodeName: "game_1@127.0.0.1", Cookie: "cookie-one", Ebin: "/srv/game/ebin", ErlBinary: "/srv/game/erts-11.2/bin/erl", IsServer: true}
	if !reflect.DeepEqual(processes[0], want) {
		t.Fatalf("got %#v, want %#v", processes[0], want)
	}
}

func TestParseProbeOutput(t *testing.T) {
	values, err := parseProbeOutput("rpc noise\n[1, 2,3,4,5,6,7,8,9,10,11,12,13]\nok")
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 13 || values[0] != 1 || values[12] != 13 {
		t.Fatalf("unexpected values: %#v", values)
	}
}

func TestParseProbeOutputRejectsInvalidData(t *testing.T) {
	if _, err := parseProbeOutput("[1,2,3]"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseRoleCounts(t *testing.T) {
	registered, online, valid, err := parseRoleCounts("MONITOR_ROLE_COUNTS:{317,1}\n")
	if err != nil || !valid || registered != 317 || online != 1 {
		t.Fatalf("counts = %d/%d valid=%v err=%v", registered, online, valid, err)
	}
	registered, online, valid, err = parseRoleCounts("MONITOR_ROLE_COUNTS:undefined\n")
	if err != nil || valid || registered != 0 || online != 0 {
		t.Fatalf("unsupported counts = %d/%d valid=%v err=%v", registered, online, valid, err)
	}
}

func TestParseProbeMNodeConnections(t *testing.T) {
	output := `nodeid----stat-------node-----------------------statepid-------mymsgpid------czone-
801000001 2 wl_ssjj_1@172.19.33.98 <1.1.1> <1.1.2> 0
901100005 1 wl_ssjj_100005@172.19.33.104 <1.1.3> <1.1.4> 0
------------------------------ process name ---------------------------
MONITOR_MNODE_STATUS:available
`
	connections, valid := parseProbeMNodeConnections(output)
	if !valid || len(connections) != 2 {
		t.Fatalf("connections = %#v valid=%v", connections, valid)
	}
	if connections[0].Type != "central" || !connections[0].Usable || connections[1].Type != "region" || connections[1].Usable {
		t.Fatalf("unexpected classified connections: %#v", connections)
	}
	if connections, valid = parseProbeMNodeConnections("MONITOR_MNODE_STATUS:unavailable\n"); valid || len(connections) != 0 {
		t.Fatalf("unavailable mnode = %#v valid=%v", connections, valid)
	}
}

func TestParseProcessIdentity(t *testing.T) {
	detail := strings.Join([]string{"<0.42.0>", "world_server", "{world_server,init,1}", "{gen_server,loop,7}"}, "\t")
	output := "MONITOR_MEMORY_PROCESS:" + base64.StdEncoding.EncodeToString([]byte(detail)) + "\n"
	got, err := parseProcessIdentity(output, "MONITOR_MEMORY_PROCESS")
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != "<0.42.0>" || got.RegisteredName != "world_server" || got.CurrentFunction != "{gen_server,loop,7}" {
		t.Fatalf("unexpected process identity: %#v", got)
	}
}

func TestShellQuote(t *testing.T) {
	got := shellQuote("a'b c")
	if got != `'a'"'"'b c'` {
		t.Fatalf("got %q", got)
	}
}

func TestSelectBeamProcessesReturnsOnlyRequestedNodes(t *testing.T) {
	processes := []beamProcess{
		{NodeName: "a@127.0.0.1"},
		{NodeName: "b@127.0.0.1"},
		{NodeName: "c@127.0.0.1"},
	}
	selected, missing := selectBeamProcesses(processes, []string{"c@127.0.0.1", "missing@127.0.0.1"})
	if !reflect.DeepEqual(selected, []beamProcess{{NodeName: "c@127.0.0.1"}}) {
		t.Fatalf("selected = %#v", selected)
	}
	if !reflect.DeepEqual(missing, []string{"missing@127.0.0.1"}) {
		t.Fatalf("missing = %#v", missing)
	}
}

func TestMissingExpectedNodesReportsDirectoryWithoutBeamProcess(t *testing.T) {
	expected := []string{"wl_ssjj_1", "wl_ssjj_1802"}
	processes := []beamProcess{{NodeName: "wl_ssjj_1@172.19.33.98"}}

	got := missingExpectedNodes(expected, processes)
	want := []NodeFailure{{Name: "wl_ssjj_1802", Error: "configured instance directory has no running Erlang node"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("missing expected nodes = %#v, want %#v", got, want)
	}
}

func TestSelectBeamProcessesMatchesExpectedShortNodeName(t *testing.T) {
	process := beamProcess{NodeName: "wl_ssjj_1802@127.0.0.1"}
	selected, missing := selectBeamProcesses([]beamProcess{process}, []string{"wl_ssjj_1802"})
	if !reflect.DeepEqual(selected, []beamProcess{process}) || len(missing) != 0 {
		t.Fatalf("selected = %#v, missing = %#v", selected, missing)
	}
}

func TestParseExpectedNodeNamesDeduplicatesDirectoryScan(t *testing.T) {
	got := parseExpectedNodeNames("wl_ssjj_1802\n\nwl_ssjj_1\nwl_ssjj_1802\n")
	want := []string{"wl_ssjj_1", "wl_ssjj_1802"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected node names = %#v, want %#v", got, want)
	}
}

func TestParseExpectedNodeNamesExcludesBackupDirectories(t *testing.T) {
	got := parseExpectedNodeNames("wl_act_7\nwl_act_8.bk.1785811556\nwl_act_9.bk.1785811704\n")
	want := []string{"wl_act_7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected node names = %#v, want backups excluded: %#v", got, want)
	}
}

func TestParseExpectedNodeNamesAcceptsYSMWServerLayoutAndExcludesAccter(t *testing.T) {
	got := parseExpectedNodeNames("wl_ssjj_1001\n/data/ysmw_c801_1\n/data/ysmw_release_1\n/data/ysmw_accter1\n")
	want := []string{"wl_ssjj_1001", "ysmw_c801_1", "ysmw_release_1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected node names = %#v, want ysmw server instances without accter: %#v", got, want)
	}
}

func TestExpectedNodeScanCommandUsesInstanceServerDirectoriesAtDataRoot(t *testing.T) {
	command := expectedNodeScanCommand("/data")
	for _, expected := range []string{"/data/wl_*/server", "/data/ysmw_*/server"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("scan command %q does not include %q", command, expected)
		}
	}
	if strings.Contains(command, "-name 'wl_*'") {
		t.Fatalf("/data scan must not treat arbitrary second-level wl_* directories as instances: %s", command)
	}

	legacyCommand := expectedNodeScanCommand("/data/server")
	if !strings.Contains(legacyCommand, "-name 'wl_*'") {
		t.Fatalf("legacy /data/server scan must retain second-level wl_* discovery: %s", legacyCommand)
	}
}

func TestProbeExpressionUsesSinglePassAggregation(t *testing.T) {
	expression := buildProbeExpression(config.Server{QueueThreshold: 50, MemoryThresholdMBytes: 200})
	if !strings.Contains(expression, "lists:foldl") {
		t.Fatalf("expression does not use foldl: %s", expression)
	}
	if got := strings.Count(expression, "process_info(P,[memory,message_queue_len])"); got != 1 {
		t.Fatalf("combined process_info calls = %d, want 1", got)
	}
	if !strings.Contains(expression, "monitor_role_counts") {
		t.Fatalf("expression does not query the optional role-count interface: %s", expression)
	}
	if !strings.Contains(expression, "mnode:i()") {
		t.Fatalf("expression does not query the optional mnode connection interface: %s", expression)
	}
	if strings.Contains(expression, "Ms=[") || strings.Contains(expression, "Qs=[") {
		t.Fatalf("expression still materializes full metric lists: %s", expression)
	}
}

func TestProbeExpressionRunsOnLocalErlang(t *testing.T) {
	erl, err := exec.LookPath("erl")
	if err != nil {
		t.Skip("erl is unavailable")
	}
	expression := buildProbeExpression(config.Server{QueueThreshold: 50, MemoryThresholdMBytes: 200})
	command := exec.Command(erl, "-noshell", "-eval", `case `+expression+` of {Metrics,MemoryProcess,QueueProcess,RoleCounts,MNodeStatus}->io:format("MONITOR_METRICS:~p~nMONITOR_MEMORY_PROCESS:~s~nMONITOR_QUEUE_PROCESS:~s~nMONITOR_ROLE_COUNTS:~p~nMONITOR_MNODE_STATUS:~p~n",[Metrics,MemoryProcess,QueueProcess,RoleCounts,MNodeStatus]) end,halt().`)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("probe expression failed: %v\n%s", err, output)
	}
	values, err := parseProbeOutput(string(output))
	if err != nil {
		t.Fatal(err)
	}
	if values[0] == 0 || values[9] == 0 {
		t.Fatalf("single-pass aggregation returned empty process metrics: %v", values)
	}
	if _, err := parseProcessIdentity(string(output), "MONITOR_MEMORY_PROCESS"); err != nil {
		t.Fatal(err)
	}
	if _, _, valid, err := parseRoleCounts(string(output)); err != nil || valid {
		t.Fatalf("local node unexpectedly returned role counts: valid=%v err=%v", valid, err)
	}
	if connections, valid := parseProbeMNodeConnections(string(output)); valid || len(connections) != 0 {
		t.Fatalf("local node unexpectedly returned mnode connections: valid=%v connections=%#v", valid, connections)
	}
}
