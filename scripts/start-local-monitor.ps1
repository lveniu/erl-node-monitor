param(
    [string]$Config = "",
    [string]$WebhookFile = "",
    [switch]$SkipDingTalk,
    [switch]$SkipInstall,
    [switch]$NoBrowser,
    [switch]$SmokeTest,
    [switch]$CheckOnly
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$runtimeRoot = Join-Path $projectRoot ".runtime"
$dataRoot = Join-Path $projectRoot "data"
$logRoot = Join-Path $dataRoot "logs"
$secretRoot = Join-Path $projectRoot "secrets"
$exporterExe = Join-Path $projectRoot "bin\erlang-exporter.exe"
$opsAgentExe = Join-Path $projectRoot "bin\ops-agent.exe"
$opsAgentConfig = Join-Path $projectRoot "ops-agent\config.local.yml"
$opsAgentModelKeyFile = Join-Path $secretRoot "ops_agent_model_api_key"
$opsAgentToolTokenFile = Join-Path $secretRoot "ops_agent_tool_api_token"
$glmKeyFile = Join-Path $secretRoot "glm_api_key"
$holmesToolTokenFile = Join-Path $secretRoot "holmes_tool_api_token"
$prometheusExe = Join-Path $runtimeRoot "prometheus-3.5.0\prometheus.exe"
$promtoolExe = Join-Path $runtimeRoot "prometheus-3.5.0\promtool.exe"
$alertmanagerExe = Join-Path $runtimeRoot "alertmanager-0.28.1\alertmanager.exe"
$amtoolExe = Join-Path $runtimeRoot "alertmanager-0.28.1\amtool.exe"
$grafanaHome = Join-Path $runtimeRoot "grafana-12.1.0"
$grafanaExe = Join-Path $grafanaHome "bin\grafana.exe"
$grafanaConfig = Join-Path $projectRoot "grafana\grafana.local.ini"
$prometheusConfig = Join-Path $projectRoot "prometheus\prometheus.local.yml"
$alertmanagerConfig = Join-Path $projectRoot "alertmanager\alertmanager.local.yml"
$grafanaProvisioning = Join-Path $projectRoot "grafana\provisioning-local"
$grafanaDashboards = Join-Path $projectRoot "grafana\dashboards"
$grafanaInternalDashboards = Join-Path $projectRoot "grafana\dashboards-internal"
$grafanaQt05InternalDashboards = Join-Path $projectRoot "grafana\dashboards-qt05-internal"
$grafanaQt07InternalDashboards = Join-Path $projectRoot "grafana\dashboards-qt07-internal"
$managedProcesses = New-Object System.Collections.ArrayList

function Resolve-ServerConfig {
    if (-not [string]::IsNullOrWhiteSpace($Config)) {
        return [IO.Path]::GetFullPath((Join-Path $projectRoot $Config))
    }

    $localConfig = Join-Path $projectRoot "config\servers.native.yml"
    if (Test-Path -LiteralPath $localConfig -PathType Leaf) {
        return $localConfig
    }

    $productionConfig = Join-Path $projectRoot "config\servers.yml"
    if (Test-Path -LiteralPath $productionConfig -PathType Leaf) {
        return $productionConfig
    }

    throw "Server config is missing. Copy config\servers.example.yml to config\servers.yml and configure it."
}

function Get-HttpStatus {
    param(
        [string]$Uri,
        [int]$TimeoutSeconds = 3
    )

    try {
        $response = Invoke-WebRequest -UseBasicParsing -Uri $Uri -TimeoutSec $TimeoutSeconds
        return [int]$response.StatusCode
    }
    catch {
        return 0
    }
}

function Wait-HttpReady {
    param(
        [string]$Name,
        [string]$Uri,
        [int]$TimeoutSeconds = 60,
        [System.Diagnostics.Process]$Process = $null
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        if ($Process -and $Process.HasExited) {
            throw "$Name exited before becoming ready (exit code $($Process.ExitCode)). Check data\logs."
        }
        if ((Get-HttpStatus -Uri $Uri) -eq 200) {
            Write-Host "$Name is ready: $Uri" -ForegroundColor Green
            return
        }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)

    throw "$Name did not become ready within $TimeoutSeconds seconds. Check data\logs."
}

function Wait-PrometheusTargets {
    param([int]$TimeoutSeconds = 75)

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        try {
            $targetsResponse = Invoke-RestMethod -Uri "http://127.0.0.1:20901/api/v1/targets" -TimeoutSec 5
            $requiredJobs = @("prometheus", "erlang-exporter")
            $healthyJobs = @($targetsResponse.data.activeTargets | Where-Object { $_.health -eq "up" } | ForEach-Object { $_.labels.job })
            $missingJobs = @($requiredJobs | Where-Object { $healthyJobs -notcontains $_ })
            if ($missingJobs.Count -eq 0) {
                Write-Host "Prometheus targets are up: $($requiredJobs -join ', ')" -ForegroundColor Green
                return
            }
        }
        catch {
            $missingJobs = @("prometheus", "erlang-exporter")
        }
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $deadline)

    throw "Prometheus targets did not become healthy: $($missingJobs -join ', ')"
}

