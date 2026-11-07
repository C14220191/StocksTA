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
#   ./ta-up.sh ingest-retry                   # ulangi hanya yang gagal jam ini (cron menit ke-35)
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
#
# Benchmark (eksperimen utama Bab 4): lab stub + k6 di background, aman ditinggal / SSH putus.
#   ./ta-up.sh bench start [DURASI] [RATE]    # default 7d (1 minggu), 6 kunjungan/menit; uji singkat: 30m
#   ./ta-up.sh bench plan                     # pratinjau distribusi popularitas (Zipf) tanpa memulai apa pun
#   ./ta-up.sh bench status [ID]              # progres, request per kondisi, link Evaluasi untuk jendela run
#   ./ta-up.sh bench stop                     # hentikan lebih awal (jendela run tetap tercatat)
#   ./ta-up.sh bench list
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

lab_up() { curl -fsS -m 2 "http://127.0.0.1:$1/health" > /dev/null 2>&1; }

lab_any_up() {
    local row port
    for row in "${LAB_MATRIX[@]}"; do
        read -r port _ <<< "$row"
        if lab_up "$port"; then return 0; fi
    done
    return 1
}

lab_all_up() {
    local row port
    for row in "${LAB_MATRIX[@]}"; do
        read -r port _ <<< "$row"
        lab_up "$port" || return 1
    done
}

lab_start() {
    # Instance kedua di port yang sama gagal bind dan menimpa file pid instance pertama (yatim)
    if lab_any_up; then
        die "Lab masih berjalan (./ta-up.sh lab status). Hentikan dulu: ./ta-up.sh lab stop"
    fi
    import_env "$ROOT/.env"
    import_env "$RUN_ENV"
    export AI_MODE="${1:-stub}"
    # Key Redis lab terpisah dari backend utama (lab.hybrid-2-pg vs hybrid-2-pg, cache.go instanceKey):
    # tanpa ini cache dan popularitas pre-generation kondisi yang sama tercampur.
    export CACHE_NAMESPACE=lab
    if [[ "$AI_MODE" == "http" && "$(health http://127.0.0.1:8000/health)" == "DOWN" ]]; then
        start_api
    fi
    local row port mode th pg
    for row in "${LAB_MATRIX[@]}"; do
        read -r port mode th pg <<< "$row"
        if [[ "$th" == "-" ]]; then th=2; fi
        if [[ "$pg" == "-" ]]; then pg=off; fi
        export PORT="$port" CACHE_MODE="$mode" INVALIDATION_THRESHOLD_PCT="$th" PREGEN="$pg"
        start_backend "lab-$port"
    done
}

lab_stop() {
    local row port
    for row in "${LAB_MATRIX[@]}"; do
        read -r port _ <<< "$row"
        stop_pid "$LOG_DIR/lab-$port.pid"
    done
}

# ---- benchmark ----
# Satu run = folder logs/bench/<ID> (ID = waktu mulai UTC): run.meta (jendela waktu, parameter, commit),
# run.env (salinan konfigurasi), popularity.csv (peringkat Zipf), k6.out. logs/bench/current -> run terakhir.
BENCH_DIR="$LOG_DIR/bench"
K6_CONTAINER=ta-bench-k6

psql_q() { docker exec -i ta_postgres psql -U ta_user -d ta_db -v ON_ERROR_STOP=1 -qAtX "$@"; }

duration_seconds() {
    [[ "$1" =~ ^([0-9]+)([dhm])$ ]] || return 1
    local n="${BASH_REMATCH[1]}"
    case "${BASH_REMATCH[2]}" in
        d) echo $((n * 86400)) ;;
        h) echo $((n * 3600)) ;;
        m) echo $((n * 60)) ;;
    esac
}

fmt_dur() { printf '%d hari %d jam %d mnt' $(($1 / 86400)) $(($1 % 86400 / 3600)) $(($1 % 3600 / 60)); }

