# Pengganti ta-up.sh + systemd untuk Windows.
# Pemakaian (dari folder proyek, mis. C:\ta\StocksTA):
#   .\ta-up.ps1 status
#   .\ta-up.ps1 build                          # build backend\ta-backend.exe
#   .\ta-up.ps1 restart [CACHE_MODE] [AI_MODE] [THRESHOLD] [PREGEN]
#                                              # contoh: .\ta-up.ps1 restart hybrid http 2 on
#   .\ta-up.ps1 stop
#   .\ta-up.ps1 ingest                         # ambil data terbaru (dipanggil Task Scheduler tiap jam)
#   .\ta-up.ps1 backtest                       # isi price_after_24h + signal_correct (boleh dijadwalkan)
#   .\ta-up.ps1 migrate 007_thesis_metrics.sql # jalankan satu file migrations\ ke database yang sudah ada
#
# Studi pendahuluan (Bab 3.4):
#   .\ta-up.ps1 pilot-prices                   # distribusi pergerakan harga + kandidat sigma_ref
#   .\ta-up.ps1 pilot-ai [N]                   # N panggilan Hermes per instrument (backend harus no_cache)
#
# Eksperimen live paralel: semua kondisi berjalan bersamaan di port 8101-8115, satu AI service.
#   .\ta-up.ps1 lab start [stub|http]          # default stub (tanpa token)
#   .\ta-up.ps1 lab status
#   .\ta-up.ps1 lab stop
param(
    [Parameter(Position = 0)][string]$Action = "status",
    [Parameter(Position = 1)][string]$CacheMode,
    [Parameter(Position = 2)][string]$AiMode,
    [Parameter(Position = 3)][string]$Threshold,
    [Parameter(Position = 4)][string]$Pregen
)

$ErrorActionPreference = "Stop"
$Root     = $PSScriptRoot
$RunEnv   = Join-Path $Root "run.env"
$LogDir   = Join-Path $Root "logs"
$Python   = Join-Path $Root ".venv\Scripts\python.exe"
$Backend  = Join-Path $Root "backend\ta-backend.exe"
New-Item -ItemType Directory -Force $LogDir | Out-Null

# Matriks kondisi eksperimen (ablation, Bab 3): port -> kondisi.
$LabMatrix = @(
    @{ Port = 8101; Mode = "no_cache" },
    @{ Port = 8102; Mode = "static_ttl" },
    @{ Port = 8103; Mode = "dynamic_ttl" },
    @{ Port = 8104; Mode = "event_only"; Th = "1" },
    @{ Port = 8105; Mode = "event_only"; Th = "2" },
    @{ Port = 8106; Mode = "event_only"; Th = "5" },
    @{ Port = 8107; Mode = "event_only"; Th = "10" },
    @{ Port = 8108; Mode = "hybrid"; Th = "1" },
    @{ Port = 8109; Mode = "hybrid"; Th = "2" },
    @{ Port = 8110; Mode = "hybrid"; Th = "5" },
    @{ Port = 8111; Mode = "hybrid"; Th = "10" },
    @{ Port = 8112; Mode = "hybrid"; Th = "1"; Pregen = "on" },
    @{ Port = 8113; Mode = "hybrid"; Th = "2"; Pregen = "on" },
    @{ Port = 8114; Mode = "hybrid"; Th = "5"; Pregen = "on" },
    @{ Port = 8115; Mode = "hybrid"; Th = "10"; Pregen = "on" }
)

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
    try { return (Invoke-RestMethod -Uri $Url -TimeoutSec 3 | ConvertTo-Json -Compress -Depth 5) }
    catch { return "DOWN" }
}

# Jalankan skrip Python di ingestion\. Di Windows PowerShell 5.1, output stderr program native
# (mis. peringatan pandas dari yfinance) dianggap error dan menghentikan skrip saat
# ErrorActionPreference = Stop, jadi di sini diturunkan ke Continue dan peringatan Python dimatikan.
function Invoke-Py([string]$Script, [string[]]$PyArgs = @()) {
    $prev = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    $env:PYTHONWARNINGS = "ignore"
    try {
        & $Python (Join-Path $Root "ingestion\$Script") @PyArgs 2>&1 | ForEach-Object { "$_" }
        if ($LASTEXITCODE -ne 0) { Write-Warning "$Script keluar dengan kode $LASTEXITCODE" }
    } finally {
        $ErrorActionPreference = $prev
    }
}

function Stop-Pid([string]$PidFile) {
    if (Test-Path $PidFile) {
        Stop-Process -Id ([int](Get-Content $PidFile)) -Force -ErrorAction SilentlyContinue
        Remove-Item $PidFile -Force
    }
}

function Stop-Services {
    foreach ($n in "backend", "api") { Stop-Pid (Join-Path $LogDir "$n.pid") }
}

