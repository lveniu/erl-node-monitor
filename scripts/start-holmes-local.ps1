param(
    [string]$ServerConfig = "",
    [switch]$CheckOnly,
    [switch]$NoWait
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
if (-not $ServerConfig) {
    $localConfig = Join-Path $projectRoot "config\servers.native.yml"
    $ServerConfig = if (Test-Path -LiteralPath $localConfig) { $localConfig } else { Join-Path $projectRoot "config\servers.yml" }
}
$runtimeRoot = Join-Path $projectRoot ".runtime\holmesgpt-0.38.1"
$venvPython = Join-Path $runtimeRoot ".venv\Scripts\python.exe"
$gatewayExe = Join-Path $projectRoot "bin\holmes-gateway.exe"
$modelList = Join-Path $projectRoot "holmes\model_list.local.yaml"
$holmesConfig = Join-Path $projectRoot "holmes\config.local.yml"
$holmesConfigDir = Join-Path $projectRoot ".runtime\holmes-config-local"
$holmesRuntimeConfig = Join-Path $holmesConfigDir "config.yaml"
$localGatewayConfig = Join-Path $projectRoot "holmes\gateway.local.yml"
$gatewayConfig = if (Test-Path -LiteralPath $localGatewayConfig) { $localGatewayConfig } else { Join-Path $projectRoot "holmes\gateway.example.yml" }
$secretRoot = Join-Path $projectRoot "secrets"

function Read-RequiredSecret([string]$Name) {
    $path = Join-Path $secretRoot $Name
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Required secret file is missing: secrets\$Name" }
    $value = (Get-Content -Raw -LiteralPath $path).Trim()
    if (-not $value) { throw "Required secret file is empty: secrets\$Name" }
    return $value
}

if (-not (Test-Path -LiteralPath $ServerConfig -PathType Leaf)) { throw "Server inventory does not exist." }
if (-not (Test-Path -LiteralPath $holmesConfig -PathType Leaf)) { throw "Holmes local configuration does not exist: holmes\config.local.yml" }
if (-not (Test-Path -LiteralPath $modelList -PathType Leaf) -and -not $CheckOnly) { throw "Copy holmes\model_list.example.yaml to the Git-ignored holmes\model_list.local.yaml and replace only the account model IDs." }
if ($CheckOnly) {
    & (Join-Path $PSScriptRoot "install-holmes-local.ps1") -CheckOnly
    $env:GOTOOLCHAIN = "go1.22.12"
    & go run ./cmd/holmes-gateway -config $gatewayConfig -servers $ServerConfig -check-config
    if ($LASTEXITCODE -ne 0) { throw "Holmes gateway configuration validation failed." }
    Write-Host "Holmes local launcher configuration is valid; secrets were not read." -ForegroundColor Green
    return
}
if (-not (Test-Path -LiteralPath $venvPython -PathType Leaf)) { throw "Install the pinned Holmes runtime with scripts\install-holmes-local.ps1 first." }

# HolmesGPT 0.38.1 reads HOLMES_CONFIGPATH_DIR/config.yaml. Keep the shared
# source file in holmes/ and stage the exact filename it expects at runtime.
New-Item -ItemType Directory -Force -Path $holmesConfigDir | Out-Null
Copy-Item -LiteralPath $holmesConfig -Destination $holmesRuntimeConfig -Force

$holmesKey = Read-RequiredSecret "holmes_api_key"
$toolToken = Read-RequiredSecret "holmes_tool_api_token"
$modelListContent = Get-Content -Raw -LiteralPath $modelList
$holmesEnvironment = @{
    HOLMES_API_KEY = $holmesKey
    HOLMES_CONFIGPATH_DIR = $holmesConfigDir
    MODEL_LIST_FILE_LOCATION = $modelList
    # Keep the optional Holmes service in the same reserved local monitoring
    # range and do not expose its unauthenticated listener beyond this host.
    HOLMES_HOST = "127.0.0.1"
    HOLMES_PORT = "20905"
    HOLMES_TOOL_RESULT_STORAGE_ENABLED = "false"
    ENABLE_JSON_LOGS_FORMAT = "true"
    # Holmes loads the project Skill and prompts containing Chinese text. Force
    # UTF-8 on native Windows so Python never falls back to the GBK locale.
    PYTHONUTF8 = "1"
    PYTHONIOENCODING = "utf-8"
}
if ($modelListContent -match '\{\{\s*env\.GLM_API_KEY\s*\}\}') {
    $holmesEnvironment.GLM_API_KEY = Read-RequiredSecret "glm_api_key"
}
if ($modelListContent -match '\{\{\s*env\.KIMI_API_KEY\s*\}\}') {
    $holmesEnvironment.KIMI_API_KEY = Read-RequiredSecret "kimi_api_key"
}
$env:GOTOOLCHAIN = "go1.22.12"
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $gatewayExe) | Out-Null
& go build -trimpath -o $gatewayExe ./cmd/holmes-gateway
if ($LASTEXITCODE -ne 0) { throw "Holmes gateway build failed." }

