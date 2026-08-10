param(
    [string]$GatewayUrl = "http://127.0.0.1:20904",
    [string]$ServerId,
    [string]$Node,
    [string]$DashboardUid = "erlang-monitor-overview",
    [switch]$ApproveHotspots,
    [switch]$RequireCompaction,
    [string[]]$ProviderErrorSessionId = @(),
    [switch]$RequireProviderErrors,
    [switch]$Execute
)

$ErrorActionPreference = "Stop"
if (-not $Execute) { throw "Real model smoke testing is disabled by default. Re-run with -Execute after reviewing provider cost and model IDs." }
if (-not $ServerId -or -not $Node) { throw "ServerId and Node are required and must come from the current server inventory/discovery." }
if (-not $ApproveHotspots) { throw "Production-gate smoke testing requires -ApproveHotspots so the approved diagnostic resume path is actually exercised." }
$projectRoot = Split-Path -Parent $PSScriptRoot
$tokenPath = Join-Path $projectRoot "secrets\holmes_tool_api_token"
if (-not (Test-Path -LiteralPath $tokenPath -PathType Leaf)) { throw "Missing ignored secrets\holmes_tool_api_token." }
$token = (Get-Content -Raw -LiteralPath $tokenPath).Trim()
if (-not $token) { throw "Holmes tool token is empty." }
$editorHeaders = @{ Authorization = "Bearer $token"; "X-Erlang-Monitor-Role" = "Editor"; "X-Grafana-User" = "real-model-smoke" }
$adminHeaders = @{ Authorization = "Bearer $token"; "X-Erlang-Monitor-Role" = "Admin"; "X-Grafana-User" = "real-model-smoke-admin" }

function New-RequestId { return [guid]::NewGuid().ToString() }

function Assert-GatewayRejectsInvalidAuthentication {
    try {
        Invoke-RestMethod -Headers @{ Authorization = "Bearer intentionally-invalid"; "X-Erlang-Monitor-Role" = "Editor"; "X-Grafana-User" = "real-model-smoke" } -Uri "$GatewayUrl/models" -TimeoutSec 10 | Out-Null
        throw "Gateway accepted an invalid internal token."
    }
    catch {
        if ($_.Exception.Response -and [int]$_.Exception.Response.StatusCode -ne 401) { throw }
    }
}

function Get-ToolEventName($Event) {
    foreach ($property in @("name", "tool_name")) {
        if ($Event.data.$property) { return [string]$Event.data.$property }
    }
    return ""
}

function Assert-InjectedProviderErrors {
    $required = @("HOLMES_AUTH_FAILED", "MODEL_RATE_LIMITED", "HOLMES_TIMEOUT", "MODEL_REQUEST_REJECTED")
    if (-not $RequireProviderErrors) { return }
    if ($ProviderErrorSessionId.Count -lt $required.Count) { throw "Provide fault-injected session IDs covering: $($required -join ', ')." }
    $observed = @()
    foreach ($sessionId in $ProviderErrorSessionId) {
        $session = Invoke-RestMethod -Headers $editorHeaders -Uri "$GatewayUrl/investigations/$sessionId" -TimeoutSec 10
        if ($session.status -ne "failed" -or -not $session.error.code) { throw "Fault-injection session $sessionId is not a normalized failed investigation." }
        $observed += [string]$session.error.code
    }
    foreach ($code in $required) {
        if ($code -notin $observed) { throw "Provider fault-injection evidence is missing normalized code $code." }
    }
}

function Wait-Investigation([string]$SessionId) {
    $deadline = (Get-Date).AddMinutes(6)
    do {
        $session = Invoke-RestMethod -Headers $editorHeaders -Uri "$GatewayUrl/investigations/$SessionId" -TimeoutSec 10
        if ($session.status -eq "awaiting_approval") {
            foreach ($tool in @($session.pending_tools | Where-Object { $_.requires_user -and $null -eq $_.approved })) {
                $approved = $ApproveHotspots.IsPresent
                $decision = @{ request_id = (New-RequestId); tool_call_id = $tool.call_id; approved = $approved } | ConvertTo-Json -Compress
                Invoke-RestMethod -Method Post -Headers $adminHeaders -ContentType "application/json" -Body $decision -Uri "$GatewayUrl/investigations/$SessionId/decisions" -TimeoutSec 10 | Out-Null
            }
        }
        if ($session.status -in @("completed", "failed", "cancelled")) { return $session }
        Start-Sleep -Seconds 2
    } until ((Get-Date) -gt $deadline)
    throw "Investigation timed out."
}