function Start-Api {
    # python -m uvicorn, bukan uvicorn.exe: launcher .exe menyimpan path venv absolut dan rusak
    # kalau folder proyek dipindah.
    $api = Start-Process -FilePath $Python `
        -ArgumentList "-m", "uvicorn", "app:app", "--host", "127.0.0.1", "--port", "8000" `
        -WorkingDirectory (Join-Path $Root "ai-service") `
        -RedirectStandardOutput (Join-Path $LogDir "api.out.log") `
        -RedirectStandardError (Join-Path $LogDir "api.err.log") `
        -WindowStyle Hidden -PassThru
    $api.Id | Set-Content (Join-Path $LogDir "api.pid")
}

function Start-Backend([string]$Name) {
    $be = Start-Process -FilePath $Backend `
        -WorkingDirectory (Join-Path $Root "backend") `
        -RedirectStandardOutput (Join-Path $LogDir "$Name.out.log") `
        -RedirectStandardError (Join-Path $LogDir "$Name.err.log") `
        -WindowStyle Hidden -PassThru
    $be.Id | Set-Content (Join-Path $LogDir "$Name.pid")
}

function Start-Services {
    Import-EnvFile (Join-Path $Root ".env")
    Import-EnvFile $RunEnv
    Start-Api
    Start-Backend "backend"
}

function Show-Status {
    Write-Host ("mode   : " + ((Get-Content $RunEnv | Where-Object { $_ -match "^(CACHE_MODE|AI_MODE|INVALIDATION_THRESHOLD_PCT|PREGEN|PROMPT_VERSION)=" }) -join " "))
    Write-Host ("Go     : " + (Get-Health "http://127.0.0.1:8080/health"))
    Write-Host ("FastAPI: " + (Get-Health "http://127.0.0.1:8000/health"))
}

function Lab-Status {
    foreach ($c in $LabMatrix) {
        $h = try { Invoke-RestMethod "http://127.0.0.1:$($c.Port)/health" -TimeoutSec 2 } catch { $null }
        $label = "$($c.Mode)$(if ($c.Th) { " th=$($c.Th)%" })$(if ($c.Pregen) { ' +pregen' })"
        Write-Host ("{0}  {1,-28} {2}" -f $c.Port, $label, $(if ($h) { "UP ($($h.instance))" } else { "DOWN" }))
    }
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
        if ($Pregen) {
            if ($Pregen -notmatch '^(on|off)$') { throw "PREGEN harus on atau off: $Pregen" }
            Set-RunEnv "PREGEN" $Pregen
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
        Invoke-Py "ingest.py" "--timeframe", "1h"
        Invoke-Py "ingest.py" "--timeframe", "1d"
    }
    "backtest" {
        Import-EnvFile (Join-Path $Root ".env")
        Invoke-Py "backtest.py"
    }
    "pilot-prices" {
        Import-EnvFile (Join-Path $Root ".env")
        Invoke-Py "pilot_prices.py" "--csv", (Join-Path $LogDir "pilot_prices.csv")
    }
    "pilot-ai" {
        $n = if ($CacheMode) { $CacheMode } else { "10" }
        Invoke-Py "pilot_ai.py" "--n", $n, "--csv", (Join-Path $LogDir "pilot_ai.csv")
    }
    "migrate" {
        # Lewat stdin, jadi tidak bergantung pada folder yang di-mount container.
        $file = Join-Path $Root "migrations\$CacheMode"
        if (-not $CacheMode -or -not (Test-Path $file)) { throw "Pakai: .\ta-up.ps1 migrate <nama file di migrations\>" }
        Get-Content $file -Raw | docker exec -i ta_postgres psql -U ta_user -d ta_db -v ON_ERROR_STOP=1 -q
        if ($LASTEXITCODE -ne 0) { throw "migrasi gagal" }
        Write-Host "migrasi $CacheMode OK"
    }
    "lab" {
        switch ($CacheMode) {
            "start" {
                Import-EnvFile (Join-Path $Root ".env")
                Import-EnvFile $RunEnv
                $env:AI_MODE = if ($AiMode) { $AiMode } else { "stub" }
                if ((Get-Health "http://127.0.0.1:8000/health") -eq "DOWN" -and $env:AI_MODE -eq "http") { Start-Api }
                foreach ($c in $LabMatrix) {
                    $env:PORT = "$($c.Port)"
                    $env:CACHE_MODE = $c.Mode
                    $env:INVALIDATION_THRESHOLD_PCT = if ($c.Th) { $c.Th } else { "2" }
                    $env:PREGEN = if ($c.Pregen) { $c.Pregen } else { "off" }
                    Start-Backend "lab-$($c.Port)"
                }
                Start-Sleep -Seconds 5
                Lab-Status
            }
            "stop" {
                foreach ($c in $LabMatrix) { Stop-Pid (Join-Path $LogDir "lab-$($c.Port).pid") }
                Write-Host "lab stopped"
            }
            default { Lab-Status }
        }
    }
    "status" { Show-Status }
    default  { throw "Aksi tidak dikenal: $Action" }
}