function Start-WithEnvironment([string]$FilePath, [string[]]$Arguments, [string]$WorkingDirectory, [hashtable]$Environment) {
    $start = New-Object System.Diagnostics.ProcessStartInfo
    $start.FileName = $FilePath
    $start.WorkingDirectory = $WorkingDirectory
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.Arguments = (($Arguments | ForEach-Object {
        if ($_ -match '[\s"]') { '"' + ($_ -replace '(\\*)"', '$1$1\"') + '"' } else { $_ }
    }) -join ' ')
    foreach ($key in $Environment.Keys) { $start.Environment[$key] = $Environment[$key] }
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $start
    if (-not $process.Start()) { throw "Failed to start $FilePath" }
    return $process
}

$gatewayEnvironment = @{ HOLMES_API_KEY = $holmesKey; HOLMES_TOOL_API_TOKEN = $toolToken }
$holmesProcess = Start-WithEnvironment $venvPython @("-u", (Join-Path $runtimeRoot "server.py")) $projectRoot $holmesEnvironment
$gatewayProcess = Start-WithEnvironment $gatewayExe @("-config", $gatewayConfig, "-servers", $ServerConfig, "-listen", "127.0.0.1:20904", "-data-dir", (Join-Path $projectRoot "data\holmes")) $projectRoot $gatewayEnvironment
$leaveRunning = $false
$holmesKey = $toolToken = $modelListContent = $null
$holmesEnvironment.Clear()
$gatewayEnvironment.Clear()

try {
    $deadline = (Get-Date).AddSeconds(180)
    do {
        Start-Sleep -Milliseconds 500
        try { $gatewayHealth = Invoke-RestMethod -TimeoutSec 3 -Uri "http://127.0.0.1:20904/healthz" } catch { $gatewayHealth = $null }
        if ($holmesProcess.HasExited) { throw "HolmesGPT exited during startup." }
        if ($gatewayProcess.HasExited) { throw "Holmes gateway exited during startup." }
        $gatewayReady = $gatewayHealth -and $gatewayHealth.dependencies.holmes_process -eq "healthy" -and $gatewayHealth.dependencies.model_availability -eq "available"
    } until ($gatewayReady -or (Get-Date) -gt $deadline)
    if (-not $gatewayReady) { throw "Holmes gateway dependencies did not become ready in time." }
    Write-Host "Holmes gateway is ready at http://127.0.0.1:20904 (Holmes dependency: $($gatewayHealth.dependencies.holmes_process))." -ForegroundColor Green
    if ($NoWait) {
        $leaveRunning = $true
        Write-Host "Holmes and its gateway were left running in the background." -ForegroundColor Green
        return
    }
    Write-Host "Press Ctrl+C to stop Holmes and its gateway. Existing monitoring services are independent." -ForegroundColor DarkGray
    while (-not $holmesProcess.HasExited -and -not $gatewayProcess.HasExited) { Start-Sleep -Seconds 2 }
}
finally {
    if (-not $leaveRunning) {
        foreach ($process in @($gatewayProcess, $holmesProcess)) {
            if ($process -and -not $process.HasExited) { $process.Kill($true) }
        }
    }
}
