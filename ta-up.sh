#!/usr/bin/env bash
# Versi Linux dari ta-up.ps1 (aksi dan perilaku sama). Postgres + Redis berjalan di docker
# (docker-compose.yml); Go backend, FastAPI + Hermes, dan ingestion berjalan langsung di host.
# Pemakaian (dari folder proyek):
#   ./ta-up.sh status
#   ./ta-up.sh build                          # build backend/ta-backend (lewat docker kalau Go tidak terpasang)
#   ./ta-up.sh restart [CACHE_MODE] [AI_MODE] [THRESHOLD] [PREGEN]
#                                             # contoh: ./ta-up.sh restart hybrid http 2 on
#   ./ta-up.sh stop
#   ./ta-up.sh ingest                         # ambil data terbaru (dipanggil cron tiap jam)
#   ./ta-up.sh backtest                       # isi price_after_24h + signal_correct (dipanggil cron)
#   ./ta-up.sh fundamentals                   # fundamental saham dari Yahoo (cron harian)
#   ./ta-up.sh quota                          # snapshot kuota langganan AI (cron tiap 10 menit saat eksperimen)
#   ./ta-up.sh migrate 007_thesis_metrics.sql # jalankan satu file migrations/ ke database yang sudah ada
#
# Studi pendahuluan (Bab 3.4):
#   ./ta-up.sh pilot-prices
#   ./ta-up.sh pilot-ai [N] [SIMBOL,...]      # background; backend harus no_cache (./ta-up.sh restart no_cache http)
#
# Eksperimen live paralel: semua kondisi berjalan bersamaan di port 8101-8115, satu AI service.
#   ./ta-up.sh lab start [stub|http]          # default stub (tanpa token)
#   ./ta-up.sh lab status
#   ./ta-up.sh lab stop
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUN_ENV="$ROOT/run.env"
LOG_DIR="$ROOT/logs"
PYTHON="$ROOT/.venv/bin/python"
BACKEND="$ROOT/backend/ta-backend"
mkdir -p "$LOG_DIR"

ACTION="${1:-status}"
ARG1="${2:-}"
ARG2="${3:-}"
ARG3="${4:-}"
ARG4="${5:-}"

# Matriks kondisi eksperimen (ablation, Bab 3), sama dengan $LabMatrix di ta-up.ps1.
# Kolom: port mode threshold pregen ("-" = default: threshold 2, pregen off).
LAB_MATRIX=(
    "8101 no_cache - -"
    "8102 static_ttl - -"
    "8103 dynamic_ttl - -"
    "8104 event_only 1 -"
    "8105 event_only 2 -"
    "8106 event_only 5 -"
    "8107 event_only 10 -"
    "8108 hybrid 1 -"
    "8109 hybrid 2 -"
    "8110 hybrid 5 -"
    "8111 hybrid 10 -"
    "8112 hybrid 1 on"
    "8113 hybrid 2 on"
    "8114 hybrid 5 on"
    "8115 hybrid 10 on"
)

die() { echo "$*" >&2; exit 1; }