wib() { TZ=Asia/Jakarta date -d "$1" '+%a %d %b %Y %H:%M WIB'; }

meta_get() { { grep -E "^$2=" "$1/run.meta" 2>/dev/null || true; } | tail -n 1 | cut -d= -f2-; }

bench_running() {
    local dir="$BENCH_DIR/current"
    [[ -f "$dir/run.meta" && -z "$(meta_get "$dir" BENCH_END)" ]] || return 1
    kill -0 "$(cat "$dir/wrapper.pid" 2>/dev/null)" 2>/dev/null
}

# Popularitas (MIX k6): Zipf atas semua instrumen yang punya data, dibekukan di awal run.
# Peringkat dalam tiap kelas aset = rata-rata nilai transaksi harian 30 hari; antar kelas disusun
# bergiliran (kripto, saham, komoditas). Nilai transaksi tidak dibandingkan lintas kelas karena
# volume futures (komoditas) adalah nilai nosional ber-leverage: diurutkan langsung, komoditas
# memenuhi peringkat atas. BENCH_MIX="SIMBOL:bobot,..." untuk distribusi lain.
# Menulis <dir>/turnover.txt dan <dir>/popularity.csv. Angka selalu memakai titik desimal
# (LC_ALL=C): dengan locale id_ID awk menulis "0,500000" sehingga kolom CSV dan bobot k6 rusak.
bench_popularity() {
    local dir="$1" usdidr="${USDIDR_RATE:-16000}" zipf_s="${BENCH_ZIPF_S:-1}"
    [[ "$usdidr" =~ ^[0-9]+(\.[0-9]+)?$ ]] || die "USDIDR_RATE tidak valid: $usdidr"
    [[ "$zipf_s" =~ ^[0-9]+(\.[0-9]+)?$ ]] || die "BENCH_ZIPF_S tidak valid: $zipf_s"
    psql_q -v rate="$usdidr" > "$dir/turnover.txt" << 'SQL' || die "Gagal menghitung peringkat popularitas"
WITH t AS (
    SELECT i.symbol, i.asset_type,
           round(avg(o.close * o.volume / CASE WHEN i.source = 'yahoo' THEN :rate ELSE 1 END))::bigint AS usd
    FROM instruments i
    JOIN ohlcv o ON o.instrument_id = i.id AND o.timeframe = '1d' AND o.ts >= now() - interval '30 days'
    WHERE EXISTS (SELECT 1 FROM ohlcv h WHERE h.instrument_id = i.id AND h.timeframe = '1h'
                  AND h.ts >= now() - interval '7 days')
    GROUP BY i.symbol, i.asset_type)
SELECT symbol, asset_type, r, usd
FROM (SELECT t.*, row_number() OVER (PARTITION BY asset_type ORDER BY usd DESC NULLS LAST, symbol) AS r FROM t) x
ORDER BY r, array_position(ARRAY['crypto', 'stock', 'commodity'], asset_type), symbol;
SQL
    if (( $(wc -l < "$dir/turnover.txt") < 3 )); then
        die "Instrumen dengan data terlalu sedikit untuk benchmark ($dir/turnover.txt)"
    fi
    LC_ALL=C awk -F'|' -v s="$zipf_s" '
        { sym[NR] = $1; cls[NR] = $2; r[NR] = $3; t[NR] = $4; w[NR] = 1 / (NR ^ s); tot += w[NR] }
        END {
            print "rank,symbol,asset_type,rank_in_class,turnover_usd_30d,weight,share_pct"
            for (i = 1; i <= NR; i++)
                printf "%d,%s,%s,%d,%s,%.6f,%.3f\n", i, sym[i], cls[i], r[i], t[i], w[i], w[i] / tot * 100
        }' "$dir/turnover.txt" > "$dir/popularity.csv"
    # Pengaman: 7 kolom per baris dan total pangsa 100%
    LC_ALL=C awk -F, 'NR > 1 { if (NF != 7) bad = 1; s += $7 } END { exit (bad || s < 99.9 || s > 100.1) }' \
        "$dir/popularity.csv" || die "popularity.csv tidak valid (cek locale/angka): $dir/popularity.csv"
}