function Wait-GrafanaDashboard {
    param([int]$TimeoutSeconds = 45)

    $requiredDashboards = @(
        [pscustomobject]@{ Uid = "erlang-monitor-overview"; FolderUid = "" },
        [pscustomobject]@{ Uid = "erlang-monitor-internal-192-168-100-23"; FolderUid = "dfu4oqx4rqmm8f" },
        [pscustomobject]@{ Uid = "erlang-monitor-internal-192-168-100-25"; FolderUid = "dfu4oqx4rqmm8f" },
        [pscustomobject]@{ Uid = "erlang-qt05-192-168-100-33"; FolderUid = "dfu56gegpvqbkc" },
        [pscustomobject]@{ Uid = "erlang-qt05-192-168-100-37"; FolderUid = "dfu56gegpvqbkc" },
        [pscustomobject]@{ Uid = "erlang-qt07-192-168-100-47"; FolderUid = "dfu57gegpvqbkc" },
        [pscustomobject]@{ Uid = "erlang-qt07-192-168-100-48"; FolderUid = "dfu57gegpvqbkc" }
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        $missingDashboards = @()
        foreach ($required in $requiredDashboards) {
            try {
                $response = Invoke-RestMethod -Uri "http://127.0.0.1:20900/api/dashboards/uid/$($required.Uid)" -TimeoutSec 5
                if (-not $response.dashboard -or ($required.FolderUid -and $response.meta.folderUid -ne $required.FolderUid)) {
                    $missingDashboards += $required.Uid
                }
            }
            catch {
                $missingDashboards += $required.Uid
            }
        }
        if ($missingDashboards.Count -eq 0) {
            Write-Host "Grafana dashboards are provisioned: $($requiredDashboards.Uid -join ', ')" -ForegroundColor Green
            return
        }
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $deadline)

    throw "Grafana dashboards were not provisioned: $($missingDashboards -join ', ')"
}

function Get-PortOwner {
    param([int]$Port)

    $listener = Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $listener) {
        return $null
    }
    return Get-Process -Id $listener.OwningProcess -ErrorAction SilentlyContinue
}

function Start-ManagedProcess {
    param(
        [string]$Name,
        [string]$FilePath,
        [string[]]$ArgumentList,
        [string]$WorkingDirectory,
        [hashtable]$Environment = @{}
    )

    $savedEnvironment = @{}
    foreach ($key in $Environment.Keys) {
        $savedEnvironment[$key] = [Environment]::GetEnvironmentVariable($key, "Process")
        [Environment]::SetEnvironmentVariable($key, [string]$Environment[$key], "Process")
    }

    try {
        $timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
        $stdoutPath = Join-Path $logRoot "$Name-$timestamp.stdout.log"
        $stderrPath = Join-Path $logRoot "$Name-$timestamp.stderr.log"
        $process = Start-Process -FilePath $FilePath -ArgumentList $ArgumentList -WorkingDirectory $WorkingDirectory -WindowStyle Hidden -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath -PassThru
    }
    finally {
        foreach ($key in $Environment.Keys) {
            if ($null -eq $savedEnvironment[$key]) {
                [Environment]::SetEnvironmentVariable($key, $null, "Process")
            }
            else {
                [Environment]::SetEnvironmentVariable($key, [string]$savedEnvironment[$key], "Process")
            }
        }
    }

    [void]$managedProcesses.Add([pscustomobject]@{
        Name = $Name
        Process = $process
        Executable = [IO.Path]::GetFullPath($FilePath)
    })
    Write-Host "Started $Name (PID $($process.Id))." -ForegroundColor Cyan
    return $process
}

