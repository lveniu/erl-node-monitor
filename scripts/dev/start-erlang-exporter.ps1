param(
    [string]$Listen = "127.0.0.1:20903",
    [string]$Config = "",
    [string]$WebhookFile = "",
    [switch]$SkipDingTalk,
    [switch]$CheckOnly
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$exporterExe = Join-Path $projectRoot "bin\erlang-exporter.exe"
$secretDirectory = Join-Path $projectRoot "secrets"

try {
    Set-Location $projectRoot
    if (-not (Test-Path -LiteralPath $exporterExe)) {
        throw "Missing $exporterExe. Run scripts\verify.ps1 first."
    }

    if ([string]::IsNullOrWhiteSpace($Config)) {
        $localConfig = Join-Path $projectRoot "config\servers.native.yml"
        $productionConfig = Join-Path $projectRoot "config\servers.yml"
        if (Test-Path -LiteralPath $localConfig) {
            $Config = $localConfig
        }
        elseif (Test-Path -LiteralPath $productionConfig) {
            $Config = $productionConfig
        }
        else {
            throw "Server config is missing. Copy config\servers.example.yml to config\servers.yml and configure it."
        }
    }

    & $exporterExe -config $Config -check-config
    if ($LASTEXITCODE -ne 0) {
        throw "Server config validation failed."
    }

    if (-not $SkipDingTalk) {
        if ([string]::IsNullOrWhiteSpace($WebhookFile)) {
            $WebhookFile = Join-Path $secretDirectory "dingtalk_webhook_url"
        }
        if (-not (Test-Path -LiteralPath $WebhookFile) -or [string]::IsNullOrWhiteSpace((Get-Content -Raw -LiteralPath $WebhookFile -ErrorAction SilentlyContinue))) {
            New-Item -ItemType Directory -Force -Path $secretDirectory | Out-Null
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
            [IO.File]::WriteAllText($WebhookFile, $webhook, [Text.UTF8Encoding]::new($false))
            $webhook = $null
            Write-Host "Webhook saved under the Git-ignored secrets directory." -ForegroundColor Green
        }

        $webhook = (Get-Content -Raw -LiteralPath $WebhookFile).Trim()
        $uri = $null
        if (-not [Uri]::TryCreate($webhook, [UriKind]::Absolute, [ref]$uri) -or $uri.Scheme -ne "https" -or $uri.Host -ne "oapi.dingtalk.com") {
            throw "Webhook must be a valid https://oapi.dingtalk.com/ URL."
        }
        $webhook = $null
        $env:DINGTALK_WEBHOOK_URL_FILE = $WebhookFile
        $signingSecretFile = Join-Path $secretDirectory "dingtalk_secret"
        if (Test-Path -LiteralPath $signingSecretFile) {
            $env:DINGTALK_SECRET_FILE = $signingSecretFile
        }
        $atMobilesFile = Join-Path $secretDirectory "dingtalk_at_mobiles"
        if (Test-Path -LiteralPath $atMobilesFile) {
            $env:DINGTALK_AT_MOBILES_FILE = $atMobilesFile
        }
        $atUserIdsFile = Join-Path $secretDirectory "dingtalk_at_user_ids"
        if (Test-Path -LiteralPath $atUserIdsFile) {
            $env:DINGTALK_AT_USER_IDS_FILE = $atUserIdsFile
        }
    }

    if ($CheckOnly) {
        Write-Host "Integrated monitor launcher configuration is valid." -ForegroundColor Green
        return
    }

	Write-Host "Starting integrated monitor: http://$Listen/status" -ForegroundColor Green
	if (-not $SkipDingTalk) {
		Write-Host "DingTalk Alertmanager endpoint: http://$Listen/alertmanager" -ForegroundColor Green
	}
    Write-Host "Press Ctrl+C to stop." -ForegroundColor DarkGray
    & $exporterExe -config $Config -listen $Listen -status-file (Join-Path $projectRoot "data\exporter-status.json")
    if ($LASTEXITCODE -ne 0) {
        throw "Erlang exporter exited with code $LASTEXITCODE"
    }
}
finally {
    Remove-Item Env:DINGTALK_WEBHOOK_URL_FILE -ErrorAction SilentlyContinue
    Remove-Item Env:DINGTALK_SECRET_FILE -ErrorAction SilentlyContinue
    Remove-Item Env:DINGTALK_AT_MOBILES_FILE -ErrorAction SilentlyContinue
    Remove-Item Env:DINGTALK_AT_USER_IDS_FILE -ErrorAction SilentlyContinue
    Set-Location $projectRoot
}