# Baca KEY=VALUE ke environment. CR dibuang supaya file yang disalin dari Windows tetap terbaca.
import_env() {
    [[ -f "$1" ]] || return 0
    local line k v
    while IFS= read -r line || [[ -n "$line" ]]; do
        line="${line%$'\r'}"
        [[ -z "${line// }" || "$line" =~ ^[[:space:]]*# || "$line" != *=* ]] && continue
        k="${line%%=*}"
        v="${line#*=}"
        k="${k//[[:space:]]/}"
        [[ -z "$k" ]] && continue
        v="${v#"${v%%[![:space:]]*}"}"
        v="${v%"${v##*[![:space:]]}"}"
        v="${v#\"}"
        v="${v%\"}"
        export "$k=$v"
    done < "$1"
}

set_run_env() {
    if grep -q "^$1=" "$RUN_ENV"; then
        sed -i "s|^$1=.*|$1=$2|" "$RUN_ENV"
    else
        echo "$1=$2" >> "$RUN_ENV"
    fi
}

health() { curl -fsS -m 3 "$1" 2>/dev/null || echo "DOWN"; }

run_py() {
    local script="$1"
    shift
    PYTHONWARNINGS=ignore "$PYTHON" "$ROOT/ingestion/$script" "$@" || echo "PERINGATAN: $script keluar dengan kode $?" >&2
}

stop_pid() {
    if [[ -f "$1" ]]; then
        kill "$(cat "$1")" 2>/dev/null || true
        rm -f "$1"
    fi
}

stop_services() {
    stop_pid "$LOG_DIR/backend.pid"
    stop_pid "$LOG_DIR/api.pid"
}

start_api() {
    (cd "$ROOT/ai-service" && exec nohup "$PYTHON" -m uvicorn app:app --host 127.0.0.1 --port 8000 \
        < /dev/null > "$LOG_DIR/api.out.log" 2> "$LOG_DIR/api.err.log") &
    echo $! > "$LOG_DIR/api.pid"
}

start_backend() {
    (cd "$ROOT/backend" && exec nohup "$BACKEND" \
        < /dev/null > "$LOG_DIR/$1.out.log" 2> "$LOG_DIR/$1.err.log") &
    echo $! > "$LOG_DIR/$1.pid"
}

start_services() {
    import_env "$ROOT/.env"
    import_env "$RUN_ENV"
    start_api
    start_backend "backend"
}

show_status() {
    echo "mode   : $(grep -E '^(CACHE_MODE|AI_MODE|INVALIDATION_THRESHOLD_PCT|PREGEN|PROMPT_VERSION)=' "$RUN_ENV" | tr -d '\r' | tr '\n' ' ')"
    echo "Go     : $(health http://127.0.0.1:8080/health)"
    echo "FastAPI: $(health http://127.0.0.1:8000/health)"
}

lab_status() {
    local row port mode th pg label h
    for row in "${LAB_MATRIX[@]}"; do
        read -r port mode th pg <<< "$row"
        label="$mode"
        if [[ "$th" != "-" ]]; then label+=" th=$th%"; fi
        if [[ "$pg" != "-" ]]; then label+=" +pregen"; fi
        if h=$(curl -fsS -m 2 "http://127.0.0.1:$port/health" 2>/dev/null); then
            h=$(grep -o '"instance":"[^"]*"' <<< "$h" | cut -d'"' -f4 || true)
            printf '%s  %-28s UP (%s)\n' "$port" "$label" "$h"
        else
            printf '%s  %-28s DOWN\n' "$port" "$label"
        fi
    done
}

case "$ACTION" in
    build)
        if command -v go > /dev/null; then
            (cd "$ROOT/backend" && CGO_ENABLED=0 go build -o ta-backend .)
        else
            # Tanpa Go di host: build di container golang. GOTOOLCHAIN=auto mengunduh versi di go.mod
            # kalau image lebih lama; cache modul disimpan di volume supaya build berikutnya cepat.
            docker run --rm -v "$ROOT/backend:/src" -w /src \
                -v ta_gomod:/go/pkg/mod -v ta_gobuild:/root/.cache/go-build \
                -e CGO_ENABLED=0 -e GOTOOLCHAIN=auto \
                golang:1.26 sh -c 'go build -buildvcs=false -o ta-backend . && chown "$(stat -c %u:%g go.mod)" ta-backend'
        fi
        echo "build OK"
        ;;
    restart)
        if [[ -n "$ARG1" ]]; then
            [[ "$ARG1" =~ ^(no_cache|static_ttl|dynamic_ttl|event_only|hybrid)$ ]] || die "CACHE_MODE tidak valid: $ARG1"
            set_run_env CACHE_MODE "$ARG1"
        fi
        if [[ -n "$ARG2" ]]; then
            [[ "$ARG2" =~ ^(stub|http)$ ]] || die "AI_MODE tidak valid: $ARG2"
            set_run_env AI_MODE "$ARG2"
        fi
        if [[ -n "$ARG3" ]]; then
            [[ "$ARG3" =~ ^[0-9]+(\.[0-9]+)?$ ]] || die "THRESHOLD tidak valid: $ARG3"
            set_run_env INVALIDATION_THRESHOLD_PCT "$ARG3"
        fi
        if [[ -n "$ARG4" ]]; then
            [[ "$ARG4" =~ ^(on|off)$ ]] || die "PREGEN harus on atau off: $ARG4"
            set_run_env PREGEN "$ARG4"
        elif [[ "$ARG1" == "no_cache" ]]; then
            # no_cache tidak punya cache untuk diisi pre-generation; backend menolak kombinasi ini
            set_run_env PREGEN off
            echo "PREGEN dimatikan (no_cache tanpa cache). Kembali: ./ta-up.sh restart hybrid http 2 on"
        fi
        stop_services
        sleep 1
        start_services
        for _ in $(seq 20); do
            if [[ "$(health http://127.0.0.1:8080/health)" != "DOWN" && "$(health http://127.0.0.1:8000/health)" != "DOWN" ]]; then
                break
            fi
            sleep 1
        done
        show_status
        if [[ "$(health http://127.0.0.1:8080/health)" == "DOWN" ]]; then
            echo "--- backend gagal start, logs/backend.err.log:"
            tail -n 5 "$LOG_DIR/backend.err.log"
        fi
        ;;
    stop)
        stop_services
        echo "stopped"
        ;;
    ingest)
        import_env "$ROOT/.env"
        run_py ingest.py --timeframe 1h
        run_py ingest.py --timeframe 1d
        ;;
    backtest)
        import_env "$ROOT/.env"
        run_py backtest.py
        ;;
    fundamentals)
        import_env "$ROOT/.env"
        run_py fundamentals.py
        ;;
    quota)
        import_env "$ROOT/.env"
        import_env "$RUN_ENV"
        run_py quota.py
        ;;
    pilot-prices)
        import_env "$ROOT/.env"
        run_py pilot_prices.py --csv "$LOG_DIR/pilot_prices.csv"
        ;;
    pilot-ai)
        # ./ta-up.sh pilot-ai [N] [SIMBOL,SIMBOL,...]  (default: semua instrument x 3 metode)
        # Berjalan di background (aman ditinggal / SSH putus). Progres: tail -f logs/pilot_ai.out
        # Ditolak kalau pilot lain masih berjalan: dua pilot bersamaan saling antre di Hermes
        # sehingga latency ikut memuat waktu antre, dan output/CSV-nya saling menimpa.
        if ! flock -n "$LOG_DIR/pilot_ai.lock" true; then
            die "Pilot lain masih berjalan (ps aux | grep [p]ilot_ai). Tunggu selesai atau: pkill -f pilot_ai.py"
        fi
        pilot_args=(--n "${ARG1:-10}" --csv "$LOG_DIR/pilot_ai.csv")
        if [[ -n "$ARG2" ]]; then
            pilot_args+=(--symbols "$ARG2")
        fi
        PYTHONWARNINGS=ignore nohup "$PYTHON" -u "$ROOT/ingestion/pilot_ai.py" "${pilot_args[@]}" \
            < /dev/null > "$LOG_DIR/pilot_ai.out" 2>&1 &
        echo "pilot berjalan di background (PID $!)."
        echo "Pantau: tail -f logs/pilot_ai.out   (Ctrl+C hanya menutup tampilan, pilot tetap jalan)"
        ;;
    migrate)
        # Lewat stdin, jadi tidak bergantung pada folder yang di-mount container.
        file="$ROOT/migrations/$ARG1"
        [[ -n "$ARG1" && -f "$file" ]] || die "Pakai: ./ta-up.sh migrate <nama file di migrations/>"
        docker exec -i ta_postgres psql -U ta_user -d ta_db -v ON_ERROR_STOP=1 -q < "$file" || die "migrasi gagal"
        echo "migrasi $ARG1 OK"
        ;;
    lab)
        case "$ARG1" in
            start)
                import_env "$ROOT/.env"
                import_env "$RUN_ENV"
                export AI_MODE="${ARG2:-stub}"
                if [[ "$AI_MODE" == "http" && "$(health http://127.0.0.1:8000/health)" == "DOWN" ]]; then
                    start_api
                fi
                for row in "${LAB_MATRIX[@]}"; do
                    read -r port mode th pg <<< "$row"
                    if [[ "$th" == "-" ]]; then th=2; fi
                    if [[ "$pg" == "-" ]]; then pg=off; fi
                    export PORT="$port" CACHE_MODE="$mode" INVALIDATION_THRESHOLD_PCT="$th" PREGEN="$pg"
                    start_backend "lab-$port"
                done
                sleep 5
                lab_status
                ;;
            stop)
                for row in "${LAB_MATRIX[@]}"; do
                    read -r port _ <<< "$row"
                    stop_pid "$LOG_DIR/lab-$port.pid"
                done
                echo "lab stopped"
                ;;
            *)
                lab_status
                ;;
        esac
        ;;
    status)
        show_status
        ;;
    *)
        die "Aksi tidak dikenal: $ACTION"
        ;;
esac