function Stop-ManagedProcesses {
    for ($index = $managedProcesses.Count - 1; $index -ge 0; $index--) {
        $entry = $managedProcesses[$index]
        $process = Get-Process -Id $entry.Process.Id -ErrorAction SilentlyContinue
        if (-not $process) {
            continue
        }

        $actualPath = [IO.Path]::GetFullPath($process.Path)
        if (-not [string]::Equals($actualPath, $entry.Executable, [StringComparison]::OrdinalIgnoreCase)) {
            Write-Warning "PID $($process.Id) no longer matches $($entry.Name); it was not stopped."
            continue
        }

        Stop-Process -Id $process.Id -ErrorAction SilentlyContinue
        Wait-Process -Id $process.Id -Timeout 10 -ErrorAction SilentlyContinue
        Write-Host "Stopped $($entry.Name) (PID $($process.Id))." -ForegroundColor DarkGray
    }
}

function Ensure-DingTalkWebhook {
    if ($SkipDingTalk) {
        return $null
    }

    if ([string]::IsNullOrWhiteSpace($WebhookFile)) {
        $script:WebhookFile = Join-Path $secretRoot "dingtalk_webhook_url"
    }
    else {
        $script:WebhookFile = [IO.Path]::GetFullPath((Join-Path $projectRoot $WebhookFile))
    }

    $webhookMissing = -not (Test-Path -LiteralPath $script:WebhookFile -PathType Leaf)
    if (-not $webhookMissing) {
        $webhookMissing = [string]::IsNullOrWhiteSpace((Get-Content -Raw -LiteralPath $script:WebhookFile -ErrorAction SilentlyContinue))
    }

    if ($webhookMissing) {
        New-Item -ItemType Directory -Force -Path $secretRoot | Out-Null
        Write-Host "First run: paste the DingTalk robot webhook (input is hidden):" -ForegroundColor Yellow
        $secureWebhook = Read-Host -AsSecureString
        $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureWebhook)
        try {
            $webhook = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
        }
        finally {
            [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
        }
        if ([string]::IsNullOrWhiteSpace($webhook)) {
            throw "Webhook cannot be empty."
        }
        [IO.File]::WriteAllText($script:WebhookFile, $webhook, [Text.UTF8Encoding]::new($false))
        $webhook = $null
        Write-Host "Webhook saved under the Git-ignored secrets directory." -ForegroundColor Green
    }

    $webhookValue = (Get-Content -Raw -LiteralPath $script:WebhookFile).Trim()
    $uri = $null
    if (-not [Uri]::TryCreate($webhookValue, [UriKind]::Absolute, [ref]$uri) -or $uri.Scheme -ne "https" -or $uri.Host -ne "oapi.dingtalk.com") {
        throw "Webhook must be a valid https://oapi.dingtalk.com/ URL."
    }
    $webhookValue = $null
    return $script:WebhookFile
}

function Ensure-GrafanaPassword {
    $passwordFile = Join-Path $secretRoot "grafana_admin_password"
    if (-not (Test-Path -LiteralPath $passwordFile -PathType Leaf) -or [string]::IsNullOrWhiteSpace((Get-Content -Raw -LiteralPath $passwordFile -ErrorAction SilentlyContinue))) {
        New-Item -ItemType Directory -Force -Path $secretRoot | Out-Null
        $randomBytes = New-Object byte[] 24
        $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
        try {
            $generator.GetBytes($randomBytes)
        }
        finally {
            $generator.Dispose()
        }
        $password = [Convert]::ToBase64String($randomBytes).TrimEnd('=').Replace('+', 'A').Replace('/', 'B')
        [IO.File]::WriteAllText($passwordFile, $password, [Text.UTF8Encoding]::new($false))
        $password = $null
        Write-Host "Generated a Grafana admin password under the Git-ignored secrets directory." -ForegroundColor Green
    }
    return $passwordFile
}

