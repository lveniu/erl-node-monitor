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
    go build -trimpath -o bin/holmes-gateway.exe ./cmd/holmes-gateway
    if ($LASTEXITCODE -ne 0) { throw "holmes-gateway build failed" }
    go build -trimpath -o bin/holmes-diagnostic-smoke.exe ./cmd/holmes-diagnostic-smoke
    if ($LASTEXITCODE -ne 0) { throw "holmes-diagnostic-smoke build failed" }
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "0"
    go build -trimpath -o bin/erlang-exporter-linux-amd64 ./cmd/erlang-exporter
    if ($LASTEXITCODE -ne 0) { throw "Linux erlang-exporter build failed" }
    go build -trimpath -o bin/holmes-gateway-linux-amd64 ./cmd/holmes-gateway
    if ($LASTEXITCODE -ne 0) { throw "Linux holmes-gateway build failed" }
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue

    & ./bin/erlang-exporter.exe -config config/servers.example.yml -check-config
    if ($LASTEXITCODE -ne 0) { throw "example server config validation failed" }
    & ./bin/holmes-gateway.exe -config holmes/gateway.example.yml -servers config/servers.example.yml -check-config
    if ($LASTEXITCODE -ne 0) { throw "local Holmes gateway config validation failed" }
    & ./bin/holmes-gateway.exe -config holmes/gateway.container.yml -servers config/servers.example.yml -check-config
    if ($LASTEXITCODE -ne 0) { throw "container Holmes gateway config validation failed" }
    & ./bin/holmes-gateway.exe -config holmes/gateway.native.yml -servers config/servers.native.yml -check-config
    if ($LASTEXITCODE -ne 0) { throw "native Holmes gateway config validation failed" }

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
    if ($pluginConfig.includes | Where-Object { $_.path -eq "/a/erlang-monitor-controls-app/holmes" }) { throw "Grafana Holmes page must remain hidden" }
    if ($pluginConfig.routes | Where-Object { $_.path -in @("holmes-health", "holmes", "holmes-admin") }) { throw "Grafana Holmes proxy routes must remain disabled" }
    $composeText = Get-Content -Raw -Encoding UTF8 compose.yml
    $localLauncherText = Get-Content -Raw -Encoding UTF8 scripts/start-local-monitor.ps1
    $nativeGrafanaConfigText = Get-Content -Raw -Encoding UTF8 grafana/grafana.local.ini
    if (-not $composeText.Contains('GF_DATAPROXY_SEND_USER_HEADER: "true"') -or -not $nativeGrafanaConfigText.Contains('send_user_header = true')) {
        throw "Grafana must forward the authenticated username to the Holmes gateway"
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
    foreach ($platformSpecificMarker in @('/data/node_monitor', 'admin_password', 'root_url =', 'domain =')) {
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
    if (-not $localServersText.Contains('ssh_key_file: "secrets/ssh/ssjj_identity.pub"')) {
        throw "Shared server config must select the external SSH Agent identity by project-relative public-key path"
    }

    $modelListText = Get-Content -Raw -Encoding UTF8 holmes/model_list.example.yaml
    if ($modelListText -match '(?im)^\s*temperature\s*:') { throw "Kimi model example must not set temperature" }
    if (-not $modelListText.Contains('{{ env.GLM_API_KEY }}') -or -not $modelListText.Contains('{{ env.KIMI_API_KEY }}')) { throw "Model example must use environment placeholders" }
	$holmesLauncherText = Get-Content -Raw -Encoding UTF8 scripts/start-holmes-local.ps1
	if (-not $holmesLauncherText.Contains('$leaveRunning = $true') -or -not $holmesLauncherText.Contains('if (-not $leaveRunning)')) { throw "Holmes -NoWait mode would not preserve background processes" }
	if (-not $holmesLauncherText.Contains('PYTHONUTF8 = "1"') -or -not $holmesLauncherText.Contains('PYTHONIOENCODING = "utf-8"')) { throw "Holmes Windows launcher must force UTF-8 for Chinese Skills and prompts" }
	if (-not $holmesLauncherText.Contains('HOLMES_CONFIGPATH_DIR = $holmesConfigDir') -or -not $holmesLauncherText.Contains('$holmesRuntimeConfig = Join-Path $holmesConfigDir "config.yaml"')) { throw "Holmes Windows launcher must stage HOLMES_CONFIGPATH_DIR/config.yaml" }

    $nativeHolmesFiles = @(
        "holmes/native/config.yaml",
        "holmes/gateway.native.yml",
        "holmes/model_list.native.example.yaml",
        "holmes/native-overrides/README.md",
        "holmes/native-overrides/holmes-0.38.1-centos7.patch",
        "linux/holmes-native-requirements.txt",
        "linux/install-holmes-runtime.sh",
        "linux/install-holmes-services.sh",
        "linux/configure-holmes-grafana.sh",
        "linux/run-holmes.sh",
        "linux/run-holmes-gateway.sh",
        "linux/validate-holmes-config.sh",
        "linux/holmes-health-check.sh",
        "linux/update-holmes-and-restart.sh",
        "linux/systemd/erlang-monitor-holmes.service",
        "linux/systemd/erlang-monitor-holmes-gateway.service",
        "linux/bin/holmes-gateway",
        "linux/packages/cpython-3.11.15+20260804-x86_64-unknown-linux-gnu-install_only_stripped.tgz",
        "linux/packages/holmesgpt-0.38.1-native-centos7.tgz",
        "linux/packages/holmes-wheels-0.38.1-centos7-x86_64.tgz"
    )
    foreach ($nativeHolmesFile in $nativeHolmesFiles) {
        if (-not (Test-Path -LiteralPath $nativeHolmesFile -PathType Leaf) -or (Get-Item -LiteralPath $nativeHolmesFile).Length -eq 0) {
            throw "Missing or empty native Holmes release file: $nativeHolmesFile"
        }
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
    foreach ($checksummedNativeArtifact in @(
        "packages/cpython-3.11.15+20260804-x86_64-unknown-linux-gnu-install_only_stripped.tgz",
        "packages/holmesgpt-0.38.1-native-centos7.tgz",
        "packages/holmes-wheels-0.38.1-centos7-x86_64.tgz",
        "bin/holmes-gateway"
    )) {
        if (-not ($checksumLines | Where-Object { $_.EndsWith("  $checksummedNativeArtifact") })) {
            throw "Native Holmes artifact is absent from linux/checksums.sha256: $checksummedNativeArtifact"
        }
    }

    $baseRuntimeInstallerText = Get-Content -Raw -Encoding UTF8 linux/install-runtime.sh
    if ($baseRuntimeInstallerText.Contains("grep '  packages/'")) {
        throw "Base runtime installer must not validate Holmes packages from the base package root"
    }
    foreach ($baseRuntimeArchive in @(
        'packages/prometheus-${PROMETHEUS_VERSION}.linux-amd64.tar.gz',
        'packages/alertmanager-${ALERTMANAGER_VERSION}.linux-amd64.tar.gz',
        'packages/grafana-${GRAFANA_VERSION}.linux-amd64.tar.gz'
    )) {
        if (-not $baseRuntimeInstallerText.Contains($baseRuntimeArchive)) {
            throw "Base runtime installer checksum selector is missing: $baseRuntimeArchive"
        }
    }

    $nativeRequirementsText = Get-Content -Raw -Encoding UTF8 linux/holmes-native-requirements.txt
    foreach ($requiredPin in @('jq==1.10.0', 'tiktoken==0.11.0')) {
        if (-not $nativeRequirementsText.Contains($requiredPin)) { throw "Native Holmes compatibility pin is missing: $requiredPin" }
    }
    $nativeDeploymentText = @(
        Get-Content -Raw -Encoding UTF8 linux/install-holmes-runtime.sh
        Get-Content -Raw -Encoding UTF8 linux/install-holmes-services.sh
        Get-Content -Raw -Encoding UTF8 linux/configure-holmes-grafana.sh
        Get-Content -Raw -Encoding UTF8 linux/run-holmes.sh
        Get-Content -Raw -Encoding UTF8 linux/run-holmes-gateway.sh
        Get-Content -Raw -Encoding UTF8 linux/update-holmes-and-restart.sh
        Get-Content -Raw -Encoding UTF8 linux/systemd/erlang-monitor-holmes.service
        Get-Content -Raw -Encoding UTF8 linux/systemd/erlang-monitor-holmes-gateway.service
    ) -join "`n"
    if ($nativeDeploymentText -match '(?i)\b(?:docker|compose)\b') { throw "Native Holmes deployment must not depend on Docker or Compose" }
    if ($nativeDeploymentText.Contains('127.0.0.1:5050')) { throw "Native Holmes deployment still references the obsolete 5050 port" }
    foreach ($nativePort in @('127.0.0.1:20904', '127.0.0.1:20905')) {
        if (-not $nativeDeploymentText.Contains($nativePort)) { throw "Native Holmes deployment is missing loopback endpoint: $nativePort" }
    }
    $baseServiceInstallerText = Get-Content -Raw -Encoding UTF8 linux/install-services.sh
    if ($baseServiceInstallerText.Contains('erlang-monitor-holmes')) { throw "Optional Holmes units must not be installed by the base four-service installer" }
    $nativeUpdateText = Get-Content -Raw -Encoding UTF8 linux/update-holmes-and-restart.sh
    foreach ($requiredSafetyMarker in @('--revision', 'Only Holmes and Holmes Gateway were restarted', 'Existing monitoring service changed state')) {
        if (-not $nativeUpdateText.Contains($requiredSafetyMarker)) { throw "Native Holmes deployment safety marker is missing: $requiredSafetyMarker" }
    }
    $grafanaHolmesConfigText = Get-Content -Raw -Encoding UTF8 linux/configure-holmes-grafana.sh
    foreach ($requiredGrafanaMarker in @('secureJsonData', 'secureJsonFields', '/api/plugin-proxy/', 'Grafana was not restarted')) {
        if (-not $grafanaHolmesConfigText.Contains($requiredGrafanaMarker)) { throw "Live Grafana Holmes configuration marker is missing: $requiredGrafanaMarker" }
    }
    $nativeGrafanaLauncherText = Get-Content -Raw -Encoding UTF8 linux/run-grafana.sh
    if (-not $nativeGrafanaLauncherText.Contains('HOLMES_TOOL_TOKEN_FILE') -or -not $nativeGrafanaLauncherText.Contains('export HOLMES_TOOL_API_TOKEN')) {
        throw "Native Grafana launcher must preserve the Holmes proxy Secret across a later restart"
    }
    $nativeGatewayUnitText = Get-Content -Raw -Encoding UTF8 linux/systemd/erlang-monitor-holmes-gateway.service
    foreach ($requiredAgentMarker in @('SSH_AUTH_SOCK=/run/erlang-monitor-ssh-agent/agent.sock', '/usr/bin/test -S /run/erlang-monitor-ssh-agent/agent.sock', '/usr/bin/ssh-add -l')) {
        if (-not $nativeGatewayUnitText.Contains($requiredAgentMarker)) { throw "Native Holmes Gateway must use the dedicated SSH Agent: $requiredAgentMarker" }
    }
	$realSmokeText = Get-Content -Raw -Encoding UTF8 scripts/smoke-real-models.ps1
	foreach ($requiredSmokeMarker in @('Prometheus-then-controlled-diagnostic sequence', 'approval_required', 'HOLMES_TIMEOUT', 'MODEL_RATE_LIMITED')) {
		if (-not $realSmokeText.Contains($requiredSmokeMarker)) { throw "Real model smoke coverage is missing marker: $requiredSmokeMarker" }
	}
    $skillText = Get-Content -Raw -Encoding UTF8 holmes/skills/erlang-external-rca/SKILL.md
    if (-not $skillText.Contains('不调用会触发钉钉通知') -or -not $skillText.Contains('BEAM 进程数不是')) { throw "Project RCA skill safety rules are incomplete" }
    $ruleText = Get-Content -Raw -Encoding UTF8 prometheus/rules/erlang-alerts.yml
    $alertNames = [regex]::Matches($ruleText, '(?m)^\s+- alert:\s*([A-Za-z0-9_]+)\s*$') | ForEach-Object { $_.Groups[1].Value }
    if (@($alertNames).Count -ne 16) { throw "Expected 16 authoritative Erlang alert rules, found $(@($alertNames).Count)" }
    $missingSkillAlerts = @($alertNames | Where-Object { -not $skillText.Contains("``$_``") })
    if ($missingSkillAlerts.Count -gt 0) { throw "Project RCA skill is missing alert branches: $($missingSkillAlerts -join ', ')" }

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
