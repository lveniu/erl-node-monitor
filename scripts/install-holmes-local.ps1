param([switch]$CheckOnly)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$runtimeRoot = Join-Path $projectRoot ".runtime\holmesgpt-0.38.1"
$expectedCommit = "7af34f5e716e28adcbcbd584cd4708434929f183"

function Get-Python311 {
    $path = & py -3.11 -c "import sys; print(sys.executable)" 2>$null
    if ($LASTEXITCODE -ne 0) {
        throw "HolmesGPT 0.38.1 requires Python 3.11 for this Windows launcher. Install Python 3.11; the existing monitoring stack remains unaffected."
    }
    return $path.Trim()
}

if ($CheckOnly) {
    if (-not (Test-Path -LiteralPath $runtimeRoot -PathType Container)) {
        Write-Warning "HolmesGPT local runtime is not installed. Run scripts\install-holmes-local.ps1 after installing Python 3.11."
        return
    }
    $actualCommit = (& git -C $runtimeRoot rev-parse HEAD).Trim()
    if ($actualCommit -ne $expectedCommit) { throw "HolmesGPT runtime commit does not match the pinned 0.38.1 source." }
    $venvPython = Join-Path $runtimeRoot ".venv\Scripts\python.exe"
    if (-not (Test-Path -LiteralPath $venvPython -PathType Leaf)) { throw "HolmesGPT Python virtual environment is missing." }
    Write-Host "HolmesGPT 0.38.1 local runtime is pinned and ready." -ForegroundColor Green
    return
}

$python = Get-Python311

if (-not (Test-Path -LiteralPath $runtimeRoot -PathType Container)) {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $runtimeRoot) | Out-Null
    & git clone --branch 0.38.1 --depth 1 https://github.com/HolmesGPT/holmesgpt.git $runtimeRoot
    if ($LASTEXITCODE -ne 0) { throw "HolmesGPT 0.38.1 source download failed." }
}
$actualCommit = (& git -C $runtimeRoot rev-parse HEAD).Trim()
if ($actualCommit -ne $expectedCommit) { throw "HolmesGPT runtime commit does not match the pinned 0.38.1 source." }

$venvRoot = Join-Path $runtimeRoot ".venv"
$venvPython = Join-Path $venvRoot "Scripts\python.exe"
$poetryRoot = Join-Path $runtimeRoot ".poetry"
$poetryPython = Join-Path $poetryRoot "Scripts\python.exe"
if (-not (Test-Path -LiteralPath $venvPython -PathType Leaf)) {
    & $python -m venv $venvRoot
    if ($LASTEXITCODE -ne 0) { throw "HolmesGPT virtual environment creation failed." }
}
if (-not (Test-Path -LiteralPath $poetryPython -PathType Leaf)) {
    & $python -m venv $poetryRoot
    if ($LASTEXITCODE -ne 0) { throw "Poetry installer environment creation failed." }
}
& $poetryPython -m pip install --disable-pip-version-check "poetry==2.1.4"
if ($LASTEXITCODE -ne 0) { throw "Pinned Poetry installation failed." }
$previousVirtualEnv = $env:VIRTUAL_ENV
$previousPath = $env:PATH
$previousPoetryVirtualenvsCreate = $env:POETRY_VIRTUALENVS_CREATE
$env:VIRTUAL_ENV = $venvRoot
$env:PATH = "$(Join-Path $venvRoot 'Scripts');$previousPath"
$env:POETRY_VIRTUALENVS_CREATE = "false"
try {
    Push-Location $runtimeRoot
    & $poetryPython -m poetry install --only main --no-interaction --no-ansi
    if ($LASTEXITCODE -ne 0) { throw "HolmesGPT locked dependency installation failed." }
}
finally {
    Pop-Location
    $env:PATH = $previousPath
    if ($null -eq $previousVirtualEnv) { Remove-Item Env:VIRTUAL_ENV -ErrorAction SilentlyContinue } else { $env:VIRTUAL_ENV = $previousVirtualEnv }
    if ($null -eq $previousPoetryVirtualenvsCreate) { Remove-Item Env:POETRY_VIRTUALENVS_CREATE -ErrorAction SilentlyContinue } else { $env:POETRY_VIRTUALENVS_CREATE = $previousPoetryVirtualenvsCreate }
}
Write-Host "HolmesGPT 0.38.1 local runtime installed from its pinned commit." -ForegroundColor Green