function Assert-LocalConfiguration {
    param([string]$ServerConfig)

    foreach ($requiredFile in @($exporterExe, $prometheusConfig, $alertmanagerConfig, $grafanaConfig, $grafanaProvisioning, $grafanaDashboards)) {
        if (-not (Test-Path -LiteralPath $requiredFile)) {
            throw "Required local monitoring file is missing: $requiredFile"
        }
    }

    if ((Test-Path -LiteralPath $opsAgentExe -PathType Leaf) -and (Test-Path -LiteralPath $opsAgentConfig -PathType Leaf)) {
        & $opsAgentExe -config $opsAgentConfig -servers $ServerConfig -check-config
        if ($LASTEXITCODE -ne 0) { throw "Ops Agent configuration validation failed" }
    }

    & $exporterExe -config $ServerConfig -check-config
    if ($LASTEXITCODE -ne 0) { throw "Server config validation failed" }
    & $promtoolExe check config $prometheusConfig
    if ($LASTEXITCODE -ne 0) { throw "Local Prometheus config validation failed" }
    & $amtoolExe check-config $alertmanagerConfig
    if ($LASTEXITCODE -ne 0) { throw "Local Alertmanager config validation failed" }
}

Set-Location $projectRoot
$serverConfig = Resolve-ServerConfig

if (-not $SkipInstall) {
    & (Join-Path $PSScriptRoot "install-local-runtime.ps1")
}
else {
    & (Join-Path $PSScriptRoot "install-local-runtime.ps1") -CheckOnly
}

Assert-LocalConfiguration -ServerConfig $serverConfig

if ($CheckOnly) {
    Write-Host "Full local monitor launcher configuration is valid." -ForegroundColor Green
    return
}

$webhookPath = Ensure-DingTalkWebhook
$grafanaPasswordFile = Ensure-GrafanaPassword
New-Item -ItemType Directory -Force -Path $dataRoot, $logRoot, (Join-Path $dataRoot "prometheus"), (Join-Path $dataRoot "alertmanager"), (Join-Path $dataRoot "grafana"), (Join-Path $dataRoot "grafana-plugins") | Out-Null