# MIX k6 dari popularity.csv: "BTCUSDT:1.000000,BBCA.JK:0.500000,..."
bench_mix() {
    local mix
    mix=$(LC_ALL=C awk -F, 'NR > 1 { printf "%s%s:%s", (NR > 2 ? "," : ""), $2, $6 }' "$1")
    [[ "$mix" =~ ^([A-Z0-9.]+:[0-9]+\.[0-9]+,)*[A-Z0-9.]+:[0-9]+\.[0-9]+$ ]] || die "MIX tidak valid: ${mix:0:120}"
    echo "$mix"
}

bench_show_popularity() {
    LC_ALL=C awk -F, -v top="${2:-5}" '
        NR > 1 && NR <= top + 1 { printf "      %2d. %-12s %-9s %6.2f%%\n", $1, $2, $3, $7 }
        NR > 1 { s[$3] += $7; n[$3]++ }
        END { printf "      per kelas:"; for (c in s) printf " %s %d instrumen %.1f%%;", c, n[c], s[c]; print "" }' "$1"
}

# Pratinjau distribusi beban tanpa memulai apa pun.
bench_plan() {
    local tmp
    import_env "$RUN_ENV"
    tmp=$(mktemp -d)
    bench_popularity "$tmp"
    echo "Distribusi popularitas (Zipf s=${BENCH_ZIPF_S:-1}) atas $(($(wc -l < "$tmp/popularity.csv") - 1)) instrumen:"
    bench_show_popularity "$tmp/popularity.csv" 1000
    echo "MIX k6 (awal): $(bench_mix "$tmp/popularity.csv" | cut -c1-110)..."
    rm -rf "$tmp"
}

