package opsagent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	monitorconfig "erlang-monitor/internal/config"
	"erlang-monitor/internal/sshprobe"
)

type ShellRequest struct {
	Target         string `json:"target"`
	Command        string `json:"command"`
	Reason         string `json:"reason"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type ShellResult struct {
	Target     string `json:"target"`
	Output     string `json:"output,omitempty"`
	ExitStatus string `json:"exit_status"`
	DurationMS int64  `json:"duration_ms"`
	Truncated  bool   `json:"truncated"`
	Error      string `json:"error,omitempty"`
}

type ShellExecutor interface {
	Execute(context.Context, monitorconfig.Server, ShellRequest, time.Duration, int) ShellResult
}

type DefaultShellExecutor struct {
	localWorkdir string
	remote       *sshprobe.DiagnosticCollector
}

func NewShellExecutor(localWorkdir string) *DefaultShellExecutor {
	return &DefaultShellExecutor{localWorkdir: localWorkdir, remote: sshprobe.NewDiagnosticCollector()}
}

func ValidateShellRequest(request ShellRequest, limits Limits) error {
	request.Command = strings.TrimSpace(request.Command)
	if request.Target != "current-server" {
		return errors.New("target must be current-server; Ops Agent shell is restricted to configured internal servers")
	}
	if request.Command == "" || len(request.Command) > limits.MaxCommandBytes || strings.ContainsRune(request.Command, 0) {
		return errors.New("command is empty or exceeds the configured limit")
	}
	if strings.TrimSpace(request.Reason) == "" {
		return errors.New("reason is required")
	}
	if request.TimeoutSeconds < 0 || request.TimeoutSeconds > 120 {
		return errors.New("timeout_seconds must be between 1 and 120")
	}
	policyCommand := stripAllowedNullOutputRedirections(request.Command)
	lower := strings.ToLower(strings.Join(strings.Fields(policyCommand), " "))
	if legacyMGECTCommand.MatchString(request.Command) {
		return errors.New("legacy mgect command is blocked; the allowlist accepts only mgectl")
	}
	if hasDeleteIntent(request.Command) && lower != strings.ToLower(trashCleanupCommand) && lower != strings.ToLower(tmpDirsCleanupCommand) {
		return errors.New("delete operations are permanently blocked except the two fixed internal disk cleanup commands")
	}
	if startIntent.MatchString(lower) && !mgectlStartCommand.MatchString(request.Command) {
		return errors.New("generic service start operations are permanently blocked; only the server-validated mgectl start form is supported")
	}
	if stopIntent.MatchString(lower) && !mgectlStopCommand.MatchString(request.Command) {
		return errors.New("generic stop and service shutdown operations are permanently blocked; only the server-validated mgectl stop form is supported")
	}
	if restartIntent.MatchString(lower) && !mgectlRestartCommand.MatchString(request.Command) {
		return errors.New("host and generic service restart operations are permanently blocked; only the server-validated mgectl restart form is supported")
	}
	for _, blocked := range permanentlyBlockedCommandPatterns {
		if blocked.re.MatchString(lower) {
			return fmt.Errorf("command contains permanently blocked operation %q", blocked.name)
		}
	}
	return nil
}

var permanentlyBlockedCommandPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{name: "shutdown/poweroff/reboot/halt", re: regexp.MustCompile(`(^|[\s;&|('\"])([^;&|\s('\"]+/)?(shutdown|poweroff|halt|reboot|kexec)([\s;&|)'\"]|$)|\b(telinit|init)\s+[06]\b`)},
	{name: "format or destructive device operation", re: regexp.MustCompile(`(^|[\s;&|('\"])([^;&|\s('\"]+/)?(mkfs(\.[^\s;&|]+)?|mke2fs|xfs_growfs|wipefs|fdisk|sfdisk|parted|format)([\s;&|)'\"]|$)|\bof\s*=\s*/dev/|\btee\s+/dev/`)},
	{name: "device redirection", re: regexp.MustCompile(`>{1,2}\s*/dev/`)},
	{name: "privilege escalation", re: regexp.MustCompile(`(^|[\s;&|('\"])([^;&|\s('\"]+/)?(sudo|su|doas)([\s;&|)'\"]|$)`)},
	{name: "manual process termination", re: regexp.MustCompile(`(^|[\s;&|('\"])([^;&|\s('\"]+/)?(kill|pkill|killall|taskkill)([\s;&|)'\"]|$)`)},
	{name: "ssh/scp/sftp/key tools", re: regexp.MustCompile(`(^|[\s;&|('\"])([^;&|\s('\"]+/)?(ssh|scp|sftp|ssh-keygen|ssh-add|ssh-keyscan)([\s;&|)'\"]|$)`)},
	{name: "secret or process environment access", re: regexp.MustCompile(`(/run/secrets|/etc/(shadow|gshadow|passwd|group|environment)|/proc(/[^\s;&|]*)?/(environ|cmdline)|\bprintenv\b|\benv\s*($|[;&|])|\bexport\s+-p\b|\bgetent\s+(passwd|group|shadow|gshadow)\b|\bps\s+([a-z]*e[a-z]*|-[a-z]+\s+e(w+)?)\b|\b(api[_-]?key|access[_-]?token|password|passwd|secret|token)\s*=|\b(api[_-]?key|access[_-]?token|password|passwd|secret|token)\b)`)},
	{name: "protected server identity/key path", re: regexp.MustCompile(`(/etc/ssh|/root/\.ssh|/\.ssh/|authorized_keys|known_hosts|ssh_host_|id_(rsa|ed25519|ecdsa|dsa)|private[_-]?key|credentials|-----begin[^\n]*private key)`)},
	{name: "unbounded root find", re: regexp.MustCompile(`(^|[\s;&|('\"])([^;&|\s('\"]+/)?find\s+(--\s+)?(/|\.\.?)(\s|$)`)},
	{name: "unsafe shell redirection or control", re: regexp.MustCompile(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}|chmod\s+-r\s+777\s+/|chown\s+-r\s+`)},
}

// IsApprovalExemptCommand accepts only a conservative shell subset made up of
// the explicitly allowed read-only commands. It permits pipelines and &&
// chains, but rejects syntax that can redirect, substitute, background, group,
// or append a different command.
func IsApprovalExemptCommand(command string) bool {
	if _, ok := allowedMGCTLAnalysisDirectory(strings.TrimSpace(command)); ok {
		return true
	}
	policyCommand := stripAllowedNullOutputRedirections(strings.TrimSpace(command))
	segments, ok := splitApprovalExemptSegments(policyCommand)
	if !ok || len(segments) == 0 {
		return false
	}
	for _, segment := range segments {
		fields := strings.Fields(segment)
		if len(fields) == 0 || strings.ContainsAny(fields[0], `"'$`) {
			return false
		}
		executable := path.Base(strings.ReplaceAll(fields[0], `\`, "/"))
		if _, allowed := approvalExemptCommands[strings.ToLower(executable)]; !allowed {
			return false
		}
		if strings.EqualFold(executable, "find") && findHasWriteAction(fields[1:]) {
			return false
		}
	}
	return true
}

// stripAllowedNullOutputRedirections removes only shell output redirections
// whose exact target is /dev/null from a policy copy of the command. The
// original command is still executed unchanged. Redirections inside quotes,
// input/read-write redirections, append redirections, and every other target
// remain present so the normal policy checks can reject or require approval.
func stripAllowedNullOutputRedirections(command string) string {
	var result strings.Builder
	result.Grow(len(command))
	var quote byte
	escaped := false

	for i := 0; i < len(command); {
		current := command[i]
		if escaped {
			result.WriteByte(current)
			escaped = false
			i++
			continue
		}
		if quote != 0 {
			result.WriteByte(current)
			if current == '\\' && quote == '"' {
				escaped = true
			} else if current == quote {
				quote = 0
			}
			i++
			continue
		}
		if current == '\\' {
			result.WriteByte(current)
			escaped = true
			i++
			continue
		}
		if current == '\'' || current == '"' {
			result.WriteByte(current)
			quote = current
			i++
			continue
		}

		operatorLength := 0
		switch {
		case current == '&' && i+1 < len(command) && command[i+1] == '>' && (i+2 >= len(command) || command[i+2] != '>'):
			operatorLength = 2
		case current == '>' &&
			(i == 0 || (command[i-1] != '>' && command[i-1] != '<' && command[i-1] != '&')) &&
			(i+1 >= len(command) || (command[i+1] != '>' && command[i+1] != '|')) &&
			!hasUnsupportedExplicitOutputDescriptor(command, i):
			operatorLength = 1
		}
		if operatorLength == 0 {
			result.WriteByte(current)
			i++
			continue
		}

		targetStart := i + operatorLength
		for targetStart < len(command) && (command[targetStart] == ' ' || command[targetStart] == '\t') {
			targetStart++
		}
		const nullDevice = "/dev/null"
		targetEnd := targetStart + len(nullDevice)
		if targetEnd > len(command) || command[targetStart:targetEnd] != nullDevice ||
			(targetEnd < len(command) && !isShellTokenBoundary(command[targetEnd])) {
			result.WriteByte(current)
			i++
			continue
		}

		result.WriteByte(' ')
		i = targetEnd
	}

	return strings.TrimSpace(result.String())
}

func hasUnsupportedExplicitOutputDescriptor(command string, operatorIndex int) bool {
	descriptorStart := operatorIndex
	for descriptorStart > 0 && command[descriptorStart-1] >= '0' && command[descriptorStart-1] <= '9' {
		descriptorStart--
	}
	if descriptorStart == operatorIndex {
		return false
	}
	if descriptorStart > 0 && !isShellTokenBoundary(command[descriptorStart-1]) && command[descriptorStart-1] != '(' {
		return false
	}
	descriptor := command[descriptorStart:operatorIndex]
	return descriptor != "1" && descriptor != "2"
}

func isShellTokenBoundary(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n', ';', '|', '&':
		return true
	default:
		return false
	}
}

func findHasWriteAction(fields []string) bool {
	for _, field := range fields {
		lower := strings.ToLower(field)
		if lower == "-delete" || strings.HasPrefix(lower, "-exec") || strings.HasPrefix(lower, "-ok") || strings.HasPrefix(lower, "-fls") || strings.HasPrefix(lower, "-fprint") {
			return true
		}
	}
	return false
}

func splitApprovalExemptSegments(command string) ([]string, bool) {
	if command == "" {
		return nil, false
	}
	segments := make([]string, 0, 3)
	start := 0
	var quote byte
	escaped := false
	appendSegment := func(end int) bool {
		segment := strings.TrimSpace(command[start:end])
		if segment == "" {
			return false
		}
		segments = append(segments, segment)
		return true
	}
	for i := 0; i < len(command); i++ {
		current := command[i]
		if escaped {
			escaped = false
			continue
		}
		if quote != 0 {
			if quote == '"' {
				if current == '\\' {
					escaped = true
					continue
				}
				if current == '`' || (current == '$' && i+1 < len(command) && (command[i+1] == '(' || command[i+1] == '{')) {
					return nil, false
				}
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\\' {
			escaped = true
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		switch current {
		case '\n', '\r', ';', '<', '>', '`', '(', ')':
			return nil, false
		case '$':
			if i+1 < len(command) && (command[i+1] == '(' || command[i+1] == '{') {
				return nil, false
			}
		case '&':
			if i+1 >= len(command) || command[i+1] != '&' || !appendSegment(i) {
				return nil, false
			}
			i++
			start = i + 1
		case '|':
			if i+1 < len(command) && command[i+1] == '|' {
				return nil, false
			}
			if !appendSegment(i) {
				return nil, false
			}
			start = i + 1
		}
	}
	if escaped || quote != 0 || !appendSegment(len(command)) {
		return nil, false
	}
	return segments, true
}

var (
	mgectlExprsCommand               = regexp.MustCompile(`^cd -- '([^']+)' && \./mgectl exprs "(.+)"$`)
	mgectlGCCommand                  = regexp.MustCompile(`^cd -- '([^']+)' && \./mgectl exprs "erlang:garbage_collect\(\)"$`)
	mgectlStartCommand               = regexp.MustCompile(`^cd -- '([^']+)' && \./mgectl start$`)
	mgectlStopCommand                = regexp.MustCompile(`^cd -- '([^']+)' && \./mgectl stop$`)
	mgectlRestartCommand             = regexp.MustCompile(`^cd -- '([^']+)' && \./mgectl restart$`)
	mgectlSnapshotOptionsExpression  = regexp.MustCompile(`^mlib_sys:monitor_snapshot\(#\{memory_threshold_bytes=>([0-9]{1,13}),message_queue_threshold=>([0-9]{1,7})\}\)$`)
	mgectlSchedulerHotspotExpression = regexp.MustCompile(`^mlib_sys:monitor_scheduler_hotspots\(([0-9]{1,5}),([0-9]{1,2})\)$`)
	mgectlProcessDetailExpression    = regexp.MustCompile(`^mlib_sys:monitor_process_detail\(erlang:list_to_pid\(\\"<0\.([0-9]{1,10})\.([0-9]{1,10})>\\"\)\)$`)
	mgectlProcessInfoExpression      = regexp.MustCompile(`^mlib_sys:info\(erlang:list_to_pid\(\\"<0\.([0-9]{1,10})\.([0-9]{1,10})>\\"\),(memory|heap_size|total_heap_size|stack_size|message_queue_len|reductions|current_function|status|garbage_collection)\)$`)
	mgectlProcessThresholdExpression = regexp.MustCompile(`^mlib_sys:(get_memory|get_heap|get_theap)\(([0-9]{1,13})\)$`)
	legacyMGECTCommand               = regexp.MustCompile(`(^|[\s;&|])\./mgect([\s;&|]|$)`)
	deleteUtility                    = regexp.MustCompile(`(^|[\s;&|()])([^\s;&|()]+[/\\])?(rm|rmdir|unlink|shred|remove-item|clear-content|del|erase|truncate)(\.exe)?([\s;&|()]|$)`)
	restartIntent                    = regexp.MustCompile(`(^|[^a-z0-9_-])restart([^a-z0-9_-]|$)`)
	startIntent                      = regexp.MustCompile(`(^|[^a-z0-9_-])start([^a-z0-9_-]|$)`)
	stopIntent                       = regexp.MustCompile(`(^|[^a-z0-9_-])stop([^a-z0-9_-]|$)`)
)

var approvalExemptCommands = map[string]struct{}{
	"ls": {}, "grep": {}, "ps": {}, "cd": {}, "head": {}, "tail": {}, "df": {}, "find": {},
}

const (
	trashCleanupCommand   = `find /data/tmp/.Trash -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +`
	tmpDirsCleanupCommand = `find /data/tmp -mindepth 1 -maxdepth 1 -type d ! -name '.Trash' -exec rm -rf -- {} +`
)

// ValidateServerShellRequest enforces server-aware boundaries that cannot be
// delegated to model instructions. Delete, service-control, and GC operations
// are restricted to configured 192.168.100.0/24 servers and exact allowlisted
// command forms.
func ValidateServerShellRequest(server monitorconfig.Server, request ShellRequest) error {
	command := strings.TrimSpace(request.Command)
	if legacyMGECTCommand.MatchString(command) {
		return errors.New("legacy mgect command is blocked; the allowlist accepts only mgectl")
	}
	lower := strings.ToLower(command)
	deleteRequested := hasDeleteIntent(command)
	startRequested := startIntent.MatchString(lower)
	restartRequested := restartIntent.MatchString(lower)
	stopRequested := stopIntent.MatchString(lower)
	if request.Target != "current-server" {
		return errors.New("all Ops Agent shell operations require target current-server")
	}
	if !internalOpsServer(server) {
		return errors.New("all Ops Agent shell operations are allowed only on configured 192.168.100.* servers")
	}
	if command == trashCleanupCommand || command == tmpDirsCleanupCommand {
		return nil
	}
	if deleteRequested {
		return errors.New("delete operations are restricted to the two allowed internal disk cleanup commands")
	}
	if directory, ok := allowedMGCTLServiceControlDirectory(command); ok {
		return validateMGCTLDirectory(server, directory)
	}
	if startRequested {
		return errors.New("start operations are restricted to the allowed mgectl start command")
	}
	if stopRequested {
		return errors.New("stop operations are restricted to the allowed mgectl stop command")
	}
	if restartRequested {
		return errors.New("restart operations are restricted to the allowed mgectl restart command")
	}
	if !strings.Contains(command, "./mgectl") {
		return nil
	}
	if request.Target != "current-server" {
		return errors.New("mgectl actions require target current-server")
	}
	if !internalOpsServer(server) {
		return errors.New("mgectl actions are allowed only on configured 192.168.100.* servers")
	}
	if directory, ok := allowedMGCTLAnalysisDirectory(command); ok {
		return validateMGCTLDirectory(server, directory)
	}
	matches := mgectlGCCommand.FindStringSubmatch(command)
	if matches != nil {
		return validateMGCTLDirectory(server, matches[1])
	}
	return errors.New("mgectl command is not an allowed analysis, garbage-collect, start, stop, or restart form")
}

// ValidateSkillShellRequest makes Skill loading a prerequisite for Shell and
// binds each enabled mutation family to its corresponding Skill. The
// command and server policy remain authoritative even after a Skill is loaded.
func ValidateSkillShellRequest(loadedSkills map[string]struct{}, request ShellRequest) error {
	if len(loadedSkills) == 0 {
		return errors.New("shell execution requires a loaded Skill")
	}
	command := strings.TrimSpace(request.Command)
	if command == trashCleanupCommand || command == tmpDirsCleanupCommand {
		if _, ok := loadedSkills["internal-disk-space-recovery"]; !ok {
			return errors.New("internal disk cleanup requires the internal-disk-space-recovery Skill")
		}
	}
	_, analysisMGCTL := allowedMGCTLAnalysisDirectory(command)
	_, serviceControlMGCTL := allowedMGCTLServiceControlDirectory(command)
	garbageCollectMGCTL := mgectlGCCommand.MatchString(command)
	if strings.Contains(command, "./mgectl") {
		if analysisMGCTL {
			if _, ok := loadedSkills["erlang-ops-analysis"]; !ok {
				return errors.New("allowlisted mgectl analysis expressions require the erlang-ops-analysis Skill")
			}
		} else if serviceControlMGCTL {
			if _, ok := loadedSkills["erlang-service-restart"]; !ok {
				return errors.New("mgectl start, stop, or restart requires the erlang-service-restart Skill")
			}
		} else if garbageCollectMGCTL {
			if _, ok := loadedSkills["erlang-node-gc"]; !ok {
				return errors.New("mgectl garbage collection requires the erlang-node-gc Skill")
			}
		} else {
			return errors.New("mgectl command is not enabled by any loaded Skill")
		}
	}
	if _, analysisLoaded := loadedSkills["erlang-ops-analysis"]; analysisLoaded {
		_, restartLoaded := loadedSkills["erlang-service-restart"]
		_, garbageCollectLoaded := loadedSkills["erlang-node-gc"]
		_, diskLoaded := loadedSkills["internal-disk-space-recovery"]
		if !restartLoaded && !garbageCollectLoaded && !diskLoaded && !analysisMGCTL && !IsApprovalExemptCommand(command) {
			return errors.New("erlang-ops-analysis permits only the approval-exempt diagnostic command set and allowlisted mgectl analysis expressions")
		}
	}
	return nil
}

func allowedMGCTLAnalysisDirectory(command string) (string, bool) {
	matches := mgectlExprsCommand.FindStringSubmatch(command)
	if matches == nil {
		return "", false
	}
	directory, expression := matches[1], matches[2]
	switch expression {
	case "mlib_sys:i()", "erlang:memory()",
		"mlib_sys:get_memory()", "mlib_sys:get_heap()", "mlib_sys:get_theap()",
		"mlib_sys:get_ets_memory()", "mlib_sys:get_ets_memory(megabyte)",
		"mlib_sys:get_total_mnesia_memory()", "mlib_sys:get_mnesia_table_memory()",
		"mlib_sys:atom_info()",
		"mlib_sys:monitor_snapshot()", "mlib_sys:monitor_role_counts()":
		return directory, true
	}
	if values := mgectlProcessThresholdExpression.FindStringSubmatch(expression); values != nil {
		threshold, err := strconv.ParseUint(values[2], 10, 64)
		minimum := uint64(100_000)
		if values[1] == "get_memory" {
			minimum = 1 << 20
		}
		if err == nil && threshold >= minimum && threshold <= 1<<40 {
			return directory, true
		}
		return "", false
	}
	if values := mgectlSnapshotOptionsExpression.FindStringSubmatch(expression); values != nil {
		memoryThreshold, memoryErr := strconv.ParseUint(values[1], 10, 64)
		queueThreshold, queueErr := strconv.ParseUint(values[2], 10, 64)
		if memoryErr == nil && queueErr == nil && memoryThreshold <= 1<<40 && queueThreshold <= 1_000_000 {
			return directory, true
		}
		return "", false
	}
	if values := mgectlSchedulerHotspotExpression.FindStringSubmatch(expression); values != nil {
		windowMS, windowErr := strconv.Atoi(values[1])
		limit, limitErr := strconv.Atoi(values[2])
		if windowErr == nil && limitErr == nil && windowMS >= 1000 && windowMS <= 10000 && limit >= 1 && limit <= 20 {
			return directory, true
		}
		return "", false
	}
	if mgectlProcessDetailExpression.MatchString(expression) || mgectlProcessInfoExpression.MatchString(expression) {
		return directory, true
	}
	return "", false
}

func allowedMGCTLServiceControlDirectory(command string) (string, bool) {
	for _, commandPattern := range []*regexp.Regexp{mgectlStartCommand, mgectlStopCommand, mgectlRestartCommand} {
		if matches := commandPattern.FindStringSubmatch(command); matches != nil {
			return matches[1], true
		}
	}
	return "", false
}

func validateMGCTLDirectory(server monitorconfig.Server, requestedDirectory string) error {
	root := path.Clean(server.InstanceDirectory)
	directory := path.Clean(requestedDirectory)
	if root == "." || root == "/" || !path.IsAbs(root) || !path.IsAbs(directory) || directory == root || !strings.HasPrefix(directory, root+"/") {
		return errors.New("mgectl service directory must be a child of the configured instance_directory")
	}
	return nil
}

func hasDeleteIntent(command string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(command), " "))
	if deleteUtility.MatchString(lower) {
		return true
	}
	fragments := []string{
		"-delete", "os.remove", "os.unlink", "shutil.rmtree", "file.delete",
		": >", "xargs rm", "find /data/tmp -execdir", "[system.io.file]::delete",
	}
	for _, fragment := range fragments {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func internalOpsServer(server monitorconfig.Server) bool {
	host, _, err := net.SplitHostPort(server.Address)
	if err != nil {
		host = server.Address
	}
	ip := net.ParseIP(strings.Trim(host, "[]")).To4()
	return ip != nil && ip[0] == 192 && ip[1] == 168 && ip[2] == 100
}

func (e *DefaultShellExecutor) Execute(parent context.Context, server monitorconfig.Server, request ShellRequest, defaultTimeout time.Duration, maxOutput int) ShellResult {
	timeout := defaultTimeout
	if request.TimeoutSeconds > 0 {
		timeout = time.Duration(request.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	started := time.Now()
	if request.Target == "current-server" {
		result, err := e.remote.RunCommand(ctx, server, request.Command, timeout)
		output, truncated := boundedText(sanitizeShellOutput(result.Output), maxOutput)
		response := ShellResult{Target: request.Target, Output: output, ExitStatus: "success", DurationMS: time.Since(started).Milliseconds(), Truncated: truncated}
		if err != nil {
			response.ExitStatus = "error"
			response.Error = safeShellError(err)
		}
		return response
	}
	return e.executeLocal(ctx, request, started, maxOutput)
}

func (e *DefaultShellExecutor) executeLocal(ctx context.Context, request ShellRequest, started time.Time, maxOutput int) ShellResult {
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", request.Command)
	} else {
		command = exec.CommandContext(ctx, "/bin/sh", "-lc", request.Command)
	}
	command.Dir = e.localWorkdir
	command.Env = filteredEnvironment(os.Environ())
	combined, err := command.CombinedOutput()
	output, truncated := boundedText(sanitizeShellOutput(string(combined)), maxOutput)
	result := ShellResult{Target: request.Target, Output: output, ExitStatus: "success", DurationMS: time.Since(started).Milliseconds(), Truncated: truncated}
	if err != nil {
		result.ExitStatus = "error"
		result.Error = safeShellError(err)
	}
	return result
}

func filteredEnvironment(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		name, _, _ := strings.Cut(value, "=")
		upper := strings.ToUpper(name)
		if strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "PASSPHRASE") || strings.Contains(upper, "API_KEY") {
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered
}

func boundedText(value string, max int) (string, bool) {
	value = strings.ReplaceAll(value, "\x00", "")
	if len(value) <= max {
		return value, false
	}
	return value[:max] + "\n[output truncated]", true
}

var (
	sshPublicKeyOutput = regexp.MustCompile(`(?i)(^|\s)(ssh-rsa|ssh-ed25519|ecdsa-sha2-[^\s]+|ssh-dss)\s+\S+`)
	privateKeyBegin    = regexp.MustCompile(`(?i)-----BEGIN [^-\r\n]*PRIVATE KEY-----`)
	privateKeyEnd      = regexp.MustCompile(`(?i)^\s*-----END [^-\r\n]*PRIVATE KEY-----`)
	sensitiveOutputKV  = regexp.MustCompile(`(?i)["']?\b(api[_-]?key|access[_-]?token|token|password|passwd|passphrase|secret)\b["']?\s*[:=]\s*\S+|["']?\bauthorization\b["']?\s*:\s*(bearer|basic)\s+\S+`)
	processPrivatePath = regexp.MustCompile(`(?i)/proc(/[^\s:]*)?/(environ|cmdline)`)
)

// sanitizeShellOutput prevents protected server identity, credential, and key
// material from reaching the model, event stream, or browser even if a command
// was approved by an administrator or a remote tool returned it unexpectedly.
func sanitizeShellOutput(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	lines := strings.Split(value, "\n")
	result := make([]string, 0, len(lines))
	inPrivateKey := false
	for _, line := range lines {
		if inPrivateKey {
			if privateKeyEnd.MatchString(line) {
				inPrivateKey = false
			}
			continue
		}
		if privateKeyBegin.MatchString(line) {
			result = append(result, "[redacted protected server-private output]")
			inPrivateKey = true
			continue
		}
		lower := strings.ToLower(line)
		if sshPublicKeyOutput.MatchString(line) || sensitiveOutputKV.MatchString(line) || processPrivatePath.MatchString(line) || containsProtectedOutputMarker(lower) {
			result = append(result, "[redacted protected server-private output]")
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

func containsProtectedOutputMarker(lower string) bool {
	markers := []string{
		"/etc/ssh", "/root/.ssh", "/.ssh/", "authorized_keys", "known_hosts", "ssh_host_",
		"/etc/passwd", "/etc/group", "/etc/shadow", "/etc/gshadow", "/etc/environment",
		"api_key=", "access_token=", "token=", "password=", "passwd=", "secret=",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func safeShellError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "command timed out"
	}
	if _, ok := err.(*exec.ExitError); ok {
		return "command exited with a non-zero status"
	}
	return "command execution failed"
}
