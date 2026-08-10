param(
    [switch]$CheckOnly
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$projectRoot = Split-Path -Parent $PSScriptRoot
$runtimeRoot = Join-Path $projectRoot ".runtime"

$packages = @(
    [pscustomobject]@{
        Name = "Prometheus"
        Directory = "prometheus-3.5.0"
        Archive = "prometheus-3.5.0.windows-amd64.zip"
        Download = "https://github.com/prometheus/prometheus/releases/download/v3.5.0/prometheus-3.5.0.windows-amd64.zip"
        Checksums = "https://github.com/prometheus/prometheus/releases/download/v3.5.0/sha256sums.txt"
        Executable = "prometheus.exe"
    },
    [pscustomobject]@{
        Name = "Alertmanager"
        Directory = "alertmanager-0.28.1"
        Archive = "alertmanager-0.28.1.windows-amd64.zip"
        Download = "https://github.com/prometheus/alertmanager/releases/download/v0.28.1/alertmanager-0.28.1.windows-amd64.zip"
        Checksums = "https://github.com/prometheus/alertmanager/releases/download/v0.28.1/sha256sums.txt"
        Executable = "alertmanager.exe"
    },
    [pscustomobject]@{
        Name = "Grafana"
        Directory = "grafana-12.1.0"
        Archive = "grafana-12.1.0.windows-amd64.zip"
        Download = "https://dl.grafana.com/oss/release/grafana-12.1.0.windows-amd64.zip"
        Checksums = "https://dl.grafana.com/oss/release/grafana-12.1.0.windows-amd64.zip.sha256"
        Executable = "bin\grafana.exe"
    }
)

function Get-ExpectedChecksum {
    param(
        [string]$ChecksumText,
        [string]$ArchiveName
    )

    $namedPattern = "(?im)^([a-f0-9]{64})\s+\*?" + [Regex]::Escape($ArchiveName) + "\s*$"
    $namedMatch = [Regex]::Match($ChecksumText, $namedPattern)
    if ($namedMatch.Success) {
        return $namedMatch.Groups[1].Value.ToLowerInvariant()
    }

    $singleMatch = [Regex]::Match($ChecksumText.Trim(), "(?i)^([a-f0-9]{64})(?:\s+.*)?$")
    if ($singleMatch.Success) {
        return $singleMatch.Groups[1].Value.ToLowerInvariant()
    }

    throw "Could not find SHA-256 checksum for $ArchiveName"
}

function Assert-PathBelow {
    param(
        [string]$Child,
        [string]$Parent
    )

    $parentFull = [IO.Path]::GetFullPath($Parent).TrimEnd('\') + '\'
    $childFull = [IO.Path]::GetFullPath($Child)
    if (-not $childFull.StartsWith($parentFull, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Unsafe path outside expected directory: $childFull"
    }
}

if ($CheckOnly) {
    $missing = @()
    foreach ($package in $packages) {
        $executable = Join-Path (Join-Path $runtimeRoot $package.Directory) $package.Executable
        if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) {
            $missing += $package.Name
        }
    }
    if ($missing.Count -gt 0) {
        throw "Local runtime is missing: $($missing -join ', ')"
    }
    Write-Host "Local runtime is complete." -ForegroundColor Green
    return
}

New-Item -ItemType Directory -Force -Path $runtimeRoot | Out-Null

foreach ($package in $packages) {
    $targetDirectory = Join-Path $runtimeRoot $package.Directory
    $targetExecutable = Join-Path $targetDirectory $package.Executable
    if (Test-Path -LiteralPath $targetExecutable -PathType Leaf) {
        Write-Host "$($package.Name) is already installed." -ForegroundColor DarkGray
        continue
    }

    if (Test-Path -LiteralPath $targetDirectory) {
        Assert-PathBelow -Child $targetDirectory -Parent $runtimeRoot
        $incompleteDirectory = "$targetDirectory.incomplete-$(Get-Date -Format 'yyyyMMdd-HHmmss')"
        Move-Item -LiteralPath $targetDirectory -Destination $incompleteDirectory
        Write-Warning "Moved incomplete runtime to $incompleteDirectory"
    }

    $temporaryDirectory = Join-Path $env:TEMP ("erlang-monitor-runtime-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $temporaryDirectory | Out-Null
    try {
        $archivePath = Join-Path $temporaryDirectory $package.Archive
        $checksumPath = Join-Path $temporaryDirectory "checksums.txt"
        $extractDirectory = Join-Path $temporaryDirectory "extract"

        Write-Host "Downloading $($package.Name)..." -ForegroundColor Cyan
        Invoke-WebRequest -UseBasicParsing -Uri $package.Download -OutFile $archivePath
        Invoke-WebRequest -UseBasicParsing -Uri $package.Checksums -OutFile $checksumPath

        $checksumText = [IO.File]::ReadAllText($checksumPath, [Text.Encoding]::UTF8)
        $expectedChecksum = Get-ExpectedChecksum -ChecksumText $checksumText -ArchiveName $package.Archive
        $actualChecksum = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actualChecksum -ne $expectedChecksum) {
            throw "$($package.Name) SHA-256 mismatch"
        }

        New-Item -ItemType Directory -Path $extractDirectory | Out-Null
        Expand-Archive -LiteralPath $archivePath -DestinationPath $extractDirectory
        $topLevel = @(Get-ChildItem -LiteralPath $extractDirectory -Directory)
        if ($topLevel.Count -ne 1) {
            throw "$($package.Name) archive did not contain exactly one top-level directory"
        }
        $sourceDirectory = $topLevel[0].FullName
        $sourceExecutable = Join-Path $sourceDirectory $package.Executable
        if (-not (Test-Path -LiteralPath $sourceExecutable -PathType Leaf)) {
            throw "$($package.Name) executable was not found after extraction"
        }

        Assert-PathBelow -Child $targetDirectory -Parent $runtimeRoot
        Move-Item -LiteralPath $sourceDirectory -Destination $targetDirectory
        Write-Host "$($package.Name) installed and checksum verified." -ForegroundColor Green
    }
    finally {
        if (Test-Path -LiteralPath $temporaryDirectory) {
            Assert-PathBelow -Child $temporaryDirectory -Parent $env:TEMP
            Remove-Item -LiteralPath $temporaryDirectory -Recurse -Force
        }
    }
}

& (Join-Path $runtimeRoot "prometheus-3.5.0\prometheus.exe") --version
if ($LASTEXITCODE -ne 0) { throw "Prometheus runtime validation failed" }
& (Join-Path $runtimeRoot "alertmanager-0.28.1\alertmanager.exe") --version
if ($LASTEXITCODE -ne 0) { throw "Alertmanager runtime validation failed" }
& (Join-Path $runtimeRoot "grafana-12.1.0\bin\grafana.exe") --version
if ($LASTEXITCODE -ne 0) { throw "Grafana runtime validation failed" }

Write-Host "Windows local monitoring runtime is ready." -ForegroundColor Green