$modelPayload = Invoke-RestMethod -Headers $editorHeaders -Uri "$GatewayUrl/models" -TimeoutSec 10
Assert-GatewayRejectsInvalidAuthentication
Assert-InjectedProviderErrors
$available = @($modelPayload.models | Where-Object available | ForEach-Object alias)
foreach ($model in @("glm", "kimi")) {
    if ($model -notin $available) { throw "Model alias $model is not available from Holmes; verify the account model ID without printing the key." }
    $to = (Get-Date).ToUniversalTime()
    $fromText = $to.AddHours(-1).ToString("o")
    $toText = $to.ToString("o")
    $body = @{
        request_id = (New-RequestId)
        model = $model
        ask = "必须依次完成：先使用 Prometheus 查询当前服务器和节点在最近一小时的指标趋势；再调用 get_node_snapshot；然后请求 get_process_hotspots(reductions, top_n=5) 并等待本次 Admin 审批。收到全部结果后，按结论、证据、反证、置信度、未确认项和建议输出中文 RCA。"
        context = @{ server_id = $ServerId; node = $Node; dashboard_uid = $DashboardUid; from = $fromText; to = $toText; alert_labels = @{} }
    } | ConvertTo-Json -Depth 8 -Compress
    $created = Invoke-RestMethod -Method Post -Headers $editorHeaders -ContentType "application/json" -Body $body -Uri "$GatewayUrl/investigations" -TimeoutSec 10
    $first = Wait-Investigation $created.session_id
    if ($first.status -ne "completed") { throw "$model initial investigation failed with normalized code $($first.error.code)." }
    $toolEvents = @($first.events | Where-Object type -eq "tool_finished")
    if ($toolEvents.Count -lt 2) { throw "$model did not complete the required two tool rounds." }
    $toolNames = @($toolEvents | ForEach-Object { Get-ToolEventName $_ })
    $controlledIndex = -1
    $prometheusIndex = -1
    for ($index = 0; $index -lt $toolEvents.Count; $index++) {
        $name = $toolNames[$index]
        $eventText = $toolEvents[$index] | ConvertTo-Json -Depth 20 -Compress
        if ($prometheusIndex -lt 0 -and ($name -match "(?i)prometheus|promql|query" -or $eventText -match "(?i)prometheus|promql")) { $prometheusIndex = $index }
        if ($controlledIndex -lt 0 -and $name -in @("get_node_snapshot", "get_process_hotspots")) { $controlledIndex = $index }
    }
    if ($prometheusIndex -lt 0 -or $controlledIndex -lt 0 -or $prometheusIndex -ge $controlledIndex) {
        throw "$model did not prove the required Prometheus-then-controlled-diagnostic sequence. Tools: $($toolNames -join ', ')"
    }
    if (-not @($first.events | Where-Object type -eq "approval_required")) { throw "$model did not pause for Admin approval." }
    $hotspot = @($toolEvents | Where-Object { (Get-ToolEventName $_) -eq "get_process_hotspots" }) | Select-Object -Last 1
    if (-not $hotspot -or $hotspot.data.result.status -ne "success") { throw "$model approved hotspot diagnostic did not succeed." }
    $followUp = @{ request_id = (New-RequestId); ask = "基于刚才证据，说明最需要继续观察的一个指标和建议观察窗口。" } | ConvertTo-Json -Compress
    Invoke-RestMethod -Method Post -Headers $editorHeaders -ContentType "application/json" -Body $followUp -Uri "$GatewayUrl/investigations/$($created.session_id)/messages" -TimeoutSec 10 | Out-Null
    $second = Wait-Investigation $created.session_id
    if ($second.status -ne "completed" -or $second.messages.Count -lt 4) { throw "$model follow-up did not preserve the conversation." }
    if ($RequireCompaction -and -not @($second.events | Where-Object type -eq "compaction_completed")) { throw "$model did not reach and recover from the configured context-compaction threshold." }
    Write-Host "$model real smoke passed: two or more tool events plus one follow-up. Session $($created.session_id)" -ForegroundColor Green
}
$token = $null