try {
    $opsAgentEnabled = $false
    $opsAgentKeyPath = $null
    $opsAgentTokenPath = $null
    if (Test-Path -LiteralPath $opsAgentToolTokenFile -PathType Leaf) {
        $opsAgentTokenPath = $opsAgentToolTokenFile
    }
    elseif (Test-Path -LiteralPath $holmesToolTokenFile -PathType Leaf) {
        $opsAgentTokenPath = $holmesToolTokenFile
        Write-Warning "Ops Agent is using secrets\holmes_tool_api_token as its tool token fallback; create secrets\ops_agent_tool_api_token to make this explicit."
    }
    if ((Test-Path -LiteralPath $opsAgentExe -PathType Leaf) -and (Test-Path -LiteralPath $opsAgentConfig -PathType Leaf) -and $opsAgentTokenPath) {
        if (Test-Path -LiteralPath $opsAgentModelKeyFile -PathType Leaf) {
            $opsAgentKeyPath = $opsAgentModelKeyFile
        }
        elseif (Test-Path -LiteralPath $glmKeyFile -PathType Leaf) {
            $opsAgentKeyPath = $glmKeyFile
            Write-Warning "Ops Agent is using secrets\glm_api_key as its model key fallback; create secrets\ops_agent_model_api_key to make this explicit."
        }
        if ($opsAgentKeyPath) {
            $opsAgentEnvironment = @{
                "OPS_AGENT_MODEL_API_KEY" = (Get-Content -Raw -LiteralPath $opsAgentKeyPath).Trim()
                "OPS_AGENT_TOOL_API_TOKEN" = (Get-Content -Raw -LiteralPath $opsAgentTokenPath).Trim()
            }
            if ([string]::IsNullOrWhiteSpace($opsAgentEnvironment["OPS_AGENT_MODEL_API_KEY"]) -or [string]::IsNullOrWhiteSpace($opsAgentEnvironment["OPS_AGENT_TOOL_API_TOKEN"])) {
                Write-Warning "Ops Agent secrets are empty; skipping Ops Agent startup."
            }
            else {
                $opsAgentProcess = Start-ManagedProcess -Name "ops-agent" -FilePath $opsAgentExe -ArgumentList @("-config", "`"$opsAgentConfig`"", "-servers", "`"$serverConfig`"", "-listen", "127.0.0.1:20906") -WorkingDirectory $projectRoot -Environment $opsAgentEnvironment
                Wait-HttpReady -Name "Ops Agent" -Uri "http://127.0.0.1:20906/healthz" -TimeoutSeconds 20 -Process $opsAgentProcess
                $opsAgentEnabled = $true
            }
        }
    }
    if (-not $opsAgentEnabled) {
        Write-Warning "Ops Agent is not started. Build bin\ops-agent.exe and configure ops-agent\config.local.yml plus secrets\ops_agent_tool_api_token and a model key."
    }

    $exporterOwner = Get-PortOwner -Port 20903
    if ($exporterOwner) {
        Wait-HttpReady -Name "Erlang Exporter (existing PID $($exporterOwner.Id))" -Uri "http://127.0.0.1:20903/healthz" -TimeoutSeconds 10
        Write-Host "Reusing the existing healthy Exporter; this launcher will not stop it." -ForegroundColor Yellow
    }
    else {
        $exporterEnvironment = @{}
        if (-not $SkipDingTalk) {
            $exporterEnvironment["DINGTALK_WEBHOOK_URL_FILE"] = $webhookPath
            $signingSecretFile = Join-Path $secretRoot "dingtalk_secret"
            if (Test-Path -LiteralPath $signingSecretFile -PathType Leaf) {
                $exporterEnvironment["DINGTALK_SECRET_FILE"] = $signingSecretFile
            }
            $atMobilesFile = Join-Path $secretRoot "dingtalk_at_mobiles"
            if (Test-Path -LiteralPath $atMobilesFile -PathType Leaf) {
                $exporterEnvironment["DINGTALK_AT_MOBILES_FILE"] = $atMobilesFile
            }
            $atUserIdsFile = Join-Path $secretRoot "dingtalk_at_user_ids"
            if (Test-Path -LiteralPath $atUserIdsFile -PathType Leaf) {
                $exporterEnvironment["DINGTALK_AT_USER_IDS_FILE"] = $atUserIdsFile
            }
            if (-not $exporterEnvironment.ContainsKey("DINGTALK_AT_MOBILES_FILE") -and -not $exporterEnvironment.ContainsKey("DINGTALK_AT_USER_IDS_FILE")) {
                Write-Warning "DingTalk alerts will be sent without @ recipients. Add secrets\dingtalk_at_mobiles or secrets\dingtalk_at_user_ids, then restart the monitor."
            }
        }
        $exporterProcess = Start-ManagedProcess -Name "erlang-exporter" -FilePath $exporterExe -ArgumentList @("-config", "`"$serverConfig`"", "-listen", "127.0.0.1:20903", "-status-file", "`"$(Join-Path $dataRoot 'exporter-status.json')`"", "-dingtalk-status-file", "`"$(Join-Path $dataRoot 'dingtalk-status.json')`"") -WorkingDirectory $projectRoot -Environment $exporterEnvironment
        Wait-HttpReady -Name "Erlang Exporter" -Uri "http://127.0.0.1:20903/healthz" -Process $exporterProcess
    }

    $alertmanagerOwner = Get-PortOwner -Port 20902
    if ($alertmanagerOwner) {
        Wait-HttpReady -Name "Alertmanager (existing PID $($alertmanagerOwner.Id))" -Uri "http://127.0.0.1:20902/-/ready" -TimeoutSeconds 10
    }
    else {
        $alertmanagerProcess = Start-ManagedProcess -Name "alertmanager" -FilePath $alertmanagerExe -ArgumentList @("--config.file=`"$alertmanagerConfig`"", "--storage.path=`"$(Join-Path $dataRoot 'alertmanager')`"", "--web.listen-address=127.0.0.1:20902") -WorkingDirectory (Split-Path -Parent $alertmanagerConfig)
        Wait-HttpReady -Name "Alertmanager" -Uri "http://127.0.0.1:20902/-/ready" -Process $alertmanagerProcess
    }

    $prometheusOwner = Get-PortOwner -Port 20901
    if ($prometheusOwner) {
        Wait-HttpReady -Name "Prometheus (existing PID $($prometheusOwner.Id))" -Uri "http://127.0.0.1:20901/-/ready" -TimeoutSeconds 10
    }
    else {
        $prometheusProcess = Start-ManagedProcess -Name "prometheus" -FilePath $prometheusExe -ArgumentList @("--config.file=`"$prometheusConfig`"", "--storage.tsdb.path=`"$(Join-Path $dataRoot 'prometheus')`"", "--storage.tsdb.retention.time=30d", "--web.listen-address=127.0.0.1:20901", "--web.enable-lifecycle") -WorkingDirectory (Split-Path -Parent $prometheusConfig)
        Wait-HttpReady -Name "Prometheus" -Uri "http://127.0.0.1:20901/-/ready" -Process $prometheusProcess
    }

    $grafanaOwner = Get-PortOwner -Port 20900
    if ($grafanaOwner) {
        Wait-HttpReady -Name "Grafana (existing PID $($grafanaOwner.Id))" -Uri "http://127.0.0.1:20900/api/health" -TimeoutSeconds 10
    }
    else {
        $grafanaEnvironment = @{
            "GF_PATHS_DATA" = (Join-Path $dataRoot "grafana")
            "GF_PATHS_LOGS" = $logRoot
            "GF_PATHS_PLUGINS" = (Join-Path $projectRoot "grafana\plugins")
            "GF_PATHS_PROVISIONING" = $grafanaProvisioning
            "GF_SECURITY_ADMIN_PASSWORD" = (Get-Content -Raw -LiteralPath $grafanaPasswordFile).Trim()
            "ERLANG_MONITOR_DASHBOARDS_PATH" = $grafanaDashboards.Replace('\', '/')
            "ERLANG_MONITOR_INTERNAL_DASHBOARDS_PATH" = $grafanaInternalDashboards.Replace('\', '/')
            "ERLANG_MONITOR_QT05_INTERNAL_DASHBOARDS_PATH" = $grafanaQt05InternalDashboards.Replace('\', '/')
            "ERLANG_MONITOR_QT07_INTERNAL_DASHBOARDS_PATH" = $grafanaQt07InternalDashboards.Replace('\', '/')
        }
        if (Test-Path -LiteralPath $holmesToolTokenFile -PathType Leaf) {
            $grafanaEnvironment["HOLMES_TOOL_API_TOKEN"] = (Get-Content -Raw -LiteralPath $holmesToolTokenFile).Trim()
        }
        if ($opsAgentEnabled) {
            $grafanaEnvironment["OPS_AGENT_TOOL_API_TOKEN"] = (Get-Content -Raw -LiteralPath $opsAgentTokenPath).Trim()
        }
        $grafanaProcess = Start-ManagedProcess -Name "grafana" -FilePath $grafanaExe -ArgumentList @("server", "--homepath=`"$grafanaHome`"", "--config=`"$grafanaConfig`"") -WorkingDirectory $grafanaHome -Environment $grafanaEnvironment
        $grafanaEnvironment["GF_SECURITY_ADMIN_PASSWORD"] = $null
        $grafanaEnvironment["HOLMES_TOOL_API_TOKEN"] = $null
        $grafanaEnvironment["OPS_AGENT_TOOL_API_TOKEN"] = $null
        Wait-HttpReady -Name "Grafana" -Uri "http://127.0.0.1:20900/api/health" -TimeoutSeconds 90 -Process $grafanaProcess
    }

    if (-not $SkipDingTalk) {
        Wait-HttpReady -Name "DingTalk module" -Uri "http://127.0.0.1:20903/dingtalk/healthz" -TimeoutSeconds 10
    }
    Wait-PrometheusTargets
    Wait-GrafanaDashboard

    Write-Host "" 
    Write-Host "Full local monitoring platform is ready." -ForegroundColor Green
    Write-Host "Grafana:      http://127.0.0.1:20900" -ForegroundColor Green
    Write-Host "Prometheus:   http://127.0.0.1:20901" -ForegroundColor DarkGray
    Write-Host "Alertmanager: http://127.0.0.1:20902" -ForegroundColor DarkGray
    Write-Host "Exporter:     http://127.0.0.1:20903/status" -ForegroundColor DarkGray
    if ($opsAgentEnabled) {
        Write-Host "Ops Agent:    http://127.0.0.1:20906/healthz" -ForegroundColor DarkGray
    }

    if ($SmokeTest) {
        Write-Host "Local full-stack smoke test passed." -ForegroundColor Green
        return
    }

    if (-not $NoBrowser) {
        # Open Grafana first, then let the preloaded controls plugin navigate
        # inside the SPA. A cold direct dashboard load can resolve Grafana API
        # requests relative to /d/<uid>/ before the plugin is initialized.
        Start-Process "http://127.0.0.1:20900/?erlang-monitor-dashboard=erlang-monitor-overview&kiosk"
    }

    Write-Host "Press Ctrl+C to stop the services started by this window." -ForegroundColor DarkGray
    while ($true) {
        Start-Sleep -Seconds 2
        foreach ($entry in $managedProcesses) {
            if ($entry.Process.HasExited) {
                throw "$($entry.Name) exited unexpectedly with code $($entry.Process.ExitCode). Check data\logs."
            }
        }
    }
}
finally {
    Stop-ManagedProcesses
    Set-Location $projectRoot
}
