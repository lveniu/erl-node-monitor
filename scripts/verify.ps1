param(
    [string]$Toolchain = "go1.22.12"
)

$ErrorActionPreference = "Stop"
$env:GOTOOLCHAIN = $Toolchain
$projectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
try {
    $unformatted = @(gofmt -l cmd internal)
    if ($unformatted.Count -gt 0) {
        throw "Go files need gofmt: $($unformatted -join ', ')"
    }

    go mod tidy
    if ($LASTEXITCODE -ne 0) { throw "go mod tidy failed" }

    $goPackages = @(go list ./... | Where-Object { $_ -notmatch '/data(?:/|$)' })
    if ($LASTEXITCODE -ne 0 -or $goPackages.Count -eq 0) { throw "go package discovery failed" }

    go vet $goPackages
    if ($LASTEXITCODE -ne 0) { throw "go vet failed" }

    go test $goPackages
    if ($LASTEXITCODE -ne 0) { throw "go test failed" }

    New-Item -ItemType Directory -Force bin | Out-Null
    go build -trimpath -o bin/erlang-exporter.exe ./cmd/erlang-exporter
    if ($LASTEXITCODE -ne 0) { throw "erlang-exporter build failed" }
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "0"
    go build -trimpath -o bin/erlang-exporter-linux-amd64 ./cmd/erlang-exporter
    if ($LASTEXITCODE -ne 0) { throw "Linux erlang-exporter build failed" }
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue

    & ./bin/erlang-exporter.exe -config config/servers.example.yml -check-config
    if ($LASTEXITCODE -ne 0) { throw "example server config validation failed" }

    $launcherParseFailed = $false
    Get-ChildItem scripts -Filter "start-*.ps1" -Recurse | ForEach-Object {
        $launcherTokens = $null
        $launcherErrors = $null
        [void][System.Management.Automation.Language.Parser]::ParseFile($_.FullName, [ref]$launcherTokens, [ref]$launcherErrors)
        if ($launcherErrors.Count -gt 0) {
            $launcherParseFailed = $true
            $launcherErrors | ForEach-Object { Write-Error "$($_.Extent.File):$($_.Extent.StartLineNumber) $($_.Message)" }
        }
    }
    if ($launcherParseFailed) { throw "Windows launcher syntax validation failed" }

    & npm --prefix grafana/plugins/erlang-monitor-controls-app test
    if ($LASTEXITCODE -ne 0) { throw "Grafana plugin tests failed" }
    & npm --prefix grafana/plugins/erlang-monitor-controls-app run build
    if ($LASTEXITCODE -ne 0) { throw "Grafana plugin build failed" }

    $pluginConfig = Get-Content -Raw -Encoding UTF8 grafana/plugins/erlang-monitor-controls-app/plugin.json | ConvertFrom-Json
    $pluginPackage = Get-Content -Raw -Encoding UTF8 grafana/plugins/erlang-monitor-controls-app/package.json | ConvertFrom-Json
    if ($pluginConfig.info.version -ne $pluginPackage.version) { throw "Grafana plugin.json and package.json versions do not match" }
    $unexpectedPluginDirs = @(Get-ChildItem grafana/plugins -Directory | Where-Object { $_.Name -ne "erlang-monitor-controls-app" })
    if ($unexpectedPluginDirs.Count -gt 0) { throw "Unused Grafana plugin directories remain active: $($unexpectedPluginDirs.Name -join ', ')" }
    $localDatasourceFiles = @(Get-ChildItem grafana/provisioning-local/datasources -File)
    if ($localDatasourceFiles.Count -ne 1) { throw "Local Grafana must provision exactly one active datasource" }
    $localDatasourceText = Get-Content -Raw -Encoding UTF8 $localDatasourceFiles[0].FullName
    $localDatasourceCount = [regex]::Matches($localDatasourceText, '(?m)^\s*-\s+name:\s*').Count
    if ($localDatasourceCount -ne 1 -or -not $localDatasourceText.Contains('uid: prometheus')) { throw "Prometheus must be the only active local datasource" }
    if (-not ($pluginConfig.includes | Where-Object { $_.path -eq "/a/erlang-monitor-controls-app/overview" -and $_.role -eq "Viewer" })) { throw "Grafana overview Viewer page is missing" }
    $composeText = Get-Content -Raw -Encoding UTF8 compose.yml
    $localLauncherText = Get-Content -Raw -Encoding UTF8 scripts/start-local-monitor.ps1
    $nativeGrafanaConfigText = Get-Content -Raw -Encoding UTF8 grafana/grafana.local.ini
    if (-not $composeText.Contains('GF_DATAPROXY_SEND_USER_HEADER: "true"') -or -not $nativeGrafanaConfigText.Contains('send_user_header = true')) {
        throw "Grafana must forward the authenticated username to backend plugin proxies"
    }
    if (-not $composeText.Contains('GF_PLUGINS_PREINSTALL_DISABLED: "true"') -or -not $nativeGrafanaConfigText.Contains('preinstall_disabled = true')) {
        throw "Grafana automatic plugin preinstallation must remain disabled"
    }
    if (-not $localLauncherText.Contains('--config=`"$grafanaConfig`"')) {
        throw "Windows Grafana launcher must load grafana/grafana.local.ini"
    }
    foreach ($duplicatedSetting in @('GF_USERS_ALLOW_SIGN_UP', 'GF_USERS_DEFAULT_LANGUAGE', 'GF_AUTH_ANONYMOUS_ENABLED', 'GF_DATAPROXY_SEND_USER_HEADER', 'GF_PLUGINS_PREINSTALL_DISABLED')) {
        if ($localLauncherText.Contains($duplicatedSetting)) {
            throw "Windows launcher duplicates shared Grafana setting: $duplicatedSetting"
        }
    }
    foreach ($platformSpecificMarker in @('/home/qt/node_monitor', 'admin_password', 'root_url =', 'domain =')) {
        if ($nativeGrafanaConfigText.Contains($platformSpecificMarker)) {
            throw "Shared native Grafana config contains platform or Secret-specific setting: $platformSpecificMarker"
        }
    }

    $localServersText = Get-Content -Raw -Encoding UTF8 config/servers.native.yml
    $gitIgnoreText = Get-Content -Raw -Encoding UTF8 .gitignore
    if ($gitIgnoreText -notmatch '(?m)^config/servers\.local\.yml\s*$') {
        throw "Legacy machine-local server config must remain excluded from version control"
    }
    if ($gitIgnoreText -match '(?m)^config/servers\.native\.yml\s*$') {
        throw "Shared native server config must not be excluded from version control"
    }
    if ($localServersText -match '(?m)^\s+(?:ssh_key_file|private_key_file|private_key_passphrase_file):\s*["'']?[A-Za-z]:[/\\]') {
        throw "Shared server config contains an active Windows-absolute SSH credential path"
    }
    if (-not $localServersText.Contains('ssh_key_file: "secrets/ssh/qthy@liujinxin.pub"')) {
        throw "Shared server config must select the external SSH Agent identity by project-relative public-key path"
    }

    $checksumLines = @(Get-Content -LiteralPath linux/checksums.sha256 -Encoding ASCII | Where-Object { $_.Trim() })
    foreach ($checksumLine in $checksumLines) {
        if ($checksumLine -notmatch '^([0-9a-f]{64})  (.+)$') { throw "Malformed checksum manifest line: $checksumLine" }
        $expectedHash = $Matches[1]
        $artifactPath = Join-Path "linux" $Matches[2]
        if (-not (Test-Path -LiteralPath $artifactPath -PathType Leaf)) { throw "Checksummed artifact is missing: $artifactPath" }
        $actualHash = (Get-FileHash -LiteralPath $artifactPath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actualHash -ne $expectedHash) { throw "Checksum mismatch: $artifactPath" }
    }
    foreach ($requiredArtifact in @(
        "packages/prometheus-3.5.0.linux-amd64.tar.gz",
        "packages/alertmanager-0.28.1.linux-amd64.tar.gz",
        "packages/grafana-12.1.0.linux-amd64.tar.gz",
        "bin/erlang-exporter",
        "bin/ops-agent"
    )) {
        if (-not ($checksumLines | Where-Object { $_.EndsWith("  $requiredArtifact") })) {
            throw "Required Linux artifact is absent from linux/checksums.sha256: $requiredArtifact"
        }
    }

    $baseRuntimeInstallerText = Get-Content -Raw -Encoding UTF8 linux/install-runtime.sh
    foreach ($baseRuntimeArchive in @(
        'packages/prometheus-${PROMETHEUS_VERSION}.linux-amd64.tar.gz',
        'packages/alertmanager-${ALERTMANAGER_VERSION}.linux-amd64.tar.gz',
        'packages/grafana-${GRAFANA_VERSION}.linux-amd64.tar.gz'
    )) {
        if (-not $baseRuntimeInstallerText.Contains($baseRuntimeArchive)) {
            throw "Base runtime installer checksum selector is missing: $baseRuntimeArchive"
        }
    }

    $nativeGrafanaLauncherText = Get-Content -Raw -Encoding UTF8 linux/run-grafana.sh
    if (-not $nativeGrafanaLauncherText.Contains('OPS_AGENT_TOOL_TOKEN_FILE') -or -not $nativeGrafanaLauncherText.Contains('export OPS_AGENT_TOOL_API_TOKEN')) {
        throw "Native Grafana launcher must inject the Ops Agent proxy token"
    }
    $nativeOpsUnitText = Get-Content -Raw -Encoding UTF8 linux/systemd/erlang-monitor-ops-agent.service
    if (-not $nativeOpsUnitText.Contains('secrets/ops_agent_tool_api_token')) {
        throw "Native Ops Agent unit must use its dedicated proxy token"
    }

    $ruleText = Get-Content -Raw -Encoding UTF8 prometheus/rules/erlang-alerts.yml
    $alertNames = [regex]::Matches($ruleText, '(?m)^\s+- alert:\s*([A-Za-z0-9_]+)\s*$') | ForEach-Object { $_.Groups[1].Value }
    if (@($alertNames).Count -ne 16) { throw "Expected 16 authoritative Erlang alert rules, found $(@($alertNames).Count)" }
    $secretPattern = '(?i)sk-[a-z0-9_-]{24,}|Bearer\s+[a-z0-9._-]{32,}'
    $secretLeak = & rg -n --no-messages --glob "*.go" --glob "*.js" --glob "*.json" --glob "*.yml" --glob "*.yaml" --glob "*.ps1" --glob "*.md" --glob "!secrets/**" --glob "!.runtime/**" --glob "!data/**" --glob "!bin/**" --glob "!logs/**" --glob "!grafana/plugins/grafana-*/**" $secretPattern
    if (@($secretLeak).Count -gt 0) { throw "Potential API key material found outside ignored secret directories." }

    & powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/dev/start-erlang-exporter.ps1 -Config ./config/servers.example.yml -WebhookFile ./config/examples/dingtalk-webhook.example.txt -CheckOnly
    if ($LASTEXITCODE -ne 0) { throw "Integrated monitor launcher configuration check failed" }

    $localRuntimeInstaller = Join-Path $projectRoot "scripts\install-local-runtime.ps1"
    $localRuntimeReady = (Test-Path -LiteralPath (Join-Path $projectRoot ".runtime\prometheus-3.5.0\promtool.exe")) -and
        (Test-Path -LiteralPath (Join-Path $projectRoot ".runtime\alertmanager-0.28.1\amtool.exe")) -and
        (Test-Path -LiteralPath (Join-Path $projectRoot ".runtime\grafana-12.1.0\bin\grafana.exe"))
    if ($localRuntimeReady) {
        & powershell -NoProfile -ExecutionPolicy Bypass -File $localRuntimeInstaller -CheckOnly
        if ($LASTEXITCODE -ne 0) { throw "Local native runtime check failed" }
        & powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/start-local-monitor.ps1 -Config ./config/servers.example.yml -WebhookFile ./config/examples/dingtalk-webhook.example.txt -SkipInstall -CheckOnly
        if ($LASTEXITCODE -ne 0) { throw "Full local monitor launcher configuration check failed" }
    }
    else {
        Write-Warning "Local native runtime is not installed; full-stack launcher runtime check was skipped."
    }

    $cmdLaunchers = @(Get-ChildItem -LiteralPath $projectRoot -Filter "*.cmd")
    if ($cmdLaunchers.Count -ne 1) { throw "Expected one integrated Windows CMD launcher" }
    foreach ($launcher in $cmdLaunchers) {
        $launcherText = [IO.File]::ReadAllText($launcher.FullName, [Text.Encoding]::UTF8)
        if (-not $launcherText.Contains("powershell.exe") -or -not $launcherText.Contains("-NoExit") -or -not $launcherText.Contains("-File")) {
            throw "$($launcher.Name) does not keep its PowerShell window open"
        }
        if (-not $launcherText.Contains("start-local-monitor.ps1")) {
            throw "$($launcher.Name) does not start the full local monitoring platform"
        }
    }

    Get-Content -Raw -Encoding UTF8 grafana/dashboards/erlang-overview.json | ConvertFrom-Json | Out-Null
    foreach ($localConfig in @("prometheus/prometheus.local.yml", "alertmanager/alertmanager.local.yml", "grafana/grafana.local.ini", "grafana/provisioning-local/datasources/prometheus.yml", "grafana/provisioning-local/dashboards/dashboards.yml")) {
        if (-not (Test-Path -LiteralPath $localConfig -PathType Leaf)) { throw "Missing local platform config: $localConfig" }
    }

    if (Get-Command docker -ErrorAction SilentlyContinue) {
        docker compose -f compose.yml config --quiet
        if ($LASTEXITCODE -ne 0) { throw "Docker Compose validation failed" }
    } else {
        Write-Warning "Docker is unavailable; Compose runtime validation was skipped."
    }

    Write-Host "Local verification passed."
}
finally {
    Pop-Location
}
