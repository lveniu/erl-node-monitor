param()

$ErrorActionPreference = "Stop"

$projectRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$runtimeRoot = Join-Path $projectRoot ".runtime"
$dataRoot = Join-Path $projectRoot "data"
$logRoot = Join-Path $dataRoot "logs"
$secretRoot = Join-Path $projectRoot "secrets"
$grafanaHome = Join-Path $runtimeRoot "grafana-12.1.0"
$grafanaExe = Join-Path $grafanaHome "bin\grafana.exe"
$grafanaConfig = Join-Path $projectRoot "grafana\grafana.local.ini"
$grafanaProvisioning = Join-Path $projectRoot "grafana\provisioning-local"
$grafanaPasswordFile = Join-Path $secretRoot "grafana_admin_password"

if (Get-NetTCPConnection -State Listen -LocalPort 20900 -ErrorAction SilentlyContinue) {
    throw "Port 20900 is already in use. Stop only the existing Grafana process before running this script."
}
if (-not (Test-Path -LiteralPath $grafanaPasswordFile -PathType Leaf)) {
    throw "Grafana admin password file is missing: $grafanaPasswordFile"
}

$savedEnvironment = @{}
$grafanaEnvironment = @{
    "GF_PATHS_DATA" = (Join-Path $dataRoot "grafana")
    "GF_PATHS_LOGS" = $logRoot
    "GF_PATHS_PLUGINS" = (Join-Path $projectRoot "grafana\plugins")
    "GF_PATHS_PROVISIONING" = $grafanaProvisioning
    "GF_SECURITY_ADMIN_PASSWORD" = (Get-Content -Raw -LiteralPath $grafanaPasswordFile).Trim()
    "ERLANG_MONITOR_DASHBOARDS_PATH" = (Join-Path $projectRoot "grafana\dashboards").Replace('\', '/')
    "ERLANG_MONITOR_INTERNAL_DASHBOARDS_PATH" = (Join-Path $projectRoot "grafana\dashboards-internal").Replace('\', '/')
    "ERLANG_MONITOR_QT05_INTERNAL_DASHBOARDS_PATH" = (Join-Path $projectRoot "grafana\dashboards-qt05-internal").Replace('\', '/')
    "ERLANG_MONITOR_QT07_INTERNAL_DASHBOARDS_PATH" = (Join-Path $projectRoot "grafana\dashboards-qt07-internal").Replace('\', '/')
}

$opsAgentTokenFile = Join-Path $secretRoot "ops_agent_tool_api_token"
if (Test-Path -LiteralPath $opsAgentTokenFile -PathType Leaf) {
    $grafanaEnvironment["OPS_AGENT_TOOL_API_TOKEN"] = (Get-Content -Raw -LiteralPath $opsAgentTokenFile).Trim()
}

foreach ($key in $grafanaEnvironment.Keys) {
    $savedEnvironment[$key] = [Environment]::GetEnvironmentVariable($key, "Process")
    [Environment]::SetEnvironmentVariable($key, [string]$grafanaEnvironment[$key], "Process")
}

try {
    $timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
    $stdoutPath = Join-Path $logRoot "grafana-$timestamp.stdout.log"
    $stderrPath = Join-Path $logRoot "grafana-$timestamp.stderr.log"
    $process = Start-Process -FilePath $grafanaExe `
        -ArgumentList @("server", "--homepath=`"$grafanaHome`"", "--config=`"$grafanaConfig`"") `
        -WorkingDirectory $grafanaHome `
        -WindowStyle Hidden `
        -RedirectStandardOutput $stdoutPath `
        -RedirectStandardError $stderrPath `
        -PassThru
}
finally {
    foreach ($key in $grafanaEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($key, $savedEnvironment[$key], "Process")
    }
}

[pscustomobject]@{
    ProcessId = $process.Id
    Stdout = $stdoutPath
    Stderr = $stderrPath
}
