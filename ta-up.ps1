# Pengganti ta-up.sh + systemd untuk Windows.
# Pemakaian (dari folder proyek, mis. C:\ta):
#   .\ta-up.ps1 status
#   .\ta-up.ps1 build                          # build backend\ta-backend.exe
#   .\ta-up.ps1 restart [CACHE_MODE] [AI_MODE] [THRESHOLD]
#                                              # contoh: .\ta-up.ps1 restart hybrid http 2
#   .\ta-up.ps1 stop
#   .\ta-up.ps1 ingest                         # ambil data terbaru (dipanggil Task Scheduler tiap jam)
#   .\ta-up.ps1 backtest                       # isi price_after_24h + signal_correct (boleh dijadwalkan)
#   .\ta-up.ps1 migrate 004_measurement.sql    # jalankan satu file migrations\ ke database yang sudah ada
param(
    [Parameter(Position = 0)][string]$Action = "status",
    [Parameter(Position = 1)][string]$CacheMode,
    [Parameter(Position = 2)][string]$AiMode,
    [Parameter(Position = 3)][string]$Threshold
)

$ErrorActionPreference = "Stop"
$Root     = $PSScriptRoot
$RunEnv   = Join-Path $Root "run.env"
$LogDir   = Join-Path $Root "logs"
$Python   = Join-Path $Root ".venv\Scripts\python.exe"
$Backend  = Join-Path $Root "backend\ta-backend.exe"
New-Item -ItemType Directory -Force $LogDir | Out-Null

function Import-EnvFile([string]$Path) {
    if (-not (Test-Path $Path)) { return }
    Get-Content $Path | ForEach-Object {
        $line = $_.Trim()
        if ($line -eq "" -or $line.StartsWith("#")) { return }
        $i = $line.IndexOf("=")
        if ($i -lt 1) { return }
        $k = $line.Substring(0, $i).Trim()
        $v = $line.Substring($i + 1).Trim().Trim('"')
        [Environment]::SetEnvironmentVariable($k, $v, "Process")
    }
}

function Set-RunEnv([string]$Key, [string]$Value) {
    $found = $false
    $lines = @(Get-Content $RunEnv | ForEach-Object {
        if ($_ -match "^$Key=") { $found = $true; "$Key=$Value" } else { $_ }
    })
    if (-not $found) { $lines += "$Key=$Value" }
    Set-Content -Path $RunEnv -Value $lines
}

function Get-Health([string]$Url) {
    try { return (Invoke-RestMethod -Uri $Url -TimeoutSec 3 | ConvertTo-Json -Compress) }
    catch { return "DOWN" }
}

function Stop-Services {
    foreach ($n in "backend", "api") {
        $f = Join-Path $LogDir "$n.pid"
        if (Test-Path $f) {
            Stop-Process -Id ([int](Get-Content $f)) -Force -ErrorAction SilentlyContinue
            Remove-Item $f -Force
        }
    }
}

function Start-Services {
    Import-EnvFile (Join-Path $Root ".env")
    Import-EnvFile $RunEnv
    # python -m uvicorn, bukan uvicorn.exe: launcher .exe menyimpan path venv absolut dan rusak
    # kalau folder proyek dipindah.
    $api = Start-Process -FilePath $Python `
        -ArgumentList "-m", "uvicorn", "app:app", "--host", "127.0.0.1", "--port", "8000" `
        -WorkingDirectory (Join-Path $Root "ai-service") `
        -RedirectStandardOutput (Join-Path $LogDir "api.out.log") `
        -RedirectStandardError (Join-Path $LogDir "api.err.log") `
        -WindowStyle Hidden -PassThru
    $api.Id | Set-Content (Join-Path $LogDir "api.pid")
    $be = Start-Process -FilePath $Backend `
        -WorkingDirectory (Join-Path $Root "backend") `
        -RedirectStandardOutput (Join-Path $LogDir "backend.out.log") `
        -RedirectStandardError (Join-Path $LogDir "backend.err.log") `
        -WindowStyle Hidden -PassThru
    $be.Id | Set-Content (Join-Path $LogDir "backend.pid")
}

function Show-Status {
    Write-Host ("mode   : " + ((Get-Content $RunEnv | Where-Object { $_ -match "^(CACHE_MODE|AI_MODE|INVALIDATION_THRESHOLD_PCT|PROMPT_VERSION)=" }) -join " "))
    Write-Host ("Go     : " + (Get-Health "http://127.0.0.1:8080/health"))
    Write-Host ("FastAPI: " + (Get-Health "http://127.0.0.1:8000/health"))
}

switch ($Action) {
    "build" {
        Push-Location (Join-Path $Root "backend")
        try { go build -o ta-backend.exe . ; Write-Host "build OK" } finally { Pop-Location }
    }
    "restart" {
        if ($CacheMode) {
            if ($CacheMode -notmatch '^(no_cache|static_ttl|dynamic_ttl|event_only|hybrid)$') { throw "CACHE_MODE tidak valid: $CacheMode" }
            Set-RunEnv "CACHE_MODE" $CacheMode
        }
        if ($AiMode) {
            if ($AiMode -notmatch '^(stub|http)$') { throw "AI_MODE tidak valid: $AiMode" }
            Set-RunEnv "AI_MODE" $AiMode
        }
        if ($Threshold) {
            if ($Threshold -notmatch '^\d+(\.\d+)?$') { throw "THRESHOLD tidak valid: $Threshold" }
            Set-RunEnv "INVALIDATION_THRESHOLD_PCT" $Threshold
        }
        Stop-Services
        Start-Sleep -Seconds 1
        Start-Services
        for ($i = 0; $i -lt 20; $i++) {
            if ((Get-Health "http://127.0.0.1:8080/health") -ne "DOWN" -and (Get-Health "http://127.0.0.1:8000/health") -ne "DOWN") { break }
            Start-Sleep -Seconds 1
        }
        Show-Status
    }
    "stop"   { Stop-Services; Write-Host "stopped" }
    "ingest" {
        Import-EnvFile (Join-Path $Root ".env")
        & $Python (Join-Path $Root "ingestion\ingest.py") --timeframe 1h
        & $Python (Join-Path $Root "ingestion\ingest.py") --timeframe 1d
    }
    "backtest" {
        Import-EnvFile (Join-Path $Root ".env")
        & $Python (Join-Path $Root "ingestion\backtest.py")
    }
    "migrate" {
        # Lewat stdin, jadi tidak bergantung pada folder yang di-mount container.
        $file = Join-Path $Root "migrations\$CacheMode"
        if (-not $CacheMode -or -not (Test-Path $file)) { throw "Pakai: .\ta-up.ps1 migrate <nama file di migrations\>" }
        Get-Content $file -Raw | docker exec -i ta_postgres psql -U ta_user -d ta_db -v ON_ERROR_STOP=1 -q
        if ($LASTEXITCODE -ne 0) { throw "migrasi gagal" }
        Write-Host "migrasi $CacheMode OK"
    }
    "status" { Show-Status }
    default  { throw "Aksi tidak dikenal: $Action" }
}