bench_start() {
    local duration="${1:-7d}" rate="${2:-6}" secs
    secs=$(duration_seconds "$duration") || die "Durasi harus seperti 7d, 168h, atau 30m (bukan: $duration)"
    [[ "$rate" =~ ^[1-9][0-9]*$ ]] || die "RATE harus bilangan bulat > 0 (kunjungan per menit)"
    if bench_running; then
        die "Benchmark masih berjalan: ./ta-up.sh bench status (hentikan: ./ta-up.sh bench stop)"
    fi
    import_env "$RUN_ENV"
    echo "Memeriksa prasyarat..."

    # k6: biner lokal kalau terpasang, kalau tidak image docker (tanpa perlu menambah repo apt k6)
    local k6mode=docker image="${K6_IMAGE:-grafana/k6:1.0.0}"
    if command -v k6 > /dev/null; then k6mode=native; fi

    # Hal yang membuat data seminggu cacat. BENCH_FORCE=1 untuk tetap jalan (catat sebagai batasan).
    local problems=() main free_gb stale
    main=$(curl -fsS -m 3 http://127.0.0.1:8080/health 2>/dev/null | grep -o '"cache_mode":"[^"]*"' | cut -d'"' -f4 || true)
    if [[ -n "$main" && "$main" != "no_cache" ]]; then
        problems+=("Backend utama (:8080) berjalan dengan $main. Evaluasi mengelompokkan per kondisi, jadi pre-generation dan cache_events-nya ikut terhitung di kondisi lab yang sama. Pilih: ./ta-up.sh stop, atau ./ta-up.sh restart no_cache (UI tetap bisa dipakai)")
    fi
    if [[ "$(timedatectl show -p NTPSynchronized --value 2>/dev/null)" != "yes" ]]; then
        problems+=("Jam server belum tersinkron NTP (timedatectl). L_inv dan umur data bergantung pada jam: sudo timedatectl set-ntp true")
    fi
    if ! crontab -l 2>/dev/null | grep -q "ta-up.sh ingest"; then
        problems+=("Cron ingest tiap jam tidak ada di crontab -l. Tanpa ingest, candle 1h dan harga saham membeku selama benchmark")
    fi
    free_gb=$(df -Pk "$ROOT" | awk 'NR == 2 { printf "%d", $4 / 1048576 }')
    if (( free_gb < 10 )); then
        problems+=("Ruang disk tersisa ${free_gb} GB. Satu minggu menambah beberapa GB ke database; sisakan >= 10 GB")
    fi
    # Per instrumen (bukan gabungan): spot yang segar tidak boleh menutupi futures yang basi.
    # Kripto/komoditas berjalan 24 jam; > 3 jam = ingest gagal lebih dari sekali berturut-turut.
    stale=$(psql_q -c "SELECT coalesce(string_agg(i.symbol || ' ' || coalesce(
                (extract(epoch FROM now() - l.ts) / 3600)::int || ' jam', 'tanpa data'), ', ' ORDER BY i.symbol), '')
        FROM instruments i
        LEFT JOIN LATERAL (SELECT max(o.ts) AS ts FROM ohlcv o
                           WHERE o.instrument_id = i.id AND o.timeframe = '1h') l ON true
        WHERE i.source <> 'yahoo' AND (l.ts IS NULL OR l.ts < now() - interval '3 hours')") \
        || die "Database tidak bisa dibaca (docker ps: ta_postgres jalan?)"
    if [[ -n "$stale" ]]; then
        problems+=("Candle 1h basi (> 3 jam): $stale. Cek ./ta-up.sh ingest dan logs/cron.log")
    fi
    if (( ${#problems[@]} > 0 )); then
        echo "Pemeriksaan sebelum benchmark:" >&2
        printf '  - %s\n' "${problems[@]}" >&2
        if [[ "${BENCH_FORCE:-}" != "1" ]]; then
            die "Perbaiki dulu, atau tetap jalankan: BENCH_FORCE=1 ./ta-up.sh bench start $duration $rate"
        fi
        echo "BENCH_FORCE=1: tetap dijalankan. Catat masalah di atas sebagai batasan." >&2
    fi

    local id dir mix zipf_s="${BENCH_ZIPF_S:-1}"
    id=$(date -u +%Y%m%dT%H%M%SZ)
    dir="$BENCH_DIR/$id"
    mkdir -p "$BENCH_DIR"
    mkdir "$dir" || die "Folder run $dir sudah ada"
    cp "$RUN_ENV" "$dir/run.env"
    if [[ -n "${BENCH_MIX:-}" ]]; then
        mix="$BENCH_MIX"
    else
        bench_popularity "$dir"
        mix=$(bench_mix "$dir/popularity.csv")
    fi

    if [[ "$k6mode" == docker ]] && ! docker image inspect "$image" > /dev/null 2>&1; then
        echo "Mengunduh image $image (sekali saja, ±30-70 MB)..."
        docker pull "$image" || die "Gagal mengunduh $image. Coba manual: docker pull $image"
    fi

    echo "Menyiapkan lab stub (15 kondisi)..."
    if lab_any_up; then
        BENCH_WRAPPER=1 lab_stop
        sleep 2
    fi
    # Cache lab dimulai kosong: entri dan popularitas run sebelumnya tidak boleh ikut tersaji
    docker exec ta_redis sh -c "redis-cli --scan --pattern 'ta:*:lab.*' | xargs -r -n 500 redis-cli DEL > /dev/null" \
        || die "Gagal mengosongkan cache lab di Redis"
    (lab_start stub)
    local i
    for i in $(seq 30); do
        if lab_all_up; then break; fi
        sleep 1
    done
    if ! lab_all_up; then
        lab_status
        lab_stop
        die "Sebagian instance lab gagal start; lihat logs/lab-<port>.err.log"
    fi
    # Request pertama sebelum tick WebSocket pertama memakai harga DB: beri jeda sebelum k6 mulai
    sleep "${BENCH_WARMUP_SECONDS:-20}"

    local start_e start planned cmd k6ver
    start_e=$(date -u +%s)
    start=$(date -u -d "@$start_e" +%Y-%m-%dT%H:%M:%SZ)
    planned=$(date -u -d "@$((start_e + secs))" +%Y-%m-%dT%H:%M:%SZ)
    local k6args=(run -q -e HOST=127.0.0.1 -e "RATE=$rate" -e "DURATION=${secs}s" -e "MIX=$mix")
    if [[ "$k6mode" == native ]]; then
        cmd=(k6 "${k6args[@]}" "$ROOT/loadtest/lab.js")
        k6ver=$(k6 version 2>/dev/null | head -n 1)
    else
        cmd=(docker run --rm --name "$K6_CONTAINER" --network host -v "$ROOT/loadtest:/scripts:ro"
             "$image" "${k6args[@]}" /scripts/lab.js)
        k6ver="$image"
    fi
    {
        echo "BENCH_ID=$id"
        echo "BENCH_START=$start"
        echo "BENCH_PLANNED_END=$planned"
        echo "DURATION=$duration"
        echo "RATE_PER_MIN=$rate"
        if [[ -n "${BENCH_MIX:-}" ]]; then echo "MIX_SOURCE=BENCH_MIX"; else echo "MIX_SOURCE=zipf s=$zipf_s, nilai transaksi 30 hari"; fi
        echo "K6_MODE=$k6mode"
        echo "K6=$k6ver"
        echo "GIT_COMMIT=$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null)$(git -C "$ROOT" diff --quiet HEAD 2>/dev/null || echo -dirty)"
        echo "FORCED=${BENCH_FORCE:-0}"
    } > "$dir/run.meta"
    # Pembungkus: jalankan k6, catat akhir run, lalu matikan lab supaya tidak ada data di luar jendela.
    {
        echo '#!/usr/bin/env bash'
        echo '# Dibuat oleh ./ta-up.sh bench start.'
        printf '%q ' "${cmd[@]}"
        echo '&'
        echo "echo \$! > $(printf '%q' "$dir/k6.pid")"
        echo 'wait $!'
        echo 'rc=$?'
        echo "echo \"BENCH_END=\$(date -u +%Y-%m-%dT%H:%M:%SZ)\" >> $(printf '%q' "$dir/run.meta")"
        echo "echo \"K6_EXIT=\$rc\" >> $(printf '%q' "$dir/run.meta")"
        echo "BENCH_WRAPPER=1 $(printf '%q' "$ROOT/ta-up.sh") lab stop"
    } > "$dir/run.sh"
    ln -sfn "$id" "$BENCH_DIR/current"
    if command -v setsid > /dev/null; then
        setsid nohup bash "$dir/run.sh" < /dev/null > "$dir/k6.out" 2>&1 &
    else
        nohup bash "$dir/run.sh" < /dev/null > "$dir/k6.out" 2>&1 &
    fi
    echo $! > "$dir/wrapper.pid"

    echo
    echo "Benchmark berjalan: $id"
    echo "  Mulai     : $start  ($(wib "$start"))"
    echo "  Selesai   : $planned  ($(wib "$planned")), rencana"
    echo "  Beban     : $rate kunjungan/menit x 3 metode, dikirim ke 15 kondisi sekaligus"
    if [[ -f "$dir/popularity.csv" ]]; then
        echo "  Popularitas (5 teratas dari $(($(wc -l < "$dir/popularity.csv") - 1)) instrumen, $dir/popularity.csv):"
        bench_show_popularity "$dir/popularity.csv" 5
    fi
    echo "  Pantau    : ./ta-up.sh bench status    (aman logout / SSH putus)"
    echo "  Hentikan  : ./ta-up.sh bench stop"
}

bench_status() {
    local dir="$BENCH_DIR/${1:-current}"
    [[ -f "$dir/run.meta" ]] || die "Belum ada run benchmark di logs/bench. Mulai: ./ta-up.sh bench start"
    local id start planned end rc now_e start_e planned_e to state
    id=$(meta_get "$dir" BENCH_ID)
    start=$(meta_get "$dir" BENCH_START)
    planned=$(meta_get "$dir" BENCH_PLANNED_END)
    end=$(meta_get "$dir" BENCH_END)
    rc=$(meta_get "$dir" K6_EXIT)
    now_e=$(date -u +%s)
    start_e=$(date -u -d "$start" +%s)
    planned_e=$(date -u -d "$planned" +%s)
    to=""
    if [[ -n "$end" ]]; then
        to="$end"
        if [[ "$rc" == "0" ]]; then
            state="SELESAI"
        else
            state="BERHENTI (k6 exit $rc: dihentikan dengan bench stop, atau error; lihat $dir/k6.out)"
        fi
    elif kill -0 "$(cat "$dir/wrapper.pid" 2>/dev/null)" 2>/dev/null; then
        state="BERJALAN $(fmt_dur $((now_e - start_e))), sisa $(fmt_dur $((planned_e > now_e ? planned_e - now_e : 0)))"
    else
        state="TERPUTUS tanpa selesai (server restart atau proses dibunuh?). Pakai request k6 terakhir di bawah sebagai akhir jendela"
    fi
    echo "Run      : $id  ($(meta_get "$dir" DURATION), $(meta_get "$dir" RATE_PER_MIN) kunjungan/menit, commit $(meta_get "$dir" GIT_COMMIT))"
    echo "Status   : $state"
    echo "Mulai    : $start  ($(wib "$start"))"
    echo "Rencana  : $planned  ($(wib "$planned"))"
    if [[ -n "$end" ]]; then echo "Akhir    : $end  ($(wib "$end"))"; fi
    if [[ "$state" == BERJALAN* ]]; then
        echo "--- lab"
        lab_status
    fi
    echo "--- request k6 per kondisi (jendela run)"
    psql_q -v from="$start" -v to="${to:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}" << 'SQL' \
        | awk -F'|' 'BEGIN { printf "  %-24s %9s %7s %7s %10s\n", "kondisi", "request", "hit", "error", "1 jam akhir" }
                     { printf "  %-24s %9d %6.1f%% %7d %10d\n", $1, $2, $3, $4, $5 }' \
        || echo "  (database tidak bisa dibaca)"
SELECT experiment_condition
       || CASE WHEN experiment_condition IN ('event_only', 'hybrid')
               THEN ' θ' || (invalidation_threshold_pct::float8)::text || '%' ELSE '' END
       || CASE WHEN pregen THEN ' +pregen' ELSE '' END,
       count(*),
       round(100.0 * count(*) FILTER (WHERE cache_status = 'HIT') / count(*), 1),
       count(*) FILTER (WHERE cache_status = 'ERROR'),
       count(*) FILTER (WHERE created_at >= now() - interval '1 hour')
FROM inference_log
WHERE trigger = 'k6' AND created_at >= :'from' AND created_at < :'to'
GROUP BY experiment_condition, invalidation_threshold_pct, pregen
ORDER BY array_position(ARRAY['no_cache', 'static_ttl', 'dynamic_ttl', 'event_only', 'hybrid'], experiment_condition),
         invalidation_threshold_pct, pregen;
SQL
    echo "  Request k6 terakhir: $(psql_q -v from="$start" <<< "SELECT coalesce(max(created_at)::text, '-') FROM inference_log WHERE trigger = 'k6' AND created_at >= :'from';" || echo '-')"
    echo "--- sumber daya"
    echo "  Database : $(psql_q -c "SELECT pg_size_pretty(pg_database_size('ta_db'))" || echo '-')"
    echo "  Disk     : $(df -Ph "$ROOT" | awk 'NR == 2 { print $4 " tersisa dari " $2 }')"
    echo "--- Evaluasi (halaman admin, lewat tunnel UI di PC)"
    if [[ -n "$to" ]]; then
        echo "  http://localhost:5173/admin/evaluation?from=$start&to=$to&trigger=k6&prompt=stub"
    else
        echo "  http://localhost:5173/admin/evaluation?from=$start&trigger=k6&prompt=stub   (sampai sekarang)"
    fi
    if [[ -n "$end" ]]; then
        echo "--- akhir k6.out"
        tail -n 12 "$dir/k6.out"
    fi
}

bench_stop() {
    if ! bench_running; then
        die "Tidak ada benchmark yang berjalan (./ta-up.sh bench list)"
    fi
    local dir="$BENCH_DIR/current"
    if [[ "$(meta_get "$dir" K6_MODE)" == docker ]]; then
        nohup docker stop -t 420 "$K6_CONTAINER" > /dev/null 2>&1 &
    else
        kill -INT "$(cat "$dir/k6.pid")"
    fi
    echo "k6 diminta berhenti. Kunjungan yang sedang berjalan diselesaikan dulu (maks ~6 menit), lalu"
    echo "akhir run dicatat dan lab dimatikan otomatis. Cek: ./ta-up.sh bench status"
}

bench_list() {
    local d
    for d in "$BENCH_DIR"/2*/; do
        [[ -f "$d/run.meta" ]] || continue
        printf '%s  %-5s mulai %s  akhir %-20s  k6 exit %s\n' "$(meta_get "$d" BENCH_ID)" "$(meta_get "$d" DURATION)" \
            "$(meta_get "$d" BENCH_START)" "$(meta_get "$d" BENCH_END)" "$(meta_get "$d" K6_EXIT)"
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
        # Selama benchmark, backend utama hanya boleh no_cache (lihat pemeriksaan di bench_start)
        if bench_running; then
            next_mode="${ARG1:-$(grep -E '^CACHE_MODE=' "$RUN_ENV" | cut -d= -f2 | tr -d '\r')}"
            if [[ "$next_mode" != "no_cache" ]]; then
                die "Benchmark sedang berjalan: backend utama hanya boleh no_cache (./ta-up.sh restart no_cache)"
            fi
        fi
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
    ingest-retry)
        # Cron menit ke-35: hanya instrumen yang ingest jam ini gagal (fapi.binance.com dari jaringan
        # server sesekali menolak). Yang sudah berhasil tidak disentuh, jadi harga saham tetap per jam.
        import_env "$ROOT/.env"
        run_py ingest.py --timeframe 1h --retry-failed
        run_py ingest.py --timeframe 1d --retry-failed
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
                if bench_running; then die "Benchmark sedang berjalan dan memakai lab (./ta-up.sh bench status)"; fi
                lab_start "${ARG2:-stub}"
                sleep 5
                lab_status
                ;;
            stop)
                if [[ "${BENCH_WRAPPER:-}" != "1" ]] && bench_running; then
                    die "Benchmark sedang berjalan; lab stop akan merusak run. Hentikan lewat: ./ta-up.sh bench stop"
                fi
                lab_stop
                echo "lab stopped"
                ;;
            *)
                lab_status
                ;;
        esac
        ;;
    bench)
        # Titik desimal untuk semua angka (awk/printf); locale id_ID memakai koma
        export LC_ALL=C
        case "$ARG1" in
            start) bench_start "${ARG2:-7d}" "${ARG3:-6}" ;;
            plan) bench_plan ;;
            stop) bench_stop ;;
            list) bench_list ;;
            status | "") bench_status "${ARG2:-current}" ;;
            *) die "Pakai: ./ta-up.sh bench start [DURASI] [RATE] | plan | status [ID] | stop | list" ;;
        esac
        ;;
    status)
        show_status
        ;;
    *)
        die "Aksi tidak dikenal: $ACTION"
        ;;
esac
